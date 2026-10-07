package billers

import (
	"sync"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// bridgeLimit is how many buttons a sign-in presses at pages that never become
// anything. Counted per page and reset when the page classifies as something
// with a name, so it is two fruitless presses: a flow crossing two bridges
// with a form between them is a sign-in going well.
const bridgeLimit = 2

// bridges is how many fruitless presses each live page has had; emptied by
// Forget, which the engine calls at session close and Classify calls when a
// page becomes something with a name.
var bridges sync.Map

func takeBridge(page browser.Page) bool {
	spent, _ := bridges.Load(page)
	count, _ := spent.(int)
	if count >= bridgeLimit {
		return false
	}
	bridges.Store(page, count+1)
	return true
}

// accountHops is the pages that have already been sent to their module's
// account page, so a page that answers the hop with the same unplaceable page
// is reported rather than sent there on every poll.
var accountHops sync.Map

func takeAccountHop(page browser.Page) bool {
	_, taken := accountHops.LoadOrStore(page, true)
	return !taken
}

// Forget drops what this package remembers about a page, for the engine, which
// knows when a page is done.
func Forget(page browser.Page) {
	bridges.Delete(page)
	accountHops.Delete(page)
}

// nudge takes whichever of the two steps this page is offering, and says
// whether it took one and whether it was a bridge.
//
// Inside the dialog in front of the page when there is one: a "Sign in" link
// behind a dialog is not the way in, and a dialog's one button is the bridge.
func (d Draft) nudge(page browser.Page, form Form) (moved bool, bridge bool) {
	scope := scopeOf(page)
	if followed, err := page.ClickText(scope.sel(signInControlSelector), signInControlWords); err == nil && followed {
		return true, false
	}
	if !bridgePage(form) || !takeBridge(page) {
		return false, false
	}
	before := agent.Signature(page)
	pressed, err := page.ClickVisible(scope.sel(agent.SubmitSelector))
	if err != nil || !pressed {
		return false, false
	}
	agent.AwaitChange(page, before)
	return true, true
}

// bridgePage: nothing to type into, and one button.
// Two buttons is a question and a box is a form; one button over nothing is a
// page whose only reading is "carry on".
func bridgePage(form Form) bool {
	return form.Boxes == 0 && form.Submits == 1 && !form.SignOutLink
}
