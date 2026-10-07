package csvimport

import (
	"slices"
	"strings"
	"unicode"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// What the file does not say, and has to be worked out from a name.
//
// A Simplifi CSV export has no account-type or category-type column, and both
// drive arithmetic, so every guess made here is recorded with the rule that
// made it and printed on the report.
//
// The rules match on finance vocabulary, never on one household's names.
// "Loan" before "card" because a card account can be called a cash card and a
// loan cannot; "card" before "cash" for the same reason in reverse.

// AccountGuess is one account's inferred classification, and the rule that
// produced it.
type AccountGuess struct {
	Name string
	Kind domain.AccountKind
	Type string
	// Rule names what matched, or is empty when nothing did and the fallback
	// decided. An empty rule is the operator's cue to set the kind by hand.
	Rule string
}

// Matched reports whether a rule fired, as opposed to the fallback.
func (g AccountGuess) Matched() bool { return g.Rule != "" }

type accountRule struct {
	// rule is the vocabulary that matched, which the report prints.
	rule string
	kind domain.AccountKind
	// accountType is the picker label, never read by a calculation.
	accountType string
	words       []string
	// phrases are runs of whole words, for vocabulary no single word carries:
	// "cash value" is not cash.
	phrases []string
}

// The order is the rule. The first match wins, so a name carrying two of these
// words is classified by whichever is listed first.
var accountRules = []accountRule{
	{rule: "loan or mortgage", kind: domain.KindLoan, accountType: "loan",
		words: []string{"loan", "mortgage", "heloc", "lease"}},
	{rule: "card issuer or the word card", kind: domain.KindCreditCard, accountType: "credit_card",
		words: []string{"card", "visa", "mastercard", "amex", "discover", "credit"}},
	{rule: "life insurance cash value", kind: domain.KindInvestment, accountType: "life_insurance",
		phrases: []string{"life insurance", "whole life", "universal life", "variable life", "cash value"}},
	{rule: "crypto wallet or exchange", kind: domain.KindInvestment, accountType: "crypto",
		words: []string{"crypto", "cryptocurrency", "bitcoin", "btc", "ethereum", "eth", "coinbase",
			"kraken", "staked", "staking"}},
	{rule: "invested health savings account", kind: domain.KindInvestment, accountType: domain.AccountTypeHSAInvestment,
		phrases: []string{
			"hsa invest", "hsa investment", "hsa investments", "hsa brokerage",
			"health savings invest", "health savings investment", "health savings investments",
			"health savings brokerage",
		}},
	{rule: "brokerage or retirement account", kind: domain.KindInvestment, accountType: "brokerage",
		words: []string{"brokerage", "ira", "401k", "403b", "roth", "investment", "investments", "portfolio"}},
	{rule: "checking", kind: domain.KindCash, accountType: "checking",
		words: []string{"checking", "chequing"}},
	{rule: "health or flexible spending account", kind: domain.KindCash, accountType: domain.AccountTypeHSA,
		words:   []string{"hsa", "fsa"},
		phrases: []string{"health savings", "flex spending", "flexible spending"}},
	{rule: "savings or a named fund", kind: domain.KindCash, accountType: "savings",
		words: []string{"savings", "saving", "fund", "reserve"}},
	{rule: "cash or wallet", kind: domain.KindCash, accountType: "cash",
		words: []string{"cash", "wallet", "petty"}},
}

// streetSuffixes are what makes a name a postal address rather than a bank's.
// Matched only alongside a leading house number, because "Way" and "Court"
// are ordinary words on their own.
var streetSuffixes = map[string]bool{
	"st": true, "street": true, "rd": true, "road": true, "ave": true, "avenue": true,
	"dr": true, "drive": true, "ln": true, "lane": true, "way": true, "ct": true,
	"court": true, "blvd": true, "boulevard": true, "pl": true, "place": true,
	"ter": true, "terrace": true, "cir": true, "circle": true, "trl": true, "trail": true,
	"pkwy": true, "parkway": true, "hwy": true, "highway": true,
}

// GuessAccount classifies one account from its name. The fallback is an asset
// rather than cash, because a car filed as cash would silently enter the
// spending plan's available balance, while the reverse is visibly wrong.
func GuessAccount(name string) AccountGuess {
	fields := fieldsOf(name)
	words := make(map[string]bool, len(fields))
	for _, field := range fields {
		words[field] = true
	}
	spaced := " " + strings.Join(fields, " ") + " "
	for _, rule := range accountRules {
		matched := slices.ContainsFunc(rule.words, func(word string) bool { return words[word] }) ||
			slices.ContainsFunc(rule.phrases, func(phrase string) bool {
				return strings.Contains(spaced, " "+phrase+" ")
			})
		if matched {
			return AccountGuess{Name: name, Kind: rule.kind, Type: rule.accountType, Rule: rule.rule}
		}
	}
	if isStreetAddress(name) {
		return AccountGuess{Name: name, Kind: domain.KindAsset, Type: "real_estate",
			Rule: "a house number followed by a street name"}
	}
	return AccountGuess{Name: name, Kind: domain.KindAsset, Type: "other_asset"}
}

func isStreetAddress(name string) bool {
	fields := strings.Fields(strings.ToLower(name))
	if len(fields) < 2 || !allDigits(fields[0]) {
		return false
	}
	for _, field := range fields[1:] {
		if streetSuffixes[strings.Trim(field, ".,")] {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// fieldsOf lowercases and splits a name into the words, in order, that a rule
// matches against. Word-wise, because "Cardinal Credit Union" is not a card.
func fieldsOf(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// CategoryGuess is one category path's inferred kind, and why.
type CategoryGuess struct {
	Path string
	Kind domain.CategoryKind
	Rule string
	// System marks the two categories the engine writes rather than the user:
	// Opening Balance and Balance Adjustment. They are not user-assignable and
	// their transactions are bookkeeping, not cash flow.
	System bool
	// Source is the transaction source a row in this category gets. Empty
	// means the run's default.
	Source domain.Source
}

// incomeRoots are the top-level names Simplifi ships as income. A child of one
// of these is income too; nothing else is, whatever sign its rows carry.
var incomeRoots = map[string]bool{
	"personal income": true, "business income": true, "income": true, "other income": true,
}

// transferRoots are Simplifi's own movement-between-accounts categories.
var transferRoots = map[string]bool{
	"transfer": true, "transfers": true, "credit card payment": true,
	"credit card payments": true, "investments transfer": true,
}

// systemRoots are the two categories the engine writes. Their rows carry a
// source excluded from cash flow rather than a kind that would land them in a
// report.
var systemRoots = map[domain.Source]string{
	domain.SourceOpeningBalance:    "opening balance",
	domain.SourceBalanceAdjustment: "balance adjustment",
}

// GuessCategory classifies one full category path.
//
// accountNames is every account name the file mentions, normalized by
// strings.ToLower. Simplifi writes a transfer's category as the *other*
// account's name, so a category matching one is a transfer leg and not a
// spending category.
func GuessCategory(path string, accountNames map[string]bool) CategoryGuess {
	root := strings.ToLower(rootOf(path))
	for source, name := range systemRoots {
		if root == name {
			return CategoryGuess{Path: path, Kind: domain.CategoryTransfer,
				Rule: "system category", System: true, Source: source}
		}
	}
	if transferRoots[root] {
		return CategoryGuess{Path: path, Kind: domain.CategoryTransfer, Rule: "transfer category"}
	}
	if accountNames[root] {
		return CategoryGuess{Path: path, Kind: domain.CategoryTransfer,
			Rule: "names an account, so the row is a transfer leg"}
	}
	if incomeRoots[root] {
		return CategoryGuess{Path: path, Kind: domain.CategoryIncome, Rule: "income category"}
	}
	return CategoryGuess{Path: path, Kind: domain.CategoryExpense, Rule: "default"}
}

// rootOf is the top level of a "Parent:Child:Grandchild" path.
func rootOf(path string) string {
	root, _, _ := strings.Cut(path, categorySeparator)
	return strings.TrimSpace(root)
}

const categorySeparator = ":"

// canonicalPath is the key the category tree is built and looked up by. The
// cell as written differs from it wherever a separator carried spaces.
func canonicalPath(path string) string {
	return strings.Join(splitCategoryPath(path), categorySeparator)
}

// splitCategoryPath breaks a path into its levels, dropping empty ones so
// "Home::Tools" does not create a nameless category between the two.
func splitCategoryPath(path string) []string {
	parts := strings.Split(path, categorySeparator)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}
