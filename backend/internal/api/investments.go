package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Investments: holdings, the portfolio header, and the two return figures
// (calculations.md §4 and §10).
//
//   - An incomplete cost basis is a state, not zero. Every unknown crosses the
//     wire unset with a boolean beside it; `market_value - 0` would report the
//     whole position as profit.
//   - A total that filters holdings out adds the owning account balances back
//     in the same function: portfolioValue.
//   - TWR and IRR are both returned, and either may be unset when it does not
//     solve.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewHoldingServiceHandler(holdingService{env}, opts...)
	})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewSecurityServiceHandler(securityService{env}, opts...)
	})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewPerformanceServiceHandler(performanceService{env}, opts...)
	})
}

type (
	holdingService     struct{ env *Env }
	securityService    struct{ env *Env }
	performanceService struct{ env *Env }
)

// valuedHolding is one row of the Portfolio table. Every figure that can be
// unknown is a pointer, with a flag beside it saying which fact the nil
// carries.
type valuedHolding struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	SecurityID uuid.UUID
	Symbol     string
	Name       string

	Shares domain.Rate
	// Price is nil for a security with no quote. A zero there would read as a
	// worthless position rather than an unpriced one.
	Price *domain.Rate
	// Currency is the security's own, not the space's.
	Currency    string
	MarketValue domain.Money
	// IsUnquoted says the market value is the provider's stored figure and
	// there is no live price behind it.
	IsUnquoted bool

	CostBasis           *domain.Money
	TotalGain           *domain.Money
	TotalGainPct        *domain.Rate
	IsCostBasisComplete bool

	DayChange    *domain.Money
	DayChangePct *domain.Rate

	// Share is this row's fraction of the portfolio's market value; nil for a
	// portfolio worth nothing.
	Share *domain.Rate
}

// portfolioTotals is the Portfolio header. The cost basis and total gain cover
// only holdings whose basis is known, and are deliberately not
// market_value − cost_basis, which would report missing positions as profit.
type portfolioTotals struct {
	MarketValue  domain.Money
	CostBasis    *domain.Money
	TotalGain    *domain.Money
	DayChange    *domain.Money
	DayChangePct *domain.Rate

	IsCostBasisIncomplete bool
	IsDayChangeIncomplete bool

	// AccountBalanceNotHeld is the cash across every investment account: each
	// balance less the positions filed under it, which §4 says it already
	// contains. TotalValue includes it. Per account, so a brokerage holding
	// cash beside stock keeps its cash and net worth agrees with this page.
	AccountBalanceNotHeld domain.Money
	TotalValue            domain.Money
}

func (s securityService) ListSecurities(
	ctx context.Context, _ *agentifiv1.ListSecuritiesRequest,
) (*agentifiv1.ListSecuritiesResponse, error) {
	rows, err := s.env.DB.ListSecurities(ctx, spaceFrom(ctx).ID())
	if err != nil {
		return nil, err
	}
	out := make([]*agentifiv1.Security, 0, len(rows))
	for _, row := range rows {
		out = append(out, securityProto(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetSymbol() < out[j].GetSymbol() })
	return &agentifiv1.ListSecuritiesResponse{Securities: out}, nil
}

// RefreshSecurityPrices re-quotes every security in the space and stores what
// came back. Only `last_price` moves: the source carries no prior close, and a
// guess would corrupt the day change. An unpriced symbol keeps its old figure
// and is named in the response, never priced at zero, because a zero price
// would wipe a holding's value.
func (s securityService) RefreshSecurityPrices(
	ctx context.Context, _ *agentifiv1.RefreshSecurityPricesRequest,
) (*agentifiv1.RefreshSecurityPricesResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if env.Prices == nil {
		return nil, errBadGateway("no market-price source is configured on this deployment")
	}
	securities, err := env.DB.ListSecurities(ctx, sp.ID())
	if err != nil {
		return nil, err
	}

	symbols := make([]string, 0, len(securities))
	for _, row := range securities {
		if strings.TrimSpace(row.Symbol) != "" {
			symbols = append(symbols, row.Symbol)
		}
	}
	prices, err := env.Prices.LatestPrices(ctx, symbols)
	if err != nil {
		return nil, errBadGateway("the price source could not be read: %v", err)
	}

	now := env.now()
	out := &agentifiv1.RefreshSecurityPricesResponse{}
	for _, row := range securities {
		symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if symbol == "" {
			continue
		}
		price, priced := prices[symbol]
		if !priced {
			out.Unpriced = append(out.Unpriced, row.Symbol)
			continue
		}
		if err := env.DB.SetSecurityPrice(ctx, sp.ID(), row.ID, price, now); err != nil {
			return nil, err
		}
		// Also kept as the day's close, since `securities` holds no history.
		// Today's row is overwritten, so two refreshes make one close.
		day := []store.SecurityPrice{{On: domain.DateOf(now), Close: price}}
		if err := env.DB.RecordSecurityPrices(ctx, sp.ID(), row.ID, day); err != nil {
			return nil, err
		}
		out.Updated++
	}
	return out, nil
}

// portfolio is the valued positions and the parts the header and allocation
// are computed from, shared with the assistant's portfolio_holdings tool so
// the two cannot disagree.
type portfolio struct {
	Items      []valuedHolding
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

	items := make([]valuedHolding, 0, len(valuations))
	values := make(map[uuid.UUID]domain.Money, len(valuations))
	for index, valuation := range valuations {
		row := rows[index]
		security := bySecurity[row.SecurityID]
		gain, hasGain := valuation.TotalGain()
		gainPct, hasGainPct := valuation.TotalGainPct()
		dayPct, hasDayPct := valuation.DayChangePct()
		items = append(items, valuedHolding{
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

func (s holdingService) ListHoldings(
	ctx context.Context, req *agentifiv1.ListHoldingsRequest,
) (*agentifiv1.ListHoldingsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	wanted, err := accountFilterOf(req.GetAccountId())
	if err != nil {
		return nil, err
	}
	book, err := loadPortfolio(ctx, env, sp, wanted)
	if err != nil {
		return nil, err
	}
	valuations, values, bySecurity := book.Valuations, book.Values, book.BySecurity

	totals, err := portfolioValue(ctx, env, sp, wanted, valuations)
	if err != nil {
		return nil, err
	}

	type slice struct {
		securityID uuid.UUID
		share      domain.Rate
		value      domain.Money
	}
	var slices []slice
	if shares, divisible := domain.Allocation(valuations); divisible {
		for securityID, share := range shares {
			key, err := store.ParseID(securityID)
			if err != nil {
				return nil, err
			}
			slices = append(slices, slice{securityID: key, share: share.Round(6), value: values[key].Round()})
		}
		sort.Slice(slices, func(i, j int) bool { return slices[i].value.GreaterThan(slices[j].value) })
	}
	allocation := make([]*agentifiv1.AllocationSlice, 0, len(slices))
	for _, one := range slices {
		allocation = append(allocation, &agentifiv1.AllocationSlice{
			SecurityId: one.securityID.String(),
			Symbol:     bySecurity[one.securityID].Symbol,
			Share:      one.share.String(),
			Value:      moneyProto(one.value),
		})
	}

	byClass, byAccount, err := allocationGroups(ctx, env, sp, wanted, book)
	if err != nil {
		return nil, err
	}

	items := make([]*agentifiv1.Holding, 0, len(book.Items))
	for _, item := range book.Items {
		items = append(items, holdingProto(item))
	}
	return &agentifiv1.ListHoldingsResponse{
		Items:               items,
		Totals:              portfolioTotalsProto(totals),
		Allocation:          allocation,
		AllocationByClass:   byClass,
		AllocationByAccount: byAccount,
	}, nil
}

// allocationGroups cuts the same valuations by asset class and by account.
// Closed accounts are in the lookup, so a position under one keeps its name.
func allocationGroups(
	ctx context.Context, env *Env, sp auth.SpaceContext, wanted accountFilter, book portfolio,
) (byClass, byAccount []*agentifiv1.AllocationGroup, err error) {
	if len(book.Valuations) == 0 {
		return nil, nil, nil
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
) []*agentifiv1.AllocationGroup {
	shares, divisible := domain.AllocationBy(valuations, key)
	if !divisible {
		return nil
	}
	values := map[string]domain.Money{}
	for _, valuation := range valuations {
		group := key(valuation)
		values[group] = values[group].Add(valuation.MarketValue)
	}

	type row struct {
		key, label string
		share      domain.Rate
		value      domain.Money
	}
	rows := make([]row, 0, len(shares))
	for group, share := range shares {
		rows = append(rows, row{key: group, label: label(group), share: share.Round(6), value: values[group].Round()})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].value.Equal(rows[j].value) {
			return rows[i].value.GreaterThan(rows[j].value)
		}
		return rows[i].label < rows[j].label
	})
	out := make([]*agentifiv1.AllocationGroup, 0, len(rows))
	for _, one := range rows {
		out = append(out, &agentifiv1.AllocationGroup{
			Key: one.key, Label: one.label, Share: one.share.String(), Value: moneyProto(one.value),
		})
	}
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
) (portfolioTotals, error) {
	totals := domain.NewPortfolioTotals(valuations)
	dayPct, hasDayPct := totals.DayChangePct()

	var accounts []store.Account
	if !wanted.selectsNothing() {
		var err error
		accounts, err = env.DB.ListAccounts(ctx, sp.ID(), wanted.narrow(store.AccountQuery{}))
		if err != nil {
			return portfolioTotals{}, err
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
		return portfolioTotals{}, err
	}

	notHeld := domain.Zero
	for _, cash := range domain.CashOutsideHoldings(investmentBalances(investment, postings), valuations) {
		notHeld = notHeld.Add(cash)
	}

	return portfolioTotals{
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

// GetPerformance draws one value series and reports both rates over it. They
// are a toggle on one series, so both are answered; either is unset when it
// does not solve.
func (s performanceService) GetPerformance(
	ctx context.Context, req *agentifiv1.GetPerformanceRequest,
) (*agentifiv1.GetPerformanceResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	requested, err := parseGranularity(req.GetGranularity())
	if err != nil {
		return nil, err
	}
	wanted, err := accountFilterOf(req.GetAccountId())
	if err != nil {
		return nil, err
	}

	var accounts []store.Account
	if !wanted.selectsNothing() {
		accounts, err = env.DB.ListAccounts(ctx, sp.ID(),
			wanted.narrow(store.AccountQuery{IncludeClosed: true}))
		if err != nil {
			return nil, err
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

	series, err := newBalanceSeries(ctx, env, sp, selected, window, requested)
	if err != nil {
		return nil, err
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

	flows, err := externalFlows(ctx, env, sp, ids, series.start, series.end)
	if err != nil {
		return nil, err
	}

	var startValue, endValue domain.Money
	if len(points) > 0 {
		startValue = points[0].Value
		endValue = points[len(points)-1].Value
	}
	out := &agentifiv1.GetPerformanceResponse{
		Window:      windowProto(window),
		Granularity: series.granularity,
		AccountIds:  idStrings(ids),
		Points:      make([]*agentifiv1.PerformancePoint, 0, len(points)),
		StartValue:  moneyProto(startValue),
		EndValue:    moneyProto(endValue),
		NetFlows:    moneyProto(domain.Sum(flows, func(f domain.CashFlow) domain.Money { return f.Amount })),
	}
	for _, point := range points {
		pct, hasPct := domain.Percent(point.Value.Sub(startValue), startValue)
		out.Points = append(out.Points, &agentifiv1.PerformancePoint{
			On:        point.On.String(),
			Value:     moneyProto(point.Value),
			ReturnPct: rateProto(pct, hasPct),
		})
	}

	twr, hasTWR := domain.TimeWeightedReturn(points, flows)
	out.Twr = rateProto(twr, hasTWR)
	out.TwrPct = rateProto(domain.RatePct(twr, hasTWR))

	if len(points) >= 2 {
		start := points[0]
		irr, hasIRR := domain.InternalRateOfReturn(flows, points[len(points)-1], &start)
		out.Irr = rateProto(irr, hasIRR)
		out.IrrPct = rateProto(domain.RatePct(irr, hasIRR))
	}
	return out, nil
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

// GetSecurity is the holding detail screen, summed from the same valuations as
// the Portfolio table. An incomplete basis in one account is incomplete here.
func (s securityService) GetSecurity(
	ctx context.Context, req *agentifiv1.GetSecurityRequest,
) (*agentifiv1.GetSecurityResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	id, err := idFrom(req.GetSecurityId(), "Security")
	if err != nil {
		return nil, err
	}
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	security, err := env.DB.GetSecurity(ctx, sp.ID(), id)
	if err != nil {
		return nil, notFoundAs(err, "Security")
	}

	// This security's rows out of the whole portfolio, rather than a second
	// valuation path.
	book, err := loadPortfolio(ctx, env, sp, accountFilter{})
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.GetSecurityResponse{
		Security: securityProto(security),
		Window:   windowProto(window),
	}

	shares := decimal.Zero
	mine := make([]domain.HoldingValuation, 0, len(book.Valuations))
	for _, valuation := range book.Valuations {
		if valuation.Holding.SecurityID != domain.ID(id.String()) {
			continue
		}
		mine = append(mine, valuation)
		shares = shares.Add(valuation.Holding.Shares)
	}
	out.Shares = shares.String()
	for _, item := range book.Items {
		if item.SecurityID == id {
			out.Positions = append(out.Positions, holdingProto(item))
		}
	}

	totals := domain.NewPortfolioTotals(mine)
	gainPct, hasGainPct := domain.Percent(totals.TotalGain, totals.CostBasis)
	dayPct, hasDayPct := totals.DayChangePct()
	out.MarketValue = moneyProto(totals.MarketValue)
	out.CostBasis = nullableMoneyProto(totals.CostBasis, totals.HasCostBasis)
	out.TotalGain = nullableMoneyProto(totals.TotalGain, totals.HasCostBasis)
	out.TotalGainPct = rateProto(gainPct, totals.HasCostBasis && hasGainPct)
	out.DayChange = nullableMoneyProto(totals.DayChange, totals.HasDayChange)
	out.DayChangePct = rateProto(dayPct, hasDayPct)
	out.IsCostBasisIncomplete = totals.IsCostBasisIncomplete

	prices, err := env.DB.ListSecurityPrices(ctx, sp.ID(), id, window.From, window.To)
	if err != nil {
		return nil, err
	}
	points := make([]domain.PricePoint, 0, len(prices))
	for _, price := range prices {
		points = append(points, domain.PricePoint{On: price.On, Close: price.Close})
		out.Prices = append(out.Prices,
			&agentifiv1.SecurityPricePoint{On: price.On.String(), Close: price.Close.String()})
	}
	change, changePct, hasPct, moved := domain.PriceChange(points)
	out.PriceChange = rateProto(change, moved)
	out.PriceChangePct = rateProto(changePct, moved && hasPct)
	return out, nil
}

// historyDefaultDays is the window a backfill covers when the caller names
// none: a year, which is the longest range the detail chart offers.
const historyDefaultDays = 365

// BackfillSecurityHistory asks the price source for daily closes and stores
// them. A write because a read that writes would spend the rate limit on a
// page load. A symbol with no public history stores nothing, which is not an
// error.
func (s securityService) BackfillSecurityHistory(
	ctx context.Context, req *agentifiv1.BackfillSecurityHistoryRequest,
) (*agentifiv1.BackfillSecurityHistoryResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if env.Prices == nil {
		return nil, errBadGateway("no market-price source is configured on this deployment")
	}
	id, err := idFrom(req.GetSecurityId(), "Security")
	if err != nil {
		return nil, err
	}
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	security, err := env.DB.GetSecurity(ctx, sp.ID(), id)
	if err != nil {
		return nil, notFoundAs(err, "Security")
	}
	if strings.TrimSpace(security.Symbol) == "" {
		return nil, errConflict("this security has no symbol to look up")
	}

	to := domain.DateOf(env.now())
	if window.HasTo {
		to = window.To
	}
	from := to.AddDays(-historyDefaultDays)
	if window.HasFrom {
		from = window.From
	}

	points, err := env.Prices.History(ctx, security.Symbol, from, to)
	if err != nil {
		return nil, errBadGateway("the price source could not be read: %v", err)
	}
	stored := make([]store.SecurityPrice, 0, len(points))
	for _, point := range points {
		stored = append(stored, store.SecurityPrice{On: point.On, Close: point.Close})
	}
	if err := env.DB.RecordSecurityPrices(ctx, sp.ID(), id, stored); err != nil {
		return nil, err
	}
	return &agentifiv1.BackfillSecurityHistoryResponse{
		Symbol: security.Symbol, Stored: int32(len(stored)), From: from.String(), To: to.String(),
	}, nil
}

// --- Adding a position by hand -----------------------------------------------
//
// The sync does not persist holdings, so this is how a position outside the
// Simplifi import gets in. A holding is an account and a security
// (`uq_holding_account_security`); the security is found by symbol first,
// because `uq_security_space_symbol` allows one per space.

// CreateHolding adds a position to an investment account. The symbol is the
// identity; the name is used only when the symbol is new. Neither cost figure
// is required: no basis is the *Incomplete* state. The answer is the
// identity, not the valued row: the client refetches the portfolio, whose
// totals this row changes.
func (s holdingService) CreateHolding(
	ctx context.Context, req *agentifiv1.CreateHoldingRequest,
) (*agentifiv1.CreateHoldingResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	symbol := strings.ToUpper(strings.TrimSpace(req.GetSymbol()))
	if symbol == "" {
		return nil, errInvalid("missing", []string{"body", "symbol"}, "symbol is required")
	}
	shares := decimal.Zero
	if raw := strings.TrimSpace(req.GetShares()); raw != "" {
		parsed, err := decimal.NewFromString(raw)
		if err != nil {
			return nil, errInvalid("decimal_parsing", []string{"body", "shares"},
				"shares must be a decimal number such as \"1.5\"")
		}
		shares = parsed
	}
	if shares.IsZero() {
		return nil, errInvalid("invalid", []string{"body", "shares"},
			"shares must not be zero; a position of none is no position")
	}
	var costBasis, marketValue *domain.Money
	if req.GetCostBasis() != nil {
		amount, err := moneyFrom(req.GetCostBasis(), "body", "cost_basis")
		if err != nil {
			return nil, err
		}
		costBasis = &amount
	}
	if req.GetMarketValue() != nil {
		amount, err := moneyFrom(req.GetMarketValue(), "body", "market_value")
		if err != nil {
			return nil, err
		}
		marketValue = &amount
	}
	accountID := uuid.Nil
	if raw := req.GetAccountId(); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, errInvalid("uuid_parsing", []string{"body", "account_id"}, "account_id must be a uuid")
		}
		accountID = parsed
	}
	account, err := requireAccount(ctx, env, sp, accountID)
	if err != nil {
		return nil, err
	}
	// A holding in a non-investment account would be counted twice.
	if account.Kind != domain.KindInvestment {
		return nil, errConflict("%s is not an investment account", account.Name)
	}

	// Looked up before anything is written, so a refusal leaves no security
	// row behind.
	security, err := env.DB.GetSecurityBySymbol(ctx, sp.ID(), symbol)
	found := err == nil
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	if !(found && security.HasLastPrice) && marketValue == nil {
		return nil, errInvalid("missing", []string{"body", "market_value"},
			"%s has no price on file, so say what the position is worth", symbol)
	}
	securityID := security.ID
	if !found {
		securityID, err = createSecurity(ctx, env, sp, symbol, req.GetName(), account.Currency)
		if err != nil {
			return nil, err
		}
	}

	holding := store.Holding{AccountID: account.ID, SecurityID: securityID, Shares: shares}
	if costBasis != nil {
		holding.CostBasis, holding.HasCostBasis = *costBasis, true
	}
	holding.IsComplete = holding.HasCostBasis
	if marketValue != nil {
		// Only ever read when there is no quote; a priced security is valued
		// from shares and its price on every read.
		holding.MarketValue, holding.HasMarketValue = *marketValue, true
	}
	added, err := env.DB.CreateHolding(ctx, sp.ID(), &holding)
	if err != nil {
		return nil, err
	}
	if !added {
		return nil, errConflict("%s already holds %s; edit that position instead", account.Name, symbol)
	}

	return &agentifiv1.CreateHoldingResponse{
		Id: holding.ID.String(), AccountId: account.ID.String(),
		SecurityId: securityID.String(), Symbol: symbol,
	}, nil
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

// DeleteHolding removes a position. A hard delete: a holding is a current
// share count; its history is the transactions, which stay.
func (s holdingService) DeleteHolding(
	ctx context.Context, req *agentifiv1.DeleteHoldingRequest,
) (*agentifiv1.DeleteHoldingResponse, error) {
	id, err := idFrom(req.GetHoldingId(), "Holding")
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteHolding(ctx, spaceFrom(ctx).ID(), id); err != nil {
		return nil, notFoundAs(err, "Holding")
	}
	return &agentifiv1.DeleteHoldingResponse{}, nil
}

// --- The wire ----------------------------------------------------------------

func securityProto(row store.Security) *agentifiv1.Security {
	out := &agentifiv1.Security{
		Id:         row.ID.String(),
		Symbol:     row.Symbol,
		Name:       row.Name,
		Kind:       row.Kind,
		Exchange:   dbconv.NullText(row.Exchange),
		Currency:   row.Currency,
		LastPrice:  rateProto(row.LastPrice, row.HasLastPrice),
		PriorClose: rateProto(row.PriorClose, row.HasPriorClose),
	}
	if row.LastPriceAt != nil {
		out.LastPriceAt = timestamppb.New(*row.LastPriceAt)
	}
	return out
}

func holdingProto(h valuedHolding) *agentifiv1.Holding {
	return &agentifiv1.Holding{
		Id:                  h.ID.String(),
		AccountId:           h.AccountID.String(),
		SecurityId:          h.SecurityID.String(),
		Symbol:              h.Symbol,
		Name:                h.Name,
		Shares:              h.Shares.String(),
		Price:               ratePtrProto(h.Price),
		Currency:            h.Currency,
		MarketValue:         moneyProto(h.MarketValue),
		IsUnquoted:          h.IsUnquoted,
		CostBasis:           moneyPtrProto(h.CostBasis),
		TotalGain:           moneyPtrProto(h.TotalGain),
		TotalGainPct:        ratePtrProto(h.TotalGainPct),
		IsCostBasisComplete: h.IsCostBasisComplete,
		DayChange:           moneyPtrProto(h.DayChange),
		DayChangePct:        ratePtrProto(h.DayChangePct),
		Share:               ratePtrProto(h.Share),
	}
}

func portfolioTotalsProto(t portfolioTotals) *agentifiv1.PortfolioTotals {
	return &agentifiv1.PortfolioTotals{
		MarketValue:           moneyProto(t.MarketValue),
		CostBasis:             moneyPtrProto(t.CostBasis),
		TotalGain:             moneyPtrProto(t.TotalGain),
		DayChange:             moneyPtrProto(t.DayChange),
		DayChangePct:          ratePtrProto(t.DayChangePct),
		IsCostBasisIncomplete: t.IsCostBasisIncomplete,
		IsDayChangeIncomplete: t.IsDayChangeIncomplete,
		AccountBalanceNotHeld: moneyProto(t.AccountBalanceNotHeld),
		TotalValue:            moneyProto(t.TotalValue),
	}
}

// moneyPtrProto and ratePtrProto carry a nil through as unset.
func moneyPtrProto(m *domain.Money) *agentifiv1.NullableMoney {
	if m == nil {
		return nil
	}
	return nullableMoneyProto(*m, true)
}

func ratePtrProto(r *domain.Rate) *string {
	if r == nil {
		return nil
	}
	return proto.String(r.String())
}

func idStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// accountFilterOf is the account_id selection a procedure was sent: unset is
// every account, an empty set none.
func accountFilterOf(set *agentifiv1.IdSet) (accountFilter, error) {
	if set == nil {
		return accountFilter{}, nil
	}
	var ids []uuid.UUID
	for _, raw := range set.GetIds() {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return accountFilter{}, errInvalid("uuid_parsing", []string{"query", "account_id"},
				"account_id must be a uuid")
		}
		ids = append(ids, id)
	}
	return accountFilter{IDs: ids, Given: true}, nil
}
