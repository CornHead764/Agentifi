package domain

import "strings"

// ActivityKind is what a row on a brokerage account was (the Investments
// screen's Action column), derived rather than stored: there is no
// investment-transaction table.
//
// The category is asked last, and only its kind: brokerages' dividends turn up
// filed under Fast Food, so the bank's wording (StatementName before Payee)
// is the signal. A row nothing recognizes is ActivityUnknown rather than a
// guess from the sign.
type ActivityKind string

const (
	ActivityBuy          ActivityKind = "buy"
	ActivitySell         ActivityKind = "sell"
	ActivityDividend     ActivityKind = "dividend"
	ActivityReinvestment ActivityKind = "reinvestment"
	ActivityInterest     ActivityKind = "interest"
	ActivityFee          ActivityKind = "fee"
	// ActivityContribution and ActivityWithdrawal are money crossing the
	// portfolio's boundary: the flows the return calculation divides by.
	ActivityContribution ActivityKind = "contribution"
	ActivityWithdrawal   ActivityKind = "withdrawal"
	// ActivityUnknown is a row no signal named.
	ActivityUnknown ActivityKind = "unknown"
)

// ActivityKinds is every kind in summary order: earned, traded, cost, unknown.
var ActivityKinds = []ActivityKind{
	ActivityDividend, ActivityInterest, ActivityReinvestment,
	ActivityBuy, ActivitySell, ActivityFee,
	ActivityContribution, ActivityWithdrawal, ActivityUnknown,
}

// IsIncome reports whether the portfolio earned this. A reinvested dividend
// counts here and is deliberately not a cash flow.
func (k ActivityKind) IsIncome() bool {
	return k == ActivityDividend || k == ActivityInterest || k == ActivityReinvestment
}

// ClassifiedActivity is one row with the name the classifier gave it.
type ClassifiedActivity struct {
	Posting Posting
	Kind    ActivityKind
}

// ActivitySummary is one kind's contribution over a window. Total keeps the
// ledger's sign (fees negative).
type ActivitySummary struct {
	Kind  ActivityKind
	Count int
	Total Money
}

// Long, unambiguous words, matched as substrings.
var activityPhrases = []struct {
	phrase string
	kind   ActivityKind
}{
	// Before dividend: "DIVIDEND REINVESTMENT" is both.
	{"reinvest", ActivityReinvestment},
	{"dividend", ActivityDividend},
	{"capital gain", ActivityDividend},
	{"cap gain", ActivityDividend},
	{"interest", ActivityInterest},
	{"commission", ActivityFee},
	{"expense ratio", ActivityFee},
	{"advisory", ActivityFee},
	{"management fee", ActivityFee},
	{"service charge", ActivityFee},
	{"purchase", ActivityBuy},
	{"bought", ActivityBuy},
	{"sold", ActivitySell},
	{"contribution", ActivityContribution},
	{"rollover", ActivityContribution},
	{"withdrawal", ActivityWithdrawal},
}

// Short words matched only whole: "div" is inside "diversified", "int" inside
// "international", "fee" inside "feeder".
var activityWords = map[string]ActivityKind{
	"div":  ActivityDividend,
	"divs": ActivityDividend,
	"int":  ActivityInterest,
	"fee":  ActivityFee,
	"fees": ActivityFee,
	"buy":  ActivityBuy,
	"sell": ActivitySell,
}

// ClassifyActivity names one row of an investment account. The order is the
// order of trust: the bank's wording (nobody edited it), then the transfer
// pair, then the category's kind (never its name) where the sign agrees.
func ClassifyActivity(p Posting) ActivityKind {
	text := strings.ToLower(p.Txn.StatementName + " " + p.Txn.Payee)
	for _, entry := range activityPhrases {
		if strings.Contains(text, entry.phrase) {
			return entry.kind
		}
	}
	for _, word := range Words(text) {
		if kind, ok := activityWords[word]; ok {
			return kind
		}
	}

	// A paired leg provably moved between the household's own accounts.
	if p.IsTransfer() {
		if p.Amount().IsNegative() {
			return ActivityWithdrawal
		}
		return ActivityContribution
	}

	// Misfiled rows carry an expense category and fall through to unknown.
	if p.HasCategory && p.Amount().IsPositive() && p.Category.Kind == CategoryIncome {
		return ActivityDividend
	}
	return ActivityUnknown
}

// ClassifyActivities names every row, keeping the caller's order.
func ClassifyActivities(postings []Posting) []ClassifiedActivity {
	out := make([]ClassifiedActivity, 0, len(postings))
	for _, posting := range postings {
		out = append(out, ClassifiedActivity{Posting: posting, Kind: ClassifyActivity(posting)})
	}
	return out
}

// SummarizeActivity totals each kind that occurs, in ActivityKinds order. A
// kind with no rows is left out: "no fees" and "fees of nothing" differ.
func SummarizeActivity(rows []ClassifiedActivity) []ActivitySummary {
	counts := make(map[ActivityKind]int, len(ActivityKinds))
	totals := make(map[ActivityKind]Money, len(ActivityKinds))
	for _, row := range rows {
		counts[row.Kind]++
		totals[row.Kind] = totals[row.Kind].Add(row.Posting.Amount())
	}
	out := make([]ActivitySummary, 0, len(ActivityKinds))
	for _, kind := range ActivityKinds {
		if counts[kind] == 0 {
			continue
		}
		out = append(out, ActivitySummary{Kind: kind, Count: counts[kind], Total: totals[kind].Round()})
	}
	return out
}

// InvestmentIncome is dividends, interest and reinvestments over the window.
// A reinvestment posts as the purchase on some statements and the
// distribution on others, so it is summed at whatever sign the ledger holds.
func InvestmentIncome(rows []ClassifiedActivity) Money {
	total := Zero
	for _, row := range rows {
		if row.Kind.IsIncome() {
			total = total.Add(row.Posting.Amount())
		}
	}
	return total.Round()
}

// InvestmentFees is what the portfolio cost over the window, as a positive
// magnitude.
func InvestmentFees(rows []ClassifiedActivity) Money {
	total := Zero
	for _, row := range rows {
		if row.Kind == ActivityFee {
			total = total.Add(row.Posting.Amount())
		}
	}
	return total.Neg().Round()
}
