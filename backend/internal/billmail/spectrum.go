package billmail

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Spectrum's statement mail states no due date, so the autopay
// date stands in and the claim's note says so. Only the last four is given, so
// it is the external id too; a pull matches it on the mask.
const (
	spectrumSender  = "MyAccount@spectrumemails.com"
	spectrumSubject = "Your Spectrum Statement is Ready"
	spectrumNote    = "the due date is the autopay date: the statement mail states no other"
)

type spectrumParser struct{}

func init() { Register(spectrumParser{}) }

func (spectrumParser) Biller() domain.BillerID { return domain.BillerSpectrum }

func (p spectrumParser) Match(m Message) (Claim, bool) {
	if !senderIs(m.Sender, spectrumSender) || !textutil.ContainsFold(m.Subject, spectrumSubject) {
		return Claim{}, false
	}

	text := bodyText(m)
	amount, ok := amountAfterAny(text, []string{"Statement Amount", "Amount Due"})
	if !ok {
		return Claim{}, false
	}
	autopay, ok := dateAfterAny(text, []string{"Auto Pay Date", "AutoPay Date"})
	if !ok {
		return Claim{}, false
	}
	four := domain.LastFour(findAfterLabel(text, "Account Number"))
	if four == "" {
		return Claim{}, false
	}

	return Claim{
		Biller: domain.BillerSpectrum,
		Bills: []Bill{{
			ExternalID:   four,
			MaskedNumber: domain.MaskAccount(four),
			AmountDue:    amount,
			DueOn:        autopay,
			AutopayOn:    autopay,
		}},
		Note: spectrumNote,
	}, true
}
