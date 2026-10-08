package api

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The investing services over their own protocol: holdings, securities,
// performance, activity, news and the retirement projection. The REST suites
// (investments_test.go, investmentdetail_test.go, news_test.go,
// planning_test.go) reach the same methods through the bridge.

func listHoldingsRPC(t *testing.T, c *client, accounts *agentifiv1.IdSet) *agentifiv1.ListHoldingsResponse {
	t.Helper()
	res, err := call[agentifiv1.ListHoldingsRequest, agentifiv1.ListHoldingsResponse](
		c, agentifiv1connect.HoldingServiceListHoldingsProcedure, &agentifiv1.ListHoldingsRequest{AccountId: accounts})
	require.Nil(t, err)
	return res
}

func TestAnUnknownBasisIsUnsetNotZeroOverConnect(t *testing.T) {
	l := buildPortfolioLedger(t)
	res := listHoldingsRPC(t, l.alex, nil)
	require.Len(t, res.GetItems(), 2)

	bbb := res.GetItems()[1]
	require.Equal(t, "BBB", bbb.GetSymbol())
	require.Nil(t, bbb.GetCostBasis())
	require.Nil(t, bbb.GetTotalGain())
	require.Nil(t, bbb.TotalGainPct)
	require.False(t, bbb.GetIsCostBasisComplete())
	require.Nil(t, bbb.GetDayChange())

	aaa := res.GetItems()[0]
	require.Equal(t, "600.00", aaa.GetCostBasis().GetAmount())
	require.Equal(t, "100", aaa.GetPrice())

	require.True(t, res.GetTotals().GetIsCostBasisIncomplete())
	require.Equal(t, "2000.00", res.GetTotals().GetMarketValue().GetAmount())
}

func TestAnEmptyAccountSelectionIsNoAccountsNotEvery(t *testing.T) {
	l := buildPortfolioLedger(t)
	none := listHoldingsRPC(t, l.alex, &agentifiv1.IdSet{})
	require.Empty(t, none.GetItems())
	require.Equal(t, "0.00", none.GetTotals().GetTotalValue().GetAmount())

	ira := listHoldingsRPC(t, l.alex, &agentifiv1.IdSet{Ids: []string{l.str("ira")}})
	require.Empty(t, ira.GetItems())
	require.Equal(t, "4000.00", ira.GetTotals().GetTotalValue().GetAmount())

	l.alex.rpc(agentifiv1connect.HoldingServiceListHoldingsProcedure,
		map[string]any{"account_id": map[string]any{"ids": []string{"nope"}}}).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
}

func TestAPositionAddedByHandReadsBackAndGoes(t *testing.T) {
	l := buildPortfolioLedger(t)
	created, err := call[agentifiv1.CreateHoldingRequest, agentifiv1.CreateHoldingResponse](
		l.alex, agentifiv1connect.HoldingServiceCreateHoldingProcedure, &agentifiv1.CreateHoldingRequest{
			AccountId: l.str("ira"), Symbol: "zzz", Shares: "3",
			MarketValue: &agentifiv1.NullableMoney{Amount: "750.00"},
		})
	require.Nil(t, err)
	require.Equal(t, "ZZZ", created.GetSymbol())

	ira := listHoldingsRPC(t, l.alex, &agentifiv1.IdSet{Ids: []string{l.str("ira")}})
	require.Len(t, ira.GetItems(), 1)
	require.True(t, ira.GetItems()[0].GetIsUnquoted())
	require.Nil(t, ira.GetItems()[0].Price)

	_, err = call[agentifiv1.DeleteHoldingRequest, agentifiv1.DeleteHoldingResponse](
		l.alex, agentifiv1connect.HoldingServiceDeleteHoldingProcedure,
		&agentifiv1.DeleteHoldingRequest{HoldingId: created.GetId()})
	require.Nil(t, err)
	_, err = call[agentifiv1.DeleteHoldingRequest, agentifiv1.DeleteHoldingResponse](
		l.alex, agentifiv1connect.HoldingServiceDeleteHoldingProcedure,
		&agentifiv1.DeleteHoldingRequest{HoldingId: created.GetId()})
	require.Equal(t, connect.CodeNotFound, err.Code())
}

func TestAPositionWithNoPriceAndNoValueIsRefusedByField(t *testing.T) {
	l := buildPortfolioLedger(t)
	_, err := call[agentifiv1.CreateHoldingRequest, agentifiv1.CreateHoldingResponse](
		l.alex, agentifiv1connect.HoldingServiceCreateHoldingProcedure, &agentifiv1.CreateHoldingRequest{
			AccountId: l.str("ira"), Symbol: "NEW", Shares: "1",
		})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "market_value"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestAViewerReadsInvestmentsButCannotAddAPosition(t *testing.T) {
	l := buildPortfolioLedger(t)
	vera := l.as("vera")
	listHoldingsRPC(t, vera, nil)

	_, err := call[agentifiv1.CreateHoldingRequest, agentifiv1.CreateHoldingResponse](
		vera, agentifiv1connect.HoldingServiceCreateHoldingProcedure, &agentifiv1.CreateHoldingRequest{
			AccountId: l.str("ira"), Symbol: "AAA", Shares: "5",
		})
	require.Equal(t, connect.CodePermissionDenied, err.Code())

	_, err = call[agentifiv1.RefreshSecurityPricesRequest, agentifiv1.RefreshSecurityPricesResponse](
		vera, agentifiv1connect.SecurityServiceRefreshSecurityPricesProcedure, &agentifiv1.RefreshSecurityPricesRequest{})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}

func TestAnotherSpacesSecurityIsNotFound(t *testing.T) {
	l := buildPortfolioLedger(t)
	theirs := seedSecurity(t, store.SpaceIDOf(l.id("other_space")), "NOPE", stringPtrOf("1.00"), nil)
	for _, id := range []string{theirs.String(), "not-an-id"} {
		_, err := call[agentifiv1.GetSecurityRequest, agentifiv1.GetSecurityResponse](
			l.alex, agentifiv1connect.SecurityServiceGetSecurityProcedure, &agentifiv1.GetSecurityRequest{SecurityId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Security not found", err.Message())
	}
}

func TestOneSecurityIsItsPositionsAndItsQuote(t *testing.T) {
	l := buildPortfolioLedger(t)
	res, err := call[agentifiv1.GetSecurityRequest, agentifiv1.GetSecurityResponse](
		l.alex, agentifiv1connect.SecurityServiceGetSecurityProcedure, &agentifiv1.GetSecurityRequest{
			SecurityId: l.str("bbb"), From: "2026-01-01", To: "2026-06-30",
		})
	require.Nil(t, err)
	require.Equal(t, "BBB", res.GetSecurity().GetSymbol())
	require.Equal(t, "50", res.GetSecurity().GetLastPrice())
	require.Nil(t, res.GetSecurity().PriorClose)
	require.NotNil(t, res.GetSecurity().GetLastPriceAt())
	require.Len(t, res.GetPositions(), 1)
	require.Equal(t, "20", res.GetShares())
	require.True(t, res.GetIsCostBasisIncomplete())
	require.Nil(t, res.GetCostBasis())
	require.Equal(t, "2026-06-30", res.GetWindow().GetTo())
	require.Empty(t, res.GetPrices())
	require.Nil(t, res.PriceChange)
}

func TestPerformanceAndActivityEchoTheirWindow(t *testing.T) {
	l := buildPortfolioLedger(t)
	performance, err := call[agentifiv1.GetPerformanceRequest, agentifiv1.GetPerformanceResponse](
		l.alex, agentifiv1connect.PerformanceServiceGetPerformanceProcedure, &agentifiv1.GetPerformanceRequest{
			From: "2026-08-01", To: "2026-08-31", Granularity: "week",
		})
	require.Nil(t, err)
	require.Equal(t, "2026-08-01", performance.GetWindow().GetFrom())
	require.Equal(t, "posted", performance.GetWindow().GetDateField())
	require.Equal(t, "week", performance.GetGranularity())
	require.ElementsMatch(t, []string{l.str("brokerage"), l.str("ira")}, performance.GetAccountIds())

	activity, err := call[agentifiv1.ListInvestmentActivityRequest, agentifiv1.ListInvestmentActivityResponse](
		l.alex, agentifiv1connect.InvestmentActivityServiceListInvestmentActivityProcedure,
		&agentifiv1.ListInvestmentActivityRequest{From: "2026-08-01", To: "2026-08-31", AccountId: &agentifiv1.IdSet{}})
	require.Nil(t, err)
	require.Equal(t, "2026-08-31", activity.GetWindow().GetTo())
	require.Empty(t, activity.GetAccountIds())
	require.Equal(t, "0.00", activity.GetFees().GetAmount())

	l.alex.rpc(agentifiv1connect.PerformanceServiceGetPerformanceProcedure, map[string]any{"granularity": "hourly"}).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
}

func TestAThrottledNewsSourceIsAnAnswerNotAnError(t *testing.T) {
	l := buildPortfolioLedger(t)
	c := newsClient(l, &fakeNews{err: provider.ErrRateLimited})
	res, err := call[agentifiv1.ListNewsRequest, agentifiv1.ListNewsResponse](
		c, agentifiv1connect.NewsServiceListNewsProcedure, &agentifiv1.ListNewsRequest{})
	require.Nil(t, err)
	require.True(t, res.GetAvailable())
	require.Contains(t, res.GetUnavailable(), "busy")
	require.ElementsMatch(t, []string{"AAA", "BBB"}, res.GetSymbols())

	published := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	c = newsClient(l, &fakeNews{items: []provider.NewsItem{
		{ID: "one", Title: "Dated", PublishedAt: published},
		{ID: "two", Title: "Undated"},
	}})
	res, err = call[agentifiv1.ListNewsRequest, agentifiv1.ListNewsResponse](
		c, agentifiv1connect.NewsServiceListNewsProcedure, &agentifiv1.ListNewsRequest{})
	require.Nil(t, err)
	require.True(t, published.Equal(res.GetItems()[0].GetPublishedAt().AsTime()))
	require.Nil(t, res.GetItems()[1].GetPublishedAt())
	require.Empty(t, res.GetItems()[1].GetSymbols())
}

func TestTheRetirementVerdictIsUnsetWithoutATarget(t *testing.T) {
	l := buildPortfolioLedger(t)
	project := func(req *agentifiv1.GetRetirementProjectionRequest) *agentifiv1.GetRetirementProjectionResponse {
		t.Helper()
		res, err := call[agentifiv1.GetRetirementProjectionRequest, agentifiv1.GetRetirementProjectionResponse](
			l.alex, agentifiv1connect.PlanningServiceGetRetirementProjectionProcedure, req)
		require.Nil(t, err)
		return res
	}

	basic := project(&agentifiv1.GetRetirementProjectionRequest{})
	require.Nil(t, basic.MeetsTarget)
	require.Nil(t, basic.GetShortfall())
	require.Nil(t, basic.GetAssumptions().GetTargetAnnualIncome())
	require.Nil(t, basic.GetAssumptions().GetAdvanced())
	require.True(t, basic.GetAssumptions().GetIsBalanceFromAccounts())
	require.Equal(t, int32(35), basic.GetAssumptions().GetCurrentAge())
	require.Equal(t, "0.07", basic.GetAssumptions().GetAnnualReturn())

	targeted := project(&agentifiv1.GetRetirementProjectionRequest{
		CurrentAge:         proto.Int32(60),
		TargetAnnualIncome: &agentifiv1.NullableMoney{Amount: "1000000.00"},
		CurrentBalance:     &agentifiv1.NullableMoney{Amount: "1000.00"},
	})
	require.NotNil(t, targeted.MeetsTarget)
	require.False(t, targeted.GetMeetsTarget())
	require.NotNil(t, targeted.GetShortfall())
	require.Equal(t, "1000000.00", targeted.GetAssumptions().GetTargetAnnualIncome().GetAmount())
	require.False(t, targeted.GetAssumptions().GetIsBalanceFromAccounts())

	advanced := project(&agentifiv1.GetRetirementProjectionRequest{Mode: "advanced"})
	require.NotNil(t, advanced.GetAssumptions().GetAdvanced())
	require.True(t, advanced.GetAssumptions().GetAdvanced().GetIsBalanceFromAccounts())
}

func TestARetirementAgeOutsideALifespanIsRefusedByName(t *testing.T) {
	l := buildPortfolioLedger(t)
	_, err := call[agentifiv1.GetRetirementProjectionRequest, agentifiv1.GetRetirementProjectionResponse](
		l.alex, agentifiv1connect.PlanningServiceGetRetirementProjectionProcedure,
		&agentifiv1.GetRetirementProjectionRequest{RetirementAge: proto.Int32(200)})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	field := problemIn(t, err).GetFields()[0]
	require.Equal(t, []string{"query", "retirement_age"}, field.GetLoc())
	require.Equal(t, "out_of_range", field.GetType())
}
