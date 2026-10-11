package domain

import "strings"

// UncategorizedName is the name a row with no category goes by on screen and
// in an export. Uncategorized is the absence of a category, so a category
// that carries the name (an import can create one) says the same thing.
const UncategorizedName = "Uncategorized"

// NamesUncategorized reports whether a category name is Uncategorized, in any
// case and with stray spacing.
func NamesUncategorized(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), UncategorizedName)
}

// CanBeSuggested reports whether a suggestion may name this category: the
// assistant's proposals, a rule or recurring series drafted for review, and
// the choices a model is offered. Suggesting Uncategorized is suggesting
// nothing.
func (c Category) CanBeSuggested() bool { return !NamesUncategorized(c.Name) }

// FitsDirection reports whether a suggestion for a row of this amount may name
// a category of this kind. Money out never takes an income category; money in
// may take any kind, since a refund goes back under what was bought. A loan
// account is exempt, as a payment on it arrives as money in and is spending.
func (c Category) FitsDirection(amount Money, onLoan bool) bool {
	return onLoan || !amount.IsNegative() || c.Kind != CategoryIncome
}
