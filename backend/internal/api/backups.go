package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The server's backups, from the administration screen: the schedule, the
// retention and the encryption recipients, the sets on disk, a run on request,
// and a rehearsal of a restore into a scratch file.
//
// A restore over the live database is not here. It renames a new file over
// the one this process has open, whose syncs, imports and automations would
// go on writing into the replaced file, so it runs from `agentifi restore`
// with the application stopped (it refuses otherwise); the screen gives the
// exact command.
//
// An identity pasted for a rehearsal, and an SSH key's passphrase, are used
// for that request and dropped. They are never stored, logged or echoed.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewAdminBackupServiceHandler(adminBackupService{env}, opts...)
	})
}

type adminBackupService struct{ env *Env }

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
		Database:        backup.SQLite{Path: db.Pool().Path()},
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

func (s adminBackupService) GetBackups(ctx context.Context, _ *agentifiv1.GetBackupsRequest) (*agentifiv1.GetBackupsResponse, error) {
	out, err := backupsProto(ctx, s.env)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetBackupsResponse{Backups: out}, nil
}

func backupsProto(ctx context.Context, env *Env) (*agentifiv1.Backups, error) {
	backups := env.ServerBackups()
	settings, err := backups.Settings(ctx)
	if err != nil {
		return nil, err
	}
	recipients := make([]*agentifiv1.BackupRecipient, 0, len(settings.Recipients))
	for _, one := range settings.Recipients {
		row := &agentifiv1.BackupRecipient{
			Recipient: one.Recipient, Label: one.Label, AddedAt: timestamppb.New(one.AddedAt),
		}
		if parsed, err := backup.ParseRecipient(one.Recipient); err == nil {
			row.Kind, row.Fingerprint = parsed.Kind, parsed.Fingerprint
		}
		recipients = append(recipients, row)
	}
	out := &agentifiv1.Backups{
		Enabled:    backups.Enabled(),
		Directory:  backups.Dir,
		At:         settings.At(),
		Timezone:   time.Local.String(),
		KeepDays:   int32(settings.KeepDays),
		Recipients: recipients,
		Sources:    settings.Sources,
		Encrypted:  len(recipients) > 0,
		Running:    backups.Running(),
		Runs:       []*agentifiv1.BackupRun{},
		Sets:       []*agentifiv1.BackupSet{},
	}

	runs, err := env.DB.ListBackupRuns(ctx, 10)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		out.Runs = append(out.Runs, &agentifiv1.BackupRun{
			Trigger: run.Trigger, Status: string(run.Status), StartedAt: timestamppb.New(run.StartedAt),
			FinishedAt: pbTimeOrNil(run.FinishedAt), SetName: dbconv.NullText(run.SetName),
			Encrypted: run.Encrypted, Bytes: run.Bytes, Error: dbconv.NullText(run.Error),
		})
	}

	if !backups.Enabled() {
		return out, nil
	}
	next, err := backups.NextNightly(ctx, settings)
	if err != nil {
		return nil, err
	}
	out.NextRun = timestamppb.New(next)

	tools := backup.CheckTools(ctx, backups.Source.Database)
	if err := backup.Writable(backups.Dir); err != nil {
		out.Problem = dbconv.NullText(err.Error())
	} else if tools.Problem != "" {
		out.Problem = dbconv.NullText(tools.Problem)
	}

	sets, err := backup.List(backups.Dir)
	if err != nil {
		return nil, err
	}
	currentKey := backups.Source.CredentialKeyID
	for _, set := range sets {
		row := &agentifiv1.BackupSet{
			Name: set.Name, CreatedAt: timestamppb.New(set.CreatedAt), Trigger: string(set.Trigger),
			Encrypted: set.Encrypted, Recipients: set.Recipients, Verified: set.Verified,
			Intact: set.Intact, Problem: dbconv.NullText(set.Problem),
			Bytes: set.Bytes, SchemaVersion: set.SchemaVersion, Parts: []*agentifiv1.BackupPart{},
		}
		for _, part := range []backup.Part{backup.PartDatabase, backup.PartAttachments, backup.PartSecrets} {
			if file, ok := set.Files[part]; ok {
				row.Parts = append(row.Parts, &agentifiv1.BackupPart{
					Part: string(part), Bytes: file.Bytes, Count: int32(file.Count),
				})
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

func (s adminBackupService) UpdateBackupSettings(ctx context.Context, req *agentifiv1.UpdateBackupSettingsRequest) (*agentifiv1.UpdateBackupSettingsResponse, error) {
	hour, minute, ok := service.ParseBackupAt(req.GetAt())
	if !ok {
		return nil, errInvalid("value_error", []string{"body", "at"}, "the time must be HH:MM in 24-hour time")
	}
	keepDays := int(req.GetKeepDays())
	if keepDays < 1 || keepDays > 3650 {
		return nil, errInvalid("value_error", []string{"body", "keep_days"},
			"keep backups for between 1 and 3650 days")
	}

	backups := s.env.ServerBackups()
	current, err := backups.Settings(ctx)
	if err != nil {
		return nil, err
	}
	added := map[string]time.Time{}
	for _, one := range current.Recipients {
		added[one.Recipient] = one.AddedAt
	}
	recipients := []service.BackupRecipient{}
	seen := map[string]bool{}
	for i, one := range req.GetRecipients() {
		parsed, err := backup.ParseRecipient(one.GetRecipient())
		if err != nil {
			return nil, errInvalid("value_error", []string{"body", "recipients", strconv.Itoa(i), "recipient"},
				"%q is not an age public key (age1…) or an SSH public key (ssh-ed25519, or ssh-rsa of 2048 bits or more)",
				strings.TrimSpace(one.GetRecipient()))
		}
		key := parsed.Key
		if seen[key] {
			continue
		}
		seen[key] = true
		label := strings.TrimSpace(one.GetLabel())
		if comment := strings.TrimSpace(parsed.Comment); label == "" && len(comment) <= 80 {
			label = comment
		}
		if len(label) > 80 {
			return nil, errInvalid("value_error", []string{"body", "recipients", strconv.Itoa(i), "label"},
				"a label is at most 80 characters")
		}
		at, kept := added[key]
		if !kept || at.IsZero() {
			at = s.env.now().UTC()
		}
		recipients = append(recipients, service.BackupRecipient{Recipient: key, Label: label, AddedAt: at})
	}

	if err := backups.SaveSettings(ctx, hour, minute, keepDays, recipients); err != nil {
		return nil, err
	}
	out, err := backupsProto(ctx, s.env)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateBackupSettingsResponse{Backups: out}, nil
}

func (s adminBackupService) RunBackup(ctx context.Context, _ *agentifiv1.RunBackupRequest) (*agentifiv1.RunBackupResponse, error) {
	err := s.env.ServerBackups().Start(backup.TriggerManual)
	switch {
	case errors.Is(err, service.ErrBackupsOff):
		return nil, errConflict("backups are off: set BACKUP_DIR (the compose file does)")
	case errors.Is(err, service.ErrBackupRunning):
		return nil, errConflict("a backup is already running")
	case err != nil:
		return nil, err
	}
	out, err := backupsProto(ctx, s.env)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.RunBackupResponse{Backups: out}, nil
}

func (s adminBackupService) RehearseBackup(ctx context.Context, req *agentifiv1.RehearseBackupRequest) (*agentifiv1.RehearseBackupResponse, error) {
	backups := s.env.ServerBackups()
	if !backups.Enabled() {
		return nil, errConflict("backups are off: set BACKUP_DIR (the compose file does)")
	}
	sets, err := backup.List(backups.Dir)
	if err != nil {
		return nil, err
	}
	var set *backup.Set
	for i := range sets {
		if sets[i].Name == req.GetName() {
			set = &sets[i]
			break
		}
	}
	if set == nil {
		return nil, errNotFound("Backup set")
	}

	identities, err := backup.ReadIdentities(strings.NewReader(req.GetIdentity()), func() ([]byte, error) {
		return []byte(req.GetPassphrase()), nil
	})
	switch {
	case !set.Encrypted:
	case errors.Is(err, backup.ErrPassphraseRequired):
		return nil, errInvalid("value_error", []string{"body", "passphrase"},
			"the SSH private key is protected by a passphrase; enter it")
	case errors.Is(err, backup.ErrWrongPassphrase):
		return nil, errInvalid("value_error", []string{"body", "passphrase"},
			"the passphrase does not open the SSH private key")
	case err != nil:
		return nil, errInvalid("value_error", []string{"body", "identity"},
			"paste or upload the identity: an age key (AGE-SECRET-KEY-1…) or an SSH private key")
	}
	plan, err := backup.PlanRestore(backup.RestoreRequest{
		Set: *set, HasIdentity: len(identities) > 0, Rehearse: true,
		CurrentKeyID: backups.Source.CredentialKeyID,
	})
	if err != nil {
		return nil, errConflict("%s", err.Error())
	}
	report, err := backup.Rehearse(ctx, *set, identities, backups.Source.Database, s.env.now())
	switch {
	case errors.Is(err, backup.ErrWrongIdentity):
		return nil, errInvalid("value_error", []string{"body", "identity"},
			"that identity is not one this set was encrypted to")
	case err != nil:
		return nil, errConflict("the rehearsal failed: %s", err.Error())
	}
	out := &agentifiv1.RehearseBackupResponse{
		Set: report.Set, Tables: make([]*agentifiv1.BackupTableCount, 0, len(report.Tables)),
		Rows: report.Rows(), Attachments: int32(report.Attachments),
		Secrets: report.Secrets, Warnings: plan.Warnings,
	}
	for _, table := range report.Tables {
		out.Tables = append(out.Tables, &agentifiv1.BackupTableCount{Table: table.Table, Rows: table.Rows})
	}
	return out, nil
}
