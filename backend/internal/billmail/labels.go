package billmail

import (
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// findAfterLabel reads the rest of the label's line, or the next non-empty line
// when the label ends its own. The label is matched as written (indexPhrase).
func findAfterLabel(text, label string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		at, end := indexPhrase(line, label)
		if at < 0 {
			continue
		}
		rest := strings.TrimLeft(line[end:], " \t:-–— ")
		if strings.TrimSpace(rest) != "" {
			return strings.TrimSpace(rest)
		}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) != "" {
				return strings.TrimSpace(next)
			}
		}
	}
	return ""
}

func AfterLabel(text, label string) string { return findAfterLabel(text, label) }

// A section is kept on a transaction for reading, so it is capped: a mail
// whose end label sits far below would otherwise hand over everything between.
const (
	sectionLines = 40
	sectionRunes = 2000
)

// sectionAfterLabel reads the lines below the one holding start, up to the line
// holding end, or with no end to the first blank line. Blank lines right
// below start are skipped, so a label above a gap still reaches its table. An
// HTML table row is one line with its cells side by side (StripHTML). It
// answers "" when start is absent or a named end never comes, since a mail
// laid out some other way would otherwise fill the notes with whatever
// follows.
func sectionAfterLabel(text, start, end string) string {
	lines := strings.Split(text, "\n")
	from := -1
	for i, line := range lines {
		if at, _ := indexPhrase(line, start); at >= 0 {
			from = i + 1
			break
		}
	}
	if from < 0 {
		return ""
	}
	var kept []string
	ended := end == ""
	for _, line := range lines[from:] {
		line = strings.TrimSpace(runOfSpace.ReplaceAllString(line, " "))
		if at, _ := indexPhrase(line, end); at >= 0 {
			ended = true
			break
		}
		if line == "" {
			if end == "" && len(kept) > 0 {
				break
			}
			continue
		}
		kept = append(kept, line)
	}
	if !ended {
		return ""
	}
	if len(kept) > sectionLines {
		kept = append(kept[:sectionLines], "…")
	}
	return textutil.ClipMarked(strings.Join(kept, "\n"), sectionRunes)
}

// amountAfterAny tries labels in order, so put a specific label before a
// general one.
func amountAfterAny(text string, labels []string) (domain.Money, bool) {
	for _, label := range labels {
		if amount, ok := ParseAmount(findAfterLabel(text, label)); ok {
			return amount, true
		}
	}
	return domain.Zero, false
}

func dateAfterAny(text string, labels []string) (domain.Date, bool) {
	for _, label := range labels {
		if day, ok := ParseDate(findAfterLabel(text, label)); ok {
			return day, true
		}
	}
	return domain.Date{}, false
}

// indexPhrase finds phrase in s as literal text, case aside, and answers
// where the match starts and ends in s, or -1, -1. Nothing in a label is a
// pattern: "Receipt #:" or "Total ($)" is that text. An edge of the phrase
// that is a letter, a digit or an underscore may not run on into one in s,
// so "Due" does not fire inside "Overdue"; a punctuation edge may sit
// against anything, so "Receipt #" reads "Receipt #4417".
func indexPhrase(s, phrase string) (start, end int) {
	return textutil.FindPhrase(s, phrase, textutil.WordOptions{Underscore: true, FoldCase: true})
}

// senderIs takes full addresses or "@domain".
func senderIs(sender string, rules ...string) bool {
	sender = strings.ToLower(strings.TrimSpace(sender))
	for _, rule := range rules {
		rule = strings.ToLower(rule)
		if strings.HasPrefix(rule, "@") {
			if strings.HasSuffix(sender, rule) {
				return true
			}
			continue
		}
		if sender == rule {
			return true
		}
	}
	return false
}
