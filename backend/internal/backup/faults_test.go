package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// failRename replaces rename for one test.
func failRename(t *testing.T, replacement func(from, to string) error) {
	t.Helper()
	rename = replacement
	t.Cleanup(func() { rename = os.Rename })
}

func TestAHouseholdThatFailsToPublishLeavesNoRestoredWALBesideTheLiveFile(t *testing.T) {
	data, scratch, aside := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "aside")
	require.NoError(t, os.Mkdir(aside, 0o700))
	id := "11111111-1111-4111-8111-111111111111"
	writeTree(t, filepath.Join(data, "households"), map[string]string{id + ".db": "the live file"})
	writeTree(t, filepath.Join(scratch, "households"), map[string]string{id + ".db": "restored", id + ".db-wal": "restored wal"})
	failRename(t, func(from, to string) error {
		if strings.HasSuffix(to, id+".db") {
			return errors.New("an invented failure")
		}
		return os.Rename(from, to)
	})

	require.ErrorContains(t, publishHousehold(data, scratch, aside, id), "an invented failure")
	require.Equal(t, map[string]string{id + ".db": "the live file"}, readTree(t, filepath.Join(data, "households")))
}

func TestWritingAndCollectingWaitForTheBackupsDirectorysLock(t *testing.T) {
	data, dir := t.TempDir(), t.TempDir()
	st, err := store.OpenSQLite(t.Context(), data, time.Now)
	require.NoError(t, err)
	st.Close()
	unlock, err := lockBackups(t.Context(), dir)
	require.NoError(t, err)
	defer unlock()
	short, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = Restore(short, RestoreOptions{Set: Set{Dir: filepath.Join(dir, "a-set")}, DataDir: data, Now: time.Now()})
	require.ErrorIs(t, err, context.DeadlineExceeded, "a restore reads its set under the backups directory's lock")

	// The data directory's own lock is not this one: another install
	// sharing the backups directory holds a lock of its own.
	unlockData, err := Lock(t.Context(), data)
	require.NoError(t, err)
	defer unlockData()
	waiting := func() context.Context {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		t.Cleanup(cancel)
		return ctx
	}
	_, err = Write(waiting(), dir, Source{DataDir: data}, nil, TriggerManual, time.Now())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = Prune(waiting(), dir, time.Now(), 14)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = CollectObjects(waiting(), dir)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	unlock()
	_, err = Write(t.Context(), dir, Source{DataDir: data}, nil, TriggerManual, time.Now())
	require.NoError(t, err)
}

func TestASetThatIsNotIntactOnceWrittenIsAFailedRun(t *testing.T) {
	data, dir := t.TempDir(), t.TempDir()
	st, err := store.OpenSQLite(t.Context(), data, time.Now)
	require.NoError(t, err)
	st.Close()
	writeTree(t, filepath.Join(data, "attachments"), map[string]string{"space/receipt.pdf": "an invented receipt"})
	failRename(t, func(from, to string) error {
		if err := os.Rename(from, to); err != nil {
			return err
		}
		return os.Truncate(to, 1)
	})

	_, err = Write(t.Context(), dir, Source{DataDir: data}, nil, TriggerManual, time.Now())
	require.ErrorContains(t, err, "not intact")
	sets, err := List(dir)
	require.NoError(t, err)
	require.Empty(t, sets)
}
