package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}
	mountInfoPath    = "/proc/self/mountinfo"
)

// requireMountedDataDir refuses a DATA_DIR inside a container's own writable
// layer, which is discarded when the container is recreated.
func requireMountedDataDir(dir string) error {
	inContainer := false
	for _, marker := range containerMarkers {
		if _, err := os.Stat(marker); err == nil {
			inContainer = true
		}
	}
	if !inContainer {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	mountinfo, err := os.ReadFile(mountInfoPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", mountInfoPath, err)
	}
	if mountedBelowRoot(string(mountinfo), resolved) {
		return nil
	}
	return fmt.Errorf("DATA_DIR %s is not on a mounted volume, so the imported data would be lost when this container is recreated; "+
		"mount a volume there first (docs/operations.md, Upgrading from Postgres)", dir)
}

func mountedBelowRoot(mountinfo, dir string) bool {
	for _, line := range strings.Split(mountinfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		point := unescapeMountPoint(fields[4])
		if point == "/" {
			continue
		}
		if dir == point || strings.HasPrefix(dir, point+"/") {
			return true
		}
	}
	return false
}

var mountPointEscapes = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

func unescapeMountPoint(s string) string { return mountPointEscapes.Replace(s) }
