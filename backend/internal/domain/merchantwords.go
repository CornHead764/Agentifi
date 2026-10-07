package domain

import (
	"strings"
	"unicode"
)

// genericMerchantWords are the words a bank puts on every row. Matching on one
// of them makes every payment similar to every other payment.
var genericMerchantWords = map[string]bool{
	"payment": true, "transfer": true, "transaction": true, "purchase": true, "debit": true,
	"credit": true, "card": true, "online": true, "ach": true, "pos": true, "withdrawal": true,
	"deposit": true, "received": true, "description": true, "enter": true, "here": true,
	"the": true, "and": true, "for": true, "from": true, "with": true, "inc": true,
	"llc": true, "corp": true, "com": true, "www": true, "cash": true, "check": true,
	"electronic": true, "recurring": true, "autopay": true, "web": true, "mobile": true,
	"direct": true, "dep": true, "pmt": true, "xfer": true, "int": true, "usa": true,
}

// IdentifyingWords is the words of a counterparty's wording, folded by Words,
// in order, without the ones that change from one row of the same merchant to
// the next: any word with a digit in it (a store number, a reference, a date)
// and single letters.
func IdentifyingWords(text string) []string {
	var out []string
	for _, field := range Words(text) {
		if len(field) < 2 || strings.ContainsFunc(field, unicode.IsDigit) {
			continue
		}
		out = append(out, field)
	}
	return out
}

// MerchantTokens is the IdentifyingWords that name the merchant: once each,
// without bank boilerplate or words shorter than three letters.
func MerchantTokens(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, field := range IdentifyingWords(text) {
		if len(field) < 3 || seen[field] || genericMerchantWords[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// MerchantWordingOverlap is how many of subject's MerchantTokens other shares,
// and whether that is enough to name the same merchant: at least half,
// rounded up, so one shared word is enough for a two-word merchant and not for
// an eight-word statement line. Wording with no tokens at all names nobody and
// matches nothing.
func MerchantWordingOverlap(subject, other string) (shared int, same bool) {
	tokens := MerchantTokens(subject)
	if len(tokens) == 0 {
		return 0, false
	}
	wanted := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		wanted[token] = true
	}
	for _, token := range MerchantTokens(other) {
		if wanted[token] {
			shared++
		}
	}
	return shared, shared >= (len(tokens)+1)/2
}

// SharesMerchantWording reports whether other names the same merchant as
// subject, by MerchantWordingOverlap.
func SharesMerchantWording(subject, other string) bool {
	_, same := MerchantWordingOverlap(subject, other)
	return same
}
