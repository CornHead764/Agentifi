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

// ReportService over its own protocol. reports_test.go and
// spendingreport_test.go reach the same methods through the REST bridge.

func TestARunReportDefaultsToTheEffectiveDateAndEchoesIt(t *testing.T) {
	l := buildReportsLedger(t)
	res, err := call[agentifiv1.RunReportRequest, agentifiv1.RunReportResponse](
		l.alex, agentifiv1connect.ReportServiceRunReportProcedure, &agentifiv1.RunReportRequest{
			From: "2026-08-01", To: "2026-08-31", Mode: "summary", Sign: "expenses",
		})
	require.Nil(t, err)
	require.Equal(t, "effective", res.GetWindow().GetDateField())
	require.Equal(t, "2026-08-01", res.GetWindow().GetFrom())
	require.Nil(t, res.GetTransaction())
	require.Equal(t, res.GetTotals().GetExpenses().GetAmount(), res.GetSummary().GetTotal().GetAmount())
	require.Nil(t, res.FilterId)
}

func TestAnUnknownReportKnobIsInvalidArgument(t *testing.T) {
	l := buildReportsLedger(t)
	_, err := call[agentifiv1.RunReportRequest, agentifiv1.RunReportResponse](
		l.alex, agentifiv1connect.ReportServiceRunReportProcedure, &agentifiv1.RunReportRequest{Rows: "weather"})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"query", "rows"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func createSavedReportRPC(t *testing.T, l *ledger) *agentifiv1.SavedReport {
	t.Helper()
	res, err := call[agentifiv1.CreateSavedReportRequest, agentifiv1.CreateSavedReportResponse](
		l.alex, agentifiv1connect.ReportServiceCreateSavedReportProcedure, &agentifiv1.CreateSavedReportRequest{
			Name:   "Grocery spending",
			Config: &agentifiv1.ReportConfig{Mode: "transaction", Sign: "expenses"},
			Items: []*agentifiv1.FilterItemWrite{{
				Field: "category", Operator: "in", ValueIds: []string{l.str("groceries")},
				AmountMin: &agentifiv1.NullableMoney{Amount: "-500.00"},
			}},
			QueryText: proto.String("groceries"),
		})
	require.Nil(t, err)
	return res.GetReport()
}

func TestASavedReportKeepsItsFilterAndItsText(t *testing.T) {
	l := buildReportsLedger(t)
	report := createSavedReportRPC(t, l)
	require.Equal(t, "category", report.GetConfig().GetRows(), "the shell's defaults are filled in")
	require.Equal(t, "groceries", report.GetFilter().GetQueryText())
	require.Len(t, report.GetFilter().GetItems(), 1)
	item := report.GetFilter().GetItems()[0]
	require.Equal(t, []string{l.str("groceries")}, item.GetValueIds())
	require.Equal(t, "-500.00", item.GetAmountMin().GetAmount())
	require.Nil(t, item.GetAmountMax())

	read, err := call[agentifiv1.GetSavedReportRequest, agentifiv1.GetSavedReportResponse](
		l.alex, agentifiv1connect.ReportServiceGetSavedReportProcedure,
		&agentifiv1.GetSavedReportRequest{ReportId: report.GetId()})
	require.Nil(t, err)
	require.True(t, proto.Equal(report, read.GetReport()))
}

func TestASavedReportUpdateChangesOnlyWhatTheMaskNames(t *testing.T) {
	l := buildReportsLedger(t)
	report := createSavedReportRPC(t, l)
	update := func(req *agentifiv1.UpdateSavedReportRequest) *agentifiv1.SavedReport {
		t.Helper()
		req.ReportId = report.GetId()
		res, err := call[agentifiv1.UpdateSavedReportRequest, agentifiv1.UpdateSavedReportResponse](
			l.alex, agentifiv1connect.ReportServiceUpdateSavedReportProcedure, req)
		require.Nil(t, err)
		return res.GetReport()
	}

	// Absent: a rename leaves the shell and the items alone.
	renamed := update(&agentifiv1.UpdateSavedReportRequest{
		Name: proto.String("Food"), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.Equal(t, "Food", renamed.GetName())
	require.Equal(t, "transaction", renamed.GetConfig().GetMode())
	require.Len(t, renamed.GetFilter().GetItems(), 1)

	// Set.
	pivot := update(&agentifiv1.UpdateSavedReportRequest{
		Config:     &agentifiv1.ReportConfig{Mode: "summary", Sign: "expenses"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"config"}},
	})
	require.Equal(t, "summary", pivot.GetConfig().GetMode())
	require.Equal(t, "groceries", pivot.GetFilter().GetQueryText(), "the free text rides along")

	// Cleared: items named and empty is no items. A config named and unset is
	// left as it was.
	cleared := update(&agentifiv1.UpdateSavedReportRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"items", "config"}},
	})
	require.Empty(t, cleared.GetFilter().GetItems())
	require.Equal(t, "summary", cleared.GetConfig().GetMode())
	require.Equal(t, "Food", cleared.GetName())
}

func TestASavedReportsNameCannotBeClearedOrLeftOutOfTheMask(t *testing.T) {
	l := buildReportsLedger(t)
	report := createSavedReportRPC(t, l)

	l.alex.rpc(agentifiv1connect.ReportServiceUpdateSavedReportProcedure, map[string]any{
		"report_id": report.GetId(), "update_mask": "name",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)

	l.alex.rpc(agentifiv1connect.ReportServiceUpdateSavedReportProcedure, map[string]any{
		"report_id": report.GetId(), "name": "Renamed", "update_mask": "config",
	}).requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)

	l.alex.rpc(agentifiv1connect.ReportServiceUpdateSavedReportProcedure, map[string]any{
		"report_id": report.GetId(), "update_mask": "filter",
	}).requireCode(connect.CodeInvalidArgument)
}

func TestAnotherSpacesSavedReportIsNotFound(t *testing.T) {
	l := buildReportsLedger(t)
	stranger := &store.Filter{Name: "Theirs", Scope: reportScope}
	require.NoError(t, db(t).CreateFilter(t.Context(), store.SpaceIDOf(l.id("other_space")), stranger))

	for _, id := range []string{stranger.ID.String(), l.str("filter"), "not-an-id"} {
		_, err := call[agentifiv1.GetSavedReportRequest, agentifiv1.GetSavedReportResponse](
			l.alex, agentifiv1connect.ReportServiceGetSavedReportProcedure,
			&agentifiv1.GetSavedReportRequest{ReportId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Report not found", err.Message())
	}
	_, err := call[agentifiv1.DeleteSavedReportRequest, agentifiv1.DeleteSavedReportResponse](
		l.alex, agentifiv1connect.ReportServiceDeleteSavedReportProcedure,
		&agentifiv1.DeleteSavedReportRequest{ReportId: stranger.ID.String()})
	require.Equal(t, connect.CodeNotFound, err.Code())
}

func TestAViewerRunsReportsButCannotSaveOne(t *testing.T) {
	l := buildReportsLedger(t)
	vera := l.as("vera")
	_, err := call[agentifiv1.GetMonthlySummaryRequest, agentifiv1.GetMonthlySummaryResponse](
		vera, agentifiv1connect.ReportServiceGetMonthlySummaryProcedure,
		&agentifiv1.GetMonthlySummaryRequest{Month: "2026-08"})
	require.Nil(t, err)

	_, err = call[agentifiv1.CreateSavedReportRequest, agentifiv1.CreateSavedReportResponse](
		vera, agentifiv1connect.ReportServiceCreateSavedReportProcedure,
		&agentifiv1.CreateSavedReportRequest{Name: "Mine"})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())
}

func TestASpendingReportOverNoAccountsIsEmptyNotEverything(t *testing.T) {
	l := spendingReportLedger(t)
	spending := func(accounts *agentifiv1.IdSet) *agentifiv1.GetSpendingReportResponse {
		t.Helper()
		res, err := call[agentifiv1.GetSpendingReportRequest, agentifiv1.GetSpendingReportResponse](
			l.alex, agentifiv1connect.ReportServiceGetSpendingReportProcedure,
			&agentifiv1.GetSpendingReportRequest{Period: "2026-08-01", AccountId: accounts})
		require.Nil(t, err)
		return res
	}

	everything := spending(nil)
	require.NotEmpty(t, everything.GetRows())
	require.Equal(t, "2026-08-01", everything.GetWindow().GetFrom())
	require.Equal(t, "2026-08-31", everything.GetWindow().GetTo())

	nothing := spending(&agentifiv1.IdSet{})
	require.Empty(t, nothing.GetRows())
	require.Equal(t, "0.00", nothing.GetSummary().GetSpent().GetAmount())
}

func TestAMonthThatIsNotAMonthIsRefused(t *testing.T) {
	l := buildReportsLedger(t)
	l.alex.rpc(agentifiv1connect.ReportServiceGetMonthlySummaryProcedure, map[string]any{"month": "August"}).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
}
