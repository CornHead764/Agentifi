package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The server's own backups: the nightly run, a run asked for, and the settings
// both read. The sets are written by internal/backup; this is when, to whom
// they are encrypted, what is recorded, and who is told when one fails.
//
// The settings are stored values with the environment behind any never
// saved, as for single sign-on, so an install configured in .env keeps
// working and the administration screen can change it without a restart.

// BackupRecipient is one public key the sets are encrypted to.
type BackupRecipient struct {
	Recipient string    `json:"recipient"`
	Label     string    `json:"label"`
	AddedAt   time.Time `json:"added_at"`
}

// BackupSettings are the effective settings, and where each came from.
type BackupSettings struct {
	Hour, Minute int
	KeepDays     int
	Recipients   []BackupRecipient
	// Sources says "database" or "environment" for at, keep_days and
	// recipients.
	Sources map[string]string
}

// At is the nightly time as HH:MM.
func (s BackupSettings) At() string { return fmt.Sprintf("%02d:%02d", s.Hour, s.Minute) }

// Keys are the recipients' public keys.
func (s BackupSettings) Keys() []string {
	out := make([]string, 0, len(s.Recipients))
	for _, one := range s.Recipients {
		out = append(out, one.Recipient)
	}
	return out
}

// BackupDefaults are the environment's settings.
type BackupDefaults struct {
	Hour, Minute int
	KeepDays     int
	Recipients   []string
}

var (
	// ErrBackupRunning is a request for a run while one is in progress.
	ErrBackupRunning = errors.New("a backup is already running")
	// ErrBackupsOff is a request for a run on a server with no BACKUP_DIR.
	ErrBackupsOff = errors.New("backups are off: BACKUP_DIR is not set")
)

type Backups struct {
	store *store.Store
	// Dir is BACKUP_DIR; empty turns the server's backups off.
	Dir      string
	Source   backup.Source
	Defaults BackupDefaults
	Alerts   *Alerts
	// Every is how often the scheduler checks whether the nightly run is due.
	Every time.Duration
	Now   func() time.Time

	running atomic.Bool
}

// NewBackups takes a store that can open server settings, which are sealed.
func NewBackups(st *store.Store) *Backups {
	return &Backups{store: st, Every: time.Minute}
}

func (b *Backups) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// Enabled reports whether this server writes backups at all.
func (b *Backups) Enabled() bool { return b.Dir != "" }

// Running reports whether a run is in progress in this process.
func (b *Backups) Running() bool { return b.running.Load() }

// Settings merges what was saved over the environment.
func (b *Backups) Settings(ctx context.Context) (BackupSettings, error) {
	settings := BackupSettings{
		Hour: b.Defaults.Hour, Minute: b.Defaults.Minute, KeepDays: b.Defaults.KeepDays,
		Sources: map[string]string{
			"at": "environment", "keep_days": "environment", "recipients": "environment",
		},
	}
	for _, key := range b.Defaults.Recipients {
		settings.Recipients = append(settings.Recipients, BackupRecipient{Recipient: key, Label: "BACKUP_RECIPIENTS"})
	}
	stored, err := b.store.GetServerSettings(ctx, store.BackupSettingKeys)
	if err != nil {
		return settings, err
	}
	if value, ok := stored[store.BackupAtSetting]; ok {
		if hour, minute, ok := ParseBackupAt(value); ok {
			settings.Hour, settings.Minute = hour, minute
			settings.Sources["at"] = "database"
		}
	}
	if value, ok := stored[store.BackupKeepDaysSetting]; ok {
		if days, err := strconv.Atoi(value); err == nil && days > 0 {
			settings.KeepDays = days
			settings.Sources["keep_days"] = "database"
		}
	}
	if value, ok := stored[store.BackupRecipientsSetting]; ok {
		var recipients []BackupRecipient
		if err := json.Unmarshal([]byte(value), &recipients); err != nil {
			return settings, fmt.Errorf("service: the saved backup recipients are unreadable: %w", err)
		}
		settings.Recipients = recipients
		settings.Sources["recipients"] = "database"
	}
	return settings, nil
}

// ParseBackupAt reads HH:MM in 24-hour time.
func ParseBackupAt(text string) (hour, minute int, ok bool) {
	h, m, found := strings.Cut(strings.TrimSpace(text), ":")
	if !found || len(m) != 2 {
		return 0, 0, false
	}
	hour, hErr := strconv.Atoi(h)
	minute, mErr := strconv.Atoi(m)
	if hErr != nil || mErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// SaveSettings stores all three settings. The caller has validated them.
func (b *Backups) SaveSettings(
	ctx context.Context, hour, minute, keepDays int, recipients []BackupRecipient,
) error {
	if recipients == nil {
		recipients = []BackupRecipient{}
	}
	encoded, err := json.Marshal(recipients)
	if err != nil {
		return err
	}
	return b.store.SetServerSettings(ctx, store.BackupSettingKeys, map[string]string{
		store.BackupAtSetting:         fmt.Sprintf("%02d:%02d", hour, minute),
		store.BackupKeepDaysSetting:   strconv.Itoa(keepDays),
		store.BackupRecipientsSetting: string(encoded),
	})
}

// Start begins a requested run in the background and returns at once.
func (b *Backups) Start(trigger backup.Trigger) error {
	if !b.Enabled() {
		return ErrBackupsOff
	}
	if !b.running.CompareAndSwap(false, true) {
		return ErrBackupRunning
	}
	go func() {
		defer b.running.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if _, err := b.take(ctx, trigger); err != nil {
			slog.Warn("backup failed", "trigger", trigger, "error", err)
		}
	}()
	return nil
}

// Take runs one backup and waits for it.
func (b *Backups) Take(ctx context.Context, trigger backup.Trigger) (backup.Set, error) {
	if !b.Enabled() {
		return backup.Set{}, ErrBackupsOff
	}
	if !b.running.CompareAndSwap(false, true) {
		return backup.Set{}, ErrBackupRunning
	}
	defer b.running.Store(false)
	return b.take(ctx, trigger)
}

// recorded reports whether a run is kept in backup_runs: the server's own runs
// are, and the ones before a migration or a restore are not, since the
// database they would be recorded in is the one about to change.
func recorded(trigger backup.Trigger) bool {
	return trigger == backup.TriggerNightly || trigger == backup.TriggerManual
}

func (b *Backups) take(ctx context.Context, trigger backup.Trigger) (backup.Set, error) {
	settings, err := b.Settings(ctx)
	if err != nil {
		return backup.Set{}, err
	}
	started := b.now()

	if problem := b.preflight(ctx); problem != "" {
		if recorded(trigger) {
			if err := b.store.RecordSkippedBackupRun(ctx, string(trigger), started, problem); err != nil {
				return backup.Set{}, err
			}
		}
		b.raise(ctx, trigger, "The backup could not run", problem)
		return backup.Set{}, errors.New(problem)
	}

	var run store.BackupRun
	if recorded(trigger) {
		if run, err = b.store.StartBackupRun(ctx, string(trigger), started); err != nil {
			return backup.Set{}, err
		}
	}
	set, err := b.write(ctx, settings, trigger, started)
	finished := b.now()
	run.FinishedAt = &finished
	if err != nil {
		run.Status, run.Error = store.BackupRunFailed, err.Error()
	} else {
		run.Status, run.SetName, run.Encrypted, run.Bytes = store.BackupRunSucceeded, set.Name, set.Encrypted, set.Bytes
	}
	if recorded(trigger) {
		// Not the run's context: a run cancelled half way is still recorded.
		if recordErr := b.store.FinishBackupRun(context.WithoutCancel(ctx), run); recordErr != nil {
			slog.Warn("recording the backup run failed", "error", recordErr)
		}
	}
	if err != nil {
		b.raise(ctx, trigger, "The backup failed", err.Error())
		return backup.Set{}, err
	}
	b.resolve(ctx)
	return set, nil
}

// preflight is why a run cannot start, or "".
func (b *Backups) preflight(ctx context.Context) string {
	if err := backup.Writable(b.Dir); err != nil {
		return err.Error()
	}
	if tools := backup.CheckTools(ctx, b.Source.Database); tools.Problem != "" {
		return tools.Problem
	}
	return ""
}

func (b *Backups) write(
	ctx context.Context, settings BackupSettings, trigger backup.Trigger, now time.Time,
) (backup.Set, error) {
	unlock, err := backup.Lock(ctx, b.Source.Database)
	if err != nil {
		return backup.Set{}, err
	}
	defer unlock()

	source := b.Source
	if version, err := b.store.SchemaVersion(ctx); err == nil {
		source.SchemaVersion = version
	}
	set, err := backup.Write(ctx, b.Dir, source, settings.Keys(), trigger, now)
	if err != nil {
		return backup.Set{}, err
	}
	removed, err := backup.Prune(b.Dir, now, settings.KeepDays)
	if err != nil {
		slog.Warn("removing old backups failed", "error", err)
	}
	slog.Info("backup written", "set", set.Name, "bytes", set.Bytes, "encrypted", set.Encrypted,
		"removed", removed)
	return set, nil
}

// Run is the nightly scheduler. It returns when ctx ends.
func (b *Backups) Run(ctx context.Context) {
	if err := b.store.FailInterruptedBackupRuns(ctx, b.now()); err != nil {
		slog.Warn("closing interrupted backup runs failed", "error", err)
	}
	ticker := time.NewTicker(b.Every)
	defer ticker.Stop()
	for {
		b.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Backups) tick(ctx context.Context) {
	settings, err := b.Settings(ctx)
	if err != nil {
		slog.Warn("reading the backup settings failed", "error", err)
		return
	}
	last, err := b.store.LastBackupRunStarted(ctx, string(backup.TriggerNightly))
	if err != nil {
		slog.Warn("reading the last backup run failed", "error", err)
		return
	}
	if !backup.NightlyDue(b.now(), settings.Hour, settings.Minute, last) {
		return
	}
	// A run already in progress is a requested one; the next tick finds the
	// nightly still due.
	if !b.running.CompareAndSwap(false, true) {
		return
	}
	defer b.running.Store(false)
	if _, err := b.take(ctx, backup.TriggerNightly); err != nil {
		slog.Warn("the nightly backup failed", "error", err)
	}
}

// NextNightly is when the scheduler will next start a nightly run.
func (b *Backups) NextNightly(ctx context.Context, settings BackupSettings) (time.Time, error) {
	last, err := b.store.LastBackupRunStarted(ctx, string(backup.TriggerNightly))
	if err != nil {
		return time.Time{}, err
	}
	return backup.NextNightly(b.now(), settings.Hour, settings.Minute, last), nil
}

// --- Telling the administrators ------------------------------------------------
//
// A failed nightly run is a standing condition in each administrator's feed,
// in every space they belong to, since the feed is per space and the server
// has none of its own. The next successful run of any kind withdraws it.

const backupCondition = "backup_failed"

type adminSeat struct {
	user  store.User
	space store.SpaceID
}

func (b *Backups) adminSeats(ctx context.Context) ([]adminSeat, error) {
	users, err := b.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	admins := map[uuid.UUID]store.User{}
	for _, user := range users {
		if user.IsSuperuser && user.IsActive {
			admins[user.ID] = user
		}
	}
	memberships, err := b.store.ListAllMemberships(ctx)
	if err != nil {
		return nil, err
	}
	var seats []adminSeat
	for _, membership := range memberships {
		if user, ok := admins[membership.UserID]; ok && membership.IsAccepted() {
			seats = append(seats, adminSeat{user: user, space: membership.SpaceID})
		}
	}
	return seats, nil
}

// raise tells the administrators about a nightly run that did not write a
// set. A requested run reports on the screen that asked for it.
func (b *Backups) raise(ctx context.Context, trigger backup.Trigger, title, problem string) {
	if trigger != backup.TriggerNightly || b.Alerts == nil {
		return
	}
	seats, err := b.adminSeats(ctx)
	if err != nil {
		slog.Warn("finding the administrators to tell about a failed backup failed", "error", err)
		return
	}
	hit := domain.AlertHit{
		Type:      domain.AlertBackupFailed,
		Title:     title,
		Body:      problem,
		URL:       "/settings/admin",
		DedupeKey: backupCondition,
		Ongoing:   true,
	}
	for _, seat := range seats {
		channels, err := b.Alerts.channels(ctx, seat.space, seat.user.ID)
		if err != nil {
			slog.Warn("reading alert channels failed", "error", err)
			continue
		}
		if _, err := b.Alerts.Deliver(ctx, seat.space, seat.user.ID, hit,
			AlertAudience{Email: seat.user.Email, Channels: channels}); err != nil {
			slog.Warn("delivering the backup alert failed", "error", err)
		}
	}
}

func (b *Backups) resolve(ctx context.Context) {
	if b.Alerts == nil {
		return
	}
	seats, err := b.adminSeats(ctx)
	if err != nil {
		return
	}
	done := map[store.SpaceID]bool{}
	for _, seat := range seats {
		if done[seat.space] {
			continue
		}
		done[seat.space] = true
		if err := b.Alerts.ResolveCondition(ctx, seat.space, backupCondition); err != nil {
			slog.Warn("withdrawing the backup alert failed", "error", err)
		}
	}
}
