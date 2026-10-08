package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Accounts, their balances, and the window summary the register is paired with.
// Every figure comes from internal/domain over one posting list, so the header
// card cannot contradict itself.
//
// provider_balance, simplefin_account_id and sync_floor_on are readable and
// not writable: the sync pipeline and the relink step own them.

func init() {
	Register(Resource{Prefix: "/accounts", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listAccounts)
		rt.Write(http.MethodPost, "/", createAccount)
		rt.Read(http.MethodGet, "/{account_id}", readAccount)
		rt.Read(http.MethodGet, "/{account_id}/summary", readAccountSummary)
		rt.Write(http.MethodPatch, "/{account_id}", updateAccount)
		rt.Write(http.MethodDelete, "/{account_id}", deleteAccount)
		rt.Read(http.MethodGet, "/valuation-sources", listValuationSources)
		rt.Write(http.MethodPost, "/revalue", revalueAssets)
		rt.Write(http.MethodPost, "/{account_id}/revalue", revalueAsset)
		rt.Write(http.MethodPost, "/{account_id}/value-history", importValueHistory)
	}})
}

// AccountResponse is one account as the client reads it.
type AccountResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Notes       *string   `json:"notes"`
	// Kind is what the account is, for arithmetic; the only one of Kind and
	// Type a calculation reads.
	Kind domain.AccountKind `json:"kind"`
	// Type is the user-facing picker label — "Roth IRA", "Home Equity Loan".
	// Never read by a calculation.
	Type          string     `json:"type"`
	Currency      string     `json:"currency"`
	InstitutionID *uuid.UUID `json:"institution_id"`
	ConnectionID  *uuid.UUID `json:"connection_id"`
	MaskedNumber  *string    `json:"masked_number"`
	// LogoURL is what to draw: the household's choice, else the provider's
	// favicon. CustomLogoURL is the raw choice, for the settings field.
	LogoURL       *string `json:"logo_url"`
	CustomLogoURL *string `json:"custom_logo_url"`
	SortOrder     int     `json:"sort_order"`

	ProviderBalance   *domain.Money `json:"provider_balance"`
	ProviderBalanceAt *time.Time    `json:"provider_balance_at"`

	// A balance the sync declined to apply because it looked like the feed
	// dropping a figure (domain.SuspectBalanceReset). ProviderBalance still
	// holds the last good figure; null when nothing is held.
	// WithheldBalanceReason says what the figure was compared with (empty when
	// nothing is held). AcceptZeroBalance turns the guard off for this account.
	WithheldBalance       *domain.Money `json:"withheld_balance"`
	WithheldBalanceAt     *time.Time    `json:"withheld_balance_at"`
	WithheldBalanceReason string        `json:"withheld_balance_reason"`
	AcceptZeroBalance     bool          `json:"accept_zero_balance"`
	OpeningBalance        domain.Money  `json:"opening_balance"`
	OpeningBalanceOn      *Date         `json:"opening_balance_on"`
	GoalBalance           domain.Money  `json:"goal_balance"`
	PendingHolds          domain.Money  `json:"pending_holds"`

	CreditLimit       *domain.Money `json:"credit_limit"`
	StatementBalance  *domain.Money `json:"statement_balance"`
	MinimumDue        *domain.Money `json:"minimum_due"`
	DueDate           *Date         `json:"due_date"`
	InterestRate      *domain.Rate  `json:"interest_rate"`
	StatementCloseDay *int          `json:"statement_close_day"`
	// StatementSource is the bill the three statement figures above were
	// copied from, null when they were typed in, reported by a connector, or
	// are absent.
	StatementSource *StatementSourceResponse `json:"statement_source"`
	statementBillID uuid.UUID

	// What an outside source needs to price a house or a car. All null on an
	// account that is not a physical asset.
	PropertyAddress *string    `json:"property_address"`
	VehicleVIN      *string    `json:"vehicle_vin"`
	VehicleMileage  *int       `json:"vehicle_mileage"`
	MileageAsOf     *Date      `json:"vehicle_mileage_as_of"`
	MilesPerYear    *int       `json:"vehicle_miles_per_year"`
	ValuationSource *string    `json:"valuation_source"`
	ValuedAt        *time.Time `json:"valued_at"`

	// SecuredByAccountID is the asset this loan is secured on. Null on a
	// non-loan or an unpaired loan. The other side is the asset's `equity`
	// block.
	SecuredByAccountID *uuid.UUID `json:"secured_by_account_id"`

	// Four independent flags; ground rule 4 is about the first two.
	ExcludedFromReports      bool `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan bool `json:"excluded_from_spending_plan"`
	ExcludedFromAccountBar   bool `json:"excluded_from_account_bar"`
	IncludeInNetWorth        bool `json:"include_in_net_worth"`
	ExcludeBankPending       bool `json:"exclude_bank_pending"`
	// RequiresReceipts is whether every spending row here needs a receipt:
	// the household's setting, or with none, true for the cash side of an HSA.
	RequiresReceipts bool `json:"requires_receipts"`

	IsClosed           bool    `json:"is_closed"`
	ClosedOn           *Date   `json:"closed_on"`
	SimpleFINAccountID *string `json:"simplefin_account_id"`
	SyncFloorOn        *Date   `json:"sync_floor_on"`

	// HistoryStartsOn is the household's override of where the balance
	// history begins, null for automatic. The day in force is on the listing's
	// `history` block.
	HistoryStartsOn *Date `json:"history_starts_on"`

	// HideBelowBalance is the account's own small-balance threshold, null to
	// follow its institution's; 0 always shows it. See HiddenSmallBalance.
	HideBelowBalance *domain.Money `json:"hide_below_balance"`

	// DefaultRegisterTab is the register tab the account opens on: "all",
	// "spending" or "income"; null opens on the rows.
	DefaultRegisterTab *string `json:"default_register_tab"`

	// ProviderExtra is everything the last sync sent that no field above
	// names — how anyone finds out whether an issuer reports a statement.
	ProviderExtra json.RawMessage `json:"provider_extra,omitempty"`
}

// BalancesResponse is the four figures the account header card shows, all from
// the same posting list.
type BalancesResponse struct {
	Balance            domain.Money `json:"balance"`
	BalanceWithPending domain.Money `json:"balance_with_pending"`
	AvailableBalance   domain.Money `json:"available_balance"`
	// CreditUsedPct is null when there is no limit; the UI must not substitute
	// zero.
	CreditUsedPct *domain.Rate `json:"credit_used_pct"`
}

// AccountWithBalances is the listing shape: the row and its computed figures.
type AccountWithBalances struct {
	AccountResponse
	Balances BalancesResponse `json:"balances"`
	// Equity is present only on an asset that secures at least one loan.
	Equity *EquityResponse `json:"equity"`
	// History is where the account's balance history begins; see
	// domain.HistoryStart.
	History HistoryStartResponse `json:"history"`
	// HiddenSmallBalance is domain.HiddenForSmallBalance over the balance
	// above: the account lists and the pickers that file into an account leave
	// the row out; no figure does.
	HiddenSmallBalance bool `json:"hidden_small_balance"`
}

// HistoryStartResponse is where an account's balance history begins, and
// where it would begin with no override.
type HistoryStartResponse struct {
	// StartsOn is the day in force: the account's history_starts_on when set,
	// AutomaticStartsOn otherwise.
	StartsOn *Date `json:"starts_on"`
	// AutomaticStartsOn is the earliest evidence: the first row, a stated
	// opening balance, an imported balance, or the day the account was added.
	AutomaticStartsOn *Date `json:"automatic_starts_on"`
}

func historyStartFor(account store.Account, postings []domain.Posting) HistoryStartResponse {
	domainAccount := store.DomainAccount(account)
	return HistoryStartResponse{
		StartsOn:          nullableDate(domain.HistoryStart(domainAccount, postings)),
		AutomaticStartsOn: nullableDate(domain.AutomaticHistoryStart(domainAccount, postings)),
	}
}

// EquityResponse is what is left of a financed asset, with Owed and Value sent
// alongside so the client never re-derives the sign convention.
type EquityResponse struct {
	Value  domain.Money `json:"value"`
	Owed   domain.Money `json:"owed"`
	Equity domain.Money `json:"equity"`
	// LoanToValue is a plain ratio — 0.6762 for 67.62% — and is null for an
	// asset recorded as worth nothing, where the ratio is undefined.
	LoanToValue *domain.Rate `json:"loan_to_value"`
	LoanIDs     []uuid.UUID  `json:"loan_ids"`
}

// AccountSummaryResponse is an account's movement over one explicit window: it
// states the balance carried into the window the register lists, so a running
// balance seeded here adds each row once. The window is echoed.
type AccountSummaryResponse struct {
	AccountID      uuid.UUID      `json:"account_id"`
	Window         WindowResponse `json:"window"`
	OpeningBalance domain.Money   `json:"opening_balance"`
	EndingBalance  domain.Money   `json:"ending_balance"`
	// Total is the net movement of the rows the register returns for the same
	// window.
	//
	// Not EndingBalance − OpeningBalance: Total counts pending rows, which the
	// register shows, while a manual account's balance excludes them until
	// they settle.
	Total    domain.Money     `json:"total"`
	Count    int              `json:"count"`
	Balances BalancesResponse `json:"balances"`
}

// accountFields are the fields a create and an edit both accept, applied the
// same way by applyAccountFields.
type accountFields struct {
	Currency          Opt[string]       `json:"currency"`
	Description       Opt[string]       `json:"description"`
	Notes             Opt[string]       `json:"notes"`
	InstitutionID     Opt[uuid.UUID]    `json:"institution_id"`
	MaskedNumber      Opt[string]       `json:"masked_number"`
	SortOrder         Opt[int]          `json:"sort_order"`
	OpeningBalance    Opt[domain.Money] `json:"opening_balance"`
	OpeningBalanceOn  Opt[Date]         `json:"opening_balance_on"`
	CreditLimit       Opt[domain.Money] `json:"credit_limit"`
	StatementCloseDay Opt[int]          `json:"statement_close_day"`
	// The statement. SimpleFIN has no field for these, so they are writable.
	// InterestRate is read at either scale; see applyReportedAPR.
	StatementBalance Opt[domain.Money] `json:"statement_balance"`
	MinimumDue       Opt[domain.Money] `json:"minimum_due"`
	DueDate          Opt[Date]         `json:"due_date"`
	InterestRate     Opt[domain.Rate]  `json:"interest_rate"`

	CustomLogoURL   Opt[string] `json:"custom_logo_url"`
	PropertyAddress Opt[string] `json:"property_address"`
	VehicleVIN      Opt[string] `json:"vehicle_vin"`
	VehicleMileage  Opt[int]    `json:"vehicle_mileage"`
	MileageAsOf     Opt[Date]   `json:"vehicle_mileage_as_of"`
	MilesPerYear    Opt[int]    `json:"vehicle_miles_per_year"`

	// SecuredBy pairs this loan with the asset it is secured on. Explicit null
	// unpairs it.
	SecuredBy Opt[uuid.UUID] `json:"secured_by_account_id"`

	// The two exclusion flags are separate fields so setting one never moves
	// the other.
	ExcludedFromReports      Opt[bool] `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan Opt[bool] `json:"excluded_from_spending_plan"`
	ExcludedFromAccountBar   Opt[bool] `json:"excluded_from_account_bar"`
	IncludeInNetWorth        Opt[bool] `json:"include_in_net_worth"`
	ExcludeBankPending       Opt[bool] `json:"exclude_bank_pending"`
	// RequiresReceipts null goes back to what the account's type implies.
	RequiresReceipts Opt[bool] `json:"requires_receipts"`
}

// AccountCreate is a new account.
type AccountCreate struct {
	Name string             `json:"name"`
	Kind domain.AccountKind `json:"kind"`
	Type string             `json:"type"`
	accountFields
}

// AccountUpdate is a partial edit; absent means "leave alone".
type AccountUpdate struct {
	Name Opt[string]             `json:"name"`
	Kind Opt[domain.AccountKind] `json:"kind"`
	Type Opt[string]             `json:"type"`
	accountFields

	IsClosed Opt[bool] `json:"is_closed"`
	ClosedOn Opt[Date] `json:"closed_on"`

	// HistoryStartsOn overrides where the balance history begins, earlier or
	// later than the first row. Explicit null returns it to automatic.
	HistoryStartsOn Opt[Date] `json:"history_starts_on"`

	// HideBelowBalance is the account's small-balance threshold. Explicit null
	// defers to the institution's; 0 always shows the account.
	HideBelowBalance Opt[domain.Money] `json:"hide_below_balance"`

	// DefaultRegisterTab is the register tab the account opens on. Explicit
	// null opens it on the rows.
	DefaultRegisterTab Opt[string] `json:"default_register_tab"`
}

// accountFilter is the repeated account_id parameter: which accounts a request
// narrowed to, and whether it narrowed at all. No parameter is every account;
// an explicitly empty selection is no accounts.
type accountFilter struct {
	IDs []uuid.UUID
	// Given is true when the request named a selection, empty or not.
	Given bool
}

func queryAccountFilter(r *http.Request) (accountFilter, error) {
	ids, given, err := queryUUIDs(r, "account_id")
	if err != nil {
		return accountFilter{}, err
	}
	return accountFilter{IDs: ids, Given: given}, nil
}

// selectsNothing is a request that named no accounts on purpose.
func (f accountFilter) selectsNothing() bool {
	return f.Given && len(f.IDs) == 0
}

// narrow scopes an account query to the selection.
func (f accountFilter) narrow(base store.AccountQuery) store.AccountQuery {
	base.IDs = f.IDs
	return base
}

// StatementSourceResponse is the bill an account's statement figures came
// from.
type StatementSourceResponse struct {
	BillID uuid.UUID `json:"bill_id"`
	// Source is the bill's: provider, email, manual or assistant.
	Source string `json:"source"`
	// Provider is the bill connection, as the Bill providers page names it.
	Provider  string    `json:"provider"`
	IssuedOn  *Date     `json:"issued_on"`
	DueOn     Date      `json:"due_on"`
	FetchedAt time.Time `json:"fetched_at"`
}

func attachStatementSources(r *http.Request, env *Env, sp auth.SpaceContext, out []*AccountResponse) error {
	var ids []uuid.UUID
	for _, one := range out {
		if one.statementBillID != uuid.Nil {
			ids = append(ids, one.statementBillID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sources, err := env.DB.StatementSources(r.Context(), sp.ID(), ids)
	if err != nil {
		return err
	}
	for _, one := range out {
		source, found := sources[one.statementBillID]
		if !found {
			continue
		}
		one.StatementSource = &StatementSourceResponse{
			BillID: source.BillID, Source: source.Source, Provider: source.Provider,
			IssuedOn: nullableDate(source.IssuedOn), DueOn: Date(source.DueOn), FetchedAt: source.FetchedAt,
		}
	}
	return nil
}

// withStatementSource is one account's response with its statement's source.
func withStatementSource(r *http.Request, env *Env, sp auth.SpaceContext, one AccountResponse) (AccountResponse, error) {
	err := attachStatementSources(r, env, sp, []*AccountResponse{&one})
	return one, err
}

func listAccounts(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	includeClosed := true
	if value, given, err := queryBool(r, "include_closed"); err != nil {
		return err
	} else if given {
		includeClosed = value
	}

	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(),
		store.AccountQuery{IncludeClosed: includeClosed})
	if err != nil {
		return err
	}
	byAccount, err := postingsByAccount(r.Context(), env, sp, accounts)
	if err != nil {
		return err
	}

	reserves, err := goalReserves(r.Context(), env, sp)
	if err != nil {
		return err
	}
	thresholds, err := institutionThresholds(r, env, sp)
	if err != nil {
		return err
	}

	out := make([]AccountWithBalances, 0, len(accounts))
	for _, account := range accounts {
		balances := balancesFor(account, byAccount[account.ID], reserves[account.ID])
		out = append(out, AccountWithBalances{
			AccountResponse: accountResponse(account, reserves[account.ID]),
			Balances:        balances,
			History:         historyStartFor(account, byAccount[account.ID]),
			HiddenSmallBalance: domain.HiddenForSmallBalance(balances.Balance,
				domain.SmallBalanceThreshold{Amount: account.HideBelowBalance, Set: account.HasHideBelowBalance},
				thresholds[account.InstitutionID]),
		})
	}
	// A second pass: equity needs the loans' balances, settled only once every
	// row has been through the first.
	attachEquity(out)
	responses := make([]*AccountResponse, 0, len(out))
	for i := range out {
		responses = append(responses, &out[i].AccountResponse)
	}
	if err := attachStatementSources(r, env, sp, responses); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// attachEquity fills in the equity block on every asset that secures a loan.
// Closed loans are already out of `rows`, so a paid-off mortgage stops
// counting against the house.
func attachEquity(rows []AccountWithBalances) {
	loans := map[uuid.UUID][]AccountWithBalances{}
	for _, row := range rows {
		if row.SecuredByAccountID != nil {
			loans[*row.SecuredByAccountID] = append(loans[*row.SecuredByAccountID], row)
		}
	}
	if len(loans) == 0 {
		return
	}
	for i := range rows {
		secured, ok := loans[rows[i].ID]
		if !ok {
			continue
		}
		balances := make([]domain.Money, 0, len(secured))
		ids := make([]uuid.UUID, 0, len(secured))
		owed := domain.Zero
		for _, loan := range secured {
			balances = append(balances, loan.Balances.Balance)
			ids = append(ids, loan.ID)
			owed = owed.Add(loan.Balances.Balance.Abs())
		}
		value := rows[i].Balances.Balance
		block := EquityResponse{
			Value:   value,
			Owed:    owed.Round(),
			Equity:  domain.Equity(value, balances),
			LoanIDs: ids,
		}
		if ratio, ok := domain.LoanToValue(value, balances); ok {
			block.LoanToValue = &ratio
		}
		rows[i].Equity = &block
	}
}

func createAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body AccountCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Name == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkAccountKind(body.Kind); err != nil {
		return err
	}
	if body.Type == "" {
		return errInvalid("missing", []string{"body", "type"}, "type is required")
	}

	account := &store.Account{
		Name: body.Name,
		Kind: body.Kind,
		Type: body.Type,
		// The space's own currency, not a hardcoded dollar.
		Currency:          sp.Space.PrimaryCurrency,
		IncludeInNetWorth: true,
	}
	if account.Currency == "" {
		account.Currency = "USD"
	}
	if body.OpeningBalance.Present() {
		account.OpeningBalance = body.OpeningBalance.Value
	}
	if err := applyAccountFields(r.Context(), env, sp, body.accountFields, account); err != nil {
		return err
	}

	if err := env.DB.CreateAccount(r.Context(), sp.ID(), account); err != nil {
		return err
	}
	return writeAccount(env, w, r, sp, *account, http.StatusCreated)
}

func readAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	account, err := liveAccount(r, env, sp)
	if err != nil {
		return err
	}
	return writeAccount(env, w, r, sp, account, http.StatusOK)
}

// readAccountSummary is the account's movement over an explicit window. It
// resolves from / to / date_field exactly as the register does: an omitted
// bound is unbounded in both, never "this month" (trap 5).
func readAccountSummary(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	account, err := liveAccount(r, env, sp)
	if err != nil {
		return err
	}

	postings, _, err := service.LoadPostings(r.Context(), env.DB, sp.ID(),
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	if err != nil {
		return err
	}
	domainAccount := store.DomainAccount(account)

	inside := make([]domain.Posting, 0, len(postings))
	for _, posting := range postings {
		if domain.CountsTowardBalance(posting) &&
			window.Contains(domain.ReportingDate(posting.Txn, window.Mode)) {
			inside = append(inside, posting)
		}
	}

	reserves, err := goalReserves(r.Context(), env, sp)
	if err != nil {
		return err
	}

	opening := domainAccount.OpeningBalance
	if before, bounded := window.DayBeforeStart(); bounded {
		opening = domain.LedgerBalanceAsOf(domainAccount, postings, before, window.Mode)
	} else if !domainAccount.IsManual() {
		if opening, err = domain.OpeningBalanceForConnected(domainAccount, postings); err != nil {
			return err
		}
	}

	ending := domain.AccountBalance(domainAccount, postings)
	if window.HasTo {
		ending = domain.LedgerBalanceAsOf(domainAccount, postings, window.To, window.Mode)
	}

	return writeJSON(w, http.StatusOK, AccountSummaryResponse{
		AccountID:      account.ID,
		Window:         windowResponse(window),
		OpeningBalance: opening,
		EndingBalance:  ending,
		Total:          domain.LedgerTotal(inside),
		Count:          len(inside),
		Balances:       balancesFor(account, postings, reserves[account.ID]),
	})
}

func updateAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	account, err := liveAccount(r, env, sp)
	if err != nil {
		return err
	}
	var body AccountUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	// The running balance is anchored at the opening balance or the re-signed
	// provider balance, so moving either means recomputing.
	balanceAnchorMoved := false

	// Every card row was stamped against the statement cycle as it stood, so
	// moving the cycle means restamping the history.
	cycleBefore := cardCycle(account)

	if err := applyRequired("name", body.Name, &account.Name); err != nil {
		return err
	}
	if body.Kind.Set {
		if body.Kind.Null {
			return errConflict("kind cannot be cleared")
		}
		before := account.Kind
		if err := checkAccountKind(body.Kind.Value); err != nil {
			return err
		}
		resignProviderBalance(&account, body.Kind.Value)
		account.Kind = body.Kind.Value
		balanceAnchorMoved = balanceAnchorMoved || before != account.Kind
	}
	if err := applyRequired("type", body.Type, &account.Type); err != nil {
		return err
	}
	applyNullable(body.OpeningBalance, &account.OpeningBalance)
	balanceAnchorMoved = balanceAnchorMoved || body.OpeningBalance.Set
	openingOnBefore := account.OpeningBalanceOn
	lookupBefore := valuationLookupOf(account)
	if err := applyAccountFields(r.Context(), env, sp, body.accountFields, &account); err != nil {
		return err
	}
	if valuationLookupOf(account) != lookupBefore {
		// The last estimate was for another address, car or odometer.
		account.ValuedAt = nil
	}
	if body.OpeningBalanceOn.Set && account.OpeningBalanceOn != openingOnBefore {
		balanceAnchorMoved = true
	}
	if body.StatementBalance.Set || body.MinimumDue.Set || body.DueDate.Set {
		// A figure typed over a bill's copy is the person's now: later bills for
		// the cycle leave it alone.
		account.StatementBillID = uuid.Nil
	}
	if err := applyRequired("is_closed", body.IsClosed, &account.IsClosed); err != nil {
		return err
	}
	applyNullable(body.ClosedOn, (*Date)(&account.ClosedOn))
	if err := checkHideBelow(body.HideBelowBalance); err != nil {
		return err
	}
	applyNullableMoney(body.HideBelowBalance, &account.HideBelowBalance, &account.HasHideBelowBalance)
	if err := checkRegisterTab(body.DefaultRegisterTab); err != nil {
		return err
	}
	applyNullable(body.DefaultRegisterTab, &account.DefaultRegisterTab)
	historyStartBefore := account.HistoryStartsOn
	applyNullable(body.HistoryStartsOn, (*Date)(&account.HistoryStartsOn))
	historyMoved := account.HistoryStartsOn != historyStartBefore ||
		account.OpeningBalanceOn != openingOnBefore

	// One transaction: a retry would see the cycle already changed and never
	// restamp.
	err = env.DB.InTx(r.Context(), func(tx *store.Store) error {
		if err := tx.UpdateAccount(r.Context(), sp.ID(), &account); err != nil {
			return err
		}
		if cardCycle(account) != cycleBefore {
			if _, err := service.NewCreditCards(tx).Restamp(
				r.Context(), sp.ID(), account.ID, nil); err != nil {
				return err
			}
		}
		if historyMoved {
			if err := tx.RebuildAccountHistory(
				r.Context(), sp.ID(), account.ID, domain.DateOf(env.now())); err != nil {
				return err
			}
		}
		if balanceAnchorMoved {
			return service.RecomputeRunningBalances(r.Context(), tx, sp.ID(), account.ID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeAccount(env, w, r, sp, account, http.StatusOK)
}

// applyAccountFields applies the fields AccountCreate and AccountUpdate share,
// except the opening balance, which the two treat differently.
func applyAccountFields(ctx context.Context, env *Env, sp auth.SpaceContext, f accountFields, account *store.Account) error {
	if err := applyRequired("currency", f.Currency, &account.Currency); err != nil {
		return err
	}
	applyNullable(f.Description, &account.Description)
	applyNullable(f.Notes, &account.Notes)
	applyNullable(f.InstitutionID, &account.InstitutionID)
	applyNullable(f.MaskedNumber, &account.MaskedNumber)
	if err := applyRequired("sort_order", f.SortOrder, &account.SortOrder); err != nil {
		return err
	}
	applyNullable(f.OpeningBalanceOn, (*Date)(&account.OpeningBalanceOn))
	applyNullableMoney(f.CreditLimit, &account.CreditLimit, &account.HasCreditLimit)
	applyNullableMoney(f.StatementBalance, &account.StatementBalance, &account.HasStatementBalance)
	applyNullableMoney(f.MinimumDue, &account.MinimumDue, &account.HasMinimumDue)
	applyNullable(f.DueDate, (*Date)(&account.DueDate))
	if err := applyReportedAPR("interest_rate", f.InterestRate,
		&account.InterestRate, &account.HasInterestRate); err != nil {
		return err
	}
	applyStatementCloseDay(f.StatementCloseDay, account)
	applyNullable(f.CustomLogoURL, &account.CustomLogoURL)
	applyNullable(f.PropertyAddress, &account.PropertyAddress)
	applyNullable(f.VehicleVIN, &account.VehicleVIN)
	applyPresent(f.VehicleMileage, &account.VehicleMileage, &account.HasVehicleMileage)
	applyNullable(f.MileageAsOf, (*Date)(&account.MileageAsOf))
	applyPresent(f.MilesPerYear, &account.MilesPerYear, &account.HasMilesPerYear)
	if err := checkSecuredBy(ctx, env, sp, account.ID, f.SecuredBy); err != nil {
		return err
	}
	applyNullable(f.SecuredBy, &account.SecuredByAccountID)
	if err := applyRequired("excluded_from_reports", f.ExcludedFromReports, &account.ExcludedFromReports); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_spending_plan", f.ExcludedFromSpendingPlan, &account.ExcludedFromSpendingPlan); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_account_bar", f.ExcludedFromAccountBar, &account.ExcludedFromAccountBar); err != nil {
		return err
	}
	if err := applyRequired("include_in_net_worth", f.IncludeInNetWorth, &account.IncludeInNetWorth); err != nil {
		return err
	}
	applyPresent(f.RequiresReceipts, &account.RequiresReceipts, &account.HasRequiresReceipts)
	return applyRequired("exclude_bank_pending", f.ExcludeBankPending, &account.ExcludeBankPending)
}

// writeAccount is the response every single-account read and write gives.
func writeAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, account store.Account, status int) error {
	reserves, err := goalReserves(r.Context(), env, sp)
	if err != nil {
		return err
	}
	out, err := withStatementSource(r, env, sp, accountResponse(account, reserves[account.ID]))
	if err != nil {
		return err
	}
	return writeJSON(w, status, out)
}

// deleteAccount soft-deletes; the transactions stay.
func deleteAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	account, err := liveAccount(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteAccount(r.Context(), sp.ID(), account.ID), "Account")
}

// liveAccount reads the account named in the path, treating a soft-deleted one
// and one in another space as the same 404.
func liveAccount(r *http.Request, env *Env, sp auth.SpaceContext) (store.Account, error) {
	account, err := fromPath(r, sp, "account_id", "Account", env.DB.GetAccount)
	if err != nil {
		return store.Account{}, err
	}
	if account.IsDeleted {
		return store.Account{}, errNotFound("Account")
	}
	return account, nil
}

// postingsByAccount loads every live posting of these accounts, grouped. The
// whole ledger by design: a past balance walks backwards from the provider's
// current figure.
func postingsByAccount(ctx context.Context, env *Env, sp auth.SpaceContext, accounts []store.Account) (map[uuid.UUID][]domain.Posting, error) {
	grouped := make(map[uuid.UUID][]domain.Posting, len(accounts))
	if len(accounts) == 0 {
		return grouped, nil
	}
	ids := make([]uuid.UUID, len(accounts))
	for i, account := range accounts {
		ids[i] = account.ID
	}

	postings, _, err := service.LoadPostings(ctx, env.DB, sp.ID(), store.TransactionQuery{AccountIDs: ids})
	if err != nil {
		return nil, err
	}
	for _, posting := range postings {
		key, err := store.ParseID(posting.Txn.AccountID)
		if err != nil {
			return nil, err
		}
		grouped[key] = append(grouped[key], posting)
	}
	return grouped, nil
}

// goalReserves is how much each account has spoken for by savings goals,
// derived from contributions. accounts.goal_balance is written at import and
// never updated as goals and contributions change afterward, so reading it
// back would show stale goal money as spendable, or spendable money as
// reserved.
func goalReserves(
	ctx context.Context, env *Env, sp auth.SpaceContext,
) (map[uuid.UUID]domain.Money, error) {
	goals, err := env.DB.ListGoals(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	if len(goals) == 0 {
		return nil, nil
	}
	contributions, _, err := goalContributions(ctx, env, sp, goals)
	if err != nil {
		return nil, err
	}

	domainGoals := make([]domain.Goal, 0, len(goals))
	accounts := map[uuid.UUID]struct{}{}
	for _, row := range goals {
		domainGoals = append(domainGoals, store.DomainGoal(row))
		accounts[row.AccountID] = struct{}{}
	}

	reserves := make(map[uuid.UUID]domain.Money, len(accounts))
	for id := range accounts {
		reserves[id] = domain.ReservedInAccount(domain.ID(id.String()), domainGoals, contributions)
	}
	return reserves, nil
}

func balancesFor(
	account store.Account, postings []domain.Posting, reserved domain.Money,
) BalancesResponse {
	domainAccount := store.DomainAccount(account)
	// The reserve is derived rather than read off the row; see goalReserves.
	domainAccount.GoalBalance = reserved
	balance := domain.AccountBalance(domainAccount, postings)
	pct, hasLimit := domain.CreditUsedPct(domainAccount, balance)
	return BalancesResponse{
		Balance:            balance,
		BalanceWithPending: domain.BalanceWithPending(domainAccount, postings),
		AvailableBalance:   domain.AvailableBalance(domainAccount, postings),
		CreditUsedPct:      store.PtrIf(pct, hasLimit),
	}
}

// accountResponse renders one account. `reserved` comes from goalReserves, not
// the stored goal_balance column.
func accountResponse(a store.Account, reserved domain.Money) AccountResponse {
	out := AccountResponse{
		ID:                       a.ID,
		Name:                     a.Name,
		Description:              dbconv.NullText(a.Description),
		Notes:                    dbconv.NullText(a.Notes),
		Kind:                     a.Kind,
		Type:                     a.Type,
		Currency:                 a.Currency,
		InstitutionID:            dbconv.NullUUID(a.InstitutionID),
		ConnectionID:             dbconv.NullUUID(a.ConnectionID),
		MaskedNumber:             dbconv.NullText(a.MaskedNumber),
		LogoURL:                  dbconv.NullText(displayLogo(a)),
		CustomLogoURL:            dbconv.NullText(a.CustomLogoURL),
		SortOrder:                a.SortOrder,
		ProviderBalance:          store.PtrIf(a.ProviderBalance, a.HasProviderBalance),
		ProviderBalanceAt:        a.ProviderBalanceAt,
		WithheldBalance:          store.PtrIf(a.WithheldBalance, a.HasWithheldBalance),
		WithheldBalanceAt:        a.WithheldBalanceAt,
		WithheldBalanceReason:    a.WithheldBalanceReason,
		AcceptZeroBalance:        a.AcceptZeroBalance,
		OpeningBalance:           a.OpeningBalance,
		OpeningBalanceOn:         nullableDate(a.OpeningBalanceOn),
		GoalBalance:              reserved,
		PendingHolds:             a.PendingHolds,
		CreditLimit:              store.PtrIf(a.CreditLimit, a.HasCreditLimit),
		StatementBalance:         store.PtrIf(a.StatementBalance, a.HasStatementBalance),
		MinimumDue:               store.PtrIf(a.MinimumDue, a.HasMinimumDue),
		DueDate:                  nullableDate(a.DueDate),
		InterestRate:             store.PtrIf(a.InterestRate, a.HasInterestRate),
		StatementCloseDay:        closeDayResponse(a.StatementCloseDay),
		PropertyAddress:          dbconv.NullText(a.PropertyAddress),
		VehicleVIN:               dbconv.NullText(a.VehicleVIN),
		VehicleMileage:           store.PtrIf(a.VehicleMileage, a.HasVehicleMileage),
		MileageAsOf:              nullableDate(a.MileageAsOf),
		MilesPerYear:             store.PtrIf(a.MilesPerYear, a.HasMilesPerYear),
		ValuationSource:          dbconv.NullText(a.ValuationSource),
		ValuedAt:                 a.ValuedAt,
		SecuredByAccountID:       dbconv.NullUUID(a.SecuredByAccountID),
		ExcludedFromReports:      a.ExcludedFromReports,
		ExcludedFromSpendingPlan: a.ExcludedFromSpendingPlan,
		ExcludedFromAccountBar:   a.ExcludedFromAccountBar,
		IncludeInNetWorth:        a.IncludeInNetWorth,
		ExcludeBankPending:       a.ExcludeBankPending,
		RequiresReceipts:         store.DomainAccount(a).RequiresReceipts,
		IsClosed:                 a.IsClosed,
		ClosedOn:                 nullableDate(a.ClosedOn),
		ProviderExtra:            a.ProviderExtra,
		SimpleFINAccountID:       dbconv.NullText(a.SimpleFINAccountID),
		SyncFloorOn:              nullableDate(a.SyncFloorOn),
		HistoryStartsOn:          nullableDate(a.HistoryStartsOn),
		HideBelowBalance:         store.PtrIf(a.HideBelowBalance, a.HasHideBelowBalance),
		DefaultRegisterTab:       dbconv.NullText(a.DefaultRegisterTab),
	}
	out.statementBillID = a.StatementBillID
	return out
}

// resignProviderBalance re-signs the bank's figure when an account changes
// kind, before the kind itself moves. The stored balance was normalized for the
// old kind (debt negative), so a mortgage reclassified from cash must flip sign
// now rather than sit in net worth as an asset until the next sync.
func resignProviderBalance(account *store.Account, next domain.AccountKind) {
	if !account.HasProviderBalance || next == account.Kind {
		return
	}
	switch {
	case next.IsDebt():
		account.ProviderBalance = account.ProviderBalance.Abs().Neg()
	case account.Kind.IsDebt():
		account.ProviderBalance = account.ProviderBalance.Abs()
	}
}

// checkSecuredBy validates a loan's pairing with the asset behind it: not
// itself, not an id from another space (answered as not found), and an asset.
// A no-op when nothing was sent or on an explicit unpairing.
func checkSecuredBy(
	ctx context.Context, env *Env, sp auth.SpaceContext, self uuid.UUID, opt Opt[uuid.UUID],
) error {
	if !opt.Set || opt.Null || opt.Value == uuid.Nil {
		return nil
	}
	field := []string{"body", "secured_by_account_id"}
	if opt.Value == self {
		return errInvalid("invalid", field, "an account cannot be secured on itself")
	}
	target, err := env.DB.GetAccount(ctx, sp.ID(), opt.Value)
	if errors.Is(err, store.ErrNotFound) {
		return errInvalid("not_found", field, "no such account")
	}
	if err != nil {
		return err
	}
	if target.Kind != domain.KindAsset {
		return errInvalid("invalid", field,
			"a loan is secured on an asset, and %q is not one", target.Name)
	}
	return nil
}

func checkRegisterTab(opt Opt[string]) error {
	if !opt.Present() {
		return nil
	}
	switch opt.Value {
	case "all", "spending", "income":
		return nil
	default:
		return errInvalid("enum", []string{"body", "default_register_tab"},
			"default_register_tab must be all, spending or income, got %q", opt.Value)
	}
}

func checkAccountKind(kind domain.AccountKind) error {
	switch kind {
	case domain.KindCash, domain.KindCreditCard, domain.KindLoan,
		domain.KindInvestment, domain.KindAsset:
		return nil
	default:
		return errInvalid("enum", []string{"body", "kind"}, "%q is not an account kind", kind)
	}
}

// cardCycle is everything that decides a charge's effective date.
func cardCycle(account store.Account) [3]int {
	card := 0
	if account.Kind == domain.KindCreditCard {
		card = 1
	}
	return [3]int{card, service.StatementCloseDay(account), service.PaymentDueDay(account)}
}

func applyStatementCloseDay(opt Opt[int], account *store.Account) {
	if !opt.Set {
		return
	}
	if opt.Null {
		account.StatementCloseDay = nil
		return
	}
	day := int16(opt.Value)
	account.StatementCloseDay = &day
}

// applyReportedAPR writes an interest rate the caller may have written at
// either scale (24.99 or 0.2499); domain.APRAsRate decides. A figure that is
// no kind of rate is refused rather than stored.
func applyReportedAPR(field string, opt Opt[domain.Rate], dst *domain.Rate, has *bool) error {
	if !opt.Set {
		return nil
	}
	if opt.Null {
		*dst, *has = domain.Rate{}, false
		return nil
	}
	rate, ok := domain.APRAsRate(opt.Value)
	if !ok {
		return errInvalid("value", []string{"body", field},
			"%s is not an interest rate a card charges", field)
	}
	*dst, *has = rate, true
	return nil
}

func closeDayResponse(day *int16) *int {
	if day == nil {
		return nil
	}
	value := int(*day)
	return &value
}

// displayLogo is the precedence a logo is drawn with, resolved on the way out
// so a re-sync overwriting logo_url cannot take the user's choice.
func displayLogo(a store.Account) string {
	if a.CustomLogoURL != "" {
		return a.CustomLogoURL
	}
	return a.LogoURL
}

// applyPresent clears the Has flag on an explicit null, which is how a user
// says "the mileage is unknown" as opposed to "it is zero".
func applyPresent[T any](opt Opt[T], dst *T, has *bool) {
	if !opt.Set {
		return
	}
	if opt.Null {
		var zero T
		*dst, *has = zero, false
		return
	}
	*dst, *has = opt.Value, true
}

// ----- valuation -------------------------------------------------------------

// Re-pricing real estate and vehicles from an outside estimate. A POST, since
// it posts a balance adjustment.

// ValuationSourcesResponse says which asset types can be priced, and which of
// them this deployment prices.
type ValuationSourcesResponse struct {
	AssetTypes []string `json:"asset_types"`
	Configured []string `json:"configured"`
}

type ValuationResultResponse struct {
	AccountID uuid.UUID `json:"account_id"`
	Name      string    `json:"name"`
	// Skipped names why nothing happened and is null when something did.
	Skipped    *string       `json:"skipped"`
	Source     *string       `json:"source"`
	Estimate   *domain.Money `json:"estimate"`
	Adjustment *domain.Money `json:"adjustment"`
	Mileage    *int          `json:"mileage_used"`
	// PricedAs is what the source took the asset to be, and is null when it
	// gave no estimate.
	PricedAs *PricedAsResponse `json:"priced_as"`
}

type PricedAsResponse struct {
	Year  *string `json:"year"`
	Make  *string `json:"make"`
	Model *string `json:"model"`
	Trim  *string `json:"trim"`
	// Mileage is what the car was priced at: the projected odometer reading,
	// or the source's typical mileage when TypicalMileage is set.
	Mileage        *int          `json:"mileage"`
	TypicalMileage bool          `json:"typical_mileage"`
	Address        *string       `json:"address"`
	Low            *domain.Money `json:"low"`
	High           *domain.Money `json:"high"`
}

type ValuationRunResponse struct {
	Results []ValuationResultResponse `json:"results"`
}

// valuationLookup is what an estimate is looked up by; changing any of it
// makes the asset due.
type valuationLookup struct {
	address, vin string
	mileage      int
	hasMileage   bool
	mileageAsOf  domain.Date
	perYear      int
	hasPerYear   bool
}

func valuationLookupOf(a store.Account) valuationLookup {
	return valuationLookup{
		address: a.PropertyAddress, vin: a.VehicleVIN,
		mileage: a.VehicleMileage, hasMileage: a.HasVehicleMileage, mileageAsOf: a.MileageAsOf,
		perYear: a.MilesPerYear, hasPerYear: a.HasMilesPerYear,
	}
}

func listValuationSources(env *Env, w http.ResponseWriter, r *http.Request, _ auth.SpaceContext) error {
	if !env.browserEngine().HasFirefox() {
		return writeJSON(w, http.StatusOK, valuationSourcesResponse(nil))
	}
	return writeJSON(w, http.StatusOK, valuationSourcesResponse(assetValuers(env)))
}

// valuationSourcesResponse reports which asset types the valuers can price.
func valuationSourcesResponse(valuers map[string]provider.AssetValuationProvider) ValuationSourcesResponse {
	configured := make([]string, 0, len(valuers))
	for _, assetType := range provider.ValuationAssetTypes() {
		if _, ok := valuers[assetType]; ok {
			configured = append(configured, assetType)
		}
	}
	return ValuationSourcesResponse{
		AssetTypes: provider.ValuationAssetTypes(),
		Configured: configured,
	}
}

// revalueAssets re-prices everything due. `?force=1` ignores the staleness
// window, for somebody who has just corrected an address.
func revalueAssets(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	force, _, err := queryBool(r, "force")
	if err != nil {
		return err
	}
	valuation := NewAssetValuation(env)
	results, err := valuation.RevalueAll(r.Context(), sp.ID(), force)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, valuationRunResponse(results))
}

func revalueAsset(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "account_id", "Account")
	if err != nil {
		return err
	}
	valuation := NewAssetValuation(env)
	result, err := valuation.Revalue(r.Context(), sp.ID(), id)
	if err != nil {
		return notFoundAs(err, "Account")
	}
	return writeJSON(w, http.StatusOK, valuationRunResponse([]service.ValuationResult{result}))
}

// NewAssetValuation builds the re-pricing service for both the handlers and
// the daily scheduler.
func NewAssetValuation(env *Env) *service.Valuation {
	return service.NewValuation(env.DB, assetValuers(env))
}

// assetValuers are the scrapers, which run in Camoufox: on a server without
// it a lookup fails with browser.ErrNoFirefox. Each lookup opens and closes
// its own context.
func assetValuers(env *Env) map[string]provider.AssetValuationProvider {
	return provider.Valuers(env.browserEngine().OpenFirefoxFetchSurface)
}

// settingsStore binds the credential cipher for the server-settings queries.
func settingsStore(cfg *config.Config, db *store.Store) (*store.Store, error) {
	cipher, err := store.NewCipher(cfg.CredentialKey())
	if err != nil {
		return nil, err
	}
	return db.WithCipher(cipher), nil
}

func valuationRunResponse(results []service.ValuationResult) ValuationRunResponse {
	out := ValuationRunResponse{Results: make([]ValuationResultResponse, 0, len(results))}
	for _, result := range results {
		row := ValuationResultResponse{
			AccountID: result.AccountID,
			Name:      result.Name,
			Skipped:   dbconv.NullText(result.Skipped),
			Source:    dbconv.NullText(result.Source),
		}
		if result.HasEstimate {
			estimate := result.Estimate
			row.Estimate = &estimate
		}
		if result.HasAdjust {
			adjustment := result.Adjustment
			row.Adjustment = &adjustment
		}
		row.Mileage = store.PtrIf(result.MileageUsed, result.HasMileage)
		if priced := result.Priced; priced != nil {
			row.PricedAs = &PricedAsResponse{
				Year:           dbconv.NullText(priced.Year),
				Make:           dbconv.NullText(priced.Make),
				Model:          dbconv.NullText(priced.Model),
				Trim:           dbconv.NullText(priced.Trim),
				Mileage:        store.PtrIf(priced.Mileage, priced.HasMileage),
				TypicalMileage: priced.TypicalMileage,
				Address:        dbconv.NullText(priced.Address),
				Low:            store.PtrIf(priced.Low, priced.HasRange),
				High:           store.PtrIf(priced.High, priced.HasRange),
			}
		}
		out.Results = append(out.Results, row)
	}
	return out
}

// ValueHistoryWrite is an exported value history: what the asset was worth on
// each of a series of days. Values, not differences.
type ValueHistoryWrite struct {
	Points []ValuePointWrite `json:"points"`
}

type ValuePointWrite struct {
	On    Date         `json:"on"`
	Value domain.Money `json:"value"`
}

// ValueHistoryResponse reports how many rows were written. A point that agrees
// with the ledger writes nothing, so re-importing reports zero.
type ValueHistoryResponse struct {
	Written int `json:"written"`
}

func importValueHistory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "account_id", "Account")
	if err != nil {
		return err
	}
	var body ValueHistoryWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if len(body.Points) == 0 {
		return errInvalid("missing", []string{"body", "points"}, "at least one point is required")
	}

	points := make([]domain.ValuePoint, 0, len(body.Points))
	for i, point := range body.Points {
		if domain.Date(point.On).IsZero() {
			return errInvalid("missing", []string{"body", "points", strconv.Itoa(i), "on"},
				"every point needs a date")
		}
		points = append(points, domain.ValuePoint{On: domain.Date(point.On), Value: point.Value})
	}

	valuation := NewAssetValuation(env)
	written, err := valuation.ImportHistory(r.Context(), sp.ID(), id, points)
	if err != nil {
		return notFoundAs(err, "Account")
	}
	return writeJSON(w, http.StatusOK, ValueHistoryResponse{Written: written})
}
