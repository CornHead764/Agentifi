package browser

import (
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// A click Playwright gives up on carries its call log: the steps it took and
// the element each one found. The log goes on a sign-in's trail, which the
// household pastes to somebody, so only Playwright's own step lines are kept,
// an element only as its tag, id and classes, and a line holding anything
// else is dropped whole: an element's text or attribute could be something
// typed.

// callLogTail is how many step lines ClickLog keeps, from the end, and
// callLogChars the most characters it answers.
const (
	callLogTail  = 12
	callLogChars = 600
)

// callLogElement is an element as Playwright previews it: the opening tag
// with its attributes, then optionally its text and closing tag.
var callLogElement = regexp.MustCompile(
	`<([a-zA-Z][a-zA-Z0-9-]*)((?:\s+[^\s="'<>/]+(?:="[^"]*")?)*)\s*/?>(?:[^<]*</[a-zA-Z][a-zA-Z0-9-]*>)?`)

var callLogAttribute = regexp.MustCompile(`\s+([^\s="'<>/]+)(?:="([^"]*)")?`)

// callLogMark stands in for an element while a line is matched against the
// step vocabulary.
const callLogMark = "\x00"

var callLogCount = regexp.MustCompile(`^(\d+) × `)

// callLogSteps is every line kept, element previews stood in for by
// callLogMark.
var callLogSteps = regexp.MustCompile(`^(?:` + strings.Join([]string{
	`attempting (?:click|dblclick|hover|tap) action`,
	`waiting for element to be (?:visible|enabled|stable|editable)(?:(?:, | and )(?:visible|enabled|stable|editable))*`,
	`element is (?:not )?(?:visible|enabled|stable|editable)(?:(?:, | and )(?:visible|enabled|stable|editable))*(?: - waiting\.\.\.)?`,
	`element is not attached to the DOM`,
	`element was detached from the DOM, retrying`,
	`element is outside of the viewport`,
	`scrolling into view if needed`,
	`done scrolling`,
	`forcing action`,
	`performing (?:click|dblclick|hover|tap) action`,
	`(?:click|dblclick|hover|tap) action done`,
	`waiting for scheduled navigations to finish`,
	`navigations have finished`,
	`retrying (?:click|dblclick|hover|tap) action(?:, attempt #\d+)?`,
	`waiting \d+ms`,
	`locator resolved to ` + callLogMark,
	callLogMark + ` intercepts pointer events`,
	callLogMark + ` from ` + callLogMark + ` subtree intercepts pointer events`,
}, "|") + `)$`)

// callLogLocator is the line naming what was waited for; the selector is
// left out, since a locator can be built from text.
var callLogLocator = regexp.MustCompile(`^waiting for (?:locator|getBy[A-Za-z]+)\(`)

// ClickLog is the tail of the Playwright call log err carries, by the rule
// above, one step per "; ", or "" when it carries none.
func ClickLog(err error) string {
	lines := callLogLines(err)
	if len(lines) > callLogTail {
		lines = append([]string{"…"}, lines[len(lines)-callLogTail:]...)
	}
	said := strings.Join(lines, "; ")
	if runes := []rune(said); len(runes) > callLogChars {
		said = "…" + string(runes[len(runes)-callLogChars:])
	}
	return said
}

// ClickPerformed says err's call log shows the click itself done, so a
// timeout on it was the wait for the navigation the click started.
func ClickPerformed(err error) bool {
	for _, line := range callLogLines(err) {
		switch line {
		case "click action done", "waiting for scheduled navigations to finish", "navigations have finished":
			return true
		}
	}
	return false
}

// callLogLines is err's call log as kept lines, a step repeated back to back
// said once.
func callLogLines(err error) []string {
	if err == nil {
		return nil
	}
	said := err.Error()
	at := strings.Index(said, "Call log:")
	if at < 0 {
		return nil
	}
	var out []string
	for _, raw := range strings.Split(said[at+len("Call log:"):], "\n") {
		line := keptStep(raw)
		if line == "" || (len(out) > 0 && out[len(out)-1] == line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func keptStep(raw string) string {
	line := strings.TrimSpace(raw)
	line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
	count := ""
	if found := callLogCount.FindStringSubmatch(line); found != nil {
		count = found[1] + " × "
		line = line[len(found[0]):]
	}
	if callLogLocator.MatchString(line) {
		return count + "waiting for the element"
	}
	var elements []string
	marked := callLogElement.ReplaceAllStringFunc(line, func(preview string) string {
		elements = append(elements, keptElement(preview))
		return callLogMark
	})
	if !callLogSteps.MatchString(marked) {
		return ""
	}
	for _, element := range elements {
		marked = strings.Replace(marked, callLogMark, element, 1)
	}
	return count + marked
}

// keptElement is a previewed element as its tag, id and classes.
func keptElement(preview string) string {
	parts := callLogElement.FindStringSubmatch(preview)
	kept := "<" + strings.ToLower(parts[1])
	for _, attribute := range callLogAttribute.FindAllStringSubmatch(parts[2], -1) {
		name := strings.ToLower(attribute[1])
		if (name == "id" || name == "class") && strings.TrimSpace(attribute[2]) != "" {
			kept += " " + name + `="` + textutil.ClipMarked(strings.Join(strings.Fields(attribute[2]), " "), 60) + `"`
		}
	}
	return kept + ">"
}
