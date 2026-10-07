package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The server's backups, from the administration screen: the schedule, the
// retention and the encryption recipients, the sets on disk, a run on request,
// and a rehearsal of a restore into a scratch database.
//
// A restore over the live database is not here. It swaps the database out
// from under this process, whose syncs, imports and automations would go on
// writing into the one being replaced, so it runs from `agentifi restore`
// with the application stopped; the screen gives the exact command.
//
// An identity pasted for a rehearsal, and an SSH key's passphrase, are used
// for that request and dropped. They are never stored, logged or echoed.

func init() {
	RegisterAdmin(Resource{Prefix: "/admin/backups", Routes: func(rt *Routes) {
		rt.Superuser(http.MethodGet, "/", readBackups)
		rt.Superuser(http.MethodPut, "/settings", saveBackupSettings)
		rt.Superuser(http.MethodPost, "/run", runBackup)
		rt.Superuser(http.MethodPost, "/sets/{name}/rehearse", rehearseBackup)
	}})
}

// NewBackups is the server's backup service as configuration describes it.
// `serve`, `migrate`, `backup` and `restore` all build theirs here, so every
// set is written the same way.
func NewBackups(cfg *config.Config, db *store.Store) *service.Backups {
	st := db
	if sealed, err := settingsStore(cfg, db); err == nil {
		st = sealed
	}
	backups := service.NewBackups(st)
	backups.Dir = cfg.BackupDir
	backups.Source = backup.Source{
		Database:        backup.Postgres{URL: cfg.DatabaseURL},
		StoragePath:     cfg.StoragePath,
		SecretsDir:      cfg.SecretsDir,
		CredentialKeyID: backup.KeyID(cfg.CredentialKey()),
	}
	backups.Defaults = service.BackupDefaults{
		Hour: cfg.BackupAt.Hour, Minute: cfg.BackupAt.Minute,
		KeepDays: cfg.BackupKeepDays, Recipients: cfg.BackupRecipients,
	}
	return backups
}

// ServerBackups is the backup service this server schedules and the screen
// drives.
func (e *Env) ServerBackups() *service.Backups {
	e.backupsOnce.Do(func() {
		if e.Backups == nil {
			e.Backups = NewBackups(e.Cfg, e.DB)
			e.Backups.Alerts = newAlerts(e)
		}
	})
	return e.Backups
}

type AdminBackupsResponse struct {
	// Enabled is false when BACKUP_DIR is unset, and nothing is written.
	Enabled bool `json:"enabled"`
	// Directory is BACKUP_DIR as this process sees it; the compose file binds
	// the host's ./backups there.
	Directory  string                 `json:"directory"`
	At         string                 `json:"at"`
	Timezone   string                 `json:"timezone"`
	KeepDays   int                    `json:"keep_days"`
	Recipients []AdminBackupRecipient `json:"recipients"`
	Sources    map[string]string      `json:"sources"`
	// Encrypted says whether the next set will be.
	Encrypted bool       `json:"encrypted"`
	NextRun   *time.Time `json:"next_run"`
	Running   bool       `json:"running"`
	// Problem is why a run cannot start right now, or null.
	Problem       *string          `json:"problem"`
	DumpVersion   string           `json:"dump_version"`
	ServerVersion string           `json:"server_version"`
	Runs          []AdminBackupRun `json:"runs"`
	Sets          []AdminBackupSet `json:"sets"`
}

type AdminBackupRun struct {
	Trigger    string     `json:"trigger"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	SetName    *string    `json:"set_name"`
	Encrypted  bool       `json:"encrypted"`
	Bytes      int64      `json:"bytes"`
	Error      *string    `json:"error"`
}

type AdminBackupSet struct {
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	Trigger    string    `json:"trigger"`
	Encrypted  bool      `json:"encrypted"`
	Recipients []string  `json:"recipients"`
	// Verified means pg_restore read the whole dump as it was written.
	Verified bool `json:"verified"`
	// Intact means every part is on disk at its recorded size.
	Intact        bool              `json:"intact"`
	Problem       *string           `json:"problem"`
	Bytes         int64             `json:"bytes"`
	SchemaVersion int64             `json:"schema_version"`
	Parts         []AdminBackupPart `json:"parts"`
	// KeyMatches is whether the set's stored connections were sealed with
	// this install's key; null when the set does not say.
	KeyMatches *bool `json:"key_matches"`
}

type AdminBackupPart struct {
	Part  string `json:"part"`
	Bytes int64  `json:"bytes"`
	Count int    `json:"count"`
}

type AdminBackupRecipient struct {
	service.BackupRecipient
	// Kind is "age", "ssh-ed25519" or "ssh-rsa"; empty for a key this build
	// cannot read.
	Kind string `json:"kind"`
	// Fingerprint is an SSH key's SHA256:…, and empty for an age key, whose
	// text is short enough to compare whole.
	Fingerprint string `json:"fingerprint"`
}

type AdminBackupSettingsWrite struct {
	At         string                      `json:"at"`
	KeepDays   int                         `json:"keep_days"`
	Recipients []AdminBackupRecipientWrite `json:"recipients"`
}

type AdminBackupRecipientWrite struct {
	Recipient string `json:"recipient"`
	Label     string `json:"label"`
}

type AdminBackupRehearse struct {
	Identity string `json:"identity"`
	// Passphrase opens an encrypted SSH private key.
	Passphrase string `json:"passphrase"`
}

type AdminBackupRehearsal struct {
	Set         string              `json:"set"`
	Tables      []backup.TableCount `json:"tables"`
	Rows        int64               `json:"rows"`
	Attachments int                 `json:"attachments"`
	Secrets     []string            `json:"secrets"`
	Warnings    []string            `json:"warnings"`
}

func readBackups(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	out, err := backupsResponse(r.Context(), env)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func backupsResponse(ctx context.Context, env *Env) (AdminBackupsResponse, error) {
	backups := env.ServerBackups()
	settings, err := backups.Settings(ctx)
	if err != nil {
		return AdminBackupsResponse{}, err
	}
	recipients := make([]AdminBackupRecipient, 0, len(settings.Recipients))
	for _, one := range settings.Recipients {
		row := AdminBackupRecipient{BackupRecipient: one}
		if parsed, err := backup.ParseRecipient(one.Recipient); err == nil {
			row.Kind, row.Fingerprint = parsed.Kind, parsed.Fingerprint
		}
		recipients = append(recipients, row)
	}
	out := AdminBackupsResponse{
		Enabled:    backups.Enabled(),
		Directory:  backups.Dir,
		At:         settings.At(),
		Timezone:   time.Local.String(),
		KeepDays:   settings.KeepDays,
		Recipients: recipients,
		Sources:    settings.Sources,
		Encrypted:  len(recipients) > 0,
		Running:    backups.Running(),
		Runs:       []AdminBackupRun{},
		Sets:       []AdminBackupSet{},
	}

	runs, err := env.DB.ListBackupRuns(ctx, 10)
	if err != nil {
		return out, err
	}
	for _, run := range runs {
		out.Runs = append(out.Runs, AdminBackupRun{
			Trigger: run.Trigger, Status: string(run.Status), StartedAt: run.StartedAt,
			FinishedAt: run.FinishedAt, SetName: pgconv.NullText(run.SetName),
			Encrypted: run.Encrypted, Bytes: run.Bytes, Error: pgconv.NullText(run.Error),
		})
	}

	if !backups.Enabled() {
		return out, nil
	}
	next, err := backups.NextNightly(ctx, settings)
	if err != nil {
		return out, err
	}
	out.NextRun = &next

	tools := backup.CheckTools(ctx, backups.Source.Database)
	out.DumpVersion, out.ServerVersion = tools.DumpVersion, tools.ServerVersion
	if err := backup.Writable(backups.Dir); err != nil {
		out.Problem = pgconv.NullText(err.Error())
	} else if tools.Problem != "" {
		out.Problem = pgconv.NullText(tools.Problem)
	}

	sets, err := backup.List(backups.Dir)
	if err != nil {
		return out, err
	}
	currentKey := backups.Source.CredentialKeyID
	for _, set := range sets {
		row := AdminBackupSet{
			Name: set.Name, CreatedAt: set.CreatedAt, Trigger: string(set.Trigger),
			Encrypted: set.Encrypted, Recipients: set.Recipients, Verified: set.Verified,
			Intact: set.Intact, Problem: pgconv.NullText(set.Problem),
			Bytes: set.Bytes, SchemaVersion: set.SchemaVersion, Parts: []AdminBackupPart{},
		}
		if row.Recipients == nil {
			row.Recipients = []string{}
		}
		for _, part := range []backup.Part{backup.PartDatabase, backup.PartAttachments, backup.PartSecrets} {
			if file, ok := set.Files[part]; ok {
				row.Parts = append(row.Parts, AdminBackupPart{Part: string(part), Bytes: file.Bytes, Count: file.Count})
			}
		}
		if set.CredentialKeyID != "" {
			matches := set.CredentialKeyID == currentKey
			row.KeyMatches = &matches
		}
		out.Sets = append(out.Sets, row)
	}
	return out, nil
}

func saveBackupSettings(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	var body AdminBackupSettingsWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	hour, minute, ok := service.ParseBackupAt(body.At)
	if !ok {
		return errInvalid("value_error", []string{"body", "at"}, "the time must be HH:MM in 24-hour time")
	}
	if body.KeepDays < 1 || body.KeepDays > 3650 {
		return errInvalid("value_error", []string{"body", "keep_days"},
			"keep backups for between 1 and 3650 days")
	}

	backups := env.ServerBackups()
	current, err := backups.Settings(r.Context())
	if err != nil {
		return err
	}
	added := map[string]time.Time{}
	for _, one := range current.Recipients {
		added[one.Recipient] = one.AddedAt
	}
	recipients := []service.BackupRecipient{}
	seen := map[string]bool{}
	for i, one := range body.Recipients {
		parsed, err := backup.ParseRecipient(one.Recipient)
		if err != nil {
			return errInvalid("value_error", []string{"body", "recipients", strconv.Itoa(i), "recipient"},
				"%q is not an age public key (age1…) or an SSH public key (ssh-ed25519, or ssh-rsa of 2048 bits or more)",
				strings.TrimSpace(one.Recipient))
		}
		key := parsed.Key
		if seen[key] {
			continue
		}
		seen[key] = true
		label := strings.TrimSpace(one.Label)
		if comment := strings.TrimSpace(parsed.Comment); label == "" && len(comment) <= 80 {
			label = comment
		}
		if len(label) > 80 {
			return errInvalid("value_error", []string{"body", "recipients", strconv.Itoa(i), "label"},
				"a label is at most 80 characters")
		}
		at, kept := added[key]
		if !kept || at.IsZero() {
			at = env.now().UTC()
		}
		recipients = append(recipients, service.BackupRecipient{Recipient: key, Label: label, AddedAt: at})
	}

	if err := backups.SaveSettings(r.Context(), hour, minute, body.KeepDays, recipients); err != nil {
		return err
	}
	out, err := backupsResponse(r.Context(), env)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func runBackup(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	err := env.ServerBackups().Start(backup.TriggerManual)
	switch {
	case errors.Is(err, service.ErrBackupsOff):
		return errConflict("backups are off: set BACKUP_DIR (the compose file does)")
	case errors.Is(err, service.ErrBackupRunning):
		return errConflict("a backup is already running")
	case err != nil:
		return err
	}
	out, err := backupsResponse(r.Context(), env)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, out)
}

func rehearseBackup(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	var body AdminBackupRehearse
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	backups := env.ServerBackups()
	if !backups.Enabled() {
		return errConflict("backups are off: set BACKUP_DIR (the compose file does)")
	}
	sets, err := backup.List(backups.Dir)
	if err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	var set *backup.Set
	for i := range sets {
		if sets[i].Name == name {
			set = &sets[i]
			break
		}
	}
	if set == nil {
		return errNotFound("Backup set")
	}

	identities, err := backup.ReadIdentities(strings.NewReader(body.Identity), func() ([]byte, error) {
		return []byte(body.Passphrase), nil
	})
	switch {
	case !set.Encrypted:
	case errors.Is(err, backup.ErrPassphraseRequired):
		return errInvalid("value_error", []string{"body", "passphrase"},
			"the SSH private key is protected by a passphrase; enter it")
	case errors.Is(err, backup.ErrWrongPassphrase):
		return errInvalid("value_error", []string{"body", "passphrase"},
			"the passphrase does not open the SSH private key")
	case err != nil:
		return errInvalid("value_error", []string{"body", "identity"},
			"paste or upload the identity: an age key (AGE-SECRET-KEY-1…) or an SSH private key")
	}
	plan, err := backup.PlanRestore(backup.RestoreRequest{
		Set: *set, HasIdentity: len(identities) > 0, Rehearse: true,
		CurrentKeyID: backups.Source.CredentialKeyID,
	})
	if err != nil {
		return errConflict("%s", err.Error())
	}
	report, err := backup.Rehearse(r.Context(), *set, identities, backups.Source.Database, env.now())
	switch {
	case errors.Is(err, backup.ErrWrongIdentity):
		return errInvalid("value_error", []string{"body", "identity"},
			"that identity is not one this set was encrypted to")
	case err != nil:
		return errConflict("the rehearsal failed: %s", err.Error())
	}
	out := AdminBackupRehearsal{
		Set: report.Set, Tables: report.Tables, Rows: report.Rows(),
		Attachments: report.Attachments, Secrets: report.Secrets, Warnings: plan.Warnings,
	}
	if out.Secrets == nil {
		out.Secrets = []string{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return writeJSON(w, http.StatusOK, out)
}
