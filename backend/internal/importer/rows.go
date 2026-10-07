package importer

import (
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The rows one export becomes.
//
// Tables the store package already writes are carried as its own structs, so
// the import writes them through the same SQL. The types below are the
// remaining tables, with the primary key already set, because a foreign key
// has to be written before the row it points at exists.
//
// Every nullable amount is a Money plus a Has flag rather than a pointer or a
// sentinel zero, matching the store: "no override" and "an override of zero"
// are different facts and the spending plan reads both.

type Institution struct {
	ID         uuid.UUID
	Name       string
	ExternalID string
	Domain     string
	LogoURL    string
	IsDeleted  bool
}

type Series struct {
	ID          uuid.UUID
	AccountID   uuid.UUID
	CategoryID  uuid.UUID
	Kind        domain.SeriesKind
	Description string
	DisplayName string
	Amount      domain.Money
	Currency    string

	Alias      domain.RecurrenceAlias
	Frequency  domain.Frequency
	Interval   int
	ByMonthDay []int
	ByDay      []string

	StartOn           domain.Date
	EndOn             domain.Date
	NextDueOn         domain.Date
	OverrideNextDueOn domain.Date

	OverrideNextAmount    domain.Money
	HasOverrideNextAmount bool

	AutoAdjustDueOn bool
	ReminderDays    int
	AutoAcceptDays  *int

	MatchCriteria     string
	MatchAmountMin    domain.Money
	HasMatchAmountMin bool
	MatchAmountMax    domain.Money
	HasMatchAmountMax bool

	LearnedDescriptions []string

	TemplatePayee                    string
	TemplateNotes                    string
	TemplateTagIDs                   []uuid.UUID
	TemplateExcludedFromReports      bool
	TemplateExcludedFromSpendingPlan bool
	// TemplateSplits is Simplifi's allocation list, kept verbatim as jsonb so
	// nothing we have not modelled is lost.
	TemplateSplits []any

	IsActive  bool
	IsDeleted bool
}

type Rule struct {
	ID        uuid.UUID
	Name      string
	FilterID  uuid.UUID
	Priority  int
	IsActive  bool
	IsDeleted bool
	// SourceRef names the Simplifi record the rule came from, which is how a
	// second import of the rules recognises one it has already written.
	SourceRef string

	SetPayee      string
	SetCategoryID uuid.UUID
	AddTagIDs     []uuid.UUID
	SetNotes      string
	// The three set flags are tri-state: null means "leave alone", false
	// clears the flag.
	SetExcludedFromReports      *bool
	SetExcludedFromSpendingPlan *bool
	SetIsReviewed               *bool
}

type Goal struct {
	ID          uuid.UUID
	AccountID   uuid.UUID
	Name        string
	Description string
	Notes       string
	Kind        string
	ImageURL    string
	TagID       uuid.UUID

	TargetAmount domain.Money
	TargetOn     domain.Date
	StartOn      domain.Date
	CompletedOn  domain.Date

	ContributionAmount    domain.Money
	ContributionFrequency string
	ContributedThisMonth  domain.Money
	SavedSoFar            domain.Money
	Spent                 domain.Money

	TxnIDs []uuid.UUID
	// WithdrawalTxnIDs is the subset of TxnIDs that takes money back out,
	// classified by sign as the export is read — see mapGoals.
	WithdrawalTxnIDs []uuid.UUID
	IsTakenFromPlan  bool
	IsDeleted        bool

	FundingAccountIDs []uuid.UUID
}

// planBucket is the five-field pattern every spending-plan bucket follows.
type planBucket struct {
	Calculated       domain.Money
	TxnIDs           []uuid.UUID
	ExcludedTxnIDs   []uuid.UUID
	Overwritten      domain.Money
	HasOverwritten   bool
	ResetOverwritten bool
}

type SpendingPlanMonth struct {
	ID    uuid.UUID
	Month domain.Date

	CalculatedRollover domain.Money

	Income          planBucket
	Bills           planBucket
	Subscriptions   planBucket
	Transfer        planBucket
	Goals           planBucket
	PlannedSpending planBucket
	Spent           planBucket

	SetAside               domain.Money
	TotalToSpend           domain.Money
	LeftToSpend            domain.Money
	ProjectedOtherSpending domain.Money

	ProjectionType      domain.ProjectionType
	ProjectionStartDate domain.Date
	ProjectionEndDate   domain.Date
	ProjectionBuffer    domain.Money
	// ProjectionWindowMonths has no source: Simplifi stores the projection
	// type but not how many months an average reads. Defaulted, and warned
	// about with the rest of the gaps.
	ProjectionWindowMonths int

	IsClosedOut        bool
	ShowClosedOut      bool
	NewMonthViewed     bool
	HideExcludedFromUI bool
}

// bucket returns the addressable bucket a name refers to, so the writer and
// the mapper share one list.
func (m *SpendingPlanMonth) bucket(name string) *planBucket {
	switch name {
	case "income":
		return &m.Income
	case "bills":
		return &m.Bills
	case "subscriptions":
		return &m.Subscriptions
	case "transfer":
		return &m.Transfer
	case "goals":
		return &m.Goals
	case "planned_spending":
		return &m.PlannedSpending
	case "spent":
		return &m.Spent
	}
	return nil
}

type Envelope struct {
	ID                  uuid.UUID
	SpendingPlanMonthID uuid.UUID
	FilterID            uuid.UUID
	// RecurringGroupID ties one envelope to its instances in other months.
	// Nil for a this-month-only envelope.
	RecurringGroupID uuid.UUID

	Name                    string
	TargetAmount            domain.Money
	OverwrittenTargetAmount domain.Money
	HasOverwrittenTarget    bool
	CalculatedSpentAmount   domain.Money
	RolloverAmount          domain.Money
	AutoReleaseRollover     bool
	Recurring               bool

	TxnIDs             []uuid.UUID
	ExcludedTxnIDs     []uuid.UUID
	HideExcludedFromUI bool
}

type Watchlist struct {
	ID           uuid.UUID
	FilterID     uuid.UUID
	Name         string
	Emoji        string
	TargetAmount domain.Money
	HasTarget    bool
	Period       string
	StartDate    domain.Date
	EndDate      domain.Date
	IsDeleted    bool
}

type Security struct {
	ID       uuid.UUID
	Symbol   string
	Name     string
	Kind     string
	Exchange string
	Currency string
	CUSIP    string
	ISIN     string

	LastPrice     domain.Rate
	HasLastPrice  bool
	LastPriceAt   *time.Time
	PriorClose    domain.Rate
	HasPriorClose bool

	IsDeleted bool
}

type Holding struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	SecurityID uuid.UUID
	ExternalID string

	Shares domain.Rate

	CostBasis    domain.Money
	HasCostBasis bool
	AverageCost  domain.Rate
	HasAverage   bool
	// IsCostBasisComplete records a state, not a zero: a portfolio that
	// renders market_value - 0 reads as a spectacular return.
	IsCostBasisComplete bool

	MarketValue    domain.Money
	HasMarketValue bool
	AsOf           domain.Date
}

type BalanceSnapshot struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	AsOf      domain.Date
	Balance   domain.Money
}

type AlertRule struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	AlertType string
	AccountID uuid.UUID

	IsEnabled bool
	IsPaused  bool

	ChannelEmail bool
	ChannelPush  bool
	ChannelInApp bool

	ThresholdAmount    domain.Money
	HasThresholdAmount bool
	ThresholdCount     *int
	ThresholdPct       domain.Rate
	HasThresholdPct    bool
}

type Attachment struct {
	ID            uuid.UUID
	TransactionID uuid.UUID
	Filename      string
	ContentType   string
	SizeBytes     int
	// StorageKey names the Simplifi document. The export carries the document
	// record but not the file, so nothing sits behind this key.
	StorageKey string
}

// Mapped is everything one export becomes, grouped as the writer inserts it.
type Mapped struct {
	Report *Report

	SpaceID store.SpaceID
	// IntoExisting says SpaceID is a space that already exists, which the
	// write renames and fills rather than creates.
	IntoExisting bool
	Space        *store.Space
	Users        []*store.User
	Memberships  []*store.Membership

	Institutions []*Institution
	Accounts     []*store.Account
	Categories   []*store.Category
	Tags         []*store.Tag
	Filters      []*store.Filter

	Series       []*Series
	Rules        []*Rule
	Transactions []*store.Transaction
	Attachments  []*Attachment

	Goals      []*Goal
	Months     []*SpendingPlanMonth
	Envelopes  []*Envelope
	Watchlists []*Watchlist

	Securities       []*Security
	Holdings         []*Holding
	BalanceSnapshots []*BalanceSnapshot
	AlertRules       []*AlertRule
}

// RowCount is every row the import writes, for the line the CLI prints.
func (m *Mapped) RowCount() int {
	splits, tags := 0, 0
	for _, txn := range m.Transactions {
		splits += len(txn.Splits)
		tags += len(txn.TagIDs)
	}
	filterItems := 0
	for _, filter := range m.Filters {
		filterItems += len(filter.Items)
	}
	return 1 + len(m.Users) + len(m.Memberships) + len(m.Institutions) + len(m.Accounts) +
		len(m.Categories) + len(m.Tags) + len(m.Filters) + filterItems + len(m.Series) +
		len(m.Rules) + len(m.Transactions) + splits + tags + len(m.Attachments) + len(m.Goals) +
		len(m.Months) + len(m.Envelopes) + len(m.Watchlists) + len(m.Securities) +
		len(m.Holdings) + len(m.BalanceSnapshots) + len(m.AlertRules)
}
