package billers

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The census `agentifi probe-sign-in` prints, here rather than in the command
// so it reads with the classifier's own code and cannot disagree with it.
//
// Nothing here may carry a secret (see internal/browser/devtools.go): names,
// types, counts and addresses only, never a value, a cookie, a storage state
// or a request body. A census ends up pasted into issues.

// Census is one reading of a page: where it stands, what the shared
// classifier makes of it, and the three places a form hides from that
// classifier — another frame, a shadow root, and behind a button that has to
// be pressed first.
type Census struct {
	URL   string
	Title string
	// State is what StateOf made of the page, with no account area named:
	// a probe has no module, so the only positive sign of being in is a
	// sign-out link.
	State  State
	Form   Form
	Frames []browser.FrameCensus
	Shadow []ShadowInput
	WayIn  []string
	// Controls is every visible control a fill could press, in page order, with
	// the rank the submit rule gives each.
	Controls []SubmitControl
	// Choices is the menu a factor page is offering, read only at a factor page;
	// anywhere else the same query is every button and link.
	Choices []FactorChoice
	Unread  string
	ReadErr error
}

// ShadowInput is one box behind a shadow root, where document.querySelectorAll
// does not reach: the classifier sees an empty page and a person looking at
// the screen sees a form.
type ShadowInput struct {
	Host string `json:"host"`
	Type string `json:"type"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

// shadowInputsScript walks the shadow roots of the main frame's document
// (deepAllJS). Names, types and ids; never a value.
const shadowInputsScript = `() => {` + deepAllJS + `
  return agentifiDeepAll(document, 'input')
    .filter((input) => input.getRootNode() !== document)
    .slice(0, 50)
    .map((input) => ({
      host: input.getRootNode().host ? input.getRootNode().host.tagName.toLowerCase() : '',
      type: (input.type || 'text').toLowerCase(),
      name: input.name || '',
      id: input.id || '',
    }));
}`

// TakeCensus reads the page and never presses.
func TakeCensus(page browser.Page) Census {
	out := Census{URL: page.URL()}
	if title, err := page.Title(); err == nil {
		out.Title = title
	}
	form, err := ReadForm(page)
	if err != nil {
		out.ReadErr = err
		return out
	}
	out.Form = form
	out.State = StateOf(form, page.URL(), nil)
	out.Frames = browser.FramesOf(page)
	if err := browser.EvaluateInto(page, shadowInputsScript, nil, &out.Shadow); err != nil {
		out.Unread = err.Error()
	}
	if controls, err := SignInControls(page); err == nil {
		out.WayIn = controls
	}
	if controls, err := SubmitControls(page); err == nil {
		out.Controls = controls
	}
	if out.State.State == StateFactor {
		if choices, err := FactorChoices(page); err == nil {
			out.Choices = choices
		}
	}
	return out
}

func (c Census) Print(out io.Writer) {
	fmt.Fprintf(out, "landed     %s\n", c.URL)
	fmt.Fprintf(out, "title      %s\n", c.Title)
	if c.ReadErr != nil {
		fmt.Fprintf(out, "reading    the page could not be read: %v\n", c.ReadErr)
		return
	}
	fmt.Fprintf(out, "state      %s", c.State.State)
	if c.State.Blocking {
		fmt.Fprint(out, " (a blocking check page, not an ordinary puzzle)")
	}
	if c.State.Bridged > 0 {
		fmt.Fprintf(out, " (%d bridged)", c.State.Bridged)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "form       %d box%s · %d submit%s · username %s · password %s · code %s · sign-out %s\n",
		c.Form.Boxes, pluralWith(c.Form.Boxes, "es"), c.Form.Submits, plural(c.Form.Submits),
		yesNo(c.Form.Username), yesNo(c.Form.Password), yesNo(c.Form.OTP), yesNo(c.Form.SignOutLink))
	fmt.Fprintf(out, "inputs     %s\n", inputCensus(c.Form.Inputs))
	fmt.Fprintf(out, "blank      %s%s\n", yesNo(c.Form.Blank()), blankNote(c.Form))
	if c.Form.Heading != "" {
		fmt.Fprintf(out, "heading    %s\n", c.Form.Heading)
	}
	// The provider's complaint is reported as a fact and never quoted: a
	// refusal names what was sent to it as often as not.
	fmt.Fprintf(out, "complains  %s\n", yesNo(c.Form.Error != ""))

	fmt.Fprintf(out, "frames     %d\n", len(c.Frames))
	for _, frame := range c.Frames {
		count := "unreadable"
		if frame.Readable {
			count = fmt.Sprintf("%d inputs", frame.Inputs)
		}
		fmt.Fprintf(out, "           %-12s %-12s %s\n", frame.Name, count, textutil.ClipMarked(frame.URL, 120))
	}

	switch {
	case c.Unread != "":
		fmt.Fprintf(out, "shadow     unreadable: %s\n", c.Unread)
	case len(c.Shadow) == 0:
		fmt.Fprintln(out, "shadow     none")
	default:
		fmt.Fprintf(out, "shadow     %d box%s the classifier does not pierce\n",
			len(c.Shadow), pluralWith(len(c.Shadow), "es"))
		for _, one := range c.Shadow {
			named := strings.TrimSpace(one.Name + " " + one.ID)
			if named == "" {
				named = "(unnamed)"
			}
			fmt.Fprintf(out, "           <%s> %s %s\n", one.Host, one.Type, named)
		}
	}

	c.printControls(out)
	c.printChoices(out)

	if len(c.WayIn) == 0 {
		fmt.Fprintln(out, "way in     none")
		return
	}
	fmt.Fprintf(out, "way in     %d control%s the follow rule would take: %s\n",
		len(c.WayIn), plural(len(c.WayIn)), strings.Join(Quoted(c.WayIn), ", "))
}

// printControls is every control a fill could press and its rank, then the one
// it would press, which on a page the rule has no word for is the Enter key.
func (c Census) printControls(out io.Writer) {
	if len(c.Controls) == 0 {
		fmt.Fprintln(out, "controls   none visible")
		return
	}
	fmt.Fprintf(out, "controls   %d visible\n", len(c.Controls))
	for _, control := range c.Controls {
		words := control.Words
		if words == "" {
			words = "(no words)"
		}
		fmt.Fprintf(out, "           %-8s %-24s %s\n", control.Kind, fmt.Sprintf("%q", words),
			strings.Join(controlNotes(control), " · "))
	}
	best, found := BestSubmit(c.Controls)
	if !found {
		fmt.Fprintln(out, "presses    nothing — the fill would fall through to the Enter key")
		return
	}
	waiting := ""
	if best.Disabled {
		waiting = ", once it becomes pressable"
	}
	fmt.Fprintf(out, "presses    the %s saying %q (rank %d%s)\n",
		best.Kind, best.Words, SubmitRank(best), waiting)
}

// printChoices is the menu a factor page offers, the rank of each option, and
// which one a sign-in would take and whether it must then be confirmed.
func (c Census) printChoices(out io.Writer) {
	if c.State.State != StateFactor {
		return
	}
	if len(c.Choices) == 0 {
		fmt.Fprintln(out, "choices    none this reading could name")
		return
	}
	fmt.Fprintf(out, "choices    %d offered\n", len(c.Choices))
	for _, choice := range c.Choices {
		fmt.Fprintf(out, "           %-8s %-40s %s\n", choice.Kind, fmt.Sprintf("%q", choice.Words),
			strings.Join(choiceNotes(choice), " · "))
	}
	best, found := BestFactor(c.Choices, "")
	if !found {
		fmt.Fprintln(out, "chooses    nothing — none of them can be answered unattended")
		return
	}
	after := "and nothing is pressed after it: it sends the page on itself"
	if best.Selects {
		after = "and the page's own button is pressed after it"
	}
	fmt.Fprintf(out, "chooses    the %s saying %q (%s), %s\n",
		best.Kind, best.Words, FactorKind(best.Words), after)
}

func choiceNotes(choice FactorChoice) []string {
	notes := []string{}
	if choice.Selects {
		notes = append(notes, "selects rather than sends")
	}
	if kind := FactorKind(choice.Words); kind != "" {
		notes = append(notes, fmt.Sprintf("%s, rank %d", kind, FactorRank(choice)))
	} else {
		notes = append(notes, "not a factor this engine can answer")
	}
	return notes
}

func controlNotes(control SubmitControl) []string {
	notes := []string{}
	if control.Typed {
		notes = append(notes, `type="submit"`)
	}
	if control.Disabled {
		notes = append(notes, "disabled")
	}
	if rank := SubmitRank(control); rank > 0 {
		notes = append(notes, fmt.Sprintf("rank %d", rank))
	} else {
		notes = append(notes, "not a submit the rule knows")
	}
	return notes
}

// inputCensus is how many visible boxes of each type the page shows, sorted so
// two readings of the same page read the same way.
func inputCensus(inputs map[string]int) string {
	if len(inputs) == 0 {
		return "none"
	}
	kinds := make([]string, 0, len(inputs))
	for kind := range inputs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d", kind, inputs[kind]))
	}
	return strings.Join(parts, " · ")
}

// blankNote states the rough edge that costs an application-rendered provider
// its grace period, at the moment it applies.
func blankNote(read Form) string {
	if read.Blank() {
		return " (the classifier grants this page its 15-second paint wait)"
	}
	return " (a page with anything on it gets no paint wait, drawn form or not)"
}

// Quoted is each of values, double-quoted for a sentence listing them.
func Quoted(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprintf("%q", value))
	}
	return out
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
