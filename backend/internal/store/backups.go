package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// The history of the server's own backup runs. Server-wide, like
// server_settings: no space owns a backup.

type BackupRunStatus string

const (
	BackupRunRunning   BackupRunStatus = "running"
	BackupRunSucceeded BackupRunStatus = "succeeded"
	BackupRunFailed    BackupRunStatus = "failed"
	// BackupRunSkipped is a run that could not start, for example because
	// there is no directory to write to or another backup is running.
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
		one      BackupRun
		status   string
		setName  sql.Null[string]
		bytes    sql.Null[int64]
		message  sql.Null[string]
		started  int64
		finished sql.Null[int64]
	)
	if err := row.Scan(&one.ID, &one.Trigger, &status, &started, &finished,
		&setName, &one.Encrypted, &bytes, &message); err != nil {
		return BackupRun{}, err
	}
	one.StartedAt, one.FinishedAt = fromUnixMillis(started), nullableTime(finished)
	one.Status = BackupRunStatus(status)
	one.SetName = setName.V
	one.Error = message.V
	one.Bytes = bytes.V
	return one, nil
}

// StartBackupRun records a run as running.
func (s *Store) StartBackupRun(ctx context.Context, trigger string, at time.Time) (BackupRun, error) {
	run := BackupRun{ID: uuid.New(), Trigger: trigger, Status: BackupRunRunning, StartedAt: at}
	db, err := s.server()
	if err != nil {
		return run, err
	}
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO backup_runs (id, trigger, status, started_at, encrypted) VALUES (?, ?, ?, ?, 0)`,
		run.ID, run.Trigger, string(run.Status), unixMillis(run.StartedAt))
	return run, wrap("store: start backup run", err)
}

// FinishBackupRun records how a run ended and trims the history.
func (s *Store) FinishBackupRun(ctx context.Context, run BackupRun) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	var bytes *int64
	if run.Bytes > 0 {
		bytes = &run.Bytes
	}
	err = sqliteExecOne(ctx, db.Write(), "store: finish backup run",
		`UPDATE backup_runs SET status = ?, finished_at = ?, set_name = ?, encrypted = ?, bytes = ?, error = ? WHERE id = ?`,
		string(run.Status), nullableMillis(run.FinishedAt), NullText(run.SetName), run.Encrypted,
		bytes, NullText(run.Error), run.ID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`DELETE FROM backup_runs WHERE id NOT IN
		     (SELECT id FROM backup_runs ORDER BY started_at DESC LIMIT ?)`, keptBackupRuns)
	return wrap("store: trim backup runs", err)
}

// RecordSkippedBackupRun records a run that could not start.
func (s *Store) RecordSkippedBackupRun(ctx context.Context, trigger string, at time.Time, reason string) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO backup_runs (id, trigger, status, started_at, finished_at, error, encrypted)
		 VALUES (?, ?, ?, ?, ?, ?, 0)`,
		uuid.New(), trigger, string(BackupRunSkipped), unixMillis(at), unixMillis(at), reason)
	return wrap("store: record skipped backup run", err)
}

// FailInterruptedBackupRuns ends every run still marked running. Called when
// the server starts, before it can have started one of its own.
func (s *Store) FailInterruptedBackupRuns(ctx context.Context, at time.Time) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE backup_runs SET status = ?, finished_at = ?,
		        error = 'the server stopped before the run finished' WHERE status = ?`,
		string(BackupRunFailed), unixMillis(at), string(BackupRunRunning))
	return wrap("store: fail interrupted backup runs", err)
}

// ListBackupRuns is the newest runs first.
func (s *Store) ListBackupRuns(ctx context.Context, limit int) ([]BackupRun, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list backup runs", scanBackupRun,
		`SELECT `+backupRunColumns+` FROM backup_runs ORDER BY started_at DESC, id LIMIT ?`, limit)
}

// LastBackupRunStarted is when the newest run with this trigger started, or
// nil when there has been none.
func (s *Store) LastBackupRunStarted(ctx context.Context, trigger string) (*time.Time, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	var at sql.Null[int64]
	err = db.Read().QueryRowContext(ctx,
		`SELECT max(started_at) FROM backup_runs WHERE trigger = ?`, trigger).Scan(&at)
	return nullableTime(at), wrap("store: last backup run", err)
}
