package domain

// The alert catalog: Simplifi's alerts plus our own (stale pending, the
// connector alerts and the server's backup). It is domain rather than
// configuration because what an alert can be thresholded on and which
// channels it can reach are facts, not preferences. A household's own choices live in alert_rules.

// AlertType identifies one alert in the catalog.
type AlertType string

const (
	AlertAchievementBadge AlertType = "achievement_badge"
	// AlertAmazonSignIn: the Amazon connector's session was challenged and the
	// daily pull is stopped until somebody signs in again.
	AlertAmazonSignIn        AlertType = "amazon_sign_in"
	AlertAvailableToSpendLow AlertType = "available_to_spend_low"
	AlertBankFee             AlertType = "bank_fee"
	// AlertBackupFailed: the server's nightly backup failed or could not run.
	// Raised for the server's administrators only.
	AlertBackupFailed AlertType = "backup_failed"
	// AlertBillChallenge: a sign-in is parked on a code prompt, waiting for a
	// person.
	AlertBillChallenge         AlertType = "bill_challenge"
	AlertBillPaid              AlertType = "bill_paid"
	AlertBillPullFailed        AlertType = "bill_pull_failed"
	AlertBillsToIncomePct      AlertType = "bills_to_income_pct"
	AlertCostcoSignIn          AlertType = "costco_sign_in"
	AlertCreditScoreNewAlert   AlertType = "credit_score_new_alert"
	AlertCreditScoreNewReport  AlertType = "credit_score_new_report"
	AlertEmailConnectionFailed AlertType = "email_connection_failed"
	AlertExtraPaycheckMonth    AlertType = "extra_paycheck_month"
	AlertGoalContribution      AlertType = "goal_contribution"
	AlertHighCardBalance       AlertType = "high_card_balance"
	AlertIncomeReceived        AlertType = "income_received"
	AlertLargeDeposit          AlertType = "large_deposit"
	AlertLargeTransaction      AlertType = "large_transaction"
	AlertLowBankBalance        AlertType = "low_bank_balance"
	// AlertMerchantPullFailed: a merchant's order pull got in but could not
	// finish, which a sign-in does not fix.
	AlertMerchantPullFailed    AlertType = "merchant_pull_failed"
	AlertMonthlySummary        AlertType = "monthly_summary"
	AlertPlannedExpenseNearing AlertType = "planned_expense_nearing"
	AlertProjectedLowBalance   AlertType = "projected_low_balance"
	AlertRefundStatus          AlertType = "refund_status"
	AlertSpendingUpdate        AlertType = "spending_update"
	// AlertStalePending: a pending charge held longer than a bank takes to
	// post one, which almost always means it settled under a different figure
	// or the hold was withdrawn.
	AlertStalePending           AlertType = "stale_pending"
	AlertUncategorized          AlertType = "uncategorized"
	AlertUpcomingBills          AlertType = "upcoming_bills"
	AlertWatchlistNearingTarget AlertType = "watchlist_nearing_target"
)

// ThresholdKind is what number, if any, an alert compares against. It is not
// inferred from whichever threshold column is non-null: a rule with an amount
// on an alert that counts things could never fire, silently.
type ThresholdKind string

const (
	ThresholdNone   ThresholdKind = "none"
	ThresholdAmount ThresholdKind = "amount"
	ThresholdCount  ThresholdKind = "count"
	// ThresholdPercent is percent units, not a fraction: 10 means 10%.
	ThresholdPercent ThresholdKind = "percent"
)

type AlertChannel string

const (
	ChannelEmail AlertChannel = "email"
	ChannelPush  AlertChannel = "push"
	ChannelInApp AlertChannel = "in_app"
)

type AlertDefinition struct {
	Type AlertType
	// Trigger is written as the condition, so a row reads as a sentence.
	Label   string
	Trigger string
	Group   AlertGroup

	Threshold ThresholdKind
	// Only the default matching Threshold is set.
	DefaultAmount  Money
	DefaultCount   int
	DefaultPercent int

	// Channels are the ones this alert can use at all.
	Channels []AlertChannel
	// DefaultOn: email and push reach somebody who did not ask, so they stay
	// off until switched on.
	DefaultOn []AlertChannel
	// OffByDefault is for alerts that report something already on screen and
	// fire often enough to bury the ones that need somebody.
	OffByDefault bool
	// Evaluated records whether this alert can fire end to end: decided in
	// EvaluateAlerts and supplied by the sweep, or delivered by the connector
	// that raises it. A decision without inputs is false. Unevaluated alerts
	// stay listed and configurable, and the settings page marks them.
	Evaluated bool
}

type AlertGroup string

const (
	GroupSpending  AlertGroup = "spending"
	GroupAccounts  AlertGroup = "accounts"
	GroupBills     AlertGroup = "bills"
	GroupPlanning  AlertGroup = "planning"
	GroupSummaries AlertGroup = "summaries"
)

var allChannels = []AlertChannel{ChannelEmail, ChannelPush, ChannelInApp}

// AlertCatalog is every alert, in the order the settings page lists them.
var AlertCatalog = []AlertDefinition{
	{
		Type: AlertLargeTransaction, Label: "Large transaction", Group: GroupSpending,
		Trigger:   "Any transaction at or above this amount",
		Threshold: ThresholdAmount, DefaultAmount: MustFromString("500"),
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertLargeDeposit, Label: "Large deposit", Group: GroupSpending,
		Trigger:   "Any deposit at or above this amount",
		Threshold: ThresholdAmount, DefaultAmount: MustFromString("500"),
		OffByDefault: true,
		Channels:     allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertBankFee, Label: "Bank fee", Group: GroupSpending,
		Trigger:   "A bank fee at or above this amount",
		Threshold: ThresholdAmount, DefaultAmount: MustFromString("5"),
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertUncategorized, Label: "Uncategorized transactions", Group: GroupSpending,
		Trigger:   "This many new transactions have no category",
		Threshold: ThresholdCount, DefaultCount: 5,
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertRefundStatus, Label: "Refund status", Group: GroupSpending,
		Trigger:   "A refund arrives, or has not by the date it was expected",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},

	{
		Type: AlertLowBankBalance, Label: "Low bank account balance", Group: GroupAccounts,
		Trigger:   "An account's available balance drops below this",
		Threshold: ThresholdAmount, DefaultAmount: MustFromString("200"),
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertProjectedLowBalance, Label: "Projected low bank account balance",
		Group: GroupAccounts, Trigger: "An account is projected to drop below this",
		Threshold: ThresholdAmount, DefaultAmount: MustFromString("500"),
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertHighCardBalance, Label: "High credit card balance", Group: GroupAccounts,
		Trigger:   "A card's balance rises above this",
		Threshold: ThresholdAmount, DefaultAmount: MustFromString("5000"),
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertStalePending, Label: "Pending charge not settling", Group: GroupAccounts,
		Trigger: "A charge has been pending this many days",
		// Days: two weeks is well past the three or four days a card takes
		// to post.
		Threshold: ThresholdCount, DefaultCount: 14,
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		// Raised by the merchant connector. Push on: the daily order pull is
		// stopped until somebody signs in.
		Type: AlertAmazonSignIn, Label: "Amazon needs a sign-in", Group: GroupAccounts,
		Trigger:   "The Amazon order pull stopped at a sign-in",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp, ChannelPush},
		Evaluated: true,
	},
	{
		Type: AlertCostcoSignIn, Label: "Costco needs a sign-in", Group: GroupAccounts,
		Trigger:   "The Costco order pull stopped at a sign-in",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp, ChannelPush},
		Evaluated: true,
	},
	{
		// Push on, as for a sign-in: nobody looks at a backup until they need it.
		Type: AlertBackupFailed, Label: "Server backup failed", Group: GroupAccounts,
		Trigger:   "The server's nightly backup failed or could not run (administrators only)",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp, ChannelPush},
		Evaluated: true,
	},
	{
		// Push on, as for a sign-in: matching stops on the orders it has.
		Type: AlertMerchantPullFailed, Label: "Order pull stopped", Group: GroupAccounts,
		Trigger:   "An Amazon or Costco order pull could not finish",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp, ChannelPush},
		Evaluated: true,
	},
	{
		Type: AlertCreditScoreNewAlert, Label: "Credit score: new alert", Group: GroupAccounts,
		Trigger:   "A new credit alert appears in your file",
		Threshold: ThresholdNone,
		// Email only: this comes from a bureau.
		Channels: []AlertChannel{ChannelEmail}, DefaultOn: nil,
	},
	{
		Type: AlertCreditScoreNewReport, Label: "Credit score: new report",
		Group: GroupAccounts, Trigger: "Your monthly credit report is ready",
		Threshold: ThresholdNone,
		Channels:  []AlertChannel{ChannelEmail}, DefaultOn: nil,
	},

	{
		Type: AlertUpcomingBills, Label: "Upcoming bills", Group: GroupBills,
		Trigger:   "This many bills fall due in the coming week, told once a week",
		Threshold: ThresholdCount, DefaultCount: 1,
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertBillPaid, Label: "Bill paid", Group: GroupBills,
		Trigger:      "A recurring bill or subscription is paid",
		Threshold:    ThresholdNone,
		OffByDefault: true,
		Channels:     allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertIncomeReceived, Label: "Income received", Group: GroupBills,
		Trigger:      "Recurring income arrives",
		Threshold:    ThresholdNone,
		OffByDefault: true,
		Channels:     allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertExtraPaycheckMonth, Label: "Extra paycheck month", Group: GroupBills,
		Trigger:   "A month with an extra paycheck is coming",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertBillsToIncomePct, Label: "Bills to income percentage", Group: GroupBills,
		Trigger:   "Bills reach this share of expected income",
		Threshold: ThresholdPercent, DefaultPercent: 50,
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		// Raised by the bills engine, not the sweep. Push is on by default
		// because the parked sign-in is reaped twenty minutes later.
		Type: AlertBillChallenge, Label: "Bill sign-in needs a code", Group: GroupBills,
		Trigger:   "A bill provider asks for a code no automatic answer could give",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp, ChannelPush},
		Evaluated: true,
	},
	{
		// Push on: a stopped pull leaves a reminder showing last cycle's figure.
		Type: AlertBillPullFailed, Label: "Bill pull stopped", Group: GroupBills,
		Trigger:   "A bill provider's pull could not finish",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp, ChannelPush},
		Evaluated: true,
	},
	{
		// Raised by the mailbox reader. Push on: the mailbox also answers
		// providers' sign-in codes.
		Type: AlertEmailConnectionFailed, Label: "Mailbox stopped reading", Group: GroupBills,
		Trigger:   "The watched mailbox could not be read",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp, ChannelPush},
		Evaluated: true,
	},
	{
		Type: AlertAvailableToSpendLow, Label: "Available to spend low", Group: GroupPlanning,
		Trigger:   "Available to spend falls to this or less",
		Threshold: ThresholdAmount, DefaultAmount: MustFromString("10"),
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertPlannedExpenseNearing, Label: "Planned expense nearing its limit",
		Group: GroupPlanning, Trigger: "A planned expense comes within this much of its limit",
		Threshold: ThresholdPercent, DefaultPercent: 10,
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertGoalContribution, Label: "Goal contribution", Group: GroupPlanning,
		Trigger:   "A reminder to contribute to a savings goal",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertWatchlistNearingTarget, Label: "Watchlist nearing its target",
		Group: GroupPlanning, Trigger: "A watchlist comes within this much of its target",
		Threshold: ThresholdPercent, DefaultPercent: 10,
		Channels: allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},

	{
		Type: AlertMonthlySummary, Label: "Monthly summary", Group: GroupSummaries,
		Trigger:   "A summary of the month's income and expenses",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertSpendingUpdate, Label: "Spending update", Group: GroupSummaries,
		Trigger:      "How this month's spending compares with last month's, once a week",
		Threshold:    ThresholdNone,
		OffByDefault: true,
		Channels:     allChannels, DefaultOn: []AlertChannel{ChannelInApp}, Evaluated: true,
	},
	{
		Type: AlertAchievementBadge, Label: "Achievement badge earned", Group: GroupSummaries,
		Trigger:   "You earn a badge",
		Threshold: ThresholdNone,
		Channels:  allChannels, DefaultOn: []AlertChannel{ChannelInApp},
	},
}

// AlertByType finds one definition. Every write goes through it, so a rule
// for a type not in the catalog is refused rather than stored and forgotten.
func AlertByType(t AlertType) (AlertDefinition, bool) {
	for _, one := range AlertCatalog {
		if one.Type == t {
			return one, true
		}
	}
	return AlertDefinition{}, false
}

func (d AlertDefinition) Allows(channel AlertChannel) bool {
	for _, one := range d.Channels {
		if one == channel {
			return true
		}
	}
	return false
}

func (d AlertDefinition) DefaultEnabled() bool { return !d.OffByDefault }

func (d AlertDefinition) DefaultsOn(channel AlertChannel) bool {
	for _, one := range d.DefaultOn {
		if one == channel {
			return true
		}
	}
	return false
}
