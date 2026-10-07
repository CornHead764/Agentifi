package domain

import (
	"strings"
	"time"
)

// ID is a string because the golden tests build these structs straight from
// the Simplifi export, whose IDs are not UUIDs. Stores convert at their own
// boundary.
type ID string

// Date is a calendar day with no time and no zone: with a timestamp, a
// month-end transaction's month depends on the reader's zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func NewDate(year int, month time.Month, day int) Date { return Date{year, month, day} }

func DateOf(t time.Time) Date { return Date{t.Year(), t.Month(), t.Day()} }

func (d Date) Time() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

func (d Date) IsZero() bool          { return d == Date{} }
func (d Date) Before(o Date) bool    { return d.Time().Before(o.Time()) }
func (d Date) After(o Date) bool     { return d.Time().After(o.Time()) }
func (d Date) NotAfter(o Date) bool  { return !d.After(o) }
func (d Date) NotBefore(o Date) bool { return !d.Before(o) }
func (d Date) String() string        { return d.Time().Format("2006-01-02") }

// ParseDate reads a calendar day written as 2006-01-02, or the day part of an
// RFC 3339 timestamp, whose clock part is dropped, not converted.
func ParseDate(s string) (Date, error) {
	s = strings.TrimSpace(s)
	if len(s) > 10 && (s[10] == 'T' || s[10] == ' ') {
		s = s[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, err
	}
	return DateOf(t), nil
}

// AddDays returns the date that many days later, crossing months and years.
func (d Date) AddDays(n int) Date { return DateOf(d.Time().AddDate(0, 0, n)) }

type Category struct {
	ID       ID
	Name     string
	Kind     CategoryKind
	ParentID ID
	// TxfID is the Tax Exchange Format code. The entire Taxes report is a
	// grouping over this one field.
	TxfID string
	// KnownCategoryID is Simplifi's stable system marker, kept so imported rows
	// keep their special meanings after a user renames the category.
	KnownCategoryID string

	// ExcludedFromReports and ExcludedFromSpendingPlan are the category's own
	// pair, independent of each other and of the account's and the
	// transaction's (ground rule 4).
	ExcludedFromReports      bool
	ExcludedFromSpendingPlan bool
}

func (c Category) IsTransfer() bool { return c.Kind == CategoryTransfer }

type Account struct {
	ID       ID
	Name     string
	Kind     AccountKind
	Currency string

	// ProviderBalance is the aggregator's figure, already normalized on ingest
	// so debt is negative. HasProviderBalance is false for manual accounts,
	// whose transactions are the balance.
	ProviderBalance    Money
	HasProviderBalance bool

	// OpeningBalance is the synthetic starting point for a manual account, and
	// for a connected loan its original principal.
	OpeningBalance Money

	// What HistoryStart reads besides the ledger. AddedOn is the day the
	// account was created here; OpeningBalanceOn the day a stated opening
	// balance was true; ObservedSince the earliest balance an import carried
	// over. HistoryStartsOn is the household's override. Zero is unknown.
	AddedOn          Date
	OpeningBalanceOn Date
	ObservedSince    Date
	HistoryStartsOn  Date

	IsClosed bool
	// AcceptZeroBalance is the household confirming that a zero reported for
	// this account is real, so neither the sync's guard (SuspectBalanceReset)
	// nor the history's (DroppedReadings) treats one as a dropped figure.
	AcceptZeroBalance bool
	// IsIgnored puts the account out of reports, the spending plan and net
	// worth whatever the three flags below say; the flags keep their values so
	// un-ignoring restores them.
	IsIgnored bool
	// ExcludedFromReports suppresses the account from reports entirely: every
	// transaction in it stops counting as income or expense.
	ExcludedFromReports bool
	// ExcludedFromSpendingPlan is independent of the above. Two flags, not one.
	ExcludedFromSpendingPlan bool
	// IncludeInNetWorth is its own flag again — an account can be reported on
	// and still be left out of net worth, or the reverse.
	IncludeInNetWorth bool

	// SecuredByAccountID is the asset this loan is secured on — the house
	// behind the mortgage. Zero on everything else, and zero on a loan nobody
	// has paired, which is not an error state.
	SecuredByAccountID ID

	// GoalBalance is money reserved by savings goals held here. It reduces the
	// available balance without leaving the account; there is no shadow ledger.
	GoalBalance  Money
	PendingHolds Money

	CreditLimit    Money
	HasCreditLimit bool

	// RequiresReceipts is AccountRequiresReceipts already answered, and false
	// on a deleted account.
	RequiresReceipts bool
}

func (a Account) IsDebt() bool   { return a.Kind.IsDebt() }
func (a Account) IsManual() bool { return !a.HasProviderBalance }

// CountsInNetWorth reports whether the account's balance is summed into net
// worth: flagged into it, and not ignored.
func (a Account) CountsInNetWorth() bool { return a.IncludeInNetWorth && !a.IsIgnored }

type Split struct {
	ID         ID
	Amount     Money
	CategoryID ID
	Memo       string
	TagIDs     []ID
}

type Transaction struct {
	ID        ID
	AccountID ID
	// Date is when it happened. The register shows this.
	Date   Date
	Amount Money

	// StatementName is the bank's wording, never edited. Rules and series
	// matching read this.
	StatementName string
	// Payee is the display name, editable. Every display path reads this.
	Payee string

	// EffectiveDate is when the row hits cash flow — for a card charge, the due
	// date of the statement it lands in. Zero means "same as Date"; use
	// ReportingDate rather than testing for it, so no caller forgets.
	EffectiveDate Date

	// Notes is in the domain because free-text search reads it: without it a
	// rule keyed on a word only in a note silently stops firing.
	Notes string

	CategoryID ID
	Source     Source
	Currency   string

	// AmountPrimary is the amount in the space's primary currency.
	// HasAmountPrimary is false when no conversion was needed. Aggregates sum
	// PrimaryAmount(), never Amount.
	AmountPrimary    Money
	HasAmountPrimary bool
	FxRateUsed       Rate

	IsPending bool
	// IsEstimate marks an occurrence a provider materialized on a bill's due
	// date before anybody paid it. It holds its series slot so the occurrence
	// is not projected twice, and counts toward nothing. No row is both this
	// and IsPending.
	IsEstimate               bool
	IsDeleted                bool
	IsReviewed               bool
	ExcludedFromReports      bool
	ExcludedFromSpendingPlan bool
	// ReceiptNotNeeded is a person saying this row needs no receipt; see
	// ReceiptStatusOf.
	ReceiptNotNeeded bool

	// TransferPairID is set on *both* legs of a matched transfer. A leg whose
	// partner was deleted must have this released, or it is excluded from
	// profit and loss forever while remaining ineligible to re-match.
	TransferPairID ID

	// PaddedTxnID is set on the income row a padding mail rule writes beside
	// a payroll-deducted purchase, naming that purchase. It changes no figure:
	// the pad is income everywhere income counts. Only lists read it.
	PaddedTxnID ID

	Splits []Split
	TagIDs []ID
}

// IsPadding is an income row that only restores what a paycheck deduction
// took for a purchase. Lists fold it away; every figure still counts it.
func (t Transaction) IsPadding() bool {
	return t.PaddedTxnID != ""
}

// DisplayPayee is what a person reads: the editable name, falling back to the
// bank's wording when nothing has renamed it. Every display path calls this;
// matching reads MatchName (ground rule 5).
func (t Transaction) DisplayPayee() string {
	if t.Payee != "" {
		return t.Payee
	}
	return t.StatementName
}

// MatchName is the one name a matcher compares: the bank's wording, falling
// back to the editable name only for a hand-entered row with no bank wording.
// A rename must not move a row in or out of a match.
func (t Transaction) MatchName() string {
	if t.StatementName != "" {
		return t.StatementName
	}
	return t.Payee
}

// IsUncategorized reports whether any part of this row still needs a category,
// as the register's "uncategorized" filter does.
//
// A split row's category lives on its splits, never on the parent, so
// `CategoryID == ""` is true of every saved split row, categorized or not.
func (t Transaction) IsUncategorized() bool {
	if len(t.Splits) == 0 {
		return t.PartNeedsCategory(nil)
	}
	for i := range t.Splits {
		if t.PartNeedsCategory(&t.Splits[i]) {
			return true
		}
	}
	return false
}

// PartNeedsCategory reports whether this part of the row (the split, or the
// row itself for nil) is uncategorized. A paired transfer leg counts toward
// nothing whatever it is filed under, so it never needs one: the pairing files
// an unsplit leg under Transfer or Credit Card Payment, and a split leg or one
// a person cleared is a transfer all the same.
func (t Transaction) PartNeedsCategory(split *Split) bool {
	if t.TransferPairID != "" {
		return false
	}
	if split != nil {
		return split.CategoryID == ""
	}
	return t.CategoryID == ""
}

// CategoryIDs is every category this row is filed under: its splits' for a
// split row, never the parent's, with an uncategorized part adding none.
func (t Transaction) CategoryIDs() []ID {
	if len(t.Splits) == 0 {
		if t.CategoryID == "" {
			return nil
		}
		return []ID{t.CategoryID}
	}
	out := make([]ID, 0, len(t.Splits))
	for _, split := range t.Splits {
		if split.CategoryID != "" {
			out = append(out, split.CategoryID)
		}
	}
	return out
}

// PrimaryAmount is the figure aggregates sum, falling back to the native amount
// in the single-currency case.
func (t Transaction) PrimaryAmount() Money {
	if t.HasAmountPrimary {
		return t.AmountPrimary
	}
	return t.Amount
}

// ReportingDate resolves which of the two dates applies, defaulting an unset
// effective date to the posted one so callers never write the fallback.
func (t Transaction) ReportingDate(mode DateMode) Date {
	if mode == DateEffective && !t.EffectiveDate.IsZero() {
		return t.EffectiveDate
	}
	return t.Date
}

// Posting is a transaction resolved against its account and its category, so
// no predicate is handed one row's account with another's category.
type Posting struct {
	Txn      Transaction
	Account  Account
	Category Category
	// HasCategory distinguishes an uncategorized row from one whose category
	// happens to be the zero value.
	HasCategory bool
}

func (p Posting) Amount() Money { return p.Txn.PrimaryAmount() }

// NativeAmount is the movement in the transaction's own currency, for
// per-account arithmetic: account balances and reserves are stored in the
// account's currency. Cross-account aggregates use Amount().
func (p Posting) NativeAmount() Money { return p.Txn.Amount }

// BalancePoint is one account's balance on one day, from the materialized
// history the net-worth chart reads.
type BalancePoint struct {
	AccountID ID
	On        Date
	Balance   Money

	// Observed is a balance reported from outside for the day — the history
	// the Simplifi import carried over — rather than one derived here. It is
	// read as written, never re-derived (RederiveHistory).
	Observed bool
	// Anchor is the provider figure a derived point was walked back from, when
	// one was recorded (AnchorFor).
	Anchor    BalanceAnchor
	HasAnchor bool
}

type Goal struct {
	ID           ID
	Name         string
	AccountID    ID
	TargetAmount Money
	TargetOn     Date
	// IsTakenFromPlan decides whether contributions appear in the spending
	// plan's Goals bucket. A goal funded from money already counted as spend
	// must not be counted twice.
	IsTakenFromPlan bool
	// FundingAccountIDs — goals can draw on more than one account. The naming
	// account is the one whose GoalBalance the reserve inflates.
	FundingAccountIDs []ID
	// ClosedOn is when the user closed the goal; zero while it is open. A
	// closed goal reserves nothing and keeps its rows and figures.
	ClosedOn Date
}

func (g Goal) IsClosed() bool { return !g.ClosedOn.IsZero() }
