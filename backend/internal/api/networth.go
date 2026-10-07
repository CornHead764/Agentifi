package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Net worth: one point series, one group breakdown, one ratio
// (calculations.md §4).
//
//   - Holdings are never added on top of a balance; net worth sums account
//     balances only. The mirror rule is investments.go's portfolioValue.
//   - Group rows are windowed: the balance at the window's start and end, the
//     difference, and the percentage relative to the start.
//   - A percentage against a zero start is null. The client renders an em
//     dash.
//
// One endpoint, because the headline, group rows and chart are the same two
// snapshots read three ways and must share one resolved window.

func init() {
	Register(Resource{Prefix: "/net-worth", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", readNetWorth)
	}})
}

// maxNetWorthPoints bounds one response. The granularity coarsens rather than
// the series being truncated.
const maxNetWorthPoints = 800

// NetWorthPoint is one day on the chart.
type NetWorthPoint struct {
	On     Date         `json:"on"`
	Assets domain.Money `json:"assets"`
	// Debt is reported positive even though balances are stored negative.
	Debt domain.Money `json:"debt"`
	Net  domain.Money `json:"net"`

	// ByKind is each account kind's contribution on this day, so the by-type
	// views are drawn from history. Debt kinds are positive, matching Debt.
	// Every point carries the same kinds in the same order, so a chart line
	// has no holes.
	ByKind []NetWorthKindAmount `json:"by_kind"`

	// Equity is domain.EquityAt on this day: every asset less the loans
	// secured on it.
	Equity domain.Money `json:"equity"`
}

// NetWorthKindAmount is one account kind's share of a day.
type NetWorthKindAmount struct {
	Kind   domain.AccountKind `json:"kind"`
	Amount domain.Money       `json:"amount"`
}

// NetWorthAccountRow is one account inside a group, over the window.
type NetWorthAccountRow struct {
	AccountID uuid.UUID          `json:"account_id"`
	Name      string             `json:"name"`
	Kind      domain.AccountKind `json:"kind"`
	Type      string             `json:"type"`
	IsClosed  bool               `json:"is_closed"`

	Start  domain.Money `json:"start"`
	End    domain.Money `json:"end"`
	Change domain.Money `json:"change"`
	// ChangePct is null when the window opened at zero.
	ChangePct *domain.Rate `json:"change_pct"`
}

// NetWorthGroupRow is one row of the accounts panel.
type NetWorthGroupRow struct {
	// Kind is the arithmetic classification; Class is the account picker's
	// label ("banking", "credit", "investments", "asset", "liability"). Side is
	// "asset" or "debt".
	Kind  domain.AccountKind `json:"kind"`
	Class string             `json:"class"`
	Side  string             `json:"side"`

	AccountCount int          `json:"account_count"`
	Start        domain.Money `json:"start"`
	End          domain.Money `json:"end"`
	Change       domain.Money `json:"change"`
	ChangePct    *domain.Rate `json:"change_pct"`

	Accounts []NetWorthAccountRow `json:"accounts"`
}

// NetWorthResponse is the whole page.
type NetWorthResponse struct {
	Window WindowResponse `json:"window"`
	// Granularity is how the point series was sampled: day, week or month.
	Granularity string          `json:"granularity"`
	Points      []NetWorthPoint `json:"points"`

	Start  NetWorthPoint `json:"start"`
	End    NetWorthPoint `json:"end"`
	Change domain.Money  `json:"change"`
	// ChangePct is null when the window opened at a net worth of zero.
	ChangePct *domain.Rate `json:"change_pct"`
	// DebtToAsset is |debt| / assets at the window's end, null when there are
	// no assets to divide by.
	DebtToAsset *domain.Rate `json:"debt_to_asset"`

	Groups []NetWorthGroupRow `json:"groups"`
	// IncludedAccounts and TotalAccounts back the "N / N Accounts Included"
	// chip. Neither counts a closed account, whose balance still counts, so
	// the chip agrees with the register header.
	IncludedAccounts int `json:"included_accounts"`
	TotalAccounts    int `json:"total_accounts"`
	// UnconvertedCurrencies names the currencies held here with no exchange
	// rate. Their balances are counted at face value, so the total is
	// approximate.
	UnconvertedCurrencies []string `json:"unconverted_currencies"`
}

func readNetWorth(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	requested, err := granularityFromRequest(r)
	if err != nil {
		return err
	}

	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(),
		store.AccountQuery{IncludeClosed: true})
	if err != nil {
		return err
	}
	series, err := newBalanceSeries(r.Context(), env, sp, accounts, window, requested)
	if err != nil {
		return err
	}

	// Every figure below is in the space's primary currency.
	converter, err := newBalanceConverter(r.Context(), env, sp, accounts, series.end)
	if err != nil {
		return err
	}

	startBalances := converter.apply(series.on(series.start))
	endBalances := converter.apply(series.on(series.end))
	domainAccounts := store.DomainAccounts(accounts)

	points := make([]NetWorthPoint, 0, len(series.samples))
	for _, on := range series.samples {
		balances := converter.apply(series.on(on))
		points = append(points, netWorthPoint(
			domain.NetWorthAt(domainAccounts, balances, on),
			netWorthByKind(domainAccounts, balances),
			domain.EquityAt(domainAccounts, balances)))
	}

	start := domain.NetWorthAt(domainAccounts, startBalances, series.start)
	end := domain.NetWorthAt(domainAccounts, endBalances, series.end)
	changePct, hasChangePct := domain.NetWorthChangePct(start, end)
	ratio, hasRatio := end.DebtToAsset()

	// Closed accounts are in neither count (see IncludedAccounts).
	included, total := 0, 0
	for _, account := range domainAccounts {
		if account.IsClosed {
			continue
		}
		total++
		if account.CountsInNetWorth() {
			included++
		}
	}

	return writeJSON(w, http.StatusOK, NetWorthResponse{
		Window:      windowResponse(window),
		Granularity: series.granularity,
		Points:      points,
		Start: netWorthPoint(start, netWorthByKind(domainAccounts, startBalances),
			domain.EquityAt(domainAccounts, startBalances)),
		End: netWorthPoint(end, netWorthByKind(domainAccounts, endBalances),
			domain.EquityAt(domainAccounts, endBalances)),
		Change:           domain.NetWorthChange(start, end),
		ChangePct:        store.PtrIf(changePct, hasChangePct),
		DebtToAsset:      store.PtrIf(ratio, hasRatio),
		Groups:           netWorthGroups(accounts, startBalances, endBalances),
		IncludedAccounts: included,
		TotalAccounts:    total,
		// Empty rather than null, so the client tests a length and never a
		// nullable array.
		UnconvertedCurrencies: store.NonNil(converter.Unconverted),
	})
}

// netWorthPoint takes its kinds and its equity so a point cannot be built
// without them.
func netWorthPoint(n domain.NetWorth, byKind []NetWorthKindAmount, equity domain.Money) NetWorthPoint {
	return NetWorthPoint{
		On: Date(n.On), Assets: n.Assets, Debt: n.Debt, Net: n.Net,
		ByKind: byKind, Equity: equity,
	}
}

// netWorthKindOrder is assets before debt, and a stable order inside each side,
// so neither the panel nor a stacked chart reshuffles between renders.
var netWorthKindOrder = []domain.AccountKind{
	domain.KindCash, domain.KindInvestment, domain.KindAsset,
	domain.KindCreditCard, domain.KindLoan,
}

// netWorthGroups rolls the accounts up by kind and expands each group. Group
// figures come from domain.GroupChanges, not from summing the rows below, so a
// subtotal cannot disagree with its children.
func netWorthGroups(
	accounts []store.Account,
	startBalances, endBalances map[domain.ID]domain.Money,
) []NetWorthGroupRow {
	domainAccounts := store.DomainAccounts(accounts)
	totals := make(map[string]domain.GroupChange, len(accounts))
	for _, group := range domain.GroupChanges(domainAccounts, startBalances, endBalances, "") {
		totals[group.Key] = group
	}

	members := map[domain.AccountKind][]NetWorthAccountRow{}
	for _, account := range accounts {
		if !store.DomainAccount(account).CountsInNetWorth() {
			continue
		}
		id := domain.ID(account.ID.String())
		row := domain.GroupChange{Start: startBalances[id], End: endBalances[id]}
		pct, hasPct := row.ChangePct()
		members[account.Kind] = append(members[account.Kind], NetWorthAccountRow{
			AccountID: account.ID,
			Name:      account.Name,
			Kind:      account.Kind,
			Type:      account.Type,
			IsClosed:  account.IsClosed,
			Start:     row.Start.Round(),
			End:       row.End.Round(),
			Change:    row.Change(),
			ChangePct: store.PtrIf(pct, hasPct),
		})
	}

	out := make([]NetWorthGroupRow, 0, len(members))
	for _, kind := range netWorthKindOrder {
		rows, present := members[kind]
		if !present {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		group := totals[string(kind)]
		pct, hasPct := group.ChangePct()
		out = append(out, NetWorthGroupRow{
			Kind:         kind,
			Class:        accountClass(kind),
			Side:         accountSide(kind),
			AccountCount: len(rows),
			Start:        group.Start,
			End:          group.End,
			Change:       group.Change(),
			ChangePct:    store.PtrIf(pct, hasPct),
			Accounts:     rows,
		})
	}
	return out
}

// netWorthByKind splits one day's balances the way the group rows split the
// window. Only accounts flagged into net worth count, and a debt kind is
// positive, as in NetWorthPoint.Debt.
func netWorthByKind(
	accounts []domain.Account, balances map[domain.ID]domain.Money,
) []NetWorthKindAmount {
	totals := map[domain.AccountKind]domain.Money{}
	for _, account := range accounts {
		if !account.CountsInNetWorth() {
			continue
		}
		balance := balances[account.ID]
		if account.Kind.IsDebt() {
			balance = balance.Neg()
		}
		totals[account.Kind] = totals[account.Kind].Add(balance)
	}

	out := make([]NetWorthKindAmount, 0, len(totals))
	for _, kind := range netWorthKindOrder {
		total, held := totals[kind]
		if !held {
			continue
		}
		out = append(out, NetWorthKindAmount{Kind: kind, Amount: total.Round()})
	}
	return out
}

// accountClass is the taxonomy class from the "Add manual account" type picker:
// what a person reads, where Kind is what the arithmetic switches on.
func accountClass(kind domain.AccountKind) string {
	switch kind {
	case domain.KindCash:
		return "banking"
	case domain.KindCreditCard:
		return "credit"
	case domain.KindInvestment:
		return "investments"
	case domain.KindAsset:
		return "asset"
	case domain.KindLoan:
		return "liability"
	default:
		return "other"
	}
}

func accountSide(kind domain.AccountKind) string {
	if kind.IsDebt() {
		return "debt"
	}
	return "asset"
}

// --- The balance series ------------------------------------------------------

// balanceSeries is every account's balance on every sampled day of a window.
//
// The materialized daily history is the source where an account has one
// (domain.BalancesOn); otherwise its ledger is walked with domain.BalanceAsOf,
// the same figure by a slower route. The two are never added together.
//
// Snapshots are built in store.BalanceHistoryMode, so they are consulted only
// when the request's date_field matches; otherwise every account is walked.
type balanceSeries struct {
	accounts []store.Account
	history  []domain.BalancePoint
	postings map[uuid.UUID][]domain.Posting
	mode     domain.DateMode

	start       domain.Date
	end         domain.Date
	granularity string
	samples     []domain.Date

	cached map[domain.Date]map[domain.ID]domain.Money
}

func newBalanceSeries(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	accounts []store.Account, window Window, requested string,
) (*balanceSeries, error) {
	postings, err := postingsByAccount(ctx, env, sp, accounts)
	if err != nil {
		return nil, err
	}
	end := window.To
	if !window.HasTo {
		end = domain.DateOf(env.now())
	}
	history, err := loadBalanceHistory(ctx, env, sp, accounts, postings, end)
	if err != nil {
		return nil, err
	}
	// A stored point before its account's history start is for a day the
	// account held nothing; BalanceAsOf says zero there too.
	starts := make(map[domain.ID]domain.Date, len(accounts))
	for _, account := range accounts {
		starts[domain.ID(account.ID.String())] = domain.HistoryStart(
			store.DomainAccount(account), postings[account.ID])
	}
	history = domain.HistoryWithin(history, starts)

	start := window.From
	if !window.HasFrom {
		// An open start is resolved against the data ("as far back as there is
		// anything"); the response still echoes the window that was asked for.
		start = earliestKnownDay(history, postings, end)
	}
	if start.After(end) {
		start = end
	}

	granularity, samples := resolveSampling(start, end, requested)
	return &balanceSeries{
		accounts:    accounts,
		history:     history,
		postings:    postings,
		mode:        window.Mode,
		start:       start,
		end:         end,
		granularity: granularity,
		samples:     samples,
		cached:      map[domain.Date]map[domain.ID]domain.Money{},
	}, nil
}

// on is every included account's balance at the end of a day.
func (s *balanceSeries) on(day domain.Date) map[domain.ID]domain.Money {
	if cached, hit := s.cached[day]; hit {
		return cached
	}
	var materialized map[domain.ID]domain.Money
	if s.mode == store.BalanceHistoryMode {
		materialized = domain.BalancesOn(s.history, day)
	}
	out := make(map[domain.ID]domain.Money, len(s.accounts))
	for _, account := range s.accounts {
		id := domain.ID(account.ID.String())
		if balance, known := materialized[id]; known {
			out[id] = balance
			continue
		}
		out[id] = domain.BalanceAsOf(
			store.DomainAccount(account), s.postings[account.ID], day, s.mode)
	}
	s.cached[day] = out
	return out
}

// loadBalanceHistory reads the materialized daily balances, re-derived over the
// ledgers as they stand (domain.RederiveHistory), so a figure written before a
// later edit or a reading the feed dropped is not what the chart shows.
func loadBalanceHistory(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	accounts []store.Account, postings map[uuid.UUID][]domain.Posting, through domain.Date,
) ([]domain.BalancePoint, error) {
	stored, err := env.DB.ListAllBalanceHistory(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	domainAccounts := make([]domain.Account, 0, len(accounts))
	ledgers := make(map[domain.ID][]domain.Posting, len(accounts))
	for _, account := range accounts {
		domainAccounts = append(domainAccounts, store.DomainAccount(account))
		ledgers[domain.ID(account.ID.String())] = postings[account.ID]
	}
	history := domain.RederiveHistory(domainAccounts, ledgers, stored)
	within := history[:0]
	for _, point := range history {
		if !point.On.After(through) {
			within = append(within, point)
		}
	}
	return within, nil
}

// earliestKnownDay is the first day the space has anything on file, which is
// where an open-ended window starts.
func earliestKnownDay(
	history []domain.BalancePoint, postings map[uuid.UUID][]domain.Posting, fallback domain.Date,
) domain.Date {
	earliest := fallback
	for _, point := range history {
		if point.On.Before(earliest) {
			earliest = point.On
		}
	}
	for _, rows := range postings {
		for _, posting := range rows {
			if posting.Txn.Date.Before(earliest) {
				earliest = posting.Txn.Date
			}
		}
	}
	return earliest
}

// --- Sampling ----------------------------------------------------------------

func granularityFromRequest(r *http.Request) (string, error) {
	switch raw := r.URL.Query().Get("granularity"); raw {
	case "", "auto":
		return "auto", nil
	case "day", "week", "month":
		return raw, nil
	default:
		return "", errInvalid("enum", []string{"query", "granularity"},
			"granularity must be day, week, month or auto, got %q", raw)
	}
}

// resolveSampling picks the step and the days, coarsening until the series
// fits rather than truncating it.
func resolveSampling(start, end domain.Date, requested string) (string, []domain.Date) {
	steps := []string{"day", "week", "month"}
	first := 0
	if requested != "auto" {
		for i, step := range steps {
			if step == requested {
				first = i
			}
		}
	} else {
		switch days := len(domain.DateRange(start, end)); {
		case days <= 62:
			first = 0
		case days <= 400:
			first = 1
		default:
			first = 2
		}
	}

	for index := first; index < len(steps); index++ {
		samples := sampleDates(start, end, steps[index])
		if len(samples) <= maxNetWorthPoints || index == len(steps)-1 {
			return steps[index], capSamples(samples)
		}
	}
	return steps[len(steps)-1], capSamples(sampleDates(start, end, steps[len(steps)-1]))
}

// capSamples is the last resort, once the coarsest step is still too long. It
// stays out of sampleDates: truncating there would stop resolveSampling from
// coarsening, and the chart would lose its first years.
func capSamples(samples []domain.Date) []domain.Date {
	if len(samples) <= maxNetWorthPoints {
		return samples
	}
	return samples[len(samples)-maxNetWorthPoints:]
}

// sampleDates is the days a chart is drawn at, the window's own ends always
// included so the headline and the chart agree at both edges.
func sampleDates(start, end domain.Date, granularity string) []domain.Date {
	seen := map[domain.Date]bool{start: true}
	out := []domain.Date{start}
	add := func(on domain.Date) {
		if on.Before(start) || on.After(end) || seen[on] {
			return
		}
		seen[on] = true
		out = append(out, on)
	}

	switch granularity {
	case "day":
		for _, on := range domain.DateRange(start, end) {
			add(on)
		}
	case "week":
		for on := start; !on.After(end); on = on.AddDays(7) {
			add(on)
		}
	default:
		for month := domain.MonthOf(start); !month.After(domain.MonthOf(end)); month = month.Next() {
			add(month.LastDay())
		}
	}
	add(end)

	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
