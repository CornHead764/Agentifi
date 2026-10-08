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

// EmailService and EmailSignInService over their own protocol. The REST URLs
// they used to answer are the bridge's, and email_test.go still reaches them.

func maskPaths(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

func TestAMailboxPatchSetsLeavesAndClearsByTheMask(t *testing.T) {
	l := buildLedger(t)
	id := newMailboxConnection(l.alex, nil)["id"].(string)
	update := func(req *agentifiv1.UpdateEmailConnectionRequest) *agentifiv1.EmailConnection {
		t.Helper()
		req.ConnectionId = id
		res, err := call[agentifiv1.UpdateEmailConnectionRequest, agentifiv1.UpdateEmailConnectionResponse](
			l.alex, agentifiv1connect.EmailServiceUpdateEmailConnectionProcedure, req)
		require.Nil(t, err)
		return res.GetConnection()
	}

	// Set.
	connection := update(&agentifiv1.UpdateEmailConnectionRequest{
		Tenant: proto.String("example-tenant"), UpdateMask: maskPaths("tenant"),
	})
	require.Equal(t, "example-tenant", connection.GetTenant())

	// Absent: a relabel leaves the tenant as it was.
	connection = update(&agentifiv1.UpdateEmailConnectionRequest{
		Label: proto.String("the household mailbox"), UpdateMask: maskPaths("label"),
	})
	require.Equal(t, "the household mailbox", connection.GetLabel())
	require.Equal(t, "example-tenant", connection.GetTenant())
	require.EqualValues(t, 993, connection.GetPort())

	// Cleared: named in the mask and unset.
	connection = update(&agentifiv1.UpdateEmailConnectionRequest{UpdateMask: maskPaths("tenant")})
	require.Equal(t, "", connection.GetTenant())
	require.Equal(t, "the household mailbox", connection.GetLabel())

	// A label is required, so clearing it is the same 409 as over REST.
	_, err := call[agentifiv1.UpdateEmailConnectionRequest, agentifiv1.UpdateEmailConnectionResponse](
		l.alex, agentifiv1connect.EmailServiceUpdateEmailConnectionProcedure,
		&agentifiv1.UpdateEmailConnectionRequest{ConnectionId: id, UpdateMask: maskPaths("label")})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
	require.Equal(t, int32(http.StatusConflict), problemIn(t, err).GetStatus())
}

func TestAnotherSpacesMailboxIsNotFound(t *testing.T) {
	l := buildLedger(t)
	id := newMailboxConnection(l.alex, nil)["id"].(string)
	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	for _, raw := range []string{id, "not-an-id"} {
		_, err := call[agentifiv1.GetEmailConnectionRequest, agentifiv1.GetEmailConnectionResponse](
			bob, agentifiv1connect.EmailServiceGetEmailConnectionProcedure,
			&agentifiv1.GetEmailConnectionRequest{ConnectionId: raw})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Email connection not found", err.Message())
	}
}

func TestAViewerReadsTheMailboxesButCannotAddOne(t *testing.T) {
	l := buildLedger(t)
	vera := l.as("vera")
	_, err := call[agentifiv1.CreateEmailConnectionRequest, agentifiv1.CreateEmailConnectionResponse](
		vera, agentifiv1connect.EmailServiceCreateEmailConnectionProcedure,
		&agentifiv1.CreateEmailConnectionRequest{
			Label: "mine", Kind: "imap", Address: "mine@example.invalid",
			Host: proto.String("imap.example.invalid"),
		})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())

	_, err = call[agentifiv1.ListEmailConnectionsRequest, agentifiv1.ListEmailConnectionsResponse](
		vera, agentifiv1connect.EmailServiceListEmailConnectionsProcedure,
		&agentifiv1.ListEmailConnectionsRequest{})
	require.Nil(t, err)
}

func TestAnIMAPSignInAnswersTheConnectionSideOfTheResult(t *testing.T) {
	l := buildLedger(t)
	useFakeMailbox(t, &fakeMailbox{})
	id := newMailboxConnection(l.alex, nil)["id"].(string)

	res, err := call[agentifiv1.StartEmailSignInRequest, agentifiv1.StartEmailSignInResponse](
		l.alex, agentifiv1connect.EmailSignInServiceStartEmailSignInProcedure,
		&agentifiv1.StartEmailSignInRequest{ConnectionId: id, Password: "abcd efgh ijkl mnop"})
	require.Nil(t, err)
	require.Nil(t, res.GetDeviceCode())
	require.Equal(t, id, res.GetConnection().GetId())
	require.True(t, res.GetConnection().GetConnected())
}

func TestTheMailboxSignInIsAPersonsAndDraftingARuleIsNotTheAssistants(t *testing.T) {
	for procedure, want := range map[string]agentifiv1.Dispatch{
		agentifiv1connect.EmailSignInServiceStartEmailSignInProcedure:    agentifiv1.Dispatch_DISPATCH_HUMAN_ONLY,
		agentifiv1connect.EmailSignInServiceGetEmailSignInStateProcedure: agentifiv1.Dispatch_DISPATCH_HUMAN_ONLY,
		agentifiv1connect.EmailSignInServiceForgetEmailSecretProcedure:   agentifiv1.Dispatch_DISPATCH_HUMAN_ONLY,
		agentifiv1connect.EmailServiceSuggestMailRuleProcedure:           agentifiv1.Dispatch_DISPATCH_DENIED,
		agentifiv1connect.EmailServiceCreateMailRuleProcedure:            agentifiv1.Dispatch_DISPATCH_ALLOWED,
	} {
		require.Equal(t, want, procedures[procedure].dispatch, procedure)
	}
}

func TestAMailRulePatchSetsLeavesAndClearsByTheMask(t *testing.T) {
	l := buildLedger(t)
	id := l.alex.post("/email/rules", lunchRuleBody(l, nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	update := func(req *agentifiv1.UpdateMailRuleRequest) *agentifiv1.MailRule {
		t.Helper()
		req.RuleId = id
		res, err := call[agentifiv1.UpdateMailRuleRequest, agentifiv1.UpdateMailRuleResponse](
			l.alex, agentifiv1connect.EmailServiceUpdateMailRuleProcedure, req)
		require.Nil(t, err)
		return res.GetRule()
	}

	rule := update(&agentifiv1.UpdateMailRuleRequest{
		NotesLabel: proto.String("Receipt Total"), UpdateMask: maskPaths("notes_label"),
	})
	require.Equal(t, "Receipt Total", rule.GetNotesLabel())

	rule = update(&agentifiv1.UpdateMailRuleRequest{
		Name: proto.String("Lunch at work"), UpdateMask: maskPaths("name"),
	})
	require.Equal(t, "Lunch at work", rule.GetName())
	require.Equal(t, "Receipt Total", rule.GetNotesLabel())
	require.Equal(t, l.str("checking"), rule.GetAccountId())

	rule = update(&agentifiv1.UpdateMailRuleRequest{UpdateMask: maskPaths("notes_label")})
	require.Equal(t, "", rule.GetNotesLabel())

	_, err := call[agentifiv1.UpdateMailRuleRequest, agentifiv1.UpdateMailRuleResponse](
		l.alex, agentifiv1connect.EmailServiceUpdateMailRuleProcedure,
		&agentifiv1.UpdateMailRuleRequest{RuleId: id, UpdateMask: maskPaths("name")})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
}

func TestAMailRuleNamingAnIDThatIsNotOneIsRefused(t *testing.T) {
	l := buildLedger(t)
	refused := l.alex.rpc(agentifiv1connect.EmailServiceCreateMailRuleProcedure,
		lunchRuleBody(l, map[string]any{"account_id": "not-an-id"})).
		requireCode(connect.CodeInvalidArgument).
		requireStatus(http.StatusUnprocessableEntity)
	detail := refused.json()["detail"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"body", "account_id"}, detail["loc"])
}

func TestATryThatMatchesNothingLeavesItsAmountsUnset(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.TryMailRuleRequest, agentifiv1.TryMailRuleResponse](
		l.alex, agentifiv1connect.EmailServiceTryMailRuleProcedure, &agentifiv1.TryMailRuleRequest{
			Rule: &agentifiv1.MailRuleDraft{
				Name: proto.String("Lunch"), Sender: proto.String("@employer.example"),
				AmountLabel: proto.String("Receipt Total"),
			},
			Sample: &agentifiv1.MailSample{Sender: "someone@elsewhere.example", Subject: "Hello"},
		})
	require.Nil(t, err)
	require.False(t, res.GetMatched())
	require.Nil(t, res.GetAmount(), "no amount is absent, not zero")
	require.Nil(t, res.Date)
	require.Empty(t, res.GetWouldPost())
}
