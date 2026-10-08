package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewAccountServiceHandler(accountService{env}, opts...)
	})
}

type accountService struct{ env *Env }

// accountBalances is the four figures the account header card shows, all from
// the same posting list.
type accountBalances struct {
	Balance            domain.Money
	BalanceWithPending domain.Money
	AvailableBalance   domain.Money
	// CreditUsedPct is absent when there is no limit; the UI must not
	// substitute zero.
	CreditUsedPct    domain.Rate
	HasCreditUsedPct bool
}

func (b accountBalances) proto() *agentifiv1.AccountBalances {
	return &agentifiv1.AccountBalances{
		Balance:            moneyProto(b.Balance),
		BalanceWithPending: moneyProto(b.BalanceWithPending),
		AvailableBalance:   moneyProto(b.AvailableBalance),
		CreditUsedPct:      rateProto(b.CreditUsedPct, b.HasCreditUsedPct),
	}
}

func historyStartFor(account store.Account, postings []domain.Posting) *agentifiv1.AccountHistoryStart {
	domainAccount := store.DomainAccount(account)
	return &agentifiv1.AccountHistoryStart{
		StartsOn:          dateOrNil(domain.HistoryStart(domainAccount, postings)),
		AutomaticStartsOn: dateOrNil(domain.AutomaticHistoryStart(domainAccount, postings)),
	}
}

// accountFields are the fields a create and an edit both accept, applied the
// same way by applyAccountFields.
type accountFields struct {
	Currency          Opt[string]
	Description       Opt[string]
	Notes             Opt[string]
	InstitutionID     Opt[uuid.UUID]
	MaskedNumber      Opt[string]
	SortOrder         Opt[int]
	OpeningBalance    Opt[domain.Money]
	OpeningBalanceOn  Opt[Date]
	CreditLimit       Opt[domain.Money]
	StatementCloseDay Opt[int]
	// The statement. SimpleFIN has no field for these, so they are writable.
	// InterestRate is read at either scale; see applyReportedAPR.
	StatementBalance Opt[domain.Money]
	MinimumDue       Opt[domain.Money]
	DueDate          Opt[Date]
	InterestRate     Opt[domain.Rate]

	CustomLogoURL   Opt[string]
	PropertyAddress Opt[string]
	VehicleVIN      Opt[string]
	VehicleMileage  Opt[int]
	MileageAsOf     Opt[Date]
	MilesPerYear    Opt[int]

	// SecuredBy pairs this loan with the asset it is secured on. Cleared
	// unpairs it.
	SecuredBy Opt[uuid.UUID]

	// The two exclusion flags are separate fields so setting one never moves
	// the other.
	ExcludedFromReports      Opt[bool]
	ExcludedFromSpendingPlan Opt[bool]
	ExcludedFromAccountBar   Opt[bool]
	IncludeInNetWorth        Opt[bool]
	ExcludeBankPending       Opt[bool]
	// RequiresReceipts cleared goes back to what the account's type implies.
	RequiresReceipts Opt[bool]
}

// accountFieldsOf reads the shared fields off a create or an update request,
// which name them alike.
func accountFieldsOf(p *patchReader) accountFields {
	return accountFields{
		Currency:                 p.text("currency"),
		Description:              p.text("description"),
		Notes:                    p.text("notes"),
		InstitutionID:            p.id("institution_id"),
		MaskedNumber:             p.text("masked_number"),
		SortOrder:                p.integer("sort_order"),
		OpeningBalance:           p.money("opening_balance"),
		OpeningBalanceOn:         p.date("opening_balance_on"),
		CreditLimit:              p.money("credit_limit"),
		StatementCloseDay:        p.integer("statement_close_day"),
		StatementBalance:         p.money("statement_balance"),
		MinimumDue:               p.money("minimum_due"),
		DueDate:                  p.date("due_date"),
		InterestRate:             p.rate("interest_rate"),
		CustomLogoURL:            p.text("custom_logo_url"),
		PropertyAddress:          p.text("property_address"),
		VehicleVIN:               p.text("vehicle_vin"),
		VehicleMileage:           p.integer("vehicle_mileage"),
		MileageAsOf:              p.date("vehicle_mileage_as_of"),
		MilesPerYear:             p.integer("vehicle_miles_per_year"),
		SecuredBy:                p.id("secured_by_account_id"),
		ExcludedFromReports:      p.flag("excluded_from_reports"),
		ExcludedFromSpendingPlan: p.flag("excluded_from_spending_plan"),
		ExcludedFromAccountBar:   p.flag("excluded_from_account_bar"),
		IncludeInNetWorth:        p.flag("include_in_net_worth"),
		ExcludeBankPending:       p.flag("exclude_bank_pending"),
		RequiresReceipts:         p.flag("requires_receipts"),
	}
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

// statementSources is the bill each account's statement figures came from,
// keyed by the bill.
func statementSources(
	ctx context.Context, env *Env, sp auth.SpaceContext, accounts []store.Account,
) (map[uuid.UUID]*agentifiv1.StatementSource, error) {
	var ids []uuid.UUID
	for _, one := range accounts {
		if one.StatementBillID != uuid.Nil {
			ids = append(ids, one.StatementBillID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sources, err := env.DB.StatementSources(ctx, sp.ID(), ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]*agentifiv1.StatementSource, len(sources))
	for id, source := range sources {
		out[id] = &agentifiv1.StatementSource{
			BillId: source.BillID.String(), Source: source.Source, Provider: source.Provider,
			IssuedOn: dateOrNil(source.IssuedOn), DueOn: source.DueOn.String(),
			FetchedAt: timestamppb.New(source.FetchedAt),
		}
	}
	return out, nil
}

// accountListing is one row of the listing while it is built: equity needs
// every loan's balance before any asset's block can be written.
type accountListing struct {
	account  store.Account
	balances accountBalances
	out      *agentifiv1.ListedAccount
}

func (s accountService) ListAccounts(ctx context.Context, req *agentifiv1.ListAccountsRequest) (*agentifiv1.ListAccountsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	includeClosed := true
	if req.IncludeClosed != nil {
		includeClosed = req.GetIncludeClosed()
	}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(), store.AccountQuery{IncludeClosed: includeClosed})
	if err != nil {
		return nil, err
	}
	byAccount, err := postingsByAccount(ctx, env, sp, accounts)
	if err != nil {
		return nil, err
	}
	reserves, err := goalReserves(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	thresholds, err := institutionThresholds(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	sources, err := statementSources(ctx, env, sp, accounts)
	if err != nil {
		return nil, err
	}

	rows := make([]accountListing, 0, len(accounts))
	for _, account := range accounts {
		balances := balancesFor(account, byAccount[account.ID], reserves[account.ID])
		out := listedFrom(accountProto(account, reserves[account.ID], sources))
		out.Balances = balances.proto()
		out.History = historyStartFor(account, byAccount[account.ID])
		out.HiddenSmallBalance = domain.HiddenForSmallBalance(balances.Balance,
			domain.SmallBalanceThreshold{Amount: account.HideBelowBalance, Set: account.HasHideBelowBalance},
			thresholds[account.InstitutionID])
		rows = append(rows, accountListing{account: account, balances: balances, out: out})
	}
	attachEquity(rows)

	res := &agentifiv1.ListAccountsResponse{Accounts: make([]*agentifiv1.ListedAccount, 0, len(rows))}
	for _, row := range rows {
		res.Accounts = append(res.Accounts, row.out)
	}
	return res, nil
}

// listedFrom is the listing's row over an account's own fields, which the
// two messages declare alike.
func listedFrom(account *agentifiv1.Account) *agentifiv1.ListedAccount {
	out := &agentifiv1.ListedAccount{}
	dst := out.ProtoReflect()
	fields := dst.Descriptor().Fields()
	account.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		dst.Set(fields.ByName(field.Name()), value)
		return true
	})
	return out
}

// attachEquity fills in the equity block on every asset that secures a loan.
// Closed loans are already out of `rows`, so a paid-off mortgage stops
// counting against the house.
func attachEquity(rows []accountListing) {
	loans := map[uuid.UUID][]accountListing{}
	for _, row := range rows {
		if secured := row.account.SecuredByAccountID; secured != uuid.Nil {
			loans[secured] = append(loans[secured], row)
		}
	}
	if len(loans) == 0 {
		return
	}
	for i := range rows {
		secured, ok := loans[rows[i].account.ID]
		if !ok {
			continue
		}
		balances := make([]domain.Money, 0, len(secured))
		ids := make([]string, 0, len(secured))
		owed := domain.Zero
		for _, loan := range secured {
			balances = append(balances, loan.balances.Balance)
			ids = append(ids, loan.account.ID.String())
			owed = owed.Add(loan.balances.Balance.Abs())
		}
		value := rows[i].balances.Balance
		ratio, hasRatio := domain.LoanToValue(value, balances)
		rows[i].out.Equity = &agentifiv1.AccountEquity{
			Value:       moneyProto(value),
			Owed:        moneyProto(owed.Round()),
			Equity:      moneyProto(domain.Equity(value, balances)),
			LoanToValue: rateProto(ratio, hasRatio),
			LoanIds:     ids,
		}
	}
}

func (s accountService) CreateAccount(ctx context.Context, req *agentifiv1.CreateAccountRequest) (*agentifiv1.CreateAccountResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if req.GetName() == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	kind := domain.AccountKind(req.GetKind())
	if err := checkAccountKind(kind); err != nil {
		return nil, err
	}
	if req.GetType() == "" {
		return nil, errInvalid("missing", []string{"body", "type"}, "type is required")
	}
	patch, err := newPatchReader(req)
	if err != nil {
		return nil, err
	}
	fields := accountFieldsOf(patch)
	if patch.err != nil {
		return nil, patch.err
	}

	account := &store.Account{
		Name: req.GetName(),
		Kind: kind,
		Type: req.GetType(),
		// The space's own currency, not a hardcoded dollar.
		Currency:          sp.Space.PrimaryCurrency,
		IncludeInNetWorth: true,
	}
	if account.Currency == "" {
		account.Currency = "USD"
	}
	if fields.OpeningBalance.Present() {
		account.OpeningBalance = fields.OpeningBalance.Value
	}
	if err := applyAccountFields(ctx, env, sp, fields, account); err != nil {
		return nil, err
	}

	if err := env.DB.CreateAccount(ctx, sp.ID(), account); err != nil {
		return nil, err
	}
	out, err := accountReply(ctx, env, sp, *account)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateAccountResponse{Account: out}, nil
}

func (s accountService) GetAccount(ctx context.Context, req *agentifiv1.GetAccountRequest) (*agentifiv1.GetAccountResponse, error) {
	sp := spaceFrom(ctx)
	account, err := liveAccount(ctx, s.env, sp, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	out, err := accountReply(ctx, s.env, sp, account)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetAccountResponse{Account: out}, nil
}

// GetAccountSummary resolves from / to / date_field exactly as the register
// does: an omitted bound is unbounded in both, never "this month" (trap 5).
func (s accountService) GetAccountSummary(ctx context.Context, req *agentifiv1.GetAccountSummaryRequest) (*agentifiv1.GetAccountSummaryResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	account, err := liveAccount(ctx, env, sp, req.GetAccountId())
	if err != nil {
		return nil, err
	}

	postings, _, err := service.LoadPostings(ctx, env.DB, sp.ID(),
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	if err != nil {
		return nil, err
	}
	domainAccount := store.DomainAccount(account)

	inside := make([]domain.Posting, 0, len(postings))
	for _, posting := range postings {
		if domain.CountsTowardBalance(posting) &&
			window.Contains(domain.ReportingDate(posting.Txn, window.Mode)) {
			inside = append(inside, posting)
		}
	}

	reserves, err := goalReserves(ctx, env, sp)
	if err != nil {
		return nil, err
	}

	opening := domainAccount.OpeningBalance
	if before, bounded := window.DayBeforeStart(); bounded {
		opening = domain.LedgerBalanceAsOf(domainAccount, postings, before, window.Mode)
	} else if !domainAccount.IsManual() {
		if opening, err = domain.OpeningBalanceForConnected(domainAccount, postings); err != nil {
			return nil, err
		}
	}

	ending := domain.AccountBalance(domainAccount, postings)
	if window.HasTo {
		ending = domain.LedgerBalanceAsOf(domainAccount, postings, window.To, window.Mode)
	}

	return &agentifiv1.GetAccountSummaryResponse{
		AccountId:      account.ID.String(),
		Window:         windowProto(window),
		OpeningBalance: moneyProto(opening),
		EndingBalance:  moneyProto(ending),
		Total:          moneyProto(domain.LedgerTotal(inside)),
		Count:          int32(len(inside)),
		Balances:       balancesFor(account, postings, reserves[account.ID]).proto(),
	}, nil
}

func (s accountService) UpdateAccount(ctx context.Context, req *agentifiv1.UpdateAccountRequest) (*agentifiv1.UpdateAccountResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	account, err := liveAccount(ctx, env, sp, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	patch, err := newPatchReader(req)
	if err != nil {
		return nil, err
	}
	fields := accountFieldsOf(patch)
	name, kind, typ := patch.text("name"), patch.text("kind"), patch.text("type")
	isClosed, closedOn := patch.flag("is_closed"), patch.date("closed_on")
	historyStartsOn := patch.date("history_starts_on")
	hideBelow := patch.money("hide_below_balance")
	registerTab := patch.text("default_register_tab")
	if patch.err != nil {
		return nil, patch.err
	}

	// The running balance is anchored at the opening balance or the re-signed
	// provider balance, so moving either means recomputing.
	balanceAnchorMoved := false

	// Every card row was stamped against the statement cycle as it stood, so
	// moving the cycle means restamping the history.
	cycleBefore := cardCycle(account)

	if err := applyRequired("name", name, &account.Name); err != nil {
		return nil, err
	}
	if kind.Set {
		if kind.Null {
			return nil, errConflict("kind cannot be cleared")
		}
		before, next := account.Kind, domain.AccountKind(kind.Value)
		if err := checkAccountKind(next); err != nil {
			return nil, err
		}
		resignProviderBalance(&account, next)
		account.Kind = next
		balanceAnchorMoved = balanceAnchorMoved || before != account.Kind
	}
	if err := applyRequired("type", typ, &account.Type); err != nil {
		return nil, err
	}
	applyNullable(fields.OpeningBalance, &account.OpeningBalance)
	balanceAnchorMoved = balanceAnchorMoved || fields.OpeningBalance.Set
	openingOnBefore := account.OpeningBalanceOn
	lookupBefore := valuationLookupOf(account)
	if err := applyAccountFields(ctx, env, sp, fields, &account); err != nil {
		return nil, err
	}
	if valuationLookupOf(account) != lookupBefore {
		// The last estimate was for another address, car or odometer.
		account.ValuedAt = nil
	}
	if fields.OpeningBalanceOn.Set && account.OpeningBalanceOn != openingOnBefore {
		balanceAnchorMoved = true
	}
	if fields.StatementBalance.Set || fields.MinimumDue.Set || fields.DueDate.Set {
		// A figure typed over a bill's copy is the person's now: later bills for
		// the cycle leave it alone.
		account.StatementBillID = uuid.Nil
	}
	if err := applyRequired("is_closed", isClosed, &account.IsClosed); err != nil {
		return nil, err
	}
	applyNullable(closedOn, (*Date)(&account.ClosedOn))
	if err := checkHideBelow(hideBelow); err != nil {
		return nil, err
	}
	applyNullableMoney(hideBelow, &account.HideBelowBalance, &account.HasHideBelowBalance)
	if err := checkRegisterTab(registerTab); err != nil {
		return nil, err
	}
	applyNullable(registerTab, &account.DefaultRegisterTab)
	historyStartBefore := account.HistoryStartsOn
	applyNullable(historyStartsOn, (*Date)(&account.HistoryStartsOn))
	historyMoved := account.HistoryStartsOn != historyStartBefore ||
		account.OpeningBalanceOn != openingOnBefore

	// One transaction: a retry would see the cycle already changed and never
	// restamp.
	err = env.DB.InTx(ctx, func(tx *store.Store) error {
		if err := tx.UpdateAccount(ctx, sp.ID(), &account); err != nil {
			return err
		}
		if cardCycle(account) != cycleBefore {
			if _, err := service.NewCreditCards(tx).Restamp(ctx, sp.ID(), account.ID, nil); err != nil {
				return err
			}
		}
		if historyMoved {
			if err := tx.RebuildAccountHistory(ctx, sp.ID(), account.ID, domain.DateOf(env.now())); err != nil {
				return err
			}
		}
		if balanceAnchorMoved {
			return service.RecomputeRunningBalances(ctx, tx, sp.ID(), account.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := accountReply(ctx, env, sp, account)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateAccountResponse{Account: out}, nil
}

// applyAccountFields applies the fields a create and an update share, except
// the opening balance, which the two treat differently.
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

// accountReply is the answer every single-account read and write gives.
func accountReply(ctx context.Context, env *Env, sp auth.SpaceContext, account store.Account) (*agentifiv1.Account, error) {
	reserves, err := goalReserves(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	sources, err := statementSources(ctx, env, sp, []store.Account{account})
	if err != nil {
		return nil, err
	}
	return accountProto(account, reserves[account.ID], sources), nil
}

func (s accountService) DeleteAccount(ctx context.Context, req *agentifiv1.DeleteAccountRequest) (*agentifiv1.DeleteAccountResponse, error) {
	sp := spaceFrom(ctx)
	account, err := liveAccount(ctx, s.env, sp, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteAccount(ctx, sp.ID(), account.ID); err != nil {
		return nil, notFoundAs(err, "Account")
	}
	return &agentifiv1.DeleteAccountResponse{}, nil
}

// liveAccount reads the account an id names, treating a soft-deleted one and
// one in another space as the same 404.
func liveAccount(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Account, error) {
	id, err := idFrom(rawID, "Account")
	if err != nil {
		return store.Account{}, err
	}
	account, err := env.DB.GetAccount(ctx, sp.ID(), id)
	if err != nil {
		return store.Account{}, notFoundAs(err, "Account")
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
) accountBalances {
	domainAccount := store.DomainAccount(account)
	// The reserve is derived rather than read off the row; see goalReserves.
	domainAccount.GoalBalance = reserved
	balance := domain.AccountBalance(domainAccount, postings)
	pct, hasLimit := domain.CreditUsedPct(domainAccount, balance)
	return accountBalances{
		Balance:            balance,
		BalanceWithPending: domain.BalanceWithPending(domainAccount, postings),
		AvailableBalance:   domain.AvailableBalance(domainAccount, postings),
		CreditUsedPct:      pct,
		HasCreditUsedPct:   hasLimit,
	}
}

// accountProto renders one account. `reserved` comes from goalReserves, not
// the stored goal_balance column; sources from statementSources.
func accountProto(
	a store.Account, reserved domain.Money, sources map[uuid.UUID]*agentifiv1.StatementSource,
) *agentifiv1.Account {
	return &agentifiv1.Account{
		Id:                       a.ID.String(),
		Name:                     a.Name,
		Description:              dbconv.NullText(a.Description),
		Notes:                    dbconv.NullText(a.Notes),
		Kind:                     string(a.Kind),
		Type:                     a.Type,
		Currency:                 a.Currency,
		InstitutionId:            idOrNil(a.InstitutionID),
		ConnectionId:             idOrNil(a.ConnectionID),
		MaskedNumber:             dbconv.NullText(a.MaskedNumber),
		LogoUrl:                  dbconv.NullText(displayLogo(a)),
		CustomLogoUrl:            dbconv.NullText(a.CustomLogoURL),
		SortOrder:                int32(a.SortOrder),
		ProviderBalance:          nullableMoneyProto(a.ProviderBalance, a.HasProviderBalance),
		ProviderBalanceAt:        timestampOrNil(a.ProviderBalanceAt),
		WithheldBalance:          nullableMoneyProto(a.WithheldBalance, a.HasWithheldBalance),
		WithheldBalanceAt:        timestampOrNil(a.WithheldBalanceAt),
		WithheldBalanceReason:    a.WithheldBalanceReason,
		AcceptZeroBalance:        a.AcceptZeroBalance,
		OpeningBalance:           moneyProto(a.OpeningBalance),
		OpeningBalanceOn:         dateOrNil(a.OpeningBalanceOn),
		GoalBalance:              moneyProto(reserved),
		PendingHolds:             moneyProto(a.PendingHolds),
		CreditLimit:              nullableMoneyProto(a.CreditLimit, a.HasCreditLimit),
		StatementBalance:         nullableMoneyProto(a.StatementBalance, a.HasStatementBalance),
		MinimumDue:               nullableMoneyProto(a.MinimumDue, a.HasMinimumDue),
		DueDate:                  dateOrNil(a.DueDate),
		InterestRate:             rateProto(a.InterestRate, a.HasInterestRate),
		StatementCloseDay:        closeDayProto(a.StatementCloseDay),
		StatementSource:          sources[a.StatementBillID],
		PropertyAddress:          dbconv.NullText(a.PropertyAddress),
		VehicleVin:               dbconv.NullText(a.VehicleVIN),
		VehicleMileage:           int32If(a.VehicleMileage, a.HasVehicleMileage),
		VehicleMileageAsOf:       dateOrNil(a.MileageAsOf),
		VehicleMilesPerYear:      int32If(a.MilesPerYear, a.HasMilesPerYear),
		ValuationSource:          dbconv.NullText(a.ValuationSource),
		ValuedAt:                 timestampOrNil(a.ValuedAt),
		SecuredByAccountId:       idOrNil(a.SecuredByAccountID),
		ExcludedFromReports:      a.ExcludedFromReports,
		ExcludedFromSpendingPlan: a.ExcludedFromSpendingPlan,
		ExcludedFromAccountBar:   a.ExcludedFromAccountBar,
		IncludeInNetWorth:        a.IncludeInNetWorth,
		ExcludeBankPending:       a.ExcludeBankPending,
		RequiresReceipts:         store.DomainAccount(a).RequiresReceipts,
		IsClosed:                 a.IsClosed,
		ClosedOn:                 dateOrNil(a.ClosedOn),
		SimplefinAccountId:       dbconv.NullText(a.SimpleFINAccountID),
		SyncFloorOn:              dateOrNil(a.SyncFloorOn),
		HistoryStartsOn:          dateOrNil(a.HistoryStartsOn),
		HideBelowBalance:         nullableMoneyProto(a.HideBelowBalance, a.HasHideBelowBalance),
		DefaultRegisterTab:       dbconv.NullText(a.DefaultRegisterTab),
		ProviderExtra:            providerExtraProto(a.ProviderExtra),
	}
}

// providerExtraProto is the feed's leftover fields as arbitrary JSON. What
// does not parse is left out: it is evidence for a person, not an input.
func providerExtraProto(raw []byte) *structpb.Value {
	if len(raw) == 0 {
		return nil
	}
	var value structpb.Value
	if err := protojson.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return &value
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

func closeDayProto(day *int16) *int32 {
	if day == nil {
		return nil
	}
	return proto.Int32(int32(*day))
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

// Re-pricing real estate and vehicles from an outside estimate. A write, since
// it posts a balance adjustment.

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

func (s accountService) ListValuationSources(context.Context, *agentifiv1.ListValuationSourcesRequest) (*agentifiv1.ListValuationSourcesResponse, error) {
	if !s.env.browserEngine().HasFirefox() {
		return valuationSourcesProto(nil), nil
	}
	return valuationSourcesProto(assetValuers(s.env)), nil
}

// valuationSourcesProto reports which asset types the valuers can price.
func valuationSourcesProto(valuers map[string]provider.AssetValuationProvider) *agentifiv1.ListValuationSourcesResponse {
	configured := make([]string, 0, len(valuers))
	for _, assetType := range provider.ValuationAssetTypes() {
		if _, ok := valuers[assetType]; ok {
			configured = append(configured, assetType)
		}
	}
	return &agentifiv1.ListValuationSourcesResponse{
		AssetTypes: provider.ValuationAssetTypes(),
		Configured: configured,
	}
}

func (s accountService) RevalueAssets(ctx context.Context, req *agentifiv1.RevalueAssetsRequest) (*agentifiv1.RevalueAssetsResponse, error) {
	results, err := NewAssetValuation(s.env).RevalueAll(ctx, spaceFrom(ctx).ID(), req.GetForce())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.RevalueAssetsResponse{Results: valuationResults(results)}, nil
}

func (s accountService) RevalueAsset(ctx context.Context, req *agentifiv1.RevalueAssetRequest) (*agentifiv1.RevalueAssetResponse, error) {
	id, err := idFrom(req.GetAccountId(), "Account")
	if err != nil {
		return nil, err
	}
	result, err := NewAssetValuation(s.env).Revalue(ctx, spaceFrom(ctx).ID(), id)
	if err != nil {
		return nil, notFoundAs(err, "Account")
	}
	return &agentifiv1.RevalueAssetResponse{Results: valuationResults([]service.ValuationResult{result})}, nil
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

func valuationResults(results []service.ValuationResult) []*agentifiv1.ValuationResult {
	out := make([]*agentifiv1.ValuationResult, 0, len(results))
	for _, result := range results {
		row := &agentifiv1.ValuationResult{
			AccountId:   result.AccountID.String(),
			Name:        result.Name,
			Skipped:     dbconv.NullText(result.Skipped),
			Source:      dbconv.NullText(result.Source),
			Estimate:    nullableMoneyProto(result.Estimate, result.HasEstimate),
			Adjustment:  nullableMoneyProto(result.Adjustment, result.HasAdjust),
			MileageUsed: int32If(result.MileageUsed, result.HasMileage),
		}
		if priced := result.Priced; priced != nil {
			row.PricedAs = &agentifiv1.ValuationPricedAs{
				Year:           dbconv.NullText(priced.Year),
				Make:           dbconv.NullText(priced.Make),
				Model:          dbconv.NullText(priced.Model),
				Trim:           dbconv.NullText(priced.Trim),
				Mileage:        int32If(priced.Mileage, priced.HasMileage),
				TypicalMileage: priced.TypicalMileage,
				Address:        dbconv.NullText(priced.Address),
				Low:            nullableMoneyProto(priced.Low, priced.HasRange),
				High:           nullableMoneyProto(priced.High, priced.HasRange),
			}
		}
		out = append(out, row)
	}
	return out
}

func (s accountService) ImportValueHistory(ctx context.Context, req *agentifiv1.ImportValueHistoryRequest) (*agentifiv1.ImportValueHistoryResponse, error) {
	id, err := idFrom(req.GetAccountId(), "Account")
	if err != nil {
		return nil, err
	}
	if len(req.GetPoints()) == 0 {
		return nil, errInvalid("missing", []string{"body", "points"}, "at least one point is required")
	}

	points := make([]domain.ValuePoint, 0, len(req.GetPoints()))
	for i, point := range req.GetPoints() {
		at := []string{"body", "points", strconv.Itoa(i)}
		if point.GetOn() == "" {
			return nil, errInvalid("missing", append(at, "on"), "every point needs a date")
		}
		on, err := parseDate(point.GetOn())
		if err != nil {
			return nil, errInvalid("date_parsing", append(at, "on"), "%s", err)
		}
		value := domain.Zero
		if point.GetValue() != nil {
			if value, err = moneyFrom(point.GetValue(), append(at, "value")...); err != nil {
				return nil, err
			}
		}
		points = append(points, domain.ValuePoint{On: on, Value: value})
	}

	written, err := NewAssetValuation(s.env).ImportHistory(ctx, spaceFrom(ctx).ID(), id, points)
	if err != nil {
		return nil, notFoundAs(err, "Account")
	}
	return &agentifiv1.ImportValueHistoryResponse{Written: int32(written)}, nil
}

// ----- the wire --------------------------------------------------------------

func idOrNil(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	return proto.String(id.String())
}

// dateOrNil is the zero date as an unset field.
func dateOrNil(d domain.Date) *string {
	if d.IsZero() {
		return nil
	}
	return proto.String(d.String())
}

func timestampOrNil(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func int32If(value int, has bool) *int32 {
	if !has {
		return nil
	}
	return proto.Int32(int32(value))
}

// patchReader reads a create or update request's optional fields as Opt
// values, through maskOf, by field name, so two requests that declare the same
// fields share one reading. The first malformed value is kept in err, with the
// field it was sent in.
type patchReader struct {
	message protoreflect.Message
	mask    patchMask
	err     error
}

func newPatchReader(req proto.Message) (*patchReader, error) {
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	return &patchReader{message: req.ProtoReflect(), mask: mask}, nil
}

// field is the named field's value, whether the request names it, and whether
// it is cleared.
func (p *patchReader) field(name string) (protoreflect.Value, bool, bool) {
	if !p.mask[name] {
		return protoreflect.Value{}, false, false
	}
	field := p.message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if !p.message.Has(field) {
		return protoreflect.Value{}, true, true
	}
	return p.message.Get(field), true, false
}

func readOpt[T any](p *patchReader, name string, convert func(protoreflect.Value) (T, error)) Opt[T] {
	value, set, null := p.field(name)
	if !set || null {
		return Opt[T]{Set: set, Null: null}
	}
	converted, err := convert(value)
	if err != nil {
		if p.err == nil {
			p.err = err
		}
		return Opt[T]{}
	}
	return Opt[T]{Set: true, Value: converted}
}

func (p *patchReader) text(name string) Opt[string] {
	return readOpt(p, name, func(v protoreflect.Value) (string, error) { return v.String(), nil })
}

func (p *patchReader) flag(name string) Opt[bool] {
	return readOpt(p, name, func(v protoreflect.Value) (bool, error) { return v.Bool(), nil })
}

func (p *patchReader) integer(name string) Opt[int] {
	return readOpt(p, name, func(v protoreflect.Value) (int, error) { return int(v.Int()), nil })
}

func (p *patchReader) id(name string) Opt[uuid.UUID] {
	return readOpt(p, name, func(v protoreflect.Value) (uuid.UUID, error) {
		id, err := uuid.Parse(v.String())
		if err != nil {
			return uuid.Nil, errInvalid("uuid_parsing", []string{"body", name}, "%s must be a uuid", name)
		}
		return id, nil
	})
}

func (p *patchReader) date(name string) Opt[Date] {
	return readOpt(p, name, func(v protoreflect.Value) (Date, error) {
		on, err := parseDate(v.String())
		if err != nil {
			return Date{}, errInvalid("date_parsing", []string{"body", name}, "%s", err)
		}
		return Date(on), nil
	})
}

func (p *patchReader) rate(name string) Opt[domain.Rate] {
	return readOpt(p, name, func(v protoreflect.Value) (domain.Rate, error) {
		rate, err := decimal.NewFromString(v.String())
		if err != nil {
			return domain.Rate{}, errInvalid("decimal_parsing", []string{"body", name},
				"%s must be a decimal number", name)
		}
		return rate, nil
	})
}

func (p *patchReader) money(name string) Opt[domain.Money] {
	return readOpt(p, name, func(v protoreflect.Value) (domain.Money, error) {
		amount, ok := v.Message().Interface().(interface{ GetAmount() string })
		if !ok {
			panic("api: patchReader.money on " + name + ", which is not an amount")
		}
		return moneyFrom(amount, "body", name)
	})
}
