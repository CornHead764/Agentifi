package billmail

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// We Energies' bill-ready mail. Only Amount Due is read: budget billing
// shows a levelised amount beside the true balance.
const weEnergiesSender = "@we-energies.com"

var (
	weEnergiesSubjects     = []string{"Your We Energies bill is ready", "Your bill is ready to view"}
	weEnergiesAmountLabels = []string{"Amount Due"}
	weEnergiesDateLabels   = []string{"Due Date"}
)

type weEnergiesParser struct{}

func init() { Register(weEnergiesParser{}) }

func (weEnergiesParser) Biller() domain.BillerID { return domain.BillerWeEnergies }

func (p weEnergiesParser) Match(m Message) (Claim, bool) {
	if !senderIs(m.Sender, weEnergiesSender) || !textutil.ContainsAnyFold(m.Subject, weEnergiesSubjects) {
		return Claim{}, false
	}

	text := bodyText(m)
	amount, hasAmount := amountAfterAny(text, weEnergiesAmountLabels)
	day, hasDay := dateAfterAny(text, weEnergiesDateLabels)
	if !hasAmount || !hasDay {
		return Claim{}, false
	}

	four := domain.LastFour(findAfterLabel(text, "Account Number"))
	return Claim{Biller: domain.BillerWeEnergies, Bills: []Bill{{
		ExternalID:   four,
		MaskedNumber: domain.MaskAccount(four),
		AmountDue:    amount,
		DueOn:        day,
	}}}, true
}
