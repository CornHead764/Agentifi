package domain

import (
	"strconv"
	"strings"
)

// Version is a release number, major.minor.patch. A pre-release suffix
// ("1.2.0-rc.1") orders before the release it leads up to.
type Version struct {
	Major, Minor, Patch int
	PreRelease          string
}

// ParseVersion reads "1.2.3" or "v1.2.3", with an optional "-pre" suffix and
// "+build" metadata, which is ignored. Anything else is not a version.
func ParseVersion(text string) (Version, bool) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "v")
	text, _, _ = strings.Cut(text, "+")
	core, pre, _ := strings.Cut(text, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var numbers [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, false
		}
		numbers[i] = n
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2], PreRelease: pre}, true
}

// Compare is negative when v is older than other, zero when they are the
// same release, and positive when v is newer. Two pre-releases of one
// release compare by their suffix as text.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		if pair[0] != pair[1] {
			return pair[0] - pair[1]
		}
	}
	switch {
	case v.PreRelease == other.PreRelease:
		return 0
	case v.PreRelease == "":
		return 1
	case other.PreRelease == "":
		return -1
	}
	return strings.Compare(v.PreRelease, other.PreRelease)
}

// UpdateAvailable is true when latest is a newer release than running. A
// running version that does not parse, as on a build that was not told one,
// has no update to compare against.
func UpdateAvailable(running, latest string) bool {
	current, ok := ParseVersion(running)
	if !ok {
		return false
	}
	newest, ok := ParseVersion(latest)
	return ok && newest.Compare(current) > 0
}
