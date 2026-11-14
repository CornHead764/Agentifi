package api

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
)

func TestReviewServiceOverTheWire(t *testing.T) {
	l := buildLedger(t)
	l.env.Now = func() time.Time { return time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC) }

	summary, err := call[agentifiv1.GetReviewSummaryRequest, agentifiv1.GetReviewSummaryResponse](
		l.alex, agentifiv1connect.ReviewServiceGetReviewSummaryProcedure, &agentifiv1.GetReviewSummaryRequest{})
	require.Nil(t, err)
	require.Equal(t, summary.GetActNow()+summary.GetMoneyAtStake()+summary.GetTidyUp()+summary.GetNeedsALook(), summary.GetBadge())

	// One day a page: the newest is the card charge's effective date.
	first, err := call[agentifiv1.ListReviewDaysRequest, agentifiv1.ListReviewDaysResponse](
		l.alex, agentifiv1connect.ReviewServiceListReviewDaysProcedure, &agentifiv1.ListReviewDaysRequest{Days: 1})
	require.Nil(t, err)
	require.Len(t, first.GetDays(), 1)
	require.Equal(t, "2026-09-10", first.GetDays()[0].GetDate())
	require.Equal(t, "75.00", first.GetDays()[0].GetOut().GetAmount())
	require.Equal(t, agentifiv1.ReviewReason_REVIEW_REASON_NO_CATEGORY, first.GetDays()[0].GetRows()[0].GetReason())
	require.Equal(t, "2026-09-10", first.GetNextBefore())

	second, err := call[agentifiv1.ListReviewDaysRequest, agentifiv1.ListReviewDaysResponse](
		l.alex, agentifiv1connect.ReviewServiceListReviewDaysProcedure,
		&agentifiv1.ListReviewDaysRequest{Days: 1, Before: first.NextBefore})
	require.Nil(t, err)
	require.True(t, second.GetDays()[0].GetDate() < "2026-09-10")

	approved, err := call[agentifiv1.ApproveRowsRequest, agentifiv1.ApproveRowsResponse](
		l.alex, agentifiv1connect.ReviewServiceApproveRowsProcedure,
		&agentifiv1.ApproveRowsRequest{TransactionIds: []string{l.str("august_groceries")}})
	require.Nil(t, err)
	require.Equal(t, []string{l.str("august_groceries")}, approved.GetApprovedIds())
	require.NotEmpty(t, approved.GetUndoToken())

	_, refused := call[agentifiv1.ApproveRowsRequest, agentifiv1.ApproveRowsResponse](
		l.as("vera"), agentifiv1connect.ReviewServiceApproveRowsProcedure,
		&agentifiv1.ApproveRowsRequest{TransactionIds: []string{l.str("july")}})
	require.NotNil(t, refused)
	require.Equal(t, connect.CodePermissionDenied, refused.Code())
}

func TestReviewSummaryOverREST(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.get("/review/summary").requireStatus(200).json()
	require.Contains(t, body, "badge")
}
