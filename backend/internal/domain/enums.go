package domain

// AccountKind is what an account is for sign and totalling purposes; checking
// and savings are both cash.
type AccountKind string

const (
	KindCash       AccountKind = "cash"
	KindCreditCard AccountKind = "credit_card"
	KindLoan       AccountKind = "loan"
	KindInvestment AccountKind = "investment"
	KindAsset      AccountKind = "asset"
)

// IsDebt reports whether balances for this kind are stored negative.
func (k AccountKind) IsDebt() bool {
	return k == KindCreditCard || k == KindLoan
}

// SpendsFrom reports whether money is spent out of this account: cash and
// cards. What posts to a loan, investment or asset is repayment, trades or
// revaluation, already counted on the side the money came from. Simplifi's
// spending-plan buckets draw the same line.
func (k AccountKind) SpendsFrom() bool {
	return k == KindCash || k == KindCreditCard
}

// BornReviewed reports whether rows on this kind of account skip the review
// queue. Review is a spending workflow; queueing loan interest and trades
// buries the card charges that need a look.
func (k AccountKind) BornReviewed() bool {
	return !k.SpendsFrom()
}

type CategoryKind string

const (
	CategoryIncome  CategoryKind = "income"
	CategoryExpense CategoryKind = "expense"
	// CategoryTransfer is movement between the user's own accounts: never
	// income, never expense.
	CategoryTransfer CategoryKind = "transfer"
)

// Source records where a row came from. Opening balances and balance
// adjustments are bookkeeping, excluded from profit and loss.
type Source string

const (
	SourceSync           Source = "sync"
	SourceManual         Source = "manual"
	SourceFileImport     Source = "file_import"
	SourceSimplifiImport Source = "simplifi_import"
	// SourceEmail is a row a mail rule posted: a wrong one is a rule to fix,
	// not a hand to correct.
	SourceEmail Source = "email"
	// SourceOpeningBalance is the synthetic row that makes an account start
	// at the right number.
	SourceOpeningBalance Source = "opening_balance"
	// SourceBalanceAdjustment is an asset revaluation: net worth moves,
	// nobody spent anything.
	SourceBalanceAdjustment Source = "balance_adjustment"
)

// IsCashFlow reports whether a row of this source represents real spending or
// earning, as opposed to bookkeeping.
func (s Source) IsCashFlow() bool {
	return s != SourceOpeningBalance && s != SourceBalanceAdjustment
}

// CashFlowSources is IsCashFlow as a list, for queries that must name the
// sources. Derived from the predicate so the two cannot drift: series matching
// filters by the list and then tests with the predicate.
func CashFlowSources() []Source {
	out := make([]Source, 0, len(AllSources))
	for _, source := range AllSources {
		if source.IsCashFlow() {
			out = append(out, source)
		}
	}
	return out
}

// AllSources is every source a stored row can carry. Adding one here is what
// makes it visible to CashFlowSources.
var AllSources = []Source{
	SourceSync,
	SourceManual,
	SourceFileImport,
	SourceSimplifiImport,
	SourceEmail,
	SourceOpeningBalance,
	SourceBalanceAdjustment,
}

// DateMode selects which of a transaction's two dates a caller means. Reports
// read Effective; the register shows Posted. Backwards shifts a month of card
// spending.
type DateMode string

const (
	DatePosted    DateMode = "posted"
	DateEffective DateMode = "effective"
)
