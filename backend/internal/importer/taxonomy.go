package importer

import (
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Simplifi's enum values, translated to ours. Every lookup fails loudly on a
// value it has not been taught: a guess, such as domain.KindCash for an
// unrecognised subType, could put a mortgage on the asset side of net worth.

// key normalises a stored value: "Roth IRA", "roth-ira" and "ROTH_IRA" are one
// value, and every table is keyed on this form.
func key(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	lastWasSeparator := false
	for _, r := range strings.ToUpper(strings.TrimSpace(value)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastWasSeparator = false
			continue
		}
		if !lastWasSeparator {
			b.WriteByte('_')
			lastWasSeparator = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// lookup translates one enum value, or refuses to with an error naming the
// value and the field; there is no default.
func lookup[T any](r *record, table map[string]T, field string, value any, what string) T {
	var zero T
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		r.fail(field, "expected a %s, found %s", what, typeName(value))
		return zero
	}
	found, known := table[key(text)]
	if !known {
		r.fail(field, "unknown %s: %q", what, text)
		return zero
	}
	return found
}

// accountTaxon is the pair our schema splits an account into: kind drives
// arithmetic, accountType is the label and never appears in a calculation.
type accountTaxon struct {
	kind        domain.AccountKind
	accountType string
}

// accountSubtypes is Simplifi's fine-grained subType.
var accountSubtypes = map[string]accountTaxon{
	"CHECKING":               {domain.KindCash, "checking"},
	"SAVINGS":                {domain.KindCash, "savings"},
	"CASH":                   {domain.KindCash, "cash"},
	"CASH_MANAGEMENT":        {domain.KindCash, "cash_management"},
	"DIGITAL_CASH":           {domain.KindCash, "digital_cash"},
	"MONEY_MARKET":           {domain.KindCash, "money_market"},
	"CD":                     {domain.KindCash, "cd"},
	"CERTIFICATE_OF_DEPOSIT": {domain.KindCash, "cd"},
	"OTHER_BANKING":          {domain.KindCash, "other_banking"},
	"CREDIT_CARD":            {domain.KindCreditCard, "credit_card"},
	"LINE_OF_CREDIT":         {domain.KindCreditCard, "line_of_credit"},
	"OTHER_CREDIT":           {domain.KindCreditCard, "other_credit"},
	"BROKERAGE":              {domain.KindInvestment, "brokerage"},
	"401K":                   {domain.KindInvestment, "401k"},
	"ROTH_401K":              {domain.KindInvestment, "roth_401k"},
	"403B":                   {domain.KindInvestment, "403b"},
	"IRA":                    {domain.KindInvestment, "ira"},
	"ROTH_IRA":               {domain.KindInvestment, "roth_ira"},
	"SEP_IRA":                {domain.KindInvestment, "sep_ira"},
	"SIMPLE_IRA":             {domain.KindInvestment, "simple_ira"},
	"KEOGH":                  {domain.KindInvestment, "keogh"},
	"529_PLAN":               {domain.KindInvestment, "529_plan"},
	"529":                    {domain.KindInvestment, "529_plan"},
	"CUSTODIAL":              {domain.KindInvestment, "custodial"},
	"UGMA_UTMA":              {domain.KindInvestment, "custodial"},
	"OTHER_INVESTMENT":       {domain.KindInvestment, "other_investment"},
	"OTHER_INVESTMENTS":      {domain.KindInvestment, "other_investment"},
	"REAL_ESTATE":            {domain.KindAsset, "real_estate"},
	"PROPERTY":               {domain.KindAsset, "real_estate"},
	"VEHICLE":                {domain.KindAsset, "vehicle"},
	"AUTOMOBILE":             {domain.KindAsset, "vehicle"},
	"OTHER_ASSET":            {domain.KindAsset, "other_asset"},
	"VEHICLE_LOAN":           {domain.KindLoan, "vehicle_loan"},
	"AUTO_LOAN":              {domain.KindLoan, "vehicle_loan"},
	"MORTGAGE":               {domain.KindLoan, "mortgage"},
	"HOME_EQUITY_LOAN":       {domain.KindLoan, "home_equity_loan"},
	"STUDENT_LOAN":           {domain.KindLoan, "student_loan"},
	"CONSUMER_LOAN":          {domain.KindLoan, "consumer_loan"},
	"CONSTRUCTION_LOAN":      {domain.KindLoan, "construction_loan"},
	"MILITARY_LOAN":          {domain.KindLoan, "military_loan"},
	"OTHER_LOAN":             {domain.KindLoan, "other_loan"},
	"OTHER_LIABILITY":        {domain.KindLoan, "other_liability"},
}

// accountTypes is the coarse type, used only when subType is absent (or
// UNKNOWN), never as a fallback for an unrecognised subType.
var accountTypes = map[string]accountTaxon{
	"BANK":       {domain.KindCash, "checking"},
	"BANKING":    {domain.KindCash, "checking"},
	"CASH":       {domain.KindCash, "cash"},
	"CREDIT":     {domain.KindCreditCard, "credit_card"},
	"INVESTMENT": {domain.KindInvestment, "brokerage"},
	"LOAN":       {domain.KindLoan, "other_loan"},
	"LIABILITY":  {domain.KindLoan, "other_liability"},
	"ASSET":      {domain.KindAsset, "other_asset"},
	"PROPERTY":   {domain.KindAsset, "real_estate"},
	// Simplifi writes subType="UNKNOWN" on accounts whose kind is carried by
	// type: a house, a car, a savings goal.
	"REAL_ESTATE": {domain.KindAsset, "real_estate"},
	"VEHICLE":     {domain.KindAsset, "vehicle"},
	"GOAL":        {domain.KindCash, "savings"},
}

var categoryKinds = map[string]domain.CategoryKind{
	"INCOME":    domain.CategoryIncome,
	"EXPENSE":   domain.CategoryExpense,
	"EXPENSES":  domain.CategoryExpense,
	"SPENDING":  domain.CategoryExpense,
	"TRANSFER":  domain.CategoryTransfer,
	"TRANSFERS": domain.CategoryTransfer,
}

// filterFields maps filterItems[].type to the one filter vocabulary.
var filterFields = map[string]domain.FilterField{
	"CATEGORY":   domain.FieldCategory,
	"CATEGORIES": domain.FieldCategory,
	"COA":        domain.FieldCategory,
	// Simplifi's category-or-account facet; in a filter its ids are
	// categories.
	"CHARTOFACCOUNT":           domain.FieldCategory,
	"CHART_OF_ACCOUNT":         domain.FieldCategory,
	"PAYEE":                    domain.FieldPayee,
	"PAYEES":                   domain.FieldPayee,
	"RENAMED_PAYEE":            domain.FieldPayee,
	"STATEMENT_NAME":           domain.FieldStatementName,
	"ORIGINAL_PAYEE":           domain.FieldStatementName,
	"TAG":                      domain.FieldTag,
	"TAGS":                     domain.FieldTag,
	"ACCOUNT":                  domain.FieldAccount,
	"ACCOUNTS":                 domain.FieldAccount,
	"FLAG":                     domain.FieldFlag,
	"FLAGS":                    domain.FieldFlag,
	"USER_FLAG":                domain.FieldFlag,
	"TEXT":                     domain.FieldText,
	"SEARCH":                   domain.FieldText,
	"FREE_TEXT":                domain.FieldText,
	"MEMO":                     domain.FieldText,
	"AMOUNT":                   domain.FieldAmount,
	"AMOUNT_RANGE":             domain.FieldAmount,
	"DATE":                     domain.FieldDate,
	"DATE_RANGE":               domain.FieldDate,
	"IS_BILL_OR_SUBSCRIPTION":  domain.FieldIsBillOrSubscription,
	"IS_BILL":                  domain.FieldIsBillOrSubscription,
	"IS_SUBSCRIPTION":          domain.FieldIsBillOrSubscription,
	"IS_EXCLUDED_FROM_REPORTS": domain.FieldIsExcludedFromReports,
	"IS_EXCLUDED_FROM_F2S":     domain.FieldIsExcludedFromSpendingPlan,
	"IS_EXCLUDED_FROM_BUDGETS": domain.FieldIsExcludedFromSpendingPlan,
	"IS_REVIEWED":              domain.FieldIsReviewed,
	"IS_PENDING":               domain.FieldIsPending,
	"IS_UNCATEGORIZED":         domain.FieldIsUncategorized,
	"HAS_TAG":                  domain.FieldHasTag,
}

// filterIDFields are the facets whose ids are foreign keys into a store we
// have already imported. Everything else lands in value_texts.
var filterIDFields = map[domain.FilterField]string{
	domain.FieldCategory: kindCategory,
	domain.FieldTag:      kindTag,
	domain.FieldAccount:  kindAccount,
}

// Filter scopes. Descriptive only: the evaluator does not read one.
const (
	scopeEnvelope  = "envelope"
	scopeWatchlist = "watchlist"
	scopeReport    = "report"
	scopeSavedView = "saved_view"
	scopeRule      = "rule"
	scopeAdHoc     = "ad_hoc"
)

var filterScopes = map[string]string{
	"ENVELOPE":            scopeEnvelope,
	"PLANNED_SPENDING":    scopeEnvelope,
	"PLANNED_SPEND":       scopeEnvelope,
	"WATCHLIST":           scopeWatchlist,
	"SPENDING_WATCH_LIST": scopeWatchlist,
	"SPENDING_WATCHLIST":  scopeWatchlist,
	"SPENDINGWATCHLIST":   scopeWatchlist,
	"FREETOSPEND":         scopeEnvelope,
	"FREE_TO_SPEND":       scopeEnvelope,
	"REPORT":              scopeReport,
	"REPORTS":             scopeReport,
	"SAVED_VIEW":          scopeSavedView,
	"RULE":                scopeRule,
	"RENAME_RULE":         scopeRule,
	"AD_HOC":              scopeAdHoc,
	"TRANSACTION":         scopeAdHoc,
	"TRANSACTIONS":        scopeAdHoc,
	"SEARCH":              scopeAdHoc,
}

var filterOperators = map[string]domain.FilterOperator{
	"IN":           domain.OpIn,
	"ONE_OF":       domain.OpIn,
	"CONTAINS":     domain.OpContains,
	"IS_EXACTLY":   domain.OpIsExactly,
	"EXACTLY":      domain.OpIsExactly,
	"EQUALS":       domain.OpEquals,
	"BETWEEN":      domain.OpBetween,
	"GREATER_THAN": domain.OpGreaterThan,
	"LESS_THAN":    domain.OpLessThan,
	"IS_TRUE":      domain.OpIsTrue,
}

var seriesKinds = map[string]domain.SeriesKind{
	"BILL":                domain.SeriesBill,
	"BILLS":               domain.SeriesBill,
	"EXPENSE":             domain.SeriesBill,
	"SUBSCRIPTION":        domain.SeriesSubscription,
	"SUBSCRIPTIONS":       domain.SeriesSubscription,
	"INCOME":              domain.SeriesIncome,
	"DEPOSIT":             domain.SeriesIncome,
	"TRANSFER":            domain.SeriesTransfer,
	"CREDIT_CARD_PAYMENT": domain.SeriesCreditCardPayment,
	"CC_PAYMENT":          domain.SeriesCreditCardPayment,
}

var frequencies = map[string]domain.Frequency{
	"DAILY":    domain.FreqDaily,
	"DAY":      domain.FreqDaily,
	"WEEKLY":   domain.FreqWeekly,
	"WEEK":     domain.FreqWeekly,
	"MONTHLY":  domain.FreqMonthly,
	"MONTH":    domain.FreqMonthly,
	"YEARLY":   domain.FreqYearly,
	"ANNUALLY": domain.FreqYearly,
	"YEAR":     domain.FreqYearly,
}

var recurrenceAliases = map[string]domain.RecurrenceAlias{
	"EVERY_WEEK":       domain.AliasEveryWeek,
	"EVERY_MONTH":      domain.AliasEveryMonth,
	"TWICE_A_MONTH":    domain.AliasTwiceAMonth,
	"EVERY_QUARTER":    domain.AliasEveryQuarter,
	"EVERY_YEAR":       domain.AliasEveryYear,
	"EVERY_X_DAYS":     domain.AliasEveryXDays,
	"MULTIPLE_FIXED":   domain.AliasMultipleFixed,
	"ONE_TIME":         domain.AliasOneTime,
	"ONE_TIME_PAYMENT": domain.AliasOneTime,
}

// matchCriteria is the four Match Criteria of the series editor, as
// series.match_criteria stores them.
var matchCriteria = map[string]string{
	"AUTO":          "auto",
	"AUTO_MATCH":    "auto",
	"ANY":           "any",
	"ANY_AMOUNT":    "any",
	"EXACT":         "exact",
	"EXACT_AMOUNT":  "exact",
	"RANGE":         "range",
	"LIMITED_RANGE": "range",
}

var projectionTypes = map[string]domain.ProjectionType{
	"RUN_RATE":         domain.ProjectionRunRate,
	"PRIOR_MONTH":      domain.ProjectionPriorMonth,
	"LAST_MONTH":       domain.ProjectionPriorMonth,
	"AVERAGE":          domain.ProjectionAverageNMonths,
	"AVERAGE_N_MONTHS": domain.ProjectionAverageNMonths,
	// The stored form is "average-for-x-months"; key() folds the hyphens.
	"AVERAGE_FOR_X_MONTHS": domain.ProjectionAverageNMonths,
}

var userFlags = map[string]string{
	"RED":    "red",
	"ORANGE": "orange",
	"YELLOW": "yellow",
	"GREEN":  "green",
	"BLUE":   "blue",
	"PURPLE": "purple",
}

var watchlistPeriods = map[string]string{
	"MONTH":     "month",
	"MONTHLY":   "month",
	"QUARTER":   "quarter",
	"QUARTERLY": "quarter",
	"YEAR":      "year",
	"YEARLY":    "year",
	"ANNUAL":    "year",
	"CUSTOM":    "custom",
}

// alertTypes is the notification catalog, keyed by the normalised catalog
// names and by the codes alertRulesStore stores. An unmatched one is an error.
var alertTypes = map[string]domain.AlertType{
	"AVAILABLE_TO_SPEND_LOW":                domain.AlertAvailableToSpendLow,
	"BANK_FEE":                              domain.AlertBankFee,
	"BILL_PAID":                             domain.AlertBillPaid,
	"BILLS_TO_INCOME_PERCENTAGE":            domain.AlertBillsToIncomePct,
	"BILLS_TO_INCOME_PCT":                   domain.AlertBillsToIncomePct,
	"EXTRA_PAYCHECK_MONTH":                  domain.AlertExtraPaycheckMonth,
	"GOAL_CONTRIBUTION":                     domain.AlertGoalContribution,
	"HIGH_CREDIT_CARD_BALANCE":              domain.AlertHighCardBalance,
	"INCOME_RECEIVED":                       domain.AlertIncomeReceived,
	"LARGE_DEPOSIT":                         domain.AlertLargeDeposit,
	"LARGE_TRANSACTION":                     domain.AlertLargeTransaction,
	"LOW_BANK_ACCOUNT_BALANCE":              domain.AlertLowBankBalance,
	"MONTHLY_SUMMARY":                       domain.AlertMonthlySummary,
	"PLANNED_EXPENSE_NEARING_LIMIT":         domain.AlertPlannedExpenseNearing,
	"PLANNED_EXPENSE_NEARING_OR_OVER_LIMIT": domain.AlertPlannedExpenseNearing,
	"PROJECTED_LOW_BANK_ACCOUNT_BALANCE":    domain.AlertProjectedLowBalance,
	"REFUND_STATUS":                         domain.AlertRefundStatus,
	"SPENDING_UPDATE":                       domain.AlertSpendingUpdate,
	"UNCATEGORIZED_TRANSACTIONS":            domain.AlertUncategorized,
	"UPCOMING_BILLS":                        domain.AlertUpcomingBills,
	"WATCHLIST_NEARING_TARGET":              domain.AlertWatchlistNearingTarget,
	"WATCHLIST_APPROACHING_OR_OVER_TARGET":  domain.AlertWatchlistNearingTarget,

	// The codes alertRulesStore stores, paired by each rule's title:
	// RECURRING_TXN_EXPENSE is "Bill paid" and RECURRING_TXNS_WEEKLY_SUMMARY
	// is "Upcoming bills", not the other way round.
	"LARGE_PURCHASE":                domain.AlertLargeTransaction,
	"LOW_BALANCE":                   domain.AlertLowBankBalance,
	"HIGH_BALANCE":                  domain.AlertHighCardBalance,
	"PROJECTED_LOW_BALANCE":         domain.AlertProjectedLowBalance,
	"FREE_TO_SPEND_LOW":             domain.AlertAvailableToSpendLow,
	"UNCATEGORIZED_TXNS":            domain.AlertUncategorized,
	"RECURRING_TXN_EXPENSE":         domain.AlertBillPaid,
	"RECURRING_TXN_INCOME":          domain.AlertIncomeReceived,
	"RECURRING_TXNS_WEEKLY_SUMMARY": domain.AlertUpcomingBills,
	"MONTHLY_SPENDING_COMPARISON":   domain.AlertSpendingUpdate,
	"EXTRA_PAYCHECK":                domain.AlertExtraPaycheckMonth,
	"WATCHLIST_TARGET":              domain.AlertWatchlistNearingTarget,
	"PLANNED_SPENDING_ITEM_TARGET":  domain.AlertPlannedExpenseNearing,
	"GOALS_CONTRIBUTIONS":           domain.AlertGoalContribution,
	"REFUND_TRACKER":                domain.AlertRefundStatus,
}

// declinedAlertTypes are the alerts Simplifi ships that Agentifi deliberately
// does not (data-model.md §9); a warning, not an error.
var declinedAlertTypes = map[string]string{
	"ACHIEVEMENT_BADGE_EARNED":       "gamification; no financial value",
	"CREDIT_SCORE_NEW_ALERT":         "needs an Equifax relationship",
	"CREDIT_SCORE_NEW_CREDIT_REPORT": "needs an Equifax relationship",

	// Same three, in the codes the export stores.
	"BADGE_EARNED":                         "gamification; no financial value",
	"CREDIT_REPORT_NEW_ALERT_NOTIFICATION": "needs an Equifax relationship",
	"CREDIT_REPORT_REFRESH":                "needs an Equifax relationship",
}

// transactionSources maps transactionStore.source to one question: did
// Simplifi generate this row from a schedule? SCHEDULED_TRANSACTION_PENDING is
// the only mark separating a forecast from an authorized charge, since both
// arrive in state PENDING. SCHEDULED_TRANSACTION without the suffix is the
// real transaction an occurrence became.
var transactionSources = map[string]bool{
	"SCHEDULED_TRANSACTION_PENDING": true,
	"SCHEDULED_TRANSACTION":         false,
	"USER_ENTERED":                  false,
	"USER_OWNED_BANK_PENDING":       false,
	"QCS_REFRESH_BANK_PENDING":      false,
	"QCS_REFRESH":                   false,
	"QCS_BATCH_REFRESH":             false,
	"QCS_IMPORT":                    false,
	"QCS_GENERATED":                 false,
	"CLIENT_AGGREGATED":             false,
}

// transactionStates maps transactionStore.state to pending or not. An unknown
// state is an error, because guessing false could let an unsettled charge into
// a reconciled balance.
var transactionStates = map[string]bool{
	"PENDING":      true,
	"POSTED":       false,
	"CLEARED":      false,
	"SETTLED":      false,
	"RECONCILED":   false,
	"UNRECONCILED": false,
	"DOWNLOADED":   false,
	"MANUAL":       false,
	"ACTIVE":       false,
	"COMPLETED":    false,
}
