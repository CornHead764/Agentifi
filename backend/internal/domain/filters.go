package domain

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// The one filter (ground rule 3): watchlists, envelopes, reports and the
// transaction list share it. Items of the same field are an or, items of
// different fields an and. Matching is per allocation, not per transaction: a
// split contributes only its own share (MatchingParts).

// FilterField is the Filter panel's facets, the Advanced radio pairs, and the
// fields only the rules builder and the search DSL reach.
type FilterField string

const (
	FieldCategory FilterField = "category"
	FieldPayee    FilterField = "payee"
	FieldTag      FilterField = "tag"
	FieldAccount  FilterField = "account"
	FieldFlag     FilterField = "flag"
	// FieldText, FieldAmount and FieldDate are the search box and the two
	// range controls.
	FieldText                       FilterField = "text"
	FieldAmount                     FilterField = "amount"
	FieldDate                       FilterField = "date"
	FieldIsBillOrSubscription       FilterField = "is_bill_or_subscription"
	FieldIsExcludedFromReports      FilterField = "is_excluded_from_reports"
	FieldIsExcludedFromSpendingPlan FilterField = "is_excluded_from_spending_plan"
	FieldIsReviewed                 FilterField = "is_reviewed"

	// FieldStatementName is the bank's unchanging wording, so a rule matching
	// on it keeps firing after its own rename action lands.
	FieldStatementName FilterField = "statement_name"
	// The search DSL's is:pending, is:uncategorized and is:tags. A paired
	// transfer leg is never uncategorized (Transaction.PartNeedsCategory).
	FieldIsPending       FilterField = "is_pending"
	FieldIsUncategorized FilterField = "is_uncategorized"
	FieldHasTag          FilterField = "has_tag"

	// FieldIsCategoryUndetermined is an uncategorized row the assistant looked
	// at and could not place. Its own field because the two facts are stored
	// in different places.
	FieldIsCategoryUndetermined FilterField = "is_category_undetermined"

	// FieldHasCategorySuggestion is a row with a pending assistant category
	// proposal. Read from Facets, so a rule cannot match on it.
	FieldHasCategorySuggestion FilterField = "has_category_suggestion"

	// FieldHasAttachment is a row with any document on it, attached by hand
	// or filed as a receipt: what the register's paperclip counts. Read from
	// Facets, so a rule cannot match on it.
	FieldHasAttachment FilterField = "has_attachment"

	// FieldIsMissingReceipt is a row whose account requires receipts with
	// none behind it, by ReceiptStatusOf. Read from Facets, so a rule cannot
	// match on it.
	FieldIsMissingReceipt FilterField = "is_missing_receipt"
)

// FilterOperator is how an item compares; the zero value is the field's
// natural comparison (membership, or an inclusive range). On the name fields
// it distinguishes the panel's exact any-of multi-select from the rules
// builder's substring all-of keyword chips.
type FilterOperator string

const (
	OpIn          FilterOperator = "in"
	OpContains    FilterOperator = "contains"
	OpIsExactly   FilterOperator = "is_exactly"
	OpEquals      FilterOperator = "equals"
	OpBetween     FilterOperator = "between"
	OpGreaterThan FilterOperator = "greater_than"
	OpLessThan    FilterOperator = "less_than"
	OpIsTrue      FilterOperator = "is_true"
	// OpMatches: any one of the patterns may match; see filterTextMatches.
	OpMatches FilterOperator = "matches"
)

// Facets are row attributes a filter can test that Transaction does not carry.
// An unknown facet fails the item rather than passing it, so a filter can only
// narrow; the zero value is "nothing known".
type Facets struct {
	UserFlag             string
	IsBillOrSubscription bool
	// CategoryChecked is that the assistant finished a category check and
	// filed nothing; with the row still uncategorized it is "Undetermined". A
	// person filing the row by hand settles it without clearing anything.
	CategoryChecked bool
	// HasCategorySuggestion is loaded only by callers that offer the field;
	// elsewhere it is false.
	HasCategorySuggestion bool
	// HasAttachment is the row's, so every split of it has one. Loaded only
	// by callers that offer the field.
	HasAttachment bool
	// MissingReceipt is ReceiptStatusOf answering ReceiptMissing. Loaded only
	// by callers that offer the field.
	MissingReceipt bool
}

// FilterItem is one facet's selection. One wide record rather than a type per
// field, so the export round-trips a homogeneous list.
type FilterItem struct {
	Field    FilterField
	Operator FilterOperator
	// Values holds ids for category/tag/account, names or keywords for
	// payee/statement name, colours for flag.
	Values []string

	Minimum    Money
	HasMinimum bool
	Maximum    Money
	HasMaximum bool

	// Start and End bound FieldDate; a zero Date is an open end.
	Start Date
	End   Date

	Text string

	// State is the Advanced radio pair (is / is not); on an amount item it is
	// Expense/Income: true income, false expense. HasState distinguishes
	// "either" from "expense".
	State    bool
	HasState bool

	// Negated wraps the whole item, so an excluded three-category item
	// excludes all three.
	Negated bool

	// GroupIndex is the rules builder's + button: items in one group are
	// AND'd, groups are OR'd. An ungrouped filter is a plain conjunction.
	GroupIndex int
}

// Filter is a reusable slice of the register, with resolved items. An empty
// filter matches everything; the rule engine checks for an empty condition set
// itself.
type Filter struct {
	ID    ID
	Items []FilterItem
	Name  string
}

// Validate reports a stored filter the evaluator cannot honour. The evaluator
// fails an unknown field silently (narrowing is safe), so the loud check is
// here.
func (f Filter) Validate() error {
	for _, item := range f.Items {
		if !filterFieldIsKnown(item.Field) {
			return fmt.Errorf("filter %s: unhandled filter field %s", f.ID, item.Field)
		}
	}
	return nil
}

// Match is one matching allocation: a whole row, or one split of one (Split
// nil for a row with none). It carries the split's own amount.
type Match struct {
	Posting Posting
	Split   *Split
	Amount  Money
}

// CategoryID is the split's category when there is one — never its parent's,
// or an uncategorized split is filed under the first allocation.
func (m Match) CategoryID() ID {
	if m.Split != nil {
		return m.Split.CategoryID
	}
	return m.Posting.Txn.CategoryID
}

func (m Match) Payee() string {
	return filterDisplayPayee(m.Posting.Txn)
}

// TagIDs is the row's tags and the matched split's, sorted for stable
// grouping.
func (m Match) TagIDs() []ID {
	seen := map[ID]bool{}
	var out []ID
	add := func(ids []ID) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(m.Posting.Txn.TagIDs)
	if m.Split != nil {
		add(m.Split.TagIDs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Matches reports whether any part of this row satisfies every item.
func Matches(filter Filter, posting Posting, facets Facets, mode DateMode) bool {
	return len(MatchingParts(filter, posting, facets, mode)) > 0
}

// Allocations is every part of one row, in the shape MatchingParts returns. A
// split's share is scaled to the primary currency as it is there, so the parts
// sum to the row.
func Allocations(posting Posting) []Match {
	txn := posting.Txn
	if len(txn.Splits) == 0 {
		return []Match{{Posting: posting, Amount: posting.Amount()}}
	}
	out := make([]Match, 0, len(txn.Splits))
	for i := range txn.Splits {
		split := &txn.Splits[i]
		out = append(out, Match{
			Posting: posting,
			Split:   split,
			Amount:  SplitAmountPrimary(txn, *split),
		})
	}
	return out
}

// MatchingParts is the allocations of this row that satisfy every item.
func MatchingParts(filter Filter, posting Posting, facets Facets, mode DateMode) []Match {
	txn := posting.Txn
	if len(txn.Splits) == 0 {
		if filterSatisfies(filter, posting, nil, facets, mode) {
			return []Match{{Posting: posting, Amount: posting.Amount()}}
		}
		return nil
	}

	var out []Match
	for i := range txn.Splits {
		split := &txn.Splits[i]
		if filterSatisfies(filter, posting, split, facets, mode) {
			out = append(out, Match{
				Posting: posting,
				Split:   split,
				Amount:  SplitAmountPrimary(txn, *split),
			})
		}
	}
	return out
}

// PartialParts is the splits a filter kept of a split row it kept only some
// of; nil for a row kept whole. The register draws a non-nil result as a
// partial view beside the full amount.
func PartialParts(filter Filter, posting Posting, facets Facets, mode DateMode) []Match {
	if len(posting.Txn.Splits) == 0 {
		return nil
	}
	parts := MatchingParts(filter, posting, facets, mode)
	if len(parts) == 0 || len(parts) == len(posting.Txn.Splits) {
		return nil
	}
	return parts
}

// MatchedAmount is how much of one register row a filter kept. A row kept
// whole contributes Posting.Amount, not the sum of its splits, which are
// scaled one at a time and can round a cent away.
func MatchedAmount(posting Posting, partial []Match) Money {
	if partial == nil {
		return posting.Amount()
	}
	return Sum(partial, func(m Match) Money { return m.Amount })
}

// Select is the rows a filter shows, in the order given. facets may be nil.
func Select(filter Filter, postings []Posting, facets map[ID]Facets, mode DateMode) []Posting {
	var out []Posting
	for _, posting := range postings {
		if Matches(filter, posting, facets[posting.Txn.ID], mode) {
			out = append(out, posting)
		}
	}
	return out
}

// filterSatisfies tests every item of any one group. Groups are alternatives:
// the rules builder's second keyword set widens what a rule catches.
func filterSatisfies(
	filter Filter, posting Posting, split *Split, facets Facets, mode DateMode,
) bool {
	if len(filter.Items) == 0 {
		return true
	}

	groups := map[int][]FilterItem{}
	var order []int
	for _, item := range filter.Items {
		if _, seen := groups[item.GroupIndex]; !seen {
			order = append(order, item.GroupIndex)
		}
		groups[item.GroupIndex] = append(groups[item.GroupIndex], item)
	}

	for _, index := range order {
		satisfied := true
		for _, item := range groups[index] {
			if !filterPasses(item, posting, split, facets, mode) {
				satisfied = false
				break
			}
		}
		if satisfied {
			return true
		}
	}
	return false
}

func filterPasses(
	item FilterItem, posting Posting, split *Split, facets Facets, mode DateMode,
) bool {
	result := filterTest(item, posting, split, facets, mode)
	if item.Negated {
		return !result
	}
	return result
}

func filterTest(
	item FilterItem, posting Posting, split *Split, facets Facets, mode DateMode,
) bool {
	txn := posting.Txn

	switch item.Field {
	case FieldCategory:
		categoryID := filterCategoryID(txn, split)
		// An uncategorized row never matches a membership test;
		// FieldIsUncategorized asks for those.
		return categoryID != "" && filterContains(item.Values, string(categoryID))
	case FieldPayee:
		return filterTextMatches(item, filterDisplayPayee(txn))
	case FieldStatementName:
		return filterTextMatches(item, txn.StatementName)
	case FieldTag:
		return filterAnyTag(item.Values, txn, split)
	case FieldHasTag:
		if len(item.Values) == 0 {
			return len(filterTagIDs(txn, split)) > 0
		}
		return filterAnyTag(item.Values, txn, split)
	case FieldAccount:
		return filterContains(item.Values, string(txn.AccountID))
	case FieldFlag:
		return facets.UserFlag != "" && filterContains(item.Values, facets.UserFlag)
	case FieldText:
		return filterContainsAnywhere(item.Text, txn, split)
	case FieldAmount:
		amount := posting.Amount()
		if split != nil {
			amount = SplitAmountPrimary(txn, *split)
		}
		return filterAmountMatches(item, amount)
	case FieldDate:
		on := ReportingDate(txn, mode)
		if !item.Start.IsZero() && on.Before(item.Start) {
			return false
		}
		return item.End.IsZero() || on.NotAfter(item.End)
	case FieldIsBillOrSubscription:
		return item.HasState && facets.IsBillOrSubscription == item.State
	case FieldIsExcludedFromReports:
		return item.HasState && txn.ExcludedFromReports == item.State
	case FieldIsExcludedFromSpendingPlan:
		return item.HasState && txn.ExcludedFromSpendingPlan == item.State
	case FieldIsReviewed:
		return item.HasState && txn.IsReviewed == item.State
	case FieldIsPending:
		return item.HasState && txn.IsPending == item.State
	case FieldIsUncategorized:
		return item.HasState && txn.PartNeedsCategory(split) == item.State
	case FieldIsCategoryUndetermined:
		undetermined := facets.CategoryChecked && txn.PartNeedsCategory(split)
		return item.HasState && undetermined == item.State
	case FieldHasCategorySuggestion:
		return item.HasState && facets.HasCategorySuggestion == item.State
	case FieldHasAttachment:
		return item.HasState && facets.HasAttachment == item.State
	case FieldIsMissingReceipt:
		return item.HasState && facets.MissingReceipt == item.State
	}
	// An unrecognized field fails; Filter.Validate catches it loudly.
	return false
}

// filterTextMatches runs one name field against the item's values:
//
//   - OpContains: every chip must be present (alternatives are groups);
//   - OpMatches: any pattern may match;
//   - otherwise: exact any-of membership, as a facet multi-select means.
//
// A blank chip fails: as a substring of everything it would hand a rule the
// whole ledger.
func filterTextMatches(item FilterItem, haystack string) bool {
	keywords := make([]string, 0, len(item.Values))
	for _, value := range item.Values {
		if strings.TrimSpace(value) != "" {
			keywords = append(keywords, value)
		}
	}
	if len(keywords) == 0 {
		return false
	}

	// A pattern runs against the raw text: SafeSearch is already
	// case-insensitive, and folding would strip characters an anchored
	// pattern is written against.
	if item.Operator == OpMatches {
		for _, pattern := range keywords {
			if SafeSearch(pattern, haystack) {
				return true
			}
		}
		return false
	}

	text := FoldName(haystack)
	if item.Operator == OpContains {
		for _, keyword := range keywords {
			if !strings.Contains(text, FoldName(keyword)) {
				return false
			}
		}
		return true
	}
	for _, keyword := range keywords {
		if text == FoldName(keyword) {
			return true
		}
	}
	return false
}

// filterAmountMatches compares on the magnitude: "greater than 50" for an
// expense means bigger, not less negative. The Expense/Income radio rides on
// State.
func filterAmountMatches(item FilterItem, amount Money) bool {
	if item.HasState {
		if item.State && !amount.IsIncome() {
			return false
		}
		if !item.State && !amount.IsExpense() {
			return false
		}
	}

	size := amount.Abs()
	switch item.Operator {
	case OpEquals:
		return item.HasMinimum && size.Equal(item.Minimum.Abs())
	case OpGreaterThan:
		return item.HasMinimum && size.GreaterThan(item.Minimum.Abs())
	case OpLessThan:
		return item.HasMaximum && size.LessThan(item.Maximum.Abs())
	}
	if item.HasMinimum && size.LessThan(item.Minimum.Abs()) {
		return false
	}
	return !item.HasMaximum || !size.GreaterThan(item.Maximum.Abs())
}

// filterContainsAnywhere is free text over both names and the memo, so a
// renamed row is still findable by what the bank called it.
func filterContainsAnywhere(needle string, txn Transaction, split *Split) bool {
	if strings.TrimSpace(needle) == "" {
		return false
	}
	haystacks := []string{txn.Payee, txn.StatementName, txn.Notes}
	if split != nil {
		haystacks = append(haystacks, split.Memo)
	} else {
		for _, other := range txn.Splits {
			haystacks = append(haystacks, other.Memo)
		}
	}

	wanted := FoldName(needle)
	for _, text := range haystacks {
		if strings.Contains(FoldName(text), wanted) {
			return true
		}
	}
	return false
}

// FoldName is the case- and accent-insensitive form for name comparison: a
// bank writes the same merchant with and without diacritics. Words splits
// what it folds.
func FoldName(text string) string {
	decomposed := norm.NFKD.String(text)
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// filterDisplayPayee is the name a person reads (ground rule 5).
// FieldStatementName reads the raw one and must not come through here.
func filterDisplayPayee(txn Transaction) string {
	if txn.Payee != "" {
		return txn.Payee
	}
	return txn.StatementName
}

// SplitAmountPrimary is a split's share in the space's primary currency,
// scaled by the row's own conversion so the parts sum to the whole.
func SplitAmountPrimary(txn Transaction, split Split) Money {
	if !txn.HasAmountPrimary {
		return split.Amount
	}
	share, ok := Ratio(txn.AmountPrimary, txn.Amount)
	if !ok {
		return split.Amount
	}
	return split.Amount.Scale(share)
}

func filterCategoryID(txn Transaction, split *Split) ID {
	if split != nil {
		return split.CategoryID
	}
	return txn.CategoryID
}

func filterTagIDs(txn Transaction, split *Split) []ID {
	tags := txn.TagIDs
	if split != nil {
		tags = append(append([]ID{}, tags...), split.TagIDs...)
	}
	return tags
}

func filterAnyTag(values []string, txn Transaction, split *Split) bool {
	for _, id := range filterTagIDs(txn, split) {
		if filterContains(values, string(id)) {
			return true
		}
	}
	return false
}

func filterContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func filterFieldIsKnown(field FilterField) bool {
	switch field {
	case FieldCategory, FieldPayee, FieldTag, FieldAccount, FieldFlag, FieldText,
		FieldAmount, FieldDate, FieldIsBillOrSubscription, FieldIsExcludedFromReports,
		FieldIsExcludedFromSpendingPlan, FieldIsReviewed, FieldStatementName,
		FieldIsPending, FieldIsUncategorized, FieldHasTag, FieldIsCategoryUndetermined,
		FieldHasCategorySuggestion, FieldHasAttachment, FieldIsMissingReceipt:
		return true
	}
	return false
}

// Tests reports whether any item reads this field, so a caller can skip
// loading a facet.
func (f Filter) Tests(field FilterField) bool {
	for _, item := range f.Items {
		if item.Field == field {
			return true
		}
	}
	return false
}

// RuleCannotMatch is a field a rule, guidance note or automation trigger may
// not test: they carry no facets, and the evaluator would fail such an item
// silently. An attachment would not help them anyway: they run as the row
// arrives, before anything can be filed on it.
func RuleCannotMatch(field FilterField) bool {
	return field == FieldIsBillOrSubscription || field == FieldHasCategorySuggestion ||
		field == FieldHasAttachment || field == FieldIsMissingReceipt
}
