package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Investments: holdings, the portfolio header, and the two return figures
// (calculations.md §4 and §10).
//
//   - An incomplete cost basis is a state, not zero. Every unknown crosses the
//     wire as null with a boolean beside it; `market_value - 0` would report
//     the whole position as profit.
//   - A total that filters holdings out adds the owning account balances back
//     in the same function: portfolioValue.
//   - TWR and IRR are both returned, and either may be null when it does not
//     solve.

func init() {
	Register(Resource{Prefix: "/holdings", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listHoldings)
		rt.Write(http.MethodPost, "/", createHolding)
		rt.Write(http.MethodDelete, "/{holding_id}", deleteHolding)
	}})

	Register(Resource{Prefix: "/securities", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listSecurities)
		rt.Write(http.MethodPost, "/refresh", refreshSecurityPrices)
		rt.Read(http.MethodGet, "/{security_id}", readSecurity)
		rt.Write(http.MethodPost, "/{security_id}/history", backfillSecurityHistory)
	}})

	Register(Resource{Prefix: "/performance", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", readPerformance)
	}})
}

// SecurityResponse is one instrument and its latest quote.
type SecurityResponse struct {
	ID       uuid.UUID `json:"id"`
	Symbol   string    `json:"symbol"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Exchange *string   `json:"exchange"`
	Currency string    `json:"currency"`
	// LastPrice is null when no quote has ever arrived, and PriorClose is null
	// before the first full session on file — at which point the day change is
	// unknown, not zero.
	LastPrice   *domain.Rate `json:"last_price"`
	PriorClose  *domain.Rate `json:"prior_close"`
	LastPriceAt *time.Time   `json:"last_price_at"`
}

// HoldingResponse is one row of the Portfolio table. Every figure that can be
// unknown is a pointer, with a flag beside it saying which fact the null
// carries.
type HoldingResponse struct {
	ID         uuid.UUID `json:"id"`
	AccountID  uuid.UUID `json:"account_id"`
	SecurityID uuid.UUID `json:"security_id"`
	Symbol     string    `json:"symbol"`
	Name       string    `json:"name"`

	Shares domain.Rate `json:"shares"`
	// Price is null for a security with no quote. A zero there would read as a
	// worthless position rather than an unpriced one.
	Price *domain.Rate `json:"price"`
	// Currency is the security's own, not the space's. Empty when never
	// recorded; the client falls back to the space's currency.
	Currency    string       `json:"currency"`
	MarketValue domain.Money `json:"market_value"`
	// IsUnquoted says the market value is the provider's stored figure and
	// there is no live price behind it.
	IsUnquoted bool `json:"is_unquoted"`

	CostBasis    *domain.Money `json:"cost_basis"`
	TotalGain    *domain.Money `json:"total_gain"`
	TotalGainPct *domain.Rate  `json:"total_gain_pct"`
	// IsCostBasisComplete is false when the basis, the gain and the gain
	// percentage above are all null for the same reason.
	IsCostBasisComplete bool `json:"is_cost_basis_complete"`

	DayChange    *domain.Money `json:"day_change"`
	DayChangePct *domain.Rate  `json:"day_change_pct"`

	// Share is this row's fraction of the portfolio's market value; null for a
	// portfolio worth nothing.
	Share *domain.Rate `json:"share"`
}

// PortfolioTotalsResponse is the Portfolio header. The cost basis and total
// gain cover only holdings whose basis is known, and are deliberately not
// market_value − cost_basis, which would report missing positions as profit.
type PortfolioTotalsResponse struct {
	MarketValue  domain.Money  `json:"market_value"`
	CostBasis    *domain.Money `json:"cost_basis"`
	TotalGain    *domain.Money `json:"total_gain"`
	DayChange    *domain.Money `json:"day_change"`
	DayChangePct *domain.Rate  `json:"day_change_pct"`

	IsCostBasisIncomplete bool `json:"is_cost_basis_incomplete"`
	IsDayChangeIncomplete bool `json:"is_day_change_incomplete"`

	// AccountBalanceNotHeld is the cash across every investment account: each
	// balance less the positions filed under it, which §4 says it already
	// contains. TotalValue includes it. Per account, so a brokerage holding
	// cash beside stock keeps its cash and net worth agrees with this page.
	AccountBalanceNotHeld domain.Money `json:"account_balance_not_held"`
	TotalValue            domain.Money `json:"total_value"`
}

// AllocationSlice is one security's share of market value.
type AllocationSlice struct {
	SecurityID uuid.UUID    `json:"security_id"`
	Symbol     string       `json:"symbol"`
	Share      domain.Rate  `json:"share"`
	Value      domain.Money `json:"value"`
}

// AllocationGroup is one grouping's share of market value. Key is stable and
// Label is display; grouping on the label would merge same-named accounts.
type AllocationGroup struct {
	Key   string       `json:"key"`
	Label string       `json:"label"`
	Share domain.Rate  `json:"share"`
	Value domain.Money `json:"value"`
}

type HoldingsResponse struct {
	Items  []HoldingResponse       `json:"items"`
	Totals PortfolioTotalsResponse `json:"totals"`
	// Allocation is empty for a portfolio worth nothing, which has no shares
	// to give.
	Allocation []AllocationSlice `json:"allocation"`
	// The same market value by asset class and by account.
	AllocationByClass   []AllocationGroup `json:"allocation_by_class"`
	AllocationByAccount []AllocationGroup `json:"allocation_by_account"`
}

// PerformancePoint is one day on the performance chart.
type PerformancePoint struct {
	On    Date         `json:"on"`
	Value domain.Money `json:"value"`
	// ReturnPct rebases the line to 0% at the window's start. Null when the
	// window opened at nothing, which has no baseline to rebase against.
	ReturnPct *domain.Rate `json:"return_pct"`
}

// PerformanceResponse carries both rates because they are a toggle on one
// series. Either is null when it does not solve.
type PerformanceResponse struct {
	Window      WindowResponse     `json:"window"`
	Granularity string             `json:"granularity"`
	AccountIDs  []uuid.UUID        `json:"account_ids"`
	Points      []PerformancePoint `json:"points"`

	// TWR removes the effect of contributions; IRR weights by how much money
	// was present. Both are fractions, not percentages.
	TWR *domain.Rate `json:"twr"`
	IRR *domain.Rate `json:"irr"`
	// TWRPct and IRRPct are the same two figures on the axis the chart draws.
	TWRPct *domain.Rate `json:"twr_pct"`
	IRRPct *domain.Rate `json:"irr_pct"`

	StartValue domain.Money `json:"start_value"`
	EndValue   domain.Money `json:"end_value"`
	NetFlows   domain.Money `json:"net_flows"`
}

func listSecurities(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListSecurities(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := make([]SecurityResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, SecurityResponse{
			ID:          row.ID,
			Symbol:      row.Symbol,
			Name:        row.Name,
			Kind:        row.Kind,
			Exchange:    pgconv.NullText(row.Exchange),
			Currency:    row.Currency,
			LastPrice:   store.PtrIf(row.LastPrice, row.HasLastPrice),
			PriorClose:  store.PtrIf(row.PriorClose, row.HasPriorClose),
			LastPriceAt: row.LastPriceAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return writeJSON(w, http.StatusOK, out)
}

// RefreshPricesResponse says what the re-quote reached.
type RefreshPricesResponse struct {
	// Updated is how many securities took a fresh price.
	Updated int `json:"updated"`
	// Unpriced names the symbols the source had no answer for — absent from
	// its reply, never priced at zero, because a zero price would wipe a
	// holding's value.
	Unpriced []string `json:"unpriced"`
}

// refreshSecurityPrices re-quotes every security in the space and stores what
// came back. Only `last_price` moves: the source carries no prior close, and a
// guess would corrupt the day change. An unpriced symbol keeps its old figure
// and is named in the response.
func refreshSecurityPrices(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if env.Prices == nil {
		return errBadGateway("no market-price source is configured on this deployment")
	}
	securities, err := env.DB.ListSecurities(r.Context(), sp.ID())
	if err != nil {
		return err
	}

	symbols := make([]string, 0, len(securities))
	for _, row := range securities {
		if strings.TrimSpace(row.Symbol) != "" {
			symbols = append(symbols, row.Symbol)
		}
	}
	prices, err := env.Prices.LatestPrices(r.Context(), symbols)
	if err != nil {
		return errBadGateway("the price source could not be read: %v", err)
	}

	now := env.now()
	updated := 0
	unpriced := make([]string, 0)
	for _, row := range securities {
		symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if symbol == "" {
			continue
		}
		price, priced := prices[symbol]
		if !priced {
			unpriced = append(unpriced, row.Symbol)
			continue
		}
		if err := env.DB.SetSecurityPrice(r.Context(), sp.ID(), row.ID, price, now); err != nil {
			return err
		}
		// Also kept as the day's close, since `securities` holds no history.
		// Today's row is overwritten, so two refreshes make one close.
		day := []store.SecurityPrice{{On: domain.DateOf(now), Close: price}}
		if err := env.DB.RecordSecurityPrices(r.Context(), sp.ID(), row.ID, day); err != nil {
			return err
		}
		updated++
	}
	return writeJSON(w, http.StatusOK, RefreshPricesResponse{Updated: updated, Unpriced: unpriced})
}

// portfolio is the valued positions and the parts the header and allocation
// are computed from, shared with the assistant's portfolio_holdings tool so
// the two cannot disagree.
type portfolio struct {
	Items      []HoldingResponse
	Valuations []domain.HoldingValuation
	// Values is market value per security, summed across the accounts holding it.
	Values     map[uuid.UUID]domain.Money
	BySecurity map[uuid.UUID]store.Security
}

func loadPortfolio(
	ctx context.Context, env *Env, sp auth.SpaceContext, wanted accountFilter,
) (portfolio, error) {
	// Every figure below is drawn from these two reads, so a selection of no
	// accounts falls out as an empty portfolio rather than as the whole one.
	var rows []store.Holding
	var err error
	if !wanted.selectsNothing() {
		rows, err = env.DB.ListHoldings(ctx, sp.ID(), wanted.IDs)
		if err != nil {
			return portfolio{}, err
		}
	}
	securities, err := env.DB.ListSecurities(ctx, sp.ID())
	if err != nil {
		return portfolio{}, err
	}

	bySecurity := make(map[uuid.UUID]store.Security, len(securities))
	quotes := make(map[domain.ID]domain.Quote, len(securities))
	for _, row := range securities {
		bySecurity[row.ID] = row
		if !row.HasLastPrice {
			continue
		}
		quotes[domain.ID(row.ID.String())] = domain.Quote{
			SecurityID:    domain.ID(row.ID.String()),
			Price:         row.LastPrice,
			PriorClose:    row.PriorClose,
			HasPriorClose: row.HasPriorClose,
		}
	}

	holdings := make([]domain.Holding, 0, len(rows))
	for _, row := range rows {
		holdings = append(holdings, store.DomainHolding(row))
	}
	// A security with no quote is valued at the provider's figure (an employer
	// plan's fund has no public price). Only a holding with neither is
	// refused; skipping it would shrink the portfolio.
	valuations, err := domain.ValueHoldings(holdings, quotes)
	if err != nil {
		return portfolio{}, errConflict("%s", err)
	}

	// One row's fraction of the portfolio, from the same function the charts
	// group by. Absent for a portfolio worth nothing.
	shares, divisible := domain.AllocationBy(valuations,
		func(v domain.HoldingValuation) domain.ID { return v.Holding.ID })

	items := make([]HoldingResponse, 0, len(valuations))
	values := make(map[uuid.UUID]domain.Money, len(valuations))
	for index, valuation := range valuations {
		row := rows[index]
		security := bySecurity[row.SecurityID]
		gain, hasGain := valuation.TotalGain()
		gainPct, hasGainPct := valuation.TotalGainPct()
		dayPct, hasDayPct := valuation.DayChangePct()
		items = append(items, HoldingResponse{
			ID:                  row.ID,
			AccountID:           row.AccountID,
			SecurityID:          row.SecurityID,
			Symbol:              security.Symbol,
			Name:                security.Name,
			Shares:              row.Shares,
			Price:               unquotedPrice(quotes, row.SecurityID),
			Currency:            security.Currency,
			MarketValue:         valuation.MarketValue,
			IsUnquoted:          valuation.IsUnquoted,
			CostBasis:           store.PtrIf(valuation.CostBasis, valuation.HasCostBasis),
			TotalGain:           store.PtrIf(gain, hasGain),
			TotalGainPct:        store.PtrIf(gainPct, hasGainPct),
			IsCostBasisComplete: valuation.HasCostBasis,
			DayChange:           store.PtrIf(valuation.DayChange, valuation.HasDayChange),
			DayChangePct:        store.PtrIf(dayPct, hasDayPct),
			Share:               store.PtrIf(shares[valuation.Holding.ID].Round(6), divisible),
		})
		values[row.SecurityID] = values[row.SecurityID].Add(valuation.MarketValue)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Symbol < items[j].Symbol })

	return portfolio{
		Items: items, Valuations: valuations, Values: values, BySecurity: bySecurity,
	}, nil
}

func listHoldings(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	wanted, err := queryAccountFilter(r)
	if err != nil {
		return err
	}
	book, err := loadPortfolio(r.Context(), env, sp, wanted)
	if err != nil {
		return err
	}
	items, valuations := book.Items, book.Valuations
	values, bySecurity := book.Values, book.BySecurity

	totals, err := portfolioValue(r.Context(), env, sp, wanted, valuations)
	if err != nil {
		return err
	}

	allocation := make([]AllocationSlice, 0)
	if shares, divisible := domain.Allocation(valuations); divisible {
		for securityID, share := range shares {
			key, err := store.ParseID(securityID)
			if err != nil {
				return err
			}
			allocation = append(allocation, AllocationSlice{
				SecurityID: key,
				Symbol:     bySecurity[key].Symbol,
				Share:      share.Round(6),
				Value:      values[key].Round(),
			})
		}
		sort.Slice(allocation, func(i, j int) bool {
			return allocation[i].Value.GreaterThan(allocation[j].Value)
		})
	}

	byClass, byAccount, err := allocationGroups(r.Context(), env, sp, wanted, book)
	if err != nil {
		return err
	}

	return writeJSON(w, http.StatusOK, HoldingsResponse{
		Items:               items,
		Totals:              totals,
		Allocation:          allocation,
		AllocationByClass:   byClass,
		AllocationByAccount: byAccount,
	})
}

// allocationGroups cuts the same valuations by asset class and by account.
// Closed accounts are in the lookup, so a position under one keeps its name.
func allocationGroups(
	ctx context.Context, env *Env, sp auth.SpaceContext, wanted accountFilter, book portfolio,
) (byClass, byAccount []AllocationGroup, err error) {
	byClass, byAccount = []AllocationGroup{}, []AllocationGroup{}
	if len(book.Valuations) == 0 {
		return byClass, byAccount, nil
	}

	accountName := map[string]string{}
	if !wanted.selectsNothing() {
		accounts, err := env.DB.ListAccounts(ctx, sp.ID(),
			wanted.narrow(store.AccountQuery{IncludeClosed: true}))
		if err != nil {
			return nil, nil, err
		}
		for _, account := range accounts {
			accountName[account.ID.String()] = account.Name
		}
	}

	classOf := func(v domain.HoldingValuation) string {
		key, err := store.ParseID(v.Holding.SecurityID)
		if err != nil {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(book.BySecurity[key].Kind))
	}
	byClass = groupedAllocation(book.Valuations, classOf, assetClassLabel)
	byAccount = groupedAllocation(book.Valuations,
		func(v domain.HoldingValuation) string { return string(v.Holding.AccountID) },
		func(key string) string {
			if name, ok := accountName[key]; ok {
				return name
			}
			return "Unknown account"
		})
	return byClass, byAccount, nil
}

// groupedAllocation is one grouping, sorted largest first.
func groupedAllocation(
	valuations []domain.HoldingValuation,
	key func(domain.HoldingValuation) string,
	label func(string) string,
) []AllocationGroup {
	shares, divisible := domain.AllocationBy(valuations, key)
	if !divisible {
		return []AllocationGroup{}
	}
	values := map[string]domain.Money{}
	for _, valuation := range valuations {
		group := key(valuation)
		values[group] = values[group].Add(valuation.MarketValue)
	}

	out := make([]AllocationGroup, 0, len(shares))
	for group, share := range shares {
		out = append(out, AllocationGroup{
			Key:   group,
			Label: label(group),
			Share: share.Round(6),
			Value: values[group].Round(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Value.Equal(out[j].Value) {
			return out[i].Value.GreaterThan(out[j].Value)
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// assetClassLabel names a security's kind for a person. The empty kind (a
// hand-made symbol, or one the provider never classified) is "Unclassified"
// rather than guessed as equity.
func assetClassLabel(kind string) string {
	switch kind {
	case "":
		return "Unclassified"
	case "equity":
		return "Stocks"
	case "etf":
		return "ETFs"
	case "mutualfund", "mutual_fund":
		return "Mutual funds"
	case "bond", "fixedincome", "fixed_income":
		return "Bonds"
	case "cryptocurrency", "crypto":
		return "Crypto"
	case "cash", "moneymarket", "money_market":
		return "Cash"
	case "option":
		return "Options"
	default:
		return textutil.Capitalize(kind)
	}
}

// portfolioValue is the header's figures and the account side, in one
// function (calculations.md §4): a holding under a brokerage account is inside
// its balance, and an investment account with no positions would vanish from
// a holdings-only total, so its balance is added back here.
func portfolioValue(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	wanted accountFilter, valuations []domain.HoldingValuation,
) (PortfolioTotalsResponse, error) {
	totals := domain.NewPortfolioTotals(valuations)
	dayPct, hasDayPct := totals.DayChangePct()

	var accounts []store.Account
	if !wanted.selectsNothing() {
		var err error
		accounts, err = env.DB.ListAccounts(ctx, sp.ID(), wanted.narrow(store.AccountQuery{}))
		if err != nil {
			return PortfolioTotalsResponse{}, err
		}
	}
	investment := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Kind == domain.KindInvestment {
			investment = append(investment, account)
		}
	}
	postings, err := postingsByAccount(ctx, env, sp, investment)
	if err != nil {
		return PortfolioTotalsResponse{}, err
	}

	notHeld := domain.Zero
	for _, cash := range domain.CashOutsideHoldings(investmentBalances(investment, postings), valuations) {
		notHeld = notHeld.Add(cash)
	}

	return PortfolioTotalsResponse{
		MarketValue:           totals.MarketValue,
		CostBasis:             store.PtrIf(totals.CostBasis, totals.HasCostBasis),
		TotalGain:             store.PtrIf(totals.TotalGain, totals.HasCostBasis),
		DayChange:             store.PtrIf(totals.DayChange, totals.HasDayChange),
		DayChangePct:          store.PtrIf(dayPct, hasDayPct),
		IsCostBasisIncomplete: totals.IsCostBasisIncomplete,
		IsDayChangeIncomplete: totals.IsDayChangeIncomplete,
		AccountBalanceNotHeld: notHeld.Round(),
		TotalValue:            domain.Total(totals.MarketValue, notHeld),
	}, nil
}

// investmentBalances is each account's balance, keyed for
// domain.CashOutsideHoldings.
func investmentBalances(
	accounts []store.Account, postings map[uuid.UUID][]domain.Posting,
) map[domain.ID]domain.Money {
	balances := make(map[domain.ID]domain.Money, len(accounts))
	for _, account := range accounts {
		balances[domain.ID(account.ID.String())] = domain.AccountBalance(store.DomainAccount(account), postings[account.ID])
	}
	return balances
}

// readPerformance draws one value series and reports both rates over it.
func readPerformance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	requested, err := granularityFromRequest(r)
	if err != nil {
		return err
	}
	wanted, err := queryAccountFilter(r)
	if err != nil {
		return err
	}

	var accounts []store.Account
	if !wanted.selectsNothing() {
		accounts, err = env.DB.ListAccounts(r.Context(), sp.ID(),
			wanted.narrow(store.AccountQuery{IncludeClosed: true}))
		if err != nil {
			return err
		}
	}
	selected := make([]store.Account, 0, len(accounts))
	ids := make([]uuid.UUID, 0, len(accounts))
	for _, account := range accounts {
		if account.Kind != domain.KindInvestment {
			continue
		}
		selected = append(selected, account)
		ids = append(ids, account.ID)
	}

	series, err := newBalanceSeries(r.Context(), env, sp, selected, window, requested)
	if err != nil {
		return err
	}

	points := make([]domain.ValuePoint, 0, len(series.samples))
	for _, on := range series.samples {
		balances := series.on(on)
		total := domain.Zero
		for _, account := range selected {
			total = total.Add(balances[domain.ID(account.ID.String())])
		}
		points = append(points, domain.ValuePoint{On: on, Value: total.Round()})
	}

	flows, err := externalFlows(r.Context(), env, sp, ids, series.start, series.end)
	if err != nil {
		return err
	}

	response := PerformanceResponse{
		Window:      windowResponse(window),
		Granularity: series.granularity,
		AccountIDs:  store.NonNil(ids),
		Points:      make([]PerformancePoint, 0, len(points)),
		NetFlows:    domain.Sum(flows, func(f domain.CashFlow) domain.Money { return f.Amount }),
	}
	if len(points) > 0 {
		response.StartValue = points[0].Value
		response.EndValue = points[len(points)-1].Value
	}
	for _, point := range points {
		pct, hasPct := domain.Percent(point.Value.Sub(response.StartValue), response.StartValue)
		response.Points = append(response.Points, PerformancePoint{
			On:        Date(point.On),
			Value:     point.Value,
			ReturnPct: store.PtrIf(pct, hasPct),
		})
	}

	twr, hasTWR := domain.TimeWeightedReturn(points, flows)
	response.TWR = store.PtrIf(twr, hasTWR)
	twrPct, hasTWRPct := domain.RatePct(twr, hasTWR)
	response.TWRPct = store.PtrIf(twrPct, hasTWRPct)

	if len(points) >= 2 {
		start := points[0]
		irr, hasIRR := domain.InternalRateOfReturn(flows, points[len(points)-1], &start)
		response.IRR = store.PtrIf(irr, hasIRR)
		irrPct, hasIRRPct := domain.RatePct(irr, hasIRR)
		response.IRRPct = store.PtrIf(irrPct, hasIRRPct)
	}

	return writeJSON(w, http.StatusOK, response)
}

// externalFlows is money crossing the portfolio boundary, positive inward. A
// reinvested dividend is not one; it would flatten the return. Transfers are
// recognised by domain.Posting.IsTransfer, which also rules out deleted legs
// and provider forecasts.
func externalFlows(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	accountIDs []uuid.UUID, start, end domain.Date,
) ([]domain.CashFlow, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	postings, _, err := service.LoadPostings(ctx, env.DB, sp.ID(), store.TransactionQuery{
		AccountIDs: accountIDs,
		From:       start,
		To:         end,
	})
	if err != nil {
		return nil, err
	}

	var flows []domain.CashFlow
	for _, posting := range postings {
		// Pending is deliberately kept: an authorized deposit has moved the
		// money even though the bank has not settled it.
		if !domain.CountsTowardBalance(posting) || !posting.IsTransfer() {
			continue
		}
		flows = append(flows, domain.CashFlow{On: posting.Txn.Date, Amount: posting.Amount()})
	}
	return flows, nil
}

// unquotedPrice is the security's price, or nothing when it has no quote.
func unquotedPrice(quotes map[domain.ID]domain.Quote, securityID uuid.UUID) *domain.Rate {
	quote, ok := quotes[domain.ID(securityID.String())]
	if !ok {
		return nil
	}
	price := quote.Price
	return &price
}

// --- One security ------------------------------------------------------------
//
// The position across every account, its cost, and its price line, in one
// response so the header and the chart share a window.

// PriceHistoryPoint is one close on one day. A Rate, not Money: a share price
// is not rounded to the cent.
type PriceHistoryPoint struct {
	On    Date        `json:"on"`
	Close domain.Rate `json:"close"`
}

// SecurityDetailResponse is the holding detail screen, summed from the same
// valuations as the Portfolio table. An incomplete basis in one account is
// incomplete here.
type SecurityDetailResponse struct {
	Security SecurityResponse `json:"security"`
	Window   WindowResponse   `json:"window"`

	// Positions is every account holding it. Empty for a sold position, which
	// keeps its history and price line.
	Positions []HoldingResponse `json:"positions"`
	Shares    domain.Rate       `json:"shares"`

	MarketValue  domain.Money  `json:"market_value"`
	CostBasis    *domain.Money `json:"cost_basis"`
	TotalGain    *domain.Money `json:"total_gain"`
	TotalGainPct *domain.Rate  `json:"total_gain_pct"`
	DayChange    *domain.Money `json:"day_change"`
	DayChangePct *domain.Rate  `json:"day_change_pct"`

	IsCostBasisIncomplete bool `json:"is_cost_basis_incomplete"`

	// Prices is what is on file for the window, oldest first; nothing is
	// invented to fill it.
	Prices []PriceHistoryPoint `json:"prices"`
	// PriceChange is the move across the series, and PriceChangePct that as a
	// fraction of where it opened. Both null for a series too short to have
	// moved; the percentage alone is null for one that opened at nothing.
	PriceChange    *domain.Rate `json:"price_change"`
	PriceChangePct *domain.Rate `json:"price_change_pct"`
}

func readSecurity(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "security_id", "Security")
	if err != nil {
		return err
	}
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	security, err := env.DB.GetSecurity(r.Context(), sp.ID(), id)
	if err != nil {
		return notFoundAs(err, "Security")
	}

	// This security's rows out of the whole portfolio, rather than a second
	// valuation path.
	book, err := loadPortfolio(r.Context(), env, sp, accountFilter{})
	if err != nil {
		return err
	}

	response := SecurityDetailResponse{
		Security: SecurityResponse{
			ID:          security.ID,
			Symbol:      security.Symbol,
			Name:        security.Name,
			Kind:        security.Kind,
			Exchange:    pgconv.NullText(security.Exchange),
			Currency:    security.Currency,
			LastPrice:   store.PtrIf(security.LastPrice, security.HasLastPrice),
			PriorClose:  store.PtrIf(security.PriorClose, security.HasPriorClose),
			LastPriceAt: security.LastPriceAt,
		},
		Window:    windowResponse(window),
		Positions: []HoldingResponse{},
		Prices:    []PriceHistoryPoint{},
	}

	mine := make([]domain.HoldingValuation, 0, len(book.Valuations))
	for _, valuation := range book.Valuations {
		if valuation.Holding.SecurityID != domain.ID(id.String()) {
			continue
		}
		mine = append(mine, valuation)
		response.Shares = response.Shares.Add(valuation.Holding.Shares)
	}
	for _, item := range book.Items {
		if item.SecurityID == id {
			response.Positions = append(response.Positions, item)
		}
	}

	totals := domain.NewPortfolioTotals(mine)
	gainPct, hasGainPct := domain.Percent(totals.TotalGain, totals.CostBasis)
	dayPct, hasDayPct := totals.DayChangePct()
	response.MarketValue = totals.MarketValue
	response.CostBasis = store.PtrIf(totals.CostBasis, totals.HasCostBasis)
	response.TotalGain = store.PtrIf(totals.TotalGain, totals.HasCostBasis)
	response.TotalGainPct = store.PtrIf(gainPct, totals.HasCostBasis && hasGainPct)
	response.DayChange = store.PtrIf(totals.DayChange, totals.HasDayChange)
	response.DayChangePct = store.PtrIf(dayPct, hasDayPct)
	response.IsCostBasisIncomplete = totals.IsCostBasisIncomplete

	prices, err := env.DB.ListSecurityPrices(r.Context(), sp.ID(), id, window.From, window.To)
	if err != nil {
		return err
	}
	points := make([]domain.PricePoint, 0, len(prices))
	for _, price := range prices {
		points = append(points, domain.PricePoint{On: price.On, Close: price.Close})
		response.Prices = append(response.Prices,
			PriceHistoryPoint{On: Date(price.On), Close: price.Close})
	}
	change, changePct, hasPct, moved := domain.PriceChange(points)
	response.PriceChange = store.PtrIf(change, moved)
	response.PriceChangePct = store.PtrIf(changePct, moved && hasPct)
	return writeJSON(w, http.StatusOK, response)
}

// BackfilledHistory says what a history fetch stored.
type BackfilledHistory struct {
	Symbol string `json:"symbol"`
	Stored int    `json:"stored"`
	From   Date   `json:"from"`
	To     Date   `json:"to"`
}

// historyDefaultDays is the window a backfill covers when the caller names
// none: a year, which is the longest range the detail chart offers.
const historyDefaultDays = 365

// backfillSecurityHistory asks the price source for daily closes and stores
// them. A write route because a GET that writes would spend the rate limit on
// a page load. A symbol with no public history stores nothing, which is not an
// error.
func backfillSecurityHistory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if env.Prices == nil {
		return errBadGateway("no market-price source is configured on this deployment")
	}
	id, err := pathUUID(r, "security_id", "Security")
	if err != nil {
		return err
	}
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	security, err := env.DB.GetSecurity(r.Context(), sp.ID(), id)
	if err != nil {
		return notFoundAs(err, "Security")
	}
	if strings.TrimSpace(security.Symbol) == "" {
		return errConflict("this security has no symbol to look up")
	}

	to := domain.DateOf(env.now())
	if window.HasTo {
		to = window.To
	}
	from := to.AddDays(-historyDefaultDays)
	if window.HasFrom {
		from = window.From
	}

	points, err := env.Prices.History(r.Context(), security.Symbol, from, to)
	if err != nil {
		return errBadGateway("the price source could not be read: %v", err)
	}
	stored := make([]store.SecurityPrice, 0, len(points))
	for _, point := range points {
		stored = append(stored, store.SecurityPrice{On: point.On, Close: point.Close})
	}
	if err := env.DB.RecordSecurityPrices(r.Context(), sp.ID(), id, stored); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, BackfilledHistory{
		Symbol: security.Symbol, Stored: len(stored), From: Date(from), To: Date(to),
	})
}

// --- Adding a position by hand -----------------------------------------------
//
// The sync does not persist holdings, so this is how a position outside the
// Simplifi import gets in. A holding is an account and a security
// (`uq_holding_account_security`); the security is found by symbol first,
// because `uq_security_space_symbol` allows one per space.

// HoldingCreate adds a position to an investment account. The symbol is the
// identity; Name is used only when the symbol is new. Neither cost figure is
// required: no basis is the *Incomplete* state.
type HoldingCreate struct {
	AccountID uuid.UUID     `json:"account_id"`
	Symbol    string        `json:"symbol"`
	Name      string        `json:"name"`
	Shares    domain.Rate   `json:"shares"`
	CostBasis *domain.Money `json:"cost_basis"`
	// MarketValue is required only for a security with no quote on file, which
	// the valuer would otherwise refuse.
	MarketValue *domain.Money `json:"market_value"`
}

func createHolding(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body HoldingCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	symbol := strings.ToUpper(strings.TrimSpace(body.Symbol))
	if symbol == "" {
		return errInvalid("missing", []string{"body", "symbol"}, "symbol is required")
	}
	if body.Shares.IsZero() {
		return errInvalid("invalid", []string{"body", "shares"},
			"shares must not be zero; a position of none is no position")
	}
	account, err := requireAccount(r.Context(), env, sp, body.AccountID)
	if err != nil {
		return err
	}
	// A holding in a non-investment account would be counted twice.
	if account.Kind != domain.KindInvestment {
		return errConflict("%s is not an investment account", account.Name)
	}

	// Looked up before anything is written, so a refusal leaves no security
	// row behind.
	security, err := env.DB.GetSecurityBySymbol(r.Context(), sp.ID(), symbol)
	found := err == nil
	if err != nil && !isNotFound(err) {
		return err
	}
	if !(found && security.HasLastPrice) && body.MarketValue == nil {
		return errInvalid("missing", []string{"body", "market_value"},
			"%s has no price on file, so say what the position is worth", symbol)
	}
	securityID := security.ID
	if !found {
		securityID, err = createSecurity(r.Context(), env, sp, symbol, body.Name, account.Currency)
		if err != nil {
			return err
		}
	}

	holding := store.Holding{AccountID: account.ID, SecurityID: securityID, Shares: body.Shares}
	if body.CostBasis != nil {
		holding.CostBasis, holding.HasCostBasis = *body.CostBasis, true
	}
	holding.IsComplete = holding.HasCostBasis
	if body.MarketValue != nil {
		// Only ever read when there is no quote; a priced security is valued
		// from shares and its price on every read.
		holding.MarketValue, holding.HasMarketValue = *body.MarketValue, true
	}
	added, err := env.DB.CreateHolding(r.Context(), sp.ID(), &holding)
	if err != nil {
		return err
	}
	if !added {
		return errConflict("%s already holds %s; edit that position instead", account.Name, symbol)
	}

	// The identity, not the valued row: the client refetches the portfolio,
	// whose totals this row changes.
	return writeJSON(w, http.StatusCreated, HoldingCreated{
		ID: holding.ID, AccountID: account.ID, SecurityID: securityID, Symbol: symbol,
	})
}

// HoldingCreated is what a successful add returns: enough to name the row, and
// no figure the caller would then have to reconcile with a stale total.
type HoldingCreated struct {
	ID         uuid.UUID `json:"id"`
	AccountID  uuid.UUID `json:"account_id"`
	SecurityID uuid.UUID `json:"security_id"`
	Symbol     string    `json:"symbol"`
}

// createSecurity writes a symbol this space has not held before. It starts
// with no `last_price` — unquoted rather than zero — until a price refresh
// finds one, which is why the caller has to say what the position is worth.
func createSecurity(
	ctx context.Context, env *Env, sp auth.SpaceContext, symbol, name, currency string,
) (uuid.UUID, error) {
	label := strings.TrimSpace(name)
	if label == "" {
		label = symbol
	}
	security := store.Security{Symbol: symbol, Name: label, Kind: "equity", Currency: currency}
	if err := env.DB.CreateSecurity(ctx, sp.ID(), &security); err != nil {
		return uuid.Nil, err
	}
	return security.ID, nil
}

// deleteHolding removes a position. A hard delete: a holding is a current
// share count; its history is the transactions, which stay.
func deleteHolding(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "holding_id", "Holding")
	if err != nil {
		return err
	}
	if err := env.DB.DeleteHolding(r.Context(), sp.ID(), id); err != nil {
		return notFoundAs(err, "Holding")
	}
	return writeNoContent(w)
}
