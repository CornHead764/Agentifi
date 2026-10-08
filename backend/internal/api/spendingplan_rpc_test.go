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
)

// SpendingPlanService over its own protocol. spendingplan_test.go reaches the
// REST URLs the bridge still answers.

func planBucketRPC(t *testing.T, month *agentifiv1.SpendingPlanMonth, key string) *agentifiv1.SpendingPlanBucket {
	t.Helper()
	for _, bucket := range month.GetBuckets() {
		if bucket.GetKey() == key {
			return bucket
		}
	}
	t.Fatalf("the month has no %s bucket", key)
	return nil
}

func TestAPlanMonthReadsAsItsOwnMessage(t *testing.T) {
	l := planLedger(t)
	res, err := call[agentifiv1.GetSpendingPlanMonthRequest, agentifiv1.GetSpendingPlanMonthResponse](
		l.alex, agentifiv1connect.SpendingPlanServiceGetSpendingPlanMonthProcedure,
		&agentifiv1.GetSpendingPlanMonthRequest{Month: augustMonth})
	require.Nil(t, err)
	month := res.GetMonth()
	require.Equal(t, augustMonth, month.GetMonth())
	require.Equal(t, "2026-08-20", month.GetAsOf(), "the server's today, not the client's")
	require.Equal(t, "-75.00", planBucketRPC(t, month, "other_spend").GetEffectiveAmount().GetAmount())
	require.Nil(t, planBucketRPC(t, month, "other_spend").GetOverwrittenAmount())
	require.NotNil(t, month.GetContestedTxnIds(), "an empty map, never null")
}

func TestAMalformedMonthIsInvalid(t *testing.T) {
	l := planLedger(t)
	l.alex.rpc(agentifiv1connect.SpendingPlanServiceGetSpendingPlanMonthProcedure, map[string]any{"month": "August"}).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
}

func TestABucketOverrideIsSetAndClearedThroughTheMask(t *testing.T) {
	l := planLedger(t)
	update := func(req *agentifiv1.UpdateSpendingPlanBucketRequest) *agentifiv1.SpendingPlanBucket {
		t.Helper()
		req.Month, req.Bucket = augustMonth, "other_spend"
		res, err := call[agentifiv1.UpdateSpendingPlanBucketRequest, agentifiv1.UpdateSpendingPlanBucketResponse](
			l.alex, agentifiv1connect.SpendingPlanServiceUpdateSpendingPlanBucketProcedure, req)
		require.Nil(t, err)
		return planBucketRPC(t, res.GetMonth(), "other_spend")
	}

	bucket := update(&agentifiv1.UpdateSpendingPlanBucketRequest{
		OverwrittenAmount: &agentifiv1.NullableMoney{Amount: "-200.00"},
	})
	require.Equal(t, "-200.00", bucket.GetOverwrittenAmount().GetAmount())
	require.Equal(t, "-200.00", bucket.GetEffectiveAmount().GetAmount())
	require.Equal(t, "-75.00", bucket.GetCalculatedAmount().GetAmount())

	bucket = update(&agentifiv1.UpdateSpendingPlanBucketRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"overwritten_amount"}},
	})
	require.Nil(t, bucket.GetOverwrittenAmount())
	require.Equal(t, "-75.00", bucket.GetEffectiveAmount().GetAmount())

	// Neither set nor named asks for nothing, which is refused.
	l.alex.rpc(agentifiv1connect.SpendingPlanServiceUpdateSpendingPlanBucketProcedure,
		map[string]any{"month": augustMonth, "bucket": "other_spend"}).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
}

func TestAnEnvelopeUpdateLeavesAloneWhatTheMaskDoesNotName(t *testing.T) {
	l := planLedger(t)
	created, err := call[agentifiv1.CreateSpendingPlanEnvelopeRequest, agentifiv1.CreateSpendingPlanEnvelopeResponse](
		l.alex, agentifiv1connect.SpendingPlanServiceCreateSpendingPlanEnvelopeProcedure,
		&agentifiv1.CreateSpendingPlanEnvelopeRequest{
			Month: augustMonth, Name: "Groceries", TargetAmount: &agentifiv1.Money{Amount: "300.00"},
			CategoryIds: []string{l.str("groceries")},
		})
	require.Nil(t, err)
	require.Len(t, created.GetMonth().GetEnvelopes(), 1)
	envelopeID := created.GetMonth().GetEnvelopes()[0].GetId()

	update := func(req *agentifiv1.UpdateSpendingPlanEnvelopeRequest) *agentifiv1.SpendingPlanEnvelope {
		t.Helper()
		req.Month, req.EnvelopeId = augustMonth, envelopeID
		res, err := call[agentifiv1.UpdateSpendingPlanEnvelopeRequest, agentifiv1.UpdateSpendingPlanEnvelopeResponse](
			l.alex, agentifiv1connect.SpendingPlanServiceUpdateSpendingPlanEnvelopeProcedure, req)
		require.Nil(t, err)
		return res.GetMonth().GetEnvelopes()[0]
	}

	// Set.
	envelope := update(&agentifiv1.UpdateSpendingPlanEnvelopeRequest{
		OverwrittenTargetAmount: &agentifiv1.NullableMoney{Amount: "250.00"},
		UpdateMask:              &fieldmaskpb.FieldMask{Paths: []string{"overwritten_target_amount"}},
	})
	require.Equal(t, "250.00", envelope.GetOverwrittenTargetAmount().GetAmount())
	require.Equal(t, "250.00", envelope.GetTarget().GetAmount())

	// Absent: a rename leaves the override and the categories.
	envelope = update(&agentifiv1.UpdateSpendingPlanEnvelopeRequest{
		Name: proto.String("Food"), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.Equal(t, "Food", envelope.GetName())
	require.Equal(t, "250.00", envelope.GetOverwrittenTargetAmount().GetAmount())
	require.Len(t, envelope.GetCategories(), 1)

	// Cleared.
	envelope = update(&agentifiv1.UpdateSpendingPlanEnvelopeRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"overwritten_target_amount"}},
	})
	require.Nil(t, envelope.GetOverwrittenTargetAmount())
	require.Equal(t, "300.00", envelope.GetTarget().GetAmount())

	// A list named empty is refused: an envelope with no categories would
	// claim the whole ledger.
	l.alex.rpc(agentifiv1connect.SpendingPlanServiceUpdateSpendingPlanEnvelopeProcedure, map[string]any{
		"month": augustMonth, "envelope_id": envelopeID, "update_mask": "categoryIds",
	}).requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
}

func TestAViewerReadsThePlanButCannotChangeIt(t *testing.T) {
	l := planLedger(t)
	vera := l.as("vera")
	_, err := call[agentifiv1.GetSpendingPlanMonthRequest, agentifiv1.GetSpendingPlanMonthResponse](
		vera, agentifiv1connect.SpendingPlanServiceGetSpendingPlanMonthProcedure,
		&agentifiv1.GetSpendingPlanMonthRequest{Month: augustMonth})
	require.Nil(t, err)

	_, err = call[agentifiv1.ReleaseAllSpendingPlanEnvelopesRequest, agentifiv1.ReleaseAllSpendingPlanEnvelopesResponse](
		vera, agentifiv1connect.SpendingPlanServiceReleaseAllSpendingPlanEnvelopesProcedure,
		&agentifiv1.ReleaseAllSpendingPlanEnvelopesRequest{Month: augustMonth})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}

func TestAnotherSpacesEnvelopeIsNotFound(t *testing.T) {
	l := planLedger(t)
	for _, id := range []string{l.str("filter"), "not-an-id"} {
		_, err := call[agentifiv1.ReleaseSpendingPlanEnvelopeRequest, agentifiv1.ReleaseSpendingPlanEnvelopeResponse](
			l.alex, agentifiv1connect.SpendingPlanServiceReleaseSpendingPlanEnvelopeProcedure,
			&agentifiv1.ReleaseSpendingPlanEnvelopeRequest{Month: augustMonth, EnvelopeId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
	}
}
