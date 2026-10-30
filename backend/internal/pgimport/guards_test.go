package pgimport

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestImportRefusesLegacyWriter(t *testing.T) {
	source, o := fixture(t)
	tx, err := source.Begin(t.Context())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(t.Context(), "UPDATE users SET theme='dark'")
	require.NoError(t, err)
	_, err = Import(t.Context(), o)
	require.ErrorContains(t, err, "stop the old agentifi")
	require.NoFileExists(t, filepath.Join(o.Data, "server.db"))
	require.NoError(t, tx.Rollback(t.Context()))
	_, err = Import(t.Context(), o)
	require.NoError(t, err)
}

func TestSourceLockPreventsNewLegacyWrites(t *testing.T) {
	source, o := fixture(t)
	reader, err := pgx.Connect(t.Context(), o.From)
	require.NoError(t, err)
	defer reader.Close(context.Background())
	tx, err := reader.Begin(t.Context())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	require.NoError(t, lockSource(t.Context(), tx))
	_, err = source.Exec(t.Context(), "SET lock_timeout='100ms'")
	require.NoError(t, err)
	_, err = source.Exec(t.Context(), "UPDATE users SET theme='dark'")
	require.ErrorContains(t, err, "lock timeout")
	require.NoError(t, tx.Rollback(t.Context()))
	_, err = source.Exec(t.Context(), "UPDATE users SET theme='dark'")
	require.NoError(t, err)
}

func TestImportCompletionMarkerSurvivesMissingDestination(t *testing.T) {
	_, o := fixture(t)
	o.RecordCompletion = true
	_, err := Import(t.Context(), o)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(o.Data))
	_, err = HasData(t.Context(), o.From)
	require.ErrorContains(t, err, "already migrated")
	require.ErrorContains(t, err, "restore")
}

func TestFailedImportDoesNotMarkCompletion(t *testing.T) {
	source, o := fixture(t)
	_, err := source.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=10")
	require.NoError(t, err)
	o.RecordCompletion = true
	_, err = Import(t.Context(), o)
	require.ErrorContains(t, err, "source migration")
	found, err := HasData(t.Context(), o.From)
	require.NoError(t, err)
	require.True(t, found)
}

func TestFailedPublicationClearsCompletion(t *testing.T) {
	_, o := fixture(t)
	o.RecordCompletion = true
	publish = func(string, string, string, io.Writer) error { return errors.New("disk full") }
	_, err := Import(t.Context(), o)
	publish = Publish
	require.ErrorContains(t, err, "disk full")
	require.NoFileExists(t, filepath.Join(o.Data, "server.db"))
	found, err := HasData(t.Context(), o.From)
	require.NoError(t, err)
	require.True(t, found)
	_, err = Import(t.Context(), o)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(o.Data, "server.db"))
}
