package billmail

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Apple's receipt mail. The charge is taken the day of the receipt, so its
// date is both due and autopay date.
var (
	appleSenders     = []string{"@email.apple.com", "@apple.com"}
	appleSubjects    = []string{"Your receipt from Apple.", "Your invoice from Apple."}
	appleAmountLabel = []string{"TOTAL"}
	appleDateLabel   = []string{"DATE"}
)

type appleParser struct{}

func init() { Register(appleParser{}) }

func (appleParser) Biller() domain.BillerID { return domain.BillerApple }

func (p appleParser) Match(m Message) (Claim, bool) {
	if !senderIs(m.Sender, appleSenders...) || !textutil.ContainsAnyFold(m.Subject, appleSubjects) {
		return Claim{}, false
	}

	text := bodyText(m)
	amount, hasAmount := amountAfterAny(text, appleAmountLabel)
	day, hasDay := dateAfterAny(text, appleDateLabel)
	if !hasAmount || !hasDay {
		return Claim{}, false
	}

	return Claim{Biller: domain.BillerApple, Bills: []Bill{{
		AmountDue: amount,
		DueOn:     day,
		AutopayOn: day,
		IssuedOn:  day,
	}}}, true
}
