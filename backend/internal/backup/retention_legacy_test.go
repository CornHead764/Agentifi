package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetentionPreservesLegacyPostgresSets(t *testing.T) {
	now := day("2026-06-30 03:30")
	legacy := set("legacy", "2025-01-01 03:30", false)
	legacy.Format = 1
	latest := set("latest", "2026-06-01 03:30", true)
	latest.Format = FormatVersion
	expired := set("expired", "2026-01-01 03:30", true)
	expired.Format = FormatVersion
	require.Equal(t, []string{"expired"}, names(PlanRetention([]Set{latest, expired, legacy}, now, 14)))
	// Even an intact legacy set must not consume the newest-SQLite exemption.
	legacy.Intact = true
	require.Empty(t, PlanRetention([]Set{legacy, latest}, now, 14))
}

func TestPruneLeavesLegacyPgDumpOnDisk(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"legacy", "latest", "expired"} {
		root := filepath.Join(dir, name)
		require.NoError(t, os.Mkdir(root, 0700))
		manifest := Manifest{Format: FormatVersion, CreatedAt: day("2026-01-01 03:30"), Files: map[Part]File{PartDatabase: {Path: "database.tar", Bytes: 0}}}
		if name == "legacy" {
			manifest.Format = 1
			manifest.CreatedAt = day("2025-01-01 03:30")
			manifest.Files[PartDatabase] = File{Path: "database.dump", Bytes: 7}
			require.NoError(t, os.WriteFile(filepath.Join(root, "database.dump"), []byte("pg_dump"), 0600))
		} else {
			require.NoError(t, os.WriteFile(filepath.Join(root, "database.tar"), nil, 0600))
		}
		if name == "latest" {
			manifest.CreatedAt = day("2026-06-01 03:30")
		}
		require.NoError(t, writeManifest(root, manifest))
	}
	removed, err := Prune(t.Context(), dir, day("2026-06-30 03:30"), 14)
	require.NoError(t, err)
	require.Equal(t, []string{"expired"}, removed)
	require.FileExists(t, filepath.Join(dir, "legacy", "database.dump"))
	require.DirExists(t, filepath.Join(dir, "latest"))
}
