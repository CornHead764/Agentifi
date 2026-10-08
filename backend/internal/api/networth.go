package api

import (
	"context"
	"net/http"
	"sort"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewNetWorthServiceHandler(netWorthService{env}, opts...)
	})
}

type netWorthService struct{ env *Env }

// maxNetWorthPoints bounds one response. The granularity coarsens rather than
// the series being truncated.
const maxNetWorthPoints = 800

func (s netWorthService) GetNetWorth(
	ctx context.Context, req *agentifiv1.GetNetWorthRequest,
) (*agentifiv1.GetNetWorthResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	requested, err := parseGranularity(req.GetGranularity())
	if err != nil {
		return nil, err
	}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(), store.AccountQuery{IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	series, err := newBalanceSeries(ctx, env, sp, accounts, window, requested)
	if err != nil {
		return nil, err
	}

	// Every figure below is in the space's primary currency.
	converter, err := newBalanceConverter(ctx, env, sp, accounts, series.end)
	if err != nil {
		return nil, err
	}

	startBalances := converter.apply(series.on(series.start))
	endBalances := converter.apply(series.on(series.end))
	domainAccounts := store.DomainAccounts(accounts)

	points := make([]*agentifiv1.NetWorthPoint, 0, len(series.samples))
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

	// Closed accounts are in neither count, so the chip agrees with the
	// register header.
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

	return &agentifiv1.GetNetWorthResponse{
		Window:      windowProto(window),
		Granularity: series.granularity,
		Points:      points,
		Start: netWorthPoint(start, netWorthByKind(domainAccounts, startBalances),
			domain.EquityAt(domainAccounts, startBalances)),
		End: netWorthPoint(end, netWorthByKind(domainAccounts, endBalances),
			domain.EquityAt(domainAccounts, endBalances)),
		Change:                moneyProto(domain.NetWorthChange(start, end)),
		ChangePct:             rateProto(changePct, hasChangePct),
		DebtToAsset:           rateProto(ratio, hasRatio),
		Groups:                netWorthGroups(accounts, startBalances, endBalances),
		IncludedAccounts:      int32(included),
		TotalAccounts:         int32(total),
		UnconvertedCurrencies: converter.Unconverted,
	}, nil
}

// netWorthPoint takes its kinds and its equity so a point cannot be built
// without them.
func netWorthPoint(
	n domain.NetWorth, byKind []*agentifiv1.NetWorthKindAmount, equity domain.Money,
) *agentifiv1.NetWorthPoint {
	return &agentifiv1.NetWorthPoint{
		On: n.On.String(), Assets: moneyProto(n.Assets), Debt: moneyProto(n.Debt),
		Net: moneyProto(n.Net), ByKind: byKind, Equity: moneyProto(equity),
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
) []*agentifiv1.NetWorthGroup {
	domainAccounts := store.DomainAccounts(accounts)
	totals := make(map[string]domain.GroupChange, len(accounts))
	for _, group := range domain.GroupChanges(domainAccounts, startBalances, endBalances, "") {
		totals[group.Key] = group
	}

	members := map[domain.AccountKind][]*agentifiv1.NetWorthAccount{}
	for _, account := range accounts {
		if !store.DomainAccount(account).CountsInNetWorth() {
			continue
		}
		id := domain.ID(account.ID.String())
		row := domain.GroupChange{Start: startBalances[id], End: endBalances[id]}
		pct, hasPct := row.ChangePct()
		members[account.Kind] = append(members[account.Kind], &agentifiv1.NetWorthAccount{
			AccountId: account.ID.String(),
			Name:      account.Name,
			Kind:      string(account.Kind),
			Type:      account.Type,
			IsClosed:  account.IsClosed,
			Start:     moneyProto(row.Start.Round()),
			End:       moneyProto(row.End.Round()),
			Change:    moneyProto(row.Change()),
			ChangePct: rateProto(pct, hasPct),
		})
	}

	out := make([]*agentifiv1.NetWorthGroup, 0, len(members))
	for _, kind := range netWorthKindOrder {
		rows, present := members[kind]
		if !present {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		group := totals[string(kind)]
		pct, hasPct := group.ChangePct()
		out = append(out, &agentifiv1.NetWorthGroup{
			Kind:         string(kind),
			Class:        accountClass(kind),
			Side:         accountSide(kind),
			AccountCount: int32(len(rows)),
			Start:        moneyProto(group.Start),
			End:          moneyProto(group.End),
			Change:       moneyProto(group.Change()),
			ChangePct:    rateProto(pct, hasPct),
			Accounts:     rows,
		})
	}
	return out
}

// netWorthByKind splits one day's balances the way the group rows split the
// window. Only accounts flagged into net worth count, and a debt kind is
// positive, as a point's debt is.
func netWorthByKind(
	accounts []domain.Account, balances map[domain.ID]domain.Money,
) []*agentifiv1.NetWorthKindAmount {
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

	out := make([]*agentifiv1.NetWorthKindAmount, 0, len(totals))
	for _, kind := range netWorthKindOrder {
		total, held := totals[kind]
		if !held {
			continue
		}
		out = append(out, &agentifiv1.NetWorthKindAmount{Kind: string(kind), Amount: moneyProto(total.Round())})
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
	return parseGranularity(r.URL.Query().Get("granularity"))
}

func parseGranularity(raw string) (string, error) {
	switch raw {
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
