package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const containerMountinfo = `27 1 0:25 / / rw,relatime - overlay overlay rw
28 27 0:26 / /proc rw,nosuid - proc proc rw
29 27 259:3 /root/Docker/agentifi/data/attachments /data/attachments rw,relatime - ext4 /dev/nvme0n1p3 rw
30 27 259:3 /srv/agentifi\040data /srv/agentifi\040data rw,relatime - ext4 /dev/nvme0n1p3 rw
`

func TestMountedBelowRoot(t *testing.T) {
	for dir, want := range map[string]bool{
		"/data/db":               false,
		"/data/attachments":      true,
		"/data/attachments/x":    true,
		"/data/attachmentsx":     false,
		"/srv/agentifi data/db":  true,
		"/procfs-lookalike/data": false,
	} {
		require.Equal(t, want, mountedBelowRoot(containerMountinfo, dir), dir)
	}
}

func TestRequireMountedDataDir(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, ".dockerenv")
	info := filepath.Join(root, "mountinfo")
	dir := filepath.Join(root, "db")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	oldMarkers, oldInfo := containerMarkers, mountInfoPath
	t.Cleanup(func() { containerMarkers, mountInfoPath = oldMarkers, oldInfo })
	containerMarkers, mountInfoPath = []string{marker}, info

	require.NoError(t, requireMountedDataDir(dir), "outside a container nothing is checked")

	require.NoError(t, os.WriteFile(marker, nil, 0o600))
	require.NoError(t, os.WriteFile(info, []byte(containerMountinfo), 0o600))
	require.ErrorContains(t, requireMountedDataDir(dir), "not on a mounted volume")

	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	mounted := containerMountinfo + "31 27 259:3 /x " + resolved + " rw - ext4 /dev/x rw\n"
	require.NoError(t, os.WriteFile(info, []byte(mounted), 0o600))
	require.NoError(t, requireMountedDataDir(dir))
}
