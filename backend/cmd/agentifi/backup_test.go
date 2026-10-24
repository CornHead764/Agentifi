package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestBackupAndRestoreCommandsUseSQLite(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	backups := filepath.Join(root, "backups")
	attachments := filepath.Join(data, "attachments")
	require.NoError(t, os.Mkdir(backups, 0700))
	require.NoError(t, os.MkdirAll(attachments, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(attachments, "receipt.pdf"), []byte("original receipt"), 0600))
	t.Setenv("DEBUG", "true")
	t.Setenv("DATA_DIR", data)
	t.Setenv("BACKUP_DIR", backups)
	t.Setenv("BACKUP_RECIPIENTS", "")
	t.Setenv("STORAGE_PATH", "")
	t.Setenv("DATABASE_URL", "postgres://unreachable.invalid/unused")
	cfg, err := config.Load()
	require.NoError(t, err)
	st, err := store.OpenSQLite(t.Context(), data, time.Now)
	require.NoError(t, err)
	st.Close()
	db, err := sqlitedb.Open(t.Context(), filepath.Join(data, "server.db"), time.Now)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(t.Context(), `CREATE TABLE cli_kept (value TEXT); INSERT INTO cli_kept VALUES ('original')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	var out bytes.Buffer
	require.NoError(t, backupCommand(t.Context(), cfg, nil, &out))
	require.Contains(t, out.String(), "wrote ")
	require.Contains(t, out.String(), "UNENCRYPTED")
	sets, err := backup.List(backups)
	require.NoError(t, err)
	require.Len(t, sets, 1)
	require.Equal(t, 1, sets[0].Files[backup.PartAttachments].Count)
	name := sets[0].Name
	out.Reset()
	require.NoError(t, restoreCommand(t.Context(), cfg, []string{"--rehearse"}, strings.NewReader(""), &out))
	require.Contains(t, out.String(), "rehearsal complete")
	db, err = sqlitedb.Open(t.Context(), filepath.Join(data, "server.db"), time.Now)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(t.Context(), `DELETE FROM cli_kept`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.NoError(t, os.WriteFile(filepath.Join(attachments, "receipt.pdf"), []byte("changed receipt"), 0600))
	holder, err := sqlitedb.AcquireLock(filepath.Join(data, "server.lock"))
	require.NoError(t, err)
	err = restoreCommand(t.Context(), cfg, []string{"--from", name, "--confirm", name}, strings.NewReader(""), &out)
	require.ErrorContains(t, err, "stop the server")
	require.NoError(t, holder.Close())
	out.Reset()
	require.NoError(t, restoreCommand(t.Context(), cfg, []string{"--from", name, "--confirm", name}, strings.NewReader(""), &out))
	require.Contains(t, out.String(), "restore complete")
	require.Contains(t, out.String(), ".before-restore-")
	db, err = sqlitedb.Open(t.Context(), filepath.Join(data, "server.db"), time.Now)
	require.NoError(t, err)
	defer db.Close()
	var value string
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT value FROM cli_kept`).Scan(&value))
	require.Equal(t, "original", value)
	receipt, err := os.ReadFile(filepath.Join(attachments, "receipt.pdf"))
	require.NoError(t, err)
	require.Equal(t, "original receipt", string(receipt))
}
