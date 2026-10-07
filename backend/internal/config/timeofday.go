package config

import (
	"fmt"
	"strconv"
	"strings"
)

// TimeOfDay is a wall-clock time with no date, for the daily sync window.
//
// It keeps the string it was parsed from so a malformed value can be reported
// back as the operator wrote it: a config error that says `SYNC_AT must be
// HH:MM, got "4pm"` is actionable, and one that says `got "00:00"` — the zero
// value a failed parse would otherwise leave — is not.
type TimeOfDay struct {
	Hour   int
	Minute int
	Raw    string
	ok     bool
}

// ParseTimeOfDay reads "HH:MM" in 24-hour time. An unreadable value comes back
// invalid rather than as an error, so Load can collect it and validate() can
// report it beside every other setting instead of failing on the first one.
//
// Exported because a Config assembled in code — a test, a one-off command —
// needs to be able to hold a valid window, and an unexported parser leaves it
// with the zero value, which is 00:00 and invalid.
func ParseTimeOfDay(raw string) TimeOfDay {
	value := TimeOfDay{Raw: strings.TrimSpace(raw)}
	hour, minute, found := strings.Cut(value.Raw, ":")
	if !found {
		return value
	}
	h, hErr := strconv.Atoi(strings.TrimSpace(hour))
	m, mErr := strconv.Atoi(strings.TrimSpace(minute))
	if hErr != nil || mErr != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return value
	}
	value.Hour, value.Minute, value.ok = h, m, true
	return value
}

func (t TimeOfDay) Valid() bool { return t.ok }

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute) }
