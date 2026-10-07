package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func clearBackupRuns(t *testing.T) {
	t.Helper()
	_, err := db(t).db.Exec(t.Context(), `DELETE FROM backup_runs`)
	require.NoError(t, err)
}

func TestABackupRunIsRecordedFromStartToFinish(t *testing.T) {
	clearBackupRuns(t)
	st := db(t)
	at := time.Date(2026, 3, 3, 3, 30, 0, 0, time.UTC)

	none, err := st.LastBackupRunStarted(t.Context(), "nightly")
	require.NoError(t, err)
	require.Nil(t, none)

	run, err := st.StartBackupRun(t.Context(), "nightly", at)
	require.NoError(t, err)
	finished := at.Add(time.Minute)
	run.Status, run.FinishedAt, run.SetName, run.Encrypted, run.Bytes =
		BackupRunSucceeded, &finished, "2026-03-03_033000_nightly", true, 4096
	require.NoError(t, st.FinishBackupRun(t.Context(), run))
	require.NoError(t, st.RecordSkippedBackupRun(t.Context(), "manual", at.Add(time.Hour), "no pg_dump"))

	runs, err := st.ListBackupRuns(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	require.Equal(t, BackupRunSkipped, runs[0].Status)
	require.Equal(t, "no pg_dump", runs[0].Error)
	require.Equal(t, BackupRunSucceeded, runs[1].Status)
	require.Equal(t, "2026-03-03_033000_nightly", runs[1].SetName)
	require.True(t, runs[1].Encrypted)
	require.EqualValues(t, 4096, runs[1].Bytes)

	last, err := st.LastBackupRunStarted(t.Context(), "nightly")
	require.NoError(t, err)
	require.True(t, last.Equal(at))
}

func TestARunTheServerDiedDuringIsClosedAsFailed(t *testing.T) {
	clearBackupRuns(t)
	st := db(t)
	at := time.Date(2026, 3, 3, 3, 30, 0, 0, time.UTC)
	_, err := st.StartBackupRun(t.Context(), "nightly", at)
	require.NoError(t, err)
	require.NoError(t, st.FailInterruptedBackupRuns(t.Context(), at.Add(time.Hour)))

	runs, err := st.ListBackupRuns(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, BackupRunFailed, runs[0].Status)
	require.Contains(t, runs[0].Error, "stopped before the run finished")
}

func TestTheRunHistoryIsTrimmed(t *testing.T) {
	clearBackupRuns(t)
	st := db(t)
	at := time.Date(2026, 1, 1, 3, 30, 0, 0, time.UTC)
	var last BackupRun
	for i := 0; i <= keptBackupRuns; i++ {
		run, err := st.StartBackupRun(t.Context(), "nightly", at.AddDate(0, 0, i))
		require.NoError(t, err)
		last = run
	}
	finished := last.StartedAt
	last.Status, last.FinishedAt = BackupRunSucceeded, &finished
	require.NoError(t, st.FinishBackupRun(t.Context(), last))

	var count int
	require.NoError(t, st.db.QueryRow(t.Context(), `SELECT count(*) FROM backup_runs`).Scan(&count))
	require.Equal(t, keptBackupRuns, count)
}
