package billmail

import (
	"regexp"
	"strings"
	"time"
)

// A forward (unlike a redirect) makes the household the sender, so the parsers
// need the pasted header block's sender and date. Only the reader may decide to
// unwrap, and only for mail the household's own addresses sent: "From:" in a
// body is a claim anybody can type.
var (
	forwardedFrom = regexp.MustCompile(
		`(?is)\bFrom:\s*(?:"?[^"<\n]{0,120}"?\s*)?<?\s*([A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,})\s*>?`)
	forwardedSubject = regexp.MustCompile(`(?i)^\s*(?:(?:fw|fwd|tr|wg)\s*:\s*)+`)
	// forwardedSent is the block's date line: Outlook writes "Sent:", Gmail
	// and Apple Mail "Date:".
	forwardedSent = regexp.MustCompile(`(?im)^[ \t]*(?:Sent|Date):[ \t]*(.+)$`)
)

// A "From:" further down is a quoted reply.
const forwardedHeaderWindow = 800

func UnwrapForward(m Message) (Message, bool) {
	if !forwardedSubject.MatchString(m.Subject) && !looksForwarded(m.Text) {
		return m, false
	}
	head := m.Text
	if len(head) > forwardedHeaderWindow {
		head = head[:forwardedHeaderWindow]
	}
	found := forwardedFrom.FindStringSubmatchIndex(head)
	if found == nil {
		return m, false
	}
	m.Sender = strings.ToLower(head[found[2]:found[3]])
	m.ReceivedAt = forwardedDate(head[found[1]:], m.ReceivedAt)
	return m, true
}

// forwardedDate keeps the forward's own time when the block's day is
// unreadable or later than the forward.
func forwardedDate(block string, received time.Time) time.Time {
	line := forwardedSent.FindStringSubmatch(block)
	if line == nil {
		return received
	}
	day, ok := ParseDate(line[1])
	if !ok || received.IsZero() {
		return received
	}
	sent := time.Date(day.Year, day.Month, day.Day,
		received.Hour(), received.Minute(), received.Second(), 0, received.Location())
	if sent.After(received) {
		return received
	}
	return sent
}

func looksForwarded(text string) bool {
	head := text
	if len(head) > forwardedHeaderWindow {
		head = head[:forwardedHeaderWindow]
	}
	lower := strings.ToLower(head)
	return strings.Contains(lower, "from:") && strings.Contains(lower, "subject:")
}
