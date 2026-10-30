package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/google/uuid"

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

const accountColumns = `id, connection_id, institution_id, external_id, name, description,
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	a.SpaceID = spaceID
	values, err := accountWriteValues(a)
	if err != nil {
		return wrap("store: create account", err)
	}
	now := db.Now()
	values = append([]any{a.ID}, values...)
	values = append(values, now, now)
	_, err = db.Write().ExecContext(ctx, `INSERT INTO accounts (id, `+accountWriteColumns+`, created_at, updated_at)
		VALUES (`+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+`)`, values...)
	if err != nil {
		return wrap("store: create account", err)
	}
	a.CreatedAt, a.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	return nil
}

func (s *Store) GetAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Account, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return Account{}, err
	}
	a, err := scanAccount(db.Read().QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id), spaceID)
	return a, wrap("store: get account", err)
}

func (s *Store) ListAccounts(ctx context.Context, spaceID SpaceID, q AccountQuery) ([]Account, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	sql := `SELECT ` + accountColumns + ` FROM accounts WHERE 1=1`
	var args []any
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
		marks, values := InArgs(q.IDs)
		args = append(args, values...)
		sql += ` AND id IN ` + marks
	}
	sql += ` ORDER BY sort_order, name`

	return sqliteQueryAll(ctx, db.Read(), "store: list accounts", func(row scanner) (Account, error) {
		return scanAccount(row, spaceID)
	}, sql, args...)
}

func (s *Store) UpdateAccount(ctx context.Context, spaceID SpaceID, a *Account) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	values, err := accountWriteValues(a)
	if err != nil {
		return wrap("store: update account", err)
	}
	var hide any
	if a.HasHideBelowBalance {
		hide, err = toMinor(a.HideBelowBalance, a.Currency)
		if err != nil {
			return wrap("store: update account", err)
		}
	}
	columns := strings.Split(accountWriteColumns, ",")
	for i := range columns {
		columns[i] = strings.TrimSpace(columns[i]) + " = ?"
	}
	now := db.Now()
	values = append(values, NullUUID(a.StatementBillID), hide, NullText(a.DefaultRegisterTab),
		a.WithheldBalanceReason, now, a.ID)
	err = sqliteExecOne(ctx, db.Write(), "store: update account", `UPDATE accounts SET `+
		strings.Join(columns, ",")+`, statement_bill_id = ?, hide_below_balance = ?,
		default_register_tab = ?, withheld_balance_reason = ?, updated_at = ? WHERE id = ?`, values...)
	if err == nil {
		a.UpdatedAt = fromUnixMillis(now)
	}
	return err
}

const accountWriteColumns = `connection_id, institution_id, external_id, name, description, notes,
	kind, type, usage_type, currency, masked_number, logo_url, sort_order, provider_balance,
	provider_balance_at, opening_balance, opening_balance_on, goal_balance, pending_holds,
	credit_limit, statement_balance, minimum_due, due_date, interest_rate, statement_close_day,
	excluded_from_reports, excluded_from_spending_plan, excluded_from_account_bar,
	include_in_net_worth, exclude_bank_pending, is_closed, closed_on, is_deleted,
	simplefin_account_id, sync_floor_on, custom_logo_url, property_address, vehicle_vin,
	vehicle_mileage, vehicle_mileage_as_of, vehicle_miles_per_year, valuation_source, valued_at,
	secured_by_account_id, provider_extra, withheld_balance, withheld_balance_at,
	accept_zero_balance, history_starts_on, requires_receipts`

func accountWriteValues(a *Account) ([]any, error) {
	var conversionErr error
	money := func(value domain.Money, present bool) any {
		if !present {
			return nil
		}
		minor, err := toMinor(value, a.Currency)
		if err != nil && conversionErr == nil {
			conversionErr = err
		}
		return minor
	}
	var rate any
	if a.HasInterestRate {
		rate = decimaltext.Format(a.InterestRate)
	}
	values := []any{
		NullUUID(a.ConnectionID), NullUUID(a.InstitutionID), NullText(a.ExternalID),
		a.Name, NullText(a.Description), NullText(a.Notes), string(a.Kind), a.Type,
		NullText(a.UsageType), a.Currency, NullText(a.MaskedNumber), NullText(a.LogoURL), a.SortOrder,
		money(a.ProviderBalance, a.HasProviderBalance), nullableMillis(a.ProviderBalanceAt),
		money(a.OpeningBalance, true), NullDate(a.OpeningBalanceOn), money(a.GoalBalance, true),
		money(a.PendingHolds, true), money(a.CreditLimit, a.HasCreditLimit),
		money(a.StatementBalance, a.HasStatementBalance), money(a.MinimumDue, a.HasMinimumDue),
		NullDate(a.DueDate), rate, a.StatementCloseDay,
		a.ExcludedFromReports, a.ExcludedFromSpendingPlan, a.ExcludedFromAccountBar, a.IncludeInNetWorth,
		a.ExcludeBankPending, a.IsClosed, NullDate(a.ClosedOn), a.IsDeleted,
		NullText(a.SimpleFINAccountID), NullDate(a.SyncFloorOn), NullText(a.CustomLogoURL),
		NullText(a.PropertyAddress), NullText(a.VehicleVIN), intArg(a.VehicleMileage, a.HasVehicleMileage),
		NullDate(a.MileageAsOf), intArg(a.MilesPerYear, a.HasMilesPerYear), NullText(a.ValuationSource),
		nullableMillis(a.ValuedAt), NullUUID(a.SecuredByAccountID), jsonArg(a.ProviderExtra),
		money(a.WithheldBalance, a.HasWithheldBalance), nullableMillis(a.WithheldBalanceAt),
		a.AcceptZeroBalance, NullDate(a.HistoryStartsOn), PtrIf(a.RequiresReceipts, a.HasRequiresReceipts),
	}
	return values, conversionErr
}

// SetAccountStatement writes the three statement figures and their bill, and
// nothing else, so a filed bill cannot overwrite a row someone is editing.
func (s *Store) SetAccountStatement(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, statement domain.CardStatement, billID uuid.UUID,
) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	var currency string
	if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id = ?`, accountID).Scan(&currency); err != nil {
		return wrap("store: set account statement", err)
	}
	var balance, minimum any
	if statement.HasBalance {
		balance, err = toMinor(statement.Balance, currency)
		if err != nil {
			return wrap("store: set account statement", err)
		}
	}
	if statement.HasMinimumDue {
		minimum, err = toMinor(statement.MinimumDue, currency)
		if err != nil {
			return wrap("store: set account statement", err)
		}
	}
	return sqliteExecOne(ctx, db.Write(), "store: set account statement",
		`UPDATE accounts SET statement_balance = ?, minimum_due = ?, due_date = ?,
		        statement_bill_id = ?, updated_at = ? WHERE id = ?`,
		balance, minimum, NullDate(statement.DueOn), NullUUID(billID), db.Now(), accountID)
}

// SetAccountSyncedThrough records the day a sync read this account through.
// The zero Date clears it, so a new link backfills rather than resumes.
func (s *Store) SetAccountSyncedThrough(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, through domain.Date,
) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set account synced through",
		`UPDATE accounts SET synced_through_on = ?, updated_at = ? WHERE id = ?`, NullDate(through), db.Now(), accountID)
}

// SetAccountBalance writes the provider figure and the hold, and nothing else:
// a sync decides the balance after the account's rows are read, and must not
// overwrite what was written to the account in between.
func (s *Store) SetAccountBalance(ctx context.Context, spaceID SpaceID, a *Account) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	var currency string
	if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id = ?`, a.ID).Scan(&currency); err != nil {
		return wrap("store: set account balance", err)
	}
	var provider, withheld any
	if a.HasProviderBalance {
		provider, err = toMinor(a.ProviderBalance, currency)
		if err != nil {
			return wrap("store: set account balance", err)
		}
	}
	if a.HasWithheldBalance {
		withheld, err = toMinor(a.WithheldBalance, currency)
		if err != nil {
			return wrap("store: set account balance", err)
		}
	}
	return sqliteExecOne(ctx, db.Write(), "store: set account balance",
		`UPDATE accounts SET provider_balance = ?, provider_balance_at = ?,
		        withheld_balance = ?, withheld_balance_at = ?, withheld_balance_reason = ?,
		        updated_at = ? WHERE id = ?`,
		provider, nullableMillis(a.ProviderBalanceAt), withheld, nullableMillis(a.WithheldBalanceAt),
		a.WithheldBalanceReason, db.Now(), a.ID)
}

// AccountHasSyncedTransactions reports whether a sync ever wrote a row to the
// account, deleted rows included.
func (s *Store) AccountHasSyncedTransactions(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID,
) (bool, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return false, err
	}
	var found bool
	err = db.Read().QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM transactions WHERE account_id = ? AND source = ?)`,
		accountID, string(domain.SourceSync)).Scan(&found)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	marks, values := InArgs(billIDs)
	sources, err := sqliteQueryAll(ctx, db.Read(), "store: statement sources", func(row scanner) (StatementSource, error) {
		var one StatementSource
		var issued sql.Null[string]
		var due string
		var fetched int64
		var connection BillConnection
		if err := row.Scan(&one.BillID, &one.Source, &issued, &due, &fetched, &connection.Biller, &connection.Label); err != nil {
			return StatementSource{}, err
		}
		day, err := time.Parse("2006-01-02", due)
		if err != nil {
			return StatementSource{}, err
		}
		one.DueOn = domain.DateOf(day)
		if issued.Valid {
			day, err = time.Parse("2006-01-02", issued.V)
			if err != nil {
				return StatementSource{}, err
			}
			one.IssuedOn = domain.DateOf(day)
		}
		one.FetchedAt, one.Provider = fromUnixMillis(fetched), connection.DisplayName()
		return one, nil
	}, `SELECT b.id, b.source, b.issued_on, b.due_on, b.fetched_at, c.biller, c.label
		   FROM bills b JOIN bill_subaccounts sa ON sa.id = b.subaccount_id
		   JOIN bill_connections c ON c.id = sa.connection_id WHERE b.id IN `+marks, values...)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	marks, values := InArgs(ids)
	args := []any{ignored, db.Now(), db.Now()}
	args = append(args, values...)
	args = append(args, ignored)
	return sqliteQueryAll(ctx, db.Write(), "store: set accounts ignored", scanValue[uuid.UUID],
		`UPDATE accounts SET ignored_at = CASE WHEN ? THEN ? END, updated_at = ?
		  WHERE id IN `+marks+` AND NOT is_deleted AND (ignored_at IS NULL) = ? RETURNING id`, args...)
}

// DeleteAccount soft-deletes, keeping the transactions as evidence. Every
// transfer pair with a leg in the account is released first: the surviving
// leg would stay paired with an invisible row, excluded from profit and loss,
// and domain.FindOrphanTransferLegs cannot flag it since the partner is not
// deleted.
func (s *Store) DeleteAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		if err := tx.releaseTransferPairs(ctx, spaceID, "account_id", []uuid.UUID{id}); err != nil {
			return err
		}
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		return sqliteExecOne(ctx, db.Write(), "store: delete account",
			`UPDATE accounts SET is_deleted = true, updated_at = ? WHERE id = ?`, db.Now(), id)
	})
}

func scanAccount(row scanner, spaceID SpaceID) (Account, error) {
	var a Account
	var connectionID, institutionID, securedBy, statementBill *uuid.UUID
	var externalID, description, notes, usageType, maskedNumber, logoURL, simplefinID *string
	var customLogoURL, propertyAddress, vehicleVIN, valuationSource, defaultRegisterTab *string
	var vehicleMileage, milesPerYear *int32
	var kind string
	var providerBalance, creditLimit, statementBalance, minimumDue, withheldBalance, hideBelowBalance sql.Null[int64]
	var openingBalance, goalBalance, pendingHolds int64
	var interestRate sql.Null[string]
	var openingBalanceOn, dueDate, closedOn, syncFloorOn, syncedThroughOn sql.Null[string]
	var historyStartsOn, historyRebuiltFrom, observedSince, mileageAsOf sql.Null[string]
	var providerAt, valuedAt, withheldAt, ignoredAt sql.Null[int64]
	var createdAt, updatedAt int64
	var providerExtra []byte
	var requiresReceipts *bool
	err := row.Scan(&a.ID, &connectionID, &institutionID, &externalID, &a.Name,
		&description, &notes, &kind, &a.Type, &usageType, &a.Currency, &maskedNumber, &logoURL,
		&a.SortOrder, &providerBalance, &providerAt, &openingBalance, &openingBalanceOn,
		&goalBalance, &pendingHolds, &creditLimit, &statementBalance, &minimumDue, &dueDate,
		&interestRate, &a.StatementCloseDay, &a.ExcludedFromReports, &a.ExcludedFromSpendingPlan,
		&a.ExcludedFromAccountBar, &a.IncludeInNetWorth, &a.ExcludeBankPending, &a.IsClosed,
		&closedOn, &a.IsDeleted, &simplefinID, &syncFloorOn, &customLogoURL, &propertyAddress,
		&vehicleVIN, &vehicleMileage, &mileageAsOf, &milesPerYear, &valuationSource, &valuedAt,
		&securedBy, &providerExtra, &withheldBalance, &withheldAt, &a.AcceptZeroBalance,
		&statementBill, &syncedThroughOn, &a.WithheldBalanceReason, &historyStartsOn, &historyRebuiltFrom, &hideBelowBalance,
		&ignoredAt, &defaultRegisterTab, &requiresReceipts, &observedSince,
		&createdAt, &updatedAt)
	if err != nil {
		return Account{}, err
	}
	if len(providerExtra) > 0 {
		a.ProviderExtra = json.RawMessage(providerExtra)
	}

	a.SpaceID = spaceID
	a.ProviderBalanceAt, a.ValuedAt = nullableTime(providerAt), nullableTime(valuedAt)
	a.WithheldBalanceAt, a.IgnoredAt = nullableTime(withheldAt), nullableTime(ignoredAt)
	a.CreatedAt, a.UpdatedAt = fromUnixMillis(createdAt), fromUnixMillis(updatedAt)
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
	for _, field := range []struct {
		raw sql.Null[string]
		dst *domain.Date
	}{
		{openingBalanceOn, &a.OpeningBalanceOn}, {dueDate, &a.DueDate}, {closedOn, &a.ClosedOn},
		{syncFloorOn, &a.SyncFloorOn}, {syncedThroughOn, &a.SyncedThroughOn},
		{historyStartsOn, &a.HistoryStartsOn}, {historyRebuiltFrom, &a.HistoryRebuiltFrom},
		{observedSince, &a.ObservedSince}, {mileageAsOf, &a.MileageAsOf},
	} {
		if !field.raw.Valid {
			continue
		}
		day, err := time.Parse("2006-01-02", field.raw.V)
		if err != nil {
			return Account{}, err
		}
		*field.dst = domain.DateOf(day)
	}
	a.CustomLogoURL = Deref(customLogoURL)
	a.PropertyAddress = Deref(propertyAddress)
	a.VehicleVIN = Deref(vehicleVIN)
	a.ValuationSource = Deref(valuationSource)
	a.DefaultRegisterTab = Deref(defaultRegisterTab)
	if vehicleMileage != nil {
		a.VehicleMileage, a.HasVehicleMileage = int(*vehicleMileage), true
	}
	if milesPerYear != nil {
		a.MilesPerYear, a.HasMilesPerYear = int(*milesPerYear), true
	}

	for _, field := range []struct {
		raw int64
		dst *domain.Money
	}{
		{openingBalance, &a.OpeningBalance}, {goalBalance, &a.GoalBalance}, {pendingHolds, &a.PendingHolds},
	} {
		value, err := fromMinor(field.raw, a.Currency)
		if err != nil {
			return Account{}, err
		}
		*field.dst = value
	}
	for _, field := range []struct {
		raw sql.Null[int64]
		dst *domain.Money
		has *bool
	}{
		{providerBalance, &a.ProviderBalance, &a.HasProviderBalance},
		{creditLimit, &a.CreditLimit, &a.HasCreditLimit},
		{statementBalance, &a.StatementBalance, &a.HasStatementBalance},
		{minimumDue, &a.MinimumDue, &a.HasMinimumDue},
		{withheldBalance, &a.WithheldBalance, &a.HasWithheldBalance},
		{hideBelowBalance, &a.HideBelowBalance, &a.HasHideBelowBalance},
	} {
		if !field.raw.Valid {
			continue
		}
		value, err := fromMinor(field.raw.V, a.Currency)
		if err != nil {
			return Account{}, err
		}
		*field.dst, *field.has = value, true
	}
	if interestRate.Valid {
		var err error
		a.InterestRate, err = decimaltext.Parse(interestRate.V)
		if err != nil {
			return Account{}, err
		}
		a.HasInterestRate = true
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
