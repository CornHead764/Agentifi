// Package billmail recognises billing e-mail: which provider sent a message,
// what bill it carries, and whether it is a one-time code a sign-in is waiting
// for. Pure and offline: a Message in, a Claim out.
package billmail

import (
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

type Attachment struct {
	Filename    string
	ContentType string
	Bytes       []byte
}

type Message struct {
	// ID is the internet message-id, angle brackets and all.
	ID string
	// Sender is the bare address, lower-cased.
	Sender     string
	Subject    string
	ReceivedAt time.Time
	// Text may be empty for an HTML-only mail; extractors read bodyText.
	Text        string
	HTML        string
	Attachments []Attachment
}

type Bill struct {
	// ExternalID must match what the pull modules report for the account.
	ExternalID string
	// MaskedNumber is in the pull modules' shape ("••••1234").
	MaskedNumber string
	Label        string
	AmountDue    domain.Money
	// MinimumDue is a magnitude.
	MinimumDue    domain.Money
	HasMinimumDue bool
	DueOn         domain.Date
	AutopayOn     domain.Date
	IssuedOn      domain.Date
	PeriodStart   domain.Date
	PeriodEnd     domain.Date
	Document      *Attachment
}

type Claim struct {
	Biller domain.BillerID
	// Empty Bills with Proposed set is a bill with no readable figure (a
	// scanned PDF), filed for a person.
	Bills    []Bill
	Proposed bool
	Note     string
}

type Parser interface {
	Biller() domain.BillerID
	// Match answers false for the provider's newsletters and notices too.
	Match(Message) (Claim, bool)
}

var parsers []Parser

func Register(p Parser) { parsers = append(parsers, p) }

func Match(m Message) (Claim, bool) {
	for _, p := range parsers {
		if claim, ok := p.Match(m); ok {
			if claim.Biller == "" {
				claim.Biller = p.Biller()
			}
			return claim, true
		}
	}
	return Claim{}, false
}

type OTP struct {
	// Biller is empty for a relayed text naming no known provider.
	Biller  domain.BillerID
	Code    string
	Relayed bool
}
