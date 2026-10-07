package billmail

import (
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// codeSenders is the whole rule: mail from anyone else (bar a relay) is not a
// code, since the same mailbox carries promo codes. Entries are best knowledge,
// not yet seen carrying a code.
var codeSenders = []struct {
	Biller  domain.BillerID
	Senders []string
}{
	{domain.BillerAlliant, []string{alliantSender}},
	{domain.BillerSpectrum, []string{spectrumSender, "no-reply@spectrum.net"}},
	{domain.BillerErie, []string{"no-reply@erieinsurance.com", "donotreply@erieinsurance.com"}},
	{domain.BillerWeEnergies, []string{weEnergiesSender}},
}

var digitRun = regexp.MustCompile(`\d+`)

var codeCues = []string{"security code", "passcode", "verification", "one-time", "otp", "code"}

// codeWindow keeps the account number further down the mail out of reach.
const codeWindow = 80

func FindOTP(m Message, relays []string) (OTP, bool) {
	sender := strings.ToLower(strings.TrimSpace(m.Sender))
	text := m.Subject + "\n" + bodyText(m)

	for _, relay := range relays {
		if sender != strings.ToLower(strings.TrimSpace(relay)) {
			continue
		}
		code := findCode(text)
		if code == "" {
			return OTP{}, false
		}
		return OTP{Biller: billerNamedIn(text), Code: code, Relayed: true}, true
	}

	for _, entry := range codeSenders {
		if !senderIs(sender, entry.Senders...) {
			continue
		}
		code := findCode(text)
		if code == "" {
			return OTP{}, false
		}
		return OTP{Biller: entry.Biller, Code: code}, true
	}
	return OTP{}, false
}

func findCode(text string) string {
	code, at := "", -1
	for _, cue := range codeCues {
		for _, start := range phraseIndices(text, cue) {
			limit := start + len(cue) + codeWindow
			if limit > len(text) {
				limit = len(text)
			}
			for _, run := range digitRun.FindAllStringIndex(text[start+len(cue):limit], -1) {
				from, to := start+len(cue)+run[0], start+len(cue)+run[1]
				if !standaloneCode(text, from, to) {
					continue
				}
				if at < 0 || from < at {
					code, at = text[from:to], from
				}
				break
			}
		}
	}
	return code
}

// standaloneCode rejects runs that belong to an amount, date, phone number or
// longer identifier.
func standaloneCode(text string, from, to int) bool {
	if to-from < 4 || to-from > 8 {
		return false
	}
	if from > 0 && strings.ContainsRune("$.,-/#", rune(text[from-1])) {
		return false
	}
	if to < len(text) {
		switch text[to] {
		case '%':
			return false
		case '.', ',', '-', '/', ':':
			if to+1 < len(text) && text[to+1] >= '0' && text[to+1] <= '9' {
				return false
			}
		}
	}
	return true
}

// billerNamedIn tries codeSenders by name or domain, then any SMS-code provider
// by its exact-case name as a whole word: a carrier's name can be an ordinary
// word.
func billerNamedIn(text string) domain.BillerID {
	for _, entry := range codeSenders {
		if biller, ok := domain.BillerByID(entry.Biller); ok && textutil.ContainsFold(text, biller.Name) {
			return entry.Biller
		}
		for _, sender := range entry.Senders {
			if at := strings.LastIndex(sender, "@"); at >= 0 && textutil.ContainsFold(text, sender[at+1:]) {
				return entry.Biller
			}
		}
	}
	for _, biller := range domain.Billers {
		if biller.Raises(domain.ChallengeSMS) && textutil.HasWord(text, biller.Name, textutil.WordOptions{}) {
			return biller.ID
		}
	}
	return ""
}

func phraseIndices(s, phrase string) []int {
	var found []int
	for from := 0; from < len(s); {
		at, end := indexPhrase(s[from:], phrase)
		if at < 0 {
			return found
		}
		found = append(found, from+at)
		from += end
	}
	return found
}
