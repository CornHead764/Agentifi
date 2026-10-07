// Package buildinfo names the commit this binary was built from.
package buildinfo

import (
	"runtime/debug"
)

// Commit and BuiltAt are set at link time by the image build:
//
//	-ldflags "-X github.com/CornHead764/agentifi/backend/internal/buildinfo.Commit=<sha>"
//
// The image is built from a tarball with no .git, so the toolchain's own
// stamp is absent there and these are the only record.
var (
	Commit  string
	BuiltAt string
)

type Info struct {
	// Commit is empty for a build that was told none and carries no VCS
	// stamp.
	Commit string
	// BuiltAt is when the image was built, or the commit's time for a stamped
	// local build; empty when neither is known.
	BuiltAt string
	// Modified is true for a local build from a working tree with changes.
	Modified  bool
	GoVersion string
}

// Read prefers the link-time values and falls back to the stamp `go build`
// records inside a git checkout.
func Read() Info {
	info := Info{Commit: Commit, BuiltAt: BuiltAt}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.GoVersion = build.GoVersion
	if info.Commit != "" {
		return info
	}
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Commit = setting.Value
		case "vcs.time":
			if info.BuiltAt == "" {
				info.BuiltAt = setting.Value
			}
		case "vcs.modified":
			info.Modified = setting.Value == "true"
		}
	}
	return info
}
