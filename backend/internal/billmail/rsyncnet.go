package billmail

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// rsync.net's invoice mail. A mail missing either figure is not claimed, so
// a wrong guess files nothing.
const rsyncNetSender = "@rsync.net"

var (
	rsyncNetSubjects     = []string{"invoice"}
	rsyncNetAmountLabels = []string{"Total Due", "Amount Due", "Total"}
	rsyncNetDateLabels   = []string{"Due Date", "Due"}
)

type rsyncNetParser struct{}

func init() { Register(rsyncNetParser{}) }

func (rsyncNetParser) Biller() domain.BillerID { return domain.BillerRsyncNet }

func (p rsyncNetParser) Match(m Message) (Claim, bool) {
	if !senderIs(m.Sender, rsyncNetSender) || !textutil.ContainsAnyFold(m.Subject, rsyncNetSubjects) {
		return Claim{}, false
	}

	text := bodyText(m)
	amount, hasAmount := amountAfterAny(text, rsyncNetAmountLabels)
	day, hasDay := dateAfterAny(text, rsyncNetDateLabels)
	if !hasAmount || !hasDay {
		return Claim{}, false
	}

	four := domain.LastFour(findAfterLabel(text, "Account"))
	return Claim{Biller: domain.BillerRsyncNet, Bills: []Bill{{
		ExternalID:   four,
		MaskedNumber: domain.MaskAccount(four),
		AmountDue:    amount,
		DueOn:        day,
	}}}, true
}
