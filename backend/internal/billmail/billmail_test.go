package billmail

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

type stubParser struct {
	biller domain.BillerID
	claims bool
}

func (s stubParser) Biller() domain.BillerID { return s.biller }

func (s stubParser) Match(Message) (Claim, bool) {
	if !s.claims {
		return Claim{}, false
	}
	return Claim{}, true
}

func TestMatchTakesTheFirstClaimAndNamesItsBiller(t *testing.T) {
	registered := parsers
	t.Cleanup(func() { parsers = registered })
	parsers = []Parser{
		stubParser{domain.BillerApple, false},
		stubParser{domain.BillerSpectrum, true},
		stubParser{domain.BillerErie, true},
	}

	claim, ok := Match(Message{})
	if !ok {
		t.Fatal("no parser claimed the message")
	}
	if claim.Biller != domain.BillerSpectrum {
		t.Fatalf("claim went to %q, want the first parser that claimed", claim.Biller)
	}
}

func TestAMessageNobodyClaimsAnswersFalse(t *testing.T) {
	claim, ok := Match(Message{
		ID:      "<x1@mail.example.com>",
		Sender:  "somebody@elsewhere.test",
		Subject: "Lunch on Thursday?",
		Text:    "Total Due $10.00 on 10/01/2026, your half.\n",
	})
	if ok {
		t.Fatalf("an unrecognised message was claimed: %+v", claim)
	}
}

func TestEveryParserNamesAKnownBiller(t *testing.T) {
	for _, p := range parsers {
		if _, ok := domain.BillerByID(p.Biller()); !ok {
			t.Fatalf("%T claims biller %q, which is not in the catalogue", p, p.Biller())
		}
	}
}
