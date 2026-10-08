package api

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// SeriesService, OccurrenceService, CashFlowService and
// CashFlowForecastService over their own protocol. series_test.go and
// cashflowforecast_test.go reach the REST URLs the bridge still answers.

func createSeriesRPC(t *testing.T, c *client, l *ledger, req *agentifiv1.CreateSeriesRequest) *agentifiv1.Series {
	t.Helper()
	if req.AccountId == "" {
		req.AccountId = l.str("checking")
	}
	if req.Kind == "" {
		req.Kind = "bill"
	}
	if req.Amount == nil {
		req.Amount = &agentifiv1.Money{Amount: "-100.00"}
	}
	if req.StartOn == "" {
		req.StartOn = "2026-01-01"
	}
	if req.Recurrence == nil {
		req.Recurrence = &agentifiv1.SeriesRecurrenceWrite{Frequency: "MONTHLY", ByMonthDay: []int32{1}}
	}
	res, err := call[agentifiv1.CreateSeriesRequest, agentifiv1.CreateSeriesResponse](
		c, agentifiv1connect.SeriesServiceCreateSeriesProcedure, req)
	require.Nil(t, err)
	return res.GetSeries()
}

func updateSeriesRPC(t *testing.T, c *client, req *agentifiv1.UpdateSeriesRequest) *agentifiv1.Series {
	t.Helper()
	res, err := call[agentifiv1.UpdateSeriesRequest, agentifiv1.UpdateSeriesResponse](
		c, agentifiv1connect.SeriesServiceUpdateSeriesProcedure, req)
	require.Nil(t, err)
	return res.GetSeries()
}

func TestACreatedSeriesReadsBackWithItsLabelAndAmounts(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := createSeriesRPC(t, alex, l, &agentifiv1.CreateSeriesRequest{Description: "ACME CORP DES:PAYROLL"})
	require.Nil(t, created.DisplayName)
	require.Equal(t, "ACME CORP DES:PAYROLL", created.GetLabel())
	require.Equal(t, "-100.00", created.GetAmount().GetAmount())
	require.Nil(t, created.GetMatchAmountMin(), "no range is unset, not zero")
	require.Equal(t, "EVERY_MONTH", created.GetRecurrence().GetAlias())
	require.Equal(t, int32(12), created.GetOccurrencesPerYear())

	read, err := call[agentifiv1.GetSeriesRequest, agentifiv1.GetSeriesResponse](
		alex, agentifiv1connect.SeriesServiceGetSeriesProcedure, &agentifiv1.GetSeriesRequest{SeriesId: created.GetId()})
	require.Nil(t, err)
	require.Equal(t, created.GetId(), read.GetSeries().GetId())
	require.Equal(t, "-1200.00", read.GetSeries().GetAnnualizedAmount().GetAmount())
}

func TestASeriesUpdateLeavesAloneWhatTheMaskDoesNotName(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := createSeriesRPC(t, alex, l, &agentifiv1.CreateSeriesRequest{
		Description: "ACME BILL AUTOPAY", TagIds: []string{l.str("tag")},
	})
	mask := func(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

	// Set.
	series := updateSeriesRPC(t, alex, &agentifiv1.UpdateSeriesRequest{
		SeriesId: created.GetId(), DisplayName: proto.String("Power"), UpdateMask: mask("display_name"),
	})
	require.Equal(t, "Power", series.GetLabel())
	require.Equal(t, "ACME BILL AUTOPAY", series.GetDescription())
	require.Equal(t, []string{l.str("tag")}, series.GetTagIds(), "an unnamed list is left alone")

	// Absent: an amount change leaves the display name.
	series = updateSeriesRPC(t, alex, &agentifiv1.UpdateSeriesRequest{
		SeriesId: created.GetId(), Amount: &agentifiv1.NullableMoney{Amount: "-120.00"}, UpdateMask: mask("amount"),
	})
	require.Equal(t, "-120.00", series.GetAmount().GetAmount())
	require.Equal(t, "Power", series.GetDisplayName())

	// Cleared: named in the mask and unset, a list by naming it empty.
	series = updateSeriesRPC(t, alex, &agentifiv1.UpdateSeriesRequest{
		SeriesId: created.GetId(), UpdateMask: mask("display_name", "tag_ids"),
	})
	require.Nil(t, series.DisplayName)
	require.Equal(t, "ACME BILL AUTOPAY", series.GetLabel())
	require.Empty(t, series.GetTagIds())

	// Without a mask, the fields set are the change.
	series = updateSeriesRPC(t, alex, &agentifiv1.UpdateSeriesRequest{
		SeriesId: created.GetId(), TagIds: []string{l.str("tag")},
	})
	require.Equal(t, []string{l.str("tag")}, series.GetTagIds())
	require.Equal(t, "-120.00", series.GetAmount().GetAmount())
}

func TestClearingASeriesAccountIsAConflict(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := createSeriesRPC(t, alex, l, &agentifiv1.CreateSeriesRequest{Description: "ACME BILL AUTOPAY"})
	alex.rpc(agentifiv1connect.SeriesServiceUpdateSeriesProcedure, map[string]any{
		"series_id": created.GetId(), "update_mask": "accountId",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)
}

func TestAnotherSpacesSeriesIsNotFound(t *testing.T) {
	l := buildLedger(t)
	created := createSeriesRPC(t, frozenClient(l, "alex"), l, &agentifiv1.CreateSeriesRequest{Description: "ACME BILL AUTOPAY"})
	bob := l.as("bob").inSpace(store.SpaceIDOf(l.id("other_space")))
	for _, id := range []string{created.GetId(), "not-an-id"} {
		_, err := call[agentifiv1.GetSeriesRequest, agentifiv1.GetSeriesResponse](
			bob, agentifiv1connect.SeriesServiceGetSeriesProcedure, &agentifiv1.GetSeriesRequest{SeriesId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Series not found", err.Message())
	}
}

func TestAViewerReadsSeriesButCannotWriteOne(t *testing.T) {
	l := buildLedger(t)
	vera := frozenClient(l, "vera")
	_, err := call[agentifiv1.CreateSeriesRequest, agentifiv1.CreateSeriesResponse](
		vera, agentifiv1connect.SeriesServiceCreateSeriesProcedure,
		&agentifiv1.CreateSeriesRequest{AccountId: l.str("checking"), Kind: "bill", Description: "MINE"})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())

	_, err = call[agentifiv1.ListSeriesRequest, agentifiv1.ListSeriesResponse](
		vera, agentifiv1connect.SeriesServiceListSeriesProcedure, &agentifiv1.ListSeriesRequest{})
	require.Nil(t, err)
}

func TestAnAcceptedOccurrenceAnswersItsChargeAndHoldsItsSlot(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := createSeriesRPC(t, alex, l, &agentifiv1.CreateSeriesRequest{Description: "ACME BILL AUTOPAY"})
	accept := &agentifiv1.AcceptOccurrenceRequest{SeriesId: created.GetId(), DueOn: "2026-09-01"}

	res, err := call[agentifiv1.AcceptOccurrenceRequest, agentifiv1.AcceptOccurrenceResponse](
		alex, agentifiv1connect.OccurrenceServiceAcceptOccurrenceProcedure, accept)
	require.Nil(t, err)
	charge := res.GetTransaction().AsMap()
	require.Equal(t, created.GetId(), charge["series_id"])
	require.Equal(t, "-100.00", charge["amount"])
	require.Equal(t, "2026-09-01", charge["date"])

	_, err = call[agentifiv1.AcceptOccurrenceRequest, agentifiv1.AcceptOccurrenceResponse](
		alex, agentifiv1connect.OccurrenceServiceAcceptOccurrenceProcedure, accept)
	require.Equal(t, connect.CodeFailedPrecondition, err.Code(), "one charge per slot")
}

func TestOccurrencesEchoTheWindowTheyWereExpandedOver(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := createSeriesRPC(t, alex, l, &agentifiv1.CreateSeriesRequest{Description: "ACME BILL AUTOPAY"})
	res, err := call[agentifiv1.ListOccurrencesRequest, agentifiv1.ListOccurrencesResponse](
		alex, agentifiv1connect.OccurrenceServiceListOccurrencesProcedure,
		&agentifiv1.ListOccurrencesRequest{From: "2026-09-01", To: "2026-11-30"})
	require.Nil(t, err)
	require.Equal(t, "2026-09-01", res.GetWindow().GetFrom())
	require.Equal(t, "2026-11-30", res.GetWindow().GetTo())
	require.Len(t, res.GetItems(), 3)
	require.Equal(t, created.GetId(), res.GetItems()[0].GetSeriesId())
	require.Equal(t, "-300.00", res.GetSummary().GetExpenses().GetAmount())
	require.Equal(t, int32(3), res.GetSummary().GetCount())
}

func TestAnEmptyAccountSelectionProjectsNothing(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	every, err := call[agentifiv1.GetCashFlowRequest, agentifiv1.GetCashFlowResponse](
		alex, agentifiv1connect.CashFlowServiceGetCashFlowProcedure, &agentifiv1.GetCashFlowRequest{})
	require.Nil(t, err)
	require.NotEmpty(t, every.GetAccounts())
	require.Nil(t, every.GetWindow().From, "an open window is echoed open")

	none, err := call[agentifiv1.GetCashFlowRequest, agentifiv1.GetCashFlowResponse](
		alex, agentifiv1connect.CashFlowServiceGetCashFlowProcedure,
		&agentifiv1.GetCashFlowRequest{AccountId: &agentifiv1.IdSet{}})
	require.Nil(t, err)
	require.Empty(t, none.GetAccounts())
	require.Equal(t, "0.00", none.GetThreshold().GetAmount())
}

func TestNoForecastIsAnAnswerWithAReason(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.GetCashFlowForecastRequest, agentifiv1.GetCashFlowForecastResponse](
		l.alex, agentifiv1connect.CashFlowForecastServiceGetCashFlowForecastProcedure,
		&agentifiv1.GetCashFlowForecastRequest{})
	require.Nil(t, err)
	require.False(t, res.GetForecast().GetAvailable())
	require.Contains(t, res.GetForecast().GetUnavailable(), "No forecast has been made yet")
	require.Nil(t, res.GetForecast().GetGeneratedAt())

	l.alex.rpc(agentifiv1connect.CashFlowForecastServiceRunCashFlowForecastProcedure, `{}`).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusBadRequest)
}
