package billmail

import (
	"strings"
	"unicode/utf8"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Helpers for a sign-in waiting on a code, which may look harder than FindOTP
// at mail from its own provider. None makes a mail from anybody else a code.

const CodeDigits = "0123456789"

// Wider than findCode's four to eight because a candidate must pass CheckCode.
const (
	MinCodeLength = 4
	MaxCodeLength = 10
)

func FindCode(text string) string { return findCode(text) }

func BodyText(m Message) string { return bodyText(m) }

func CodeSendersFor(biller domain.BillerID) []string {
	for _, entry := range codeSenders {
		if entry.Biller == biller {
			return append([]string(nil), entry.Senders...)
		}
	}
	return nil
}

// merchantCodeSenders are whole domains, which is safe only because they are
// read while that merchant's sign-in is waiting.
var merchantCodeSenders = map[domain.MerchantID][]string{
	domain.MerchantAmazon: {"@amazon.com"},
	domain.MerchantCostco: {"@costco.com", "@digital.costco.com", "@online.costco.com", "@email.costco.com"},
}

func MerchantCodeSendersFor(merchant domain.MerchantID) []string {
	return append([]string(nil), merchantCodeSenders[merchant]...)
}

// SentBy takes whole addresses or "@domain".
func SentBy(m Message, senders []string) bool {
	return senderIs(m.Sender, senders...)
}

// SentFromDomain includes subdomains.
func SentFromDomain(m Message, domains []string) bool {
	sender := strings.ToLower(strings.TrimSpace(m.Sender))
	at := strings.LastIndex(sender, "@")
	if at < 0 {
		return false
	}
	host := sender[at+1:]
	for _, one := range domains {
		one = strings.ToLower(strings.TrimSpace(one))
		if one != "" && (host == one || strings.HasSuffix(host, "."+one)) {
			return true
		}
	}
	return false
}

func IsRelay(m Message, relays []string) bool {
	sender := strings.ToLower(strings.TrimSpace(m.Sender))
	for _, relay := range relays {
		if sender != "" && sender == strings.ToLower(strings.TrimSpace(relay)) {
			return true
		}
	}
	return false
}

// CheckCode is the mechanical gate for a model's answer: the code must appear
// verbatim as a standalone token in the message. The mail can talk a model into
// anything but cannot make a string appear in itself.
func CheckCode(code string, m Message, alphabet string) bool {
	if alphabet == "" {
		alphabet = CodeDigits
	}
	length := utf8.RuneCountInString(code)
	if length < MinCodeLength || length > MaxCodeLength {
		return false
	}
	for _, r := range code {
		if !strings.ContainsRune(alphabet, r) {
			return false
		}
	}
	for _, text := range []string{m.Subject, bodyText(m)} {
		if textutil.HasWord(text, code, textutil.WordOptions{Underscore: true}) {
			return true
		}
	}
	return false
}

// MayCarryCode keeps a code-like mail's text from being shown back later,
// whether or not a sign-in was waiting.
func MayCarryCode(m Message) bool {
	text := m.Subject + "\n" + bodyText(m)
	if findCode(text) == "" {
		return false
	}
	for _, entry := range codeSenders {
		if senderIs(m.Sender, entry.Senders...) {
			return true
		}
	}
	for _, senders := range merchantCodeSenders {
		if senderIs(m.Sender, senders...) {
			return true
		}
	}
	return false
}
