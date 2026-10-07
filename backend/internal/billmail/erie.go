package billmail

import (
	"regexp"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Erie Insurance's invoice mail: one per policy; the parenthesised
// policy id matches what a pull reports. Newsletters share the domain, so the
// sender rule is the full address.
const (
	erieSender  = "donotreply-edelivery@erieinsurance.com"
	erieSubject = "Your new Erie Insurance invoice"
)

var eriePolicy = regexp.MustCompile(`(?i)policy\s+([A-Za-z][A-Za-z ]*?)\s*\(\s*([A-Z0-9][A-Z0-9-]{3,})\s*\)`)

type erieParser struct{}

func init() { Register(erieParser{}) }

func (erieParser) Biller() domain.BillerID { return domain.BillerErie }

func (p erieParser) Match(m Message) (Claim, bool) {
	if !senderIs(m.Sender, erieSender) || !textutil.ContainsFold(m.Subject, erieSubject) {
		return Claim{}, false
	}

	text := bodyText(m)
	policy := eriePolicy.FindStringSubmatch(text)
	if policy == nil {
		return Claim{}, false
	}
	amount, ok := amountAfterAny(text, []string{"Total Due", "Amount Due"})
	if !ok {
		return Claim{}, false
	}
	day, ok := dateAfterAny(text, []string{"Due Date"})
	if !ok {
		return Claim{}, false
	}

	id := policy[2]
	return Claim{Biller: domain.BillerErie, Bills: []Bill{{
		ExternalID:   id,
		MaskedNumber: domain.MaskAccount(id),
		Label:        policy[1],
		AmountDue:    amount,
		DueOn:        day,
	}}}, true
}
