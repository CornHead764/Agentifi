package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Account is the accounts row. Every nullable amount is a Money plus a Has
// flag, never a pointer or a sentinel zero: "unknown" is not "$0.00".
type Account struct {
	ID      uuid.UUID
	SpaceID SpaceID

	ConnectionID  uuid.UUID
	InstitutionID uuid.UUID
	ExternalID    string
	Name          string
	Description   string
	Notes         string

	// Kind is the coarse classification the calculations switch on; Type is
	// the user-facing one (checking, savings, brokerage…).
	Kind      domain.AccountKind
	Type      string
	UsageType string

	Currency     string
	MaskedNumber string
	// LogoURL is replaced on every sync; CustomLogoURL is the household's own
	// choice, wins wherever a logo is drawn, and a sync never touches it.
	LogoURL       string
	CustomLogoURL string
	SortOrder     int

	ProviderBalance    domain.Money
	HasProviderBalance bool
	ProviderBalanceAt  *time.Time

	// A balance the last sync withheld as a likely dropped figure — see
	// domain.SuspectBalanceReset — while ProviderBalance keeps its last good
	// value. WithheldBalanceAt is when it was first seen, stable across syncs;
	// WithheldBalanceReason is domain.BalanceExplanation.Reason. AcceptZeroBalance
	// is the household confirming the zero is real.
	WithheldBalance       domain.Money
	HasWithheldBalance    bool
	WithheldBalanceAt     *time.Time
	WithheldBalanceReason string
	AcceptZeroBalance     bool

	OpeningBalance   domain.Money
	OpeningBalanceOn domain.Date

	// GoalBalance is money reserved by savings goals held here; PendingHolds
	// is authorized-but-unposted card activity. Both reduce what is available.
	GoalBalance  domain.Money
	PendingHolds domain.Money

	CreditLimit    domain.Money
	HasCreditLimit bool

	StatementBalance    domain.Money
	HasStatementBalance bool
	MinimumDue          domain.Money
	HasMinimumDue       bool
	DueDate             domain.Date
	// StatementBillID is the bill the three figures above were copied from,
	// nil otherwise. A hand edit or a connector's report clears it.
	StatementBillID uuid.UUID

	InterestRate    domain.Rate
	HasInterestRate bool
	// StatementCloseDay drives a card charge's effective date; nil means the
	// posted date.
	StatementCloseDay *int16

	// Three independent exclusions plus net worth; never collapse any pair.
	ExcludedFromReports      bool
	ExcludedFromSpendingPlan bool
	ExcludedFromAccountBar   bool
	IncludeInNetWorth        bool
	ExcludeBankPending       bool

	IsClosed  bool
	ClosedOn  domain.Date
	IsDeleted bool

	SimpleFINAccountID string
	// SyncFloorOn is the date before which the aggregator's rows are ignored,
	// so a first sync cannot duplicate an imported history.
	SyncFloorOn domain.Date
	// SyncedThroughOn is the last day a sync read this account through. Only
	// SetAccountSyncedThrough writes it, so UpdateAccount cannot move it.
	SyncedThroughOn domain.Date

	// HistoryStartsOn overrides where balance history begins
	// (domain.HistoryStart); zero means automatic.
	HistoryStartsOn domain.Date
	// HistoryRebuiltFrom is the start the snapshots were last rebuilt for
	// (written only by RebuildAccountHistory); ObservedSince is the earliest
	// imported snapshot and is not a column.
	HistoryRebuiltFrom domain.Date
	ObservedSince      domain.Date

	// HideBelowBalance is the account's small-balance threshold
	// (domain.HiddenForSmallBalance); absent defers to the institution's, and
	// zero is "always show".
	HideBelowBalance    domain.Money
	HasHideBelowBalance bool

	// DefaultRegisterTab is the register tab the account opens on: "all",
	// "spending" or "income"; empty opens on the rows.
	DefaultRegisterTab string

	// RequiresReceipts is the household's choice whether every spending row
	// here needs a receipt; absent follows the type
	// (domain.AccountRequiresReceipts).
	RequiresReceipts    bool
	HasRequiresReceipts bool

	// IgnoredAt is when the household ignored the account. Only
	// SetAccountsIgnored writes it, so a sync's UpdateAccount cannot undo it.
	IgnoredAt *time.Time

	// What an outside source needs to price a physical asset.
	PropertyAddress string
	VehicleVIN      string
	// The Has… flags separate "no reading" from zero, both plausible for a new
	// car. MilesPerYear ages the reading forward from MileageAsOf.
	VehicleMileage    int
	HasVehicleMileage bool
	MileageAsOf       domain.Date
	MilesPerYear      int
	HasMilesPerYear   bool
	// ValuationSource names the provider behind the current balance; a manual
	// revaluation clears it.
	ValuationSource string
	ValuedAt        *time.Time

	// SecuredByAccountID is the asset a loan is secured on (the house behind
	// the mortgage); nil when unpaired.
	SecuredByAccountID uuid.UUID

	// ProviderExtra is everything the feed sent that no column names. Evidence
	// only: recognised fields are mapped onto their columns as well.
	ProviderExtra json.RawMessage

	CreatedAt time.Time
	UpdatedAt time.Time
}

const accountColumns = `id, space_id, connection_id, institution_id, external_id, name, description,
	notes, kind, type, usage_type, currency, masked_number, logo_url, sort_order,
	provider_balance, provider_balance_at, opening_balance, opening_balance_on,
	goal_balance, pending_holds, credit_limit, statement_balance, minimum_due, due_date,
	interest_rate, statement_close_day, excluded_from_reports, excluded_from_spending_plan,
	excluded_from_account_bar, include_in_net_worth, exclude_bank_pending, is_closed, closed_on,
	is_deleted, simplefin_account_id, sync_floor_on, custom_logo_url, property_address,
	vehicle_vin, vehicle_mileage, vehicle_mileage_as_of, vehicle_miles_per_year,
	valuation_source, valued_at, secured_by_account_id, provider_extra,
	withheld_balance, withheld_balance_at, accept_zero_balance, statement_bill_id, synced_through_on,
	withheld_balance_reason, history_starts_on, history_rebuilt_from, hide_below_balance, ignored_at,
	default_register_tab, requires_receipts,
	(SELECT min(bs.as_of) FROM balance_snapshots bs
	  WHERE bs.account_id = accounts.id AND bs.is_imported) AS observed_since,
	created_at, updated_at`

// AccountQuery narrows a listing. The zero value returns the live accounts —
// not deleted, closed or ignored.
type AccountQuery struct {
	// IncludeDeleted also includes ignored accounts: a caller wanting deleted
	// ones is resolving transactions, which ignored accounts own too.
	IncludeDeleted bool
	IncludeClosed  bool
	IncludeIgnored bool
	// IDs restricts to these accounts, still space-scoped.
	IDs []uuid.UUID
}

func (s *Store) CreateAccount(ctx context.Context, spaceID SpaceID, a *Account) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	a.SpaceID = spaceID
	err := s.db.QueryRow(ctx, `
		INSERT INTO accounts (id, space_id, connection_id, institution_id, external_id, name,
			description, notes, kind, type, usage_type, currency, masked_number, logo_url, sort_order,
			provider_balance, provider_balance_at, opening_balance, opening_balance_on,
			goal_balance, pending_holds, credit_limit, statement_balance, minimum_due, due_date,
			interest_rate, statement_close_day, excluded_from_reports, excluded_from_spending_plan,
			excluded_from_account_bar, include_in_net_worth, exclude_bank_pending, is_closed,
			closed_on, is_deleted, simplefin_account_id, sync_floor_on, custom_logo_url,
			property_address, vehicle_vin, vehicle_mileage, vehicle_mileage_as_of,
			vehicle_miles_per_year, valuation_source, valued_at, secured_by_account_id,
			provider_extra, withheld_balance, withheld_balance_at, accept_zero_balance,
			history_starts_on, requires_receipts)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
			$19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37,
			$38, $39, $40, $41, $42, $43, $44, $45, $46, $47, $48, $49, $50, $51, $52)
		RETURNING created_at, updated_at`,
		a.ID, spaceID.UUID(), dbconv.NullUUID(a.ConnectionID), dbconv.NullUUID(a.InstitutionID),
		dbconv.NullText(a.ExternalID), a.Name, dbconv.NullText(a.Description), dbconv.NullText(a.Notes),
		string(a.Kind), a.Type, dbconv.NullText(a.UsageType), a.Currency, dbconv.NullText(a.MaskedNumber),
		dbconv.NullText(a.LogoURL), a.SortOrder,
		dbconv.NullMoney(a.ProviderBalance, a.HasProviderBalance), a.ProviderBalanceAt,
		dbconv.Money(a.OpeningBalance), dbconv.NullDate(a.OpeningBalanceOn),
		dbconv.Money(a.GoalBalance), dbconv.Money(a.PendingHolds),
		dbconv.NullMoney(a.CreditLimit, a.HasCreditLimit),
		dbconv.NullMoney(a.StatementBalance, a.HasStatementBalance),
		dbconv.NullMoney(a.MinimumDue, a.HasMinimumDue), dbconv.NullDate(a.DueDate),
		dbconv.NullNumeric(a.InterestRate, a.HasInterestRate), a.StatementCloseDay,
		a.ExcludedFromReports, a.ExcludedFromSpendingPlan, a.ExcludedFromAccountBar,
		a.IncludeInNetWorth, a.ExcludeBankPending, a.IsClosed, dbconv.NullDate(a.ClosedOn),
		a.IsDeleted, dbconv.NullText(a.SimpleFINAccountID), dbconv.NullDate(a.SyncFloorOn),
		dbconv.NullText(a.CustomLogoURL), dbconv.NullText(a.PropertyAddress), dbconv.NullText(a.VehicleVIN),
		intArg(a.VehicleMileage, a.HasVehicleMileage), dbconv.NullDate(a.MileageAsOf),
		intArg(a.MilesPerYear, a.HasMilesPerYear), dbconv.NullText(a.ValuationSource), a.ValuedAt,
		dbconv.NullUUID(a.SecuredByAccountID), jsonArg(a.ProviderExtra),
		dbconv.NullMoney(a.WithheldBalance, a.HasWithheldBalance), a.WithheldBalanceAt, a.AcceptZeroBalance,
		dbconv.NullDate(a.HistoryStartsOn), PtrIf(a.RequiresReceipts, a.HasRequiresReceipts),
	).Scan(&a.CreatedAt, &a.UpdatedAt)
	return wrap("store: create account", err)
}

func (s *Store) GetAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Account, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	a, err := scanAccount(row)
	return a, wrap("store: get account", err)
}

func (s *Store) ListAccounts(ctx context.Context, spaceID SpaceID, q AccountQuery) ([]Account, error) {
	sql := `SELECT ` + accountColumns + ` FROM accounts WHERE space_id = $1`
	args := []any{spaceID.UUID()}
	if !q.IncludeDeleted {
		sql += ` AND NOT is_deleted`
	}
	if !q.IncludeClosed {
		sql += ` AND NOT is_closed`
	}
	if !q.IncludeIgnored && !q.IncludeDeleted {
		sql += ` AND ignored_at IS NULL`
	}
	if q.IDs != nil {
		args = append(args, q.IDs)
		sql += ` AND id = ANY($2)`
	}
	sql += ` ORDER BY sort_order, name`

	return queryAll(ctx, s.db, "store: list accounts", scanAccount, sql, args...)
}

func (s *Store) UpdateAccount(ctx context.Context, spaceID SpaceID, a *Account) error {
	err := s.db.QueryRow(ctx, `
		UPDATE accounts SET
			connection_id = $3, institution_id = $4, external_id = $5, name = $6, description = $7,
			notes = $8, kind = $9, type = $10, usage_type = $11, currency = $12, masked_number = $13,
			logo_url = $14, sort_order = $15, provider_balance = $16, provider_balance_at = $17,
			opening_balance = $18, opening_balance_on = $19, goal_balance = $20, pending_holds = $21,
			credit_limit = $22, statement_balance = $23, minimum_due = $24, due_date = $25,
			interest_rate = $26, statement_close_day = $27, excluded_from_reports = $28,
			excluded_from_spending_plan = $29, excluded_from_account_bar = $30,
			include_in_net_worth = $31, exclude_bank_pending = $32, is_closed = $33, closed_on = $34,
			is_deleted = $35, simplefin_account_id = $36, sync_floor_on = $37,
			custom_logo_url = $38, property_address = $39, vehicle_vin = $40,
			vehicle_mileage = $41, vehicle_mileage_as_of = $42, vehicle_miles_per_year = $43,
			valuation_source = $44, valued_at = $45, secured_by_account_id = $46,
			provider_extra = $47, withheld_balance = $48, withheld_balance_at = $49,
			accept_zero_balance = $50, statement_bill_id = $51, history_starts_on = $52,
			hide_below_balance = $53, default_register_tab = $54, withheld_balance_reason = $55,
			requires_receipts = $56, updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), a.ID, dbconv.NullUUID(a.ConnectionID), dbconv.NullUUID(a.InstitutionID),
		dbconv.NullText(a.ExternalID), a.Name, dbconv.NullText(a.Description), dbconv.NullText(a.Notes),
		string(a.Kind), a.Type, dbconv.NullText(a.UsageType), a.Currency, dbconv.NullText(a.MaskedNumber),
		dbconv.NullText(a.LogoURL), a.SortOrder,
		dbconv.NullMoney(a.ProviderBalance, a.HasProviderBalance), a.ProviderBalanceAt,
		dbconv.Money(a.OpeningBalance), dbconv.NullDate(a.OpeningBalanceOn),
		dbconv.Money(a.GoalBalance), dbconv.Money(a.PendingHolds),
		dbconv.NullMoney(a.CreditLimit, a.HasCreditLimit),
		dbconv.NullMoney(a.StatementBalance, a.HasStatementBalance),
		dbconv.NullMoney(a.MinimumDue, a.HasMinimumDue), dbconv.NullDate(a.DueDate),
		dbconv.NullNumeric(a.InterestRate, a.HasInterestRate), a.StatementCloseDay,
		a.ExcludedFromReports, a.ExcludedFromSpendingPlan, a.ExcludedFromAccountBar,
		a.IncludeInNetWorth, a.ExcludeBankPending, a.IsClosed, dbconv.NullDate(a.ClosedOn),
		a.IsDeleted, dbconv.NullText(a.SimpleFINAccountID), dbconv.NullDate(a.SyncFloorOn),
		dbconv.NullText(a.CustomLogoURL), dbconv.NullText(a.PropertyAddress), dbconv.NullText(a.VehicleVIN),
		intArg(a.VehicleMileage, a.HasVehicleMileage), dbconv.NullDate(a.MileageAsOf),
		intArg(a.MilesPerYear, a.HasMilesPerYear), dbconv.NullText(a.ValuationSource), a.ValuedAt,
		dbconv.NullUUID(a.SecuredByAccountID), jsonArg(a.ProviderExtra),
		dbconv.NullMoney(a.WithheldBalance, a.HasWithheldBalance), a.WithheldBalanceAt, a.AcceptZeroBalance,
		dbconv.NullUUID(a.StatementBillID), dbconv.NullDate(a.HistoryStartsOn),
		dbconv.NullMoney(a.HideBelowBalance, a.HasHideBelowBalance),
		dbconv.NullText(a.DefaultRegisterTab), a.WithheldBalanceReason,
		PtrIf(a.RequiresReceipts, a.HasRequiresReceipts),
	).Scan(&a.UpdatedAt)
	return wrap("store: update account", err)
}

// SetAccountStatement writes the three statement figures and their bill, and
// nothing else, so a filed bill cannot overwrite a row someone is editing.
func (s *Store) SetAccountStatement(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, statement domain.CardStatement, billID uuid.UUID,
) error {
	return s.execOne(ctx, "store: set account statement",
		`UPDATE accounts SET statement_balance = $3, minimum_due = $4, due_date = $5,
		        statement_bill_id = $6, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), accountID,
		dbconv.NullMoney(statement.Balance, statement.HasBalance),
		dbconv.NullMoney(statement.MinimumDue, statement.HasMinimumDue),
		dbconv.NullDate(statement.DueOn), dbconv.NullUUID(billID))
}

// SetAccountSyncedThrough records the day a sync read this account through.
// The zero Date clears it, so a new link backfills rather than resumes.
func (s *Store) SetAccountSyncedThrough(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, through domain.Date,
) error {
	return s.execOne(ctx, "store: set account synced through",
		`UPDATE accounts SET synced_through_on = $3 WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), accountID, dbconv.NullDate(through))
}

// SetAccountBalance writes the provider figure and the hold, and nothing else:
// a sync decides the balance after the account's rows are read, and must not
// overwrite what was written to the account in between.
func (s *Store) SetAccountBalance(ctx context.Context, spaceID SpaceID, a *Account) error {
	return s.execOne(ctx, "store: set account balance",
		`UPDATE accounts SET provider_balance = $3, provider_balance_at = $4,
		        withheld_balance = $5, withheld_balance_at = $6, withheld_balance_reason = $7,
		        updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), a.ID,
		dbconv.NullMoney(a.ProviderBalance, a.HasProviderBalance), a.ProviderBalanceAt,
		dbconv.NullMoney(a.WithheldBalance, a.HasWithheldBalance), a.WithheldBalanceAt,
		a.WithheldBalanceReason)
}

// AccountHasSyncedTransactions reports whether a sync ever wrote a row to the
// account, deleted rows included.
func (s *Store) AccountHasSyncedTransactions(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID,
) (bool, error) {
	var found bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM transactions
		                 WHERE space_id = $1 AND account_id = $2 AND source = $3)`,
		spaceID.UUID(), accountID, string(domain.SourceSync)).Scan(&found)
	return found, wrap("store: account has synced transactions", err)
}

// StatementSource is where an account's statement figures came from.
type StatementSource struct {
	BillID uuid.UUID
	// Source is the bill's: provider, email, manual or assistant.
	Source    string
	Provider  string
	IssuedOn  domain.Date
	DueOn     domain.Date
	FetchedAt time.Time
}

// StatementSources answers where each bill came from; a deleted bill is
// missing from the answer.
func (s *Store) StatementSources(
	ctx context.Context, spaceID SpaceID, billIDs []uuid.UUID,
) (map[uuid.UUID]StatementSource, error) {
	out := make(map[uuid.UUID]StatementSource, len(billIDs))
	if len(billIDs) == 0 {
		return out, nil
	}
	sources, err := queryAll(ctx, s.db, "store: statement sources", func(row scanner) (StatementSource, error) {
		var (
			one        StatementSource
			issued     *time.Time
			due        time.Time
			connection BillConnection
		)
		if err := row.Scan(&one.BillID, &one.Source, &issued, &due, &one.FetchedAt,
			&connection.Biller, &connection.Label); err != nil {
			return StatementSource{}, err
		}
		one.IssuedOn, one.DueOn = dbconv.ReadNullDate(issued), dateOf(due)
		one.Provider = connection.DisplayName()
		return one, nil
	},
		`SELECT b.id, b.source, b.issued_on, b.due_on, b.fetched_at, c.biller, c.label
		   FROM bills b
		   JOIN bill_subaccounts sa ON sa.id = b.subaccount_id
		   JOIN bill_connections c ON c.id = sa.connection_id
		  WHERE b.space_id = $1 AND b.id = ANY($2)`,
		spaceID.UUID(), billIDs)
	if err != nil {
		return nil, err
	}
	for _, one := range sources {
		out[one.BillID] = one
	}
	return out, nil
}

func (a Account) IsIgnored() bool { return a.IgnoredAt != nil }

// IsConnected reports whether a live feed writes the account's balance: a
// connection, or a SimpleFIN account it was linked to. An imported account
// keeps the provider balance the import gave it without either.
func (a Account) IsConnected() bool {
	return a.ConnectionID != uuid.Nil || a.SimpleFINAccountID != ""
}

// NotInIgnoredAccountOn is the clause for a transaction whose account is not
// ignored, for queries that do not join accounts.
func NotInIgnoredAccountOn(alias string) string {
	qualifier := ""
	if alias != "" {
		qualifier = alias + "."
	}
	return "NOT EXISTS (SELECT 1 FROM accounts ignored_account WHERE ignored_account.id = " +
		qualifier + "account_id AND ignored_account.ignored_at IS NOT NULL)"
}

// SetAccountsIgnored ignores, or stops ignoring, these accounts, returning the
// ids it changed; an account already in that state keeps its timestamp.
// Transfer pairs are kept, unlike DeleteAccount: both legs still moved money,
// and un-ignoring must restore exactly what was there.
func (s *Store) SetAccountsIgnored(
	ctx context.Context, spaceID SpaceID, ids []uuid.UUID, ignored bool,
) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.db, "store: set accounts ignored", scanValue[uuid.UUID],
		`UPDATE accounts
		    SET ignored_at = CASE WHEN $3 THEN now() END, updated_at = now()
		  WHERE space_id = $1 AND id = ANY($2) AND NOT is_deleted
		    AND (ignored_at IS NULL) = $3
		  RETURNING id`,
		spaceID.UUID(), ids, ignored)
}

// DeleteAccount soft-deletes, keeping the transactions as evidence. Every
// transfer pair with a leg in the account is released first: the surviving
// leg would stay paired with an invisible row, excluded from profit and loss,
// and domain.FindOrphanTransferLegs cannot flag it since the partner is not
// deleted.
func (s *Store) DeleteAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.releaseTransferPairs(ctx, spaceID, `account_id = ANY($2)`, []uuid.UUID{id}); err != nil {
			return err
		}
		return tx.execOne(ctx, "store: delete account",
			`UPDATE accounts SET is_deleted = true, updated_at = now() WHERE space_id = $1 AND id = $2`,
			spaceID.UUID(), id)
	})
}

func scanAccount(row scanner) (Account, error) {
	var (
		a                                                       Account
		spaceID                                                 uuid.UUID
		connectionID, institutionID                             *uuid.UUID
		externalID, description, notes, usageType               *string
		maskedNumber, logoURL, simplefinID                      *string
		customLogoURL, propertyAddress, vehicleVIN              *string
		valuationSource, defaultRegisterTab                     *string
		securedBy, statementBill                                *uuid.UUID
		vehicleMileage, milesPerYear                            *int32
		mileageAsOf                                             *time.Time
		kind                                                    string
		providerBalance, openingBalance, goalBalance            dbconv.Number
		pendingHolds, creditLimit, statementBalance, minimumDue dbconv.Number
		interestRate                                            dbconv.Number
		withheldBalance, hideBelowBalance                       dbconv.Number
		openingBalanceOn, dueDate, closedOn, syncFloorOn        *time.Time
		syncedThroughOn                                         *time.Time
		historyStartsOn, historyRebuiltFrom, observedSince      *time.Time
		providerExtra                                           []byte
		requiresReceipts                                        *bool
	)
	err := row.Scan(&a.ID, &spaceID, &connectionID, &institutionID, &externalID, &a.Name,
		&description, &notes, &kind, &a.Type, &usageType, &a.Currency, &maskedNumber, &logoURL,
		&a.SortOrder, &providerBalance, &a.ProviderBalanceAt, &openingBalance, &openingBalanceOn,
		&goalBalance, &pendingHolds, &creditLimit, &statementBalance, &minimumDue, &dueDate,
		&interestRate, &a.StatementCloseDay, &a.ExcludedFromReports, &a.ExcludedFromSpendingPlan,
		&a.ExcludedFromAccountBar, &a.IncludeInNetWorth, &a.ExcludeBankPending, &a.IsClosed,
		&closedOn, &a.IsDeleted, &simplefinID, &syncFloorOn, &customLogoURL, &propertyAddress,
		&vehicleVIN, &vehicleMileage, &mileageAsOf, &milesPerYear, &valuationSource, &a.ValuedAt,
		&securedBy, &providerExtra, &withheldBalance, &a.WithheldBalanceAt, &a.AcceptZeroBalance,
		&statementBill, &syncedThroughOn, &a.WithheldBalanceReason, &historyStartsOn, &historyRebuiltFrom, &hideBelowBalance,
		&a.IgnoredAt, &defaultRegisterTab, &requiresReceipts, &observedSince,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return Account{}, err
	}
	if len(providerExtra) > 0 {
		a.ProviderExtra = json.RawMessage(providerExtra)
	}

	a.SpaceID = SpaceID(spaceID)
	a.Kind = domain.AccountKind(kind)
	if requiresReceipts != nil {
		a.RequiresReceipts, a.HasRequiresReceipts = *requiresReceipts, true
	}
	a.ConnectionID = Deref(connectionID)
	a.InstitutionID = Deref(institutionID)
	a.SecuredByAccountID = Deref(securedBy)
	a.StatementBillID = Deref(statementBill)
	a.ExternalID = Deref(externalID)
	a.Description = Deref(description)
	a.Notes = Deref(notes)
	a.UsageType = Deref(usageType)
	a.MaskedNumber = Deref(maskedNumber)
	a.LogoURL = Deref(logoURL)
	a.SimpleFINAccountID = Deref(simplefinID)
	a.OpeningBalanceOn = dbconv.ReadNullDate(openingBalanceOn)
	a.DueDate = dbconv.ReadNullDate(dueDate)
	a.ClosedOn = dbconv.ReadNullDate(closedOn)
	a.SyncFloorOn = dbconv.ReadNullDate(syncFloorOn)
	a.SyncedThroughOn = dbconv.ReadNullDate(syncedThroughOn)
	a.HistoryStartsOn = dbconv.ReadNullDate(historyStartsOn)
	a.HistoryRebuiltFrom = dbconv.ReadNullDate(historyRebuiltFrom)
	a.ObservedSince = dbconv.ReadNullDate(observedSince)
	a.CustomLogoURL = Deref(customLogoURL)
	a.PropertyAddress = Deref(propertyAddress)
	a.VehicleVIN = Deref(vehicleVIN)
	a.ValuationSource = Deref(valuationSource)
	a.DefaultRegisterTab = Deref(defaultRegisterTab)
	a.MileageAsOf = dbconv.ReadNullDate(mileageAsOf)
	if vehicleMileage != nil {
		a.VehicleMileage, a.HasVehicleMileage = int(*vehicleMileage), true
	}
	if milesPerYear != nil {
		a.MilesPerYear, a.HasMilesPerYear = int(*milesPerYear), true
	}

	if a.ProviderBalance, a.HasProviderBalance, err = dbconv.ReadNullMoney(providerBalance, "accounts.provider_balance"); err != nil {
		return Account{}, err
	}
	if a.OpeningBalance, err = dbconv.ReadMoney(openingBalance, "accounts.opening_balance"); err != nil {
		return Account{}, err
	}
	if a.GoalBalance, err = dbconv.ReadMoney(goalBalance, "accounts.goal_balance"); err != nil {
		return Account{}, err
	}
	if a.PendingHolds, err = dbconv.ReadMoney(pendingHolds, "accounts.pending_holds"); err != nil {
		return Account{}, err
	}
	if a.CreditLimit, a.HasCreditLimit, err = dbconv.ReadNullMoney(creditLimit, "accounts.credit_limit"); err != nil {
		return Account{}, err
	}
	if a.StatementBalance, a.HasStatementBalance, err = dbconv.ReadNullMoney(statementBalance, "accounts.statement_balance"); err != nil {
		return Account{}, err
	}
	if a.MinimumDue, a.HasMinimumDue, err = dbconv.ReadNullMoney(minimumDue, "accounts.minimum_due"); err != nil {
		return Account{}, err
	}
	if a.InterestRate, a.HasInterestRate, err = dbconv.ReadNullDecimal(interestRate, "accounts.interest_rate"); err != nil {
		return Account{}, err
	}
	if a.WithheldBalance, a.HasWithheldBalance, err = dbconv.ReadNullMoney(withheldBalance, "accounts.withheld_balance"); err != nil {
		return Account{}, err
	}
	if a.HideBelowBalance, a.HasHideBelowBalance, err = dbconv.ReadNullMoney(hideBelowBalance, "accounts.hide_below_balance"); err != nil {
		return Account{}, err
	}
	return a, nil
}

// intArg writes an absent optional integer as NULL; zero is a real reading.
func intArg(value int, present bool) *int32 {
	if !present {
		return nil
	}
	out := int32(value)
	return &out
}
