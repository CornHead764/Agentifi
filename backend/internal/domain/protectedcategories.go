package domain

import (
	"strings"
	"unicode"
)

// The categories the app's own processes find by known_category_id. A rename
// keeps the marker, so a renamed one keeps working; a deleted one would leave
// the process with nowhere to file, which is why none of them can be deleted.
//
// Simplifi's export carries no Transfer or Credit Card Payment category (its
// transfers name the other account), so those two markers are this app's,
// chosen in Simplifi's scheme: ten digits, top-level ids in alphabetical
// order, Transfer between Taxes and Travel.
const (
	KnownCategoryTransfer          = "7125000000"
	KnownCategoryCreditCardPayment = "7125250000"
	KnownCategoryFeesAndCharges    = "3000000000"
)

// dependedOn is what each marked category is kept for, worded to follow
// "it cannot be deleted because".
var dependedOn = map[string]string{
	KnownCategoryTransfer:          "matched transfers are filed under it",
	KnownCategoryCreditCardPayment: "matched credit-card payments are filed under it",
	KnownCategoryFeesAndCharges:    "the fee alert watches it",
}

// CategoryDependedOn reports whether a process finds the category by its
// marker, and what for. Such a category keeps its kind as well as its place in
// the tree: Transfer turned into an expense would make every matched transfer
// spending.
func CategoryDependedOn(knownCategoryID string) (string, bool) {
	reason, ok := dependedOn[knownCategoryID]
	return reason, ok
}

// CategoryProtection says why a category cannot be deleted, or "" when it can:
// a process depends on it, or it is one of the system categories nobody may
// edit (Opening Balance, Balance Adjustment, Federal Estimated Tax Payment).
func CategoryProtection(knownCategoryID string, editable bool) string {
	if reason, ok := dependedOn[knownCategoryID]; ok {
		return reason
	}
	if !editable {
		return "it is maintained by the app"
	}
	return ""
}

// ProtectedKnownCategoryIDs is every marker CategoryDependedOn answers for, for
// a guard written in SQL.
func ProtectedKnownCategoryIDs() []string {
	out := make([]string, 0, len(dependedOn))
	for id := range dependedOn {
		out = append(out, id)
	}
	return out
}

// TransferCategoryFor is the marker of the category a matched pair's
// uncategorized leg is filed under, given the kinds of the pair's accounts:
// Credit Card Payment when either leg is on a credit card, Transfer otherwise.
func TransferCategoryFor(legAccounts ...AccountKind) string {
	for _, kind := range legAccounts {
		if kind == KindCreditCard {
			return KnownCategoryCreditCardPayment
		}
	}
	return KnownCategoryTransfer
}

// CardPaymentConfidence is how sure the category check is that money arriving
// on a credit card under a payment's wording is a payment on the card. It is
// below certainty because a merchant's credit can read "payment" too.
const CardPaymentConfidence = 0.8

// LooksLikeCardPayment reports whether a row is a payment on a credit card:
// money in on a card whose statement wording says payment and not refund.
func LooksLikeCardPayment(kind AccountKind, amount Money, statementName string) bool {
	if kind != KindCreditCard || !amount.IsPositive() {
		return false
	}
	payment := false
	for _, word := range strings.FieldsFunc(strings.ToUpper(statementName), func(r rune) bool {
		return !unicode.IsLetter(r)
	}) {
		switch word {
		case "PAYMENT", "PYMT", "PMT", "AUTOPAY":
			payment = true
		case "REFUND", "RETURN", "REVERSAL", "CASHBACK", "REWARD", "REWARDS":
			return false
		}
	}
	return payment
}
