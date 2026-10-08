package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
)

// The history of the server's own backup runs. Server-wide, like
// server_settings: no space owns a backup.

type BackupRunStatus string

const (
	BackupRunRunning   BackupRunStatus = "running"
	BackupRunSucceeded BackupRunStatus = "succeeded"
	BackupRunFailed    BackupRunStatus = "failed"
	// BackupRunSkipped is a run that could not start: no directory to write
	// to, or no pg_dump that can read the server.
	BackupRunSkipped BackupRunStatus = "skipped"
)

type BackupRun struct {
	ID         uuid.UUID
	Trigger    string
	Status     BackupRunStatus
	StartedAt  time.Time
	FinishedAt *time.Time
	SetName    string
	Encrypted  bool
	Bytes      int64
	Error      string
}

// keptBackupRuns is how much history survives; the sets on disk are the
// record that matters.
const keptBackupRuns = 200

const backupRunColumns = `id, trigger, status, started_at, finished_at, set_name, encrypted, bytes, error`

func scanBackupRun(row scanner) (BackupRun, error) {
	var (
		one     BackupRun
		status  string
		setName *string
		bytes   *int64
		message *string
	)
	if err := row.Scan(&one.ID, &one.Trigger, &status, &one.StartedAt, &one.FinishedAt,
		&setName, &one.Encrypted, &bytes, &message); err != nil {
		return BackupRun{}, err
	}
	one.Status = BackupRunStatus(status)
	one.SetName = Deref(setName)
	one.Error = Deref(message)
	if bytes != nil {
		one.Bytes = *bytes
	}
	return one, nil
}

// StartBackupRun records a run as running.
func (s *Store) StartBackupRun(ctx context.Context, trigger string, at time.Time) (BackupRun, error) {
	run := BackupRun{ID: uuid.New(), Trigger: trigger, Status: BackupRunRunning, StartedAt: at}
	_, err := s.db.Exec(ctx,
		`INSERT INTO backup_runs (id, trigger, status, started_at) VALUES ($1, $2, $3, $4)`,
		run.ID, run.Trigger, string(run.Status), run.StartedAt)
	return run, wrap("store: start backup run", err)
}

// FinishBackupRun records how a run ended and trims the history.
func (s *Store) FinishBackupRun(ctx context.Context, run BackupRun) error {
	var bytes *int64
	if run.Bytes > 0 {
		bytes = &run.Bytes
	}
	err := s.execOne(ctx, "store: finish backup run",
		`UPDATE backup_runs
		    SET status = $2, finished_at = $3, set_name = $4, encrypted = $5, bytes = $6, error = $7
		  WHERE id = $1`,
		run.ID, string(run.Status), run.FinishedAt, dbconv.NullText(run.SetName), run.Encrypted,
		bytes, dbconv.NullText(run.Error))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`DELETE FROM backup_runs WHERE id NOT IN
		     (SELECT id FROM backup_runs ORDER BY started_at DESC LIMIT $1)`, keptBackupRuns)
	return wrap("store: trim backup runs", err)
}

// RecordSkippedBackupRun records a run that could not start.
func (s *Store) RecordSkippedBackupRun(ctx context.Context, trigger string, at time.Time, reason string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO backup_runs (id, trigger, status, started_at, finished_at, error)
		 VALUES ($1, $2, $3, $4, $4, $5)`,
		uuid.New(), trigger, string(BackupRunSkipped), at, reason)
	return wrap("store: record skipped backup run", err)
}

// FailInterruptedBackupRuns ends every run still marked running. Called when
// the server starts, before it can have started one of its own.
func (s *Store) FailInterruptedBackupRuns(ctx context.Context, at time.Time) error {
	_, err := s.db.Exec(ctx,
		`UPDATE backup_runs SET status = $1, finished_at = $2,
		        error = 'the server stopped before the run finished'
		  WHERE status = $3`,
		string(BackupRunFailed), at, string(BackupRunRunning))
	return wrap("store: fail interrupted backup runs", err)
}

// ListBackupRuns is the newest runs first.
func (s *Store) ListBackupRuns(ctx context.Context, limit int) ([]BackupRun, error) {
	return queryAll(ctx, s.db, "store: list backup runs", scanBackupRun,
		`SELECT `+backupRunColumns+` FROM backup_runs ORDER BY started_at DESC, id LIMIT $1`, limit)
}

// LastBackupRunStarted is when the newest run with this trigger started, or
// nil when there has been none.
func (s *Store) LastBackupRunStarted(ctx context.Context, trigger string) (*time.Time, error) {
	var at *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT max(started_at) FROM backup_runs WHERE trigger = $1`, trigger).Scan(&at)
	return at, wrap("store: last backup run", err)
}
