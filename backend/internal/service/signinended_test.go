package service

import (
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A sign-in that never landed is kept on the connection that started it, and
// one nobody here started is kept nowhere. Every value is invented.
func TestAnUnfinishedSignInIsKeptOnTheConnectionThatStartedIt(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerMyChart)
	session := uuid.NewString()
	rememberSignIn(session, fixture.space, fixture.connection.ID)
	t.Cleanup(func() { signIns.Delete(session) })

	fixture.bills.KeepSignInEnd(t.Context(), provider.BillSignInEnded{
		SessionID: session,
		Detail:    "The sign-in was closed before it finished, while MyChart was asking for a code.",
		Image:     base64.StdEncoding.EncodeToString(pngOf(t, plainPage(64, 40))),
		Trail:     []provider.BillTrailEntry{{Step: "factor: sms", State: "factor", Chose: "Text message"}},
	})

	kept, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullSignInFailed, kept.LastPullStatus)
	require.Equal(t, "The sign-in was closed before it finished, while MyChart was asking for a code.", kept.LastPullError)
	require.True(t, kept.HasFailureScreenshot, "the page is kept as a pull's is")
	require.Equal(t, fixture.connection.NeedsSignIn, kept.NeedsSignIn)
	trail, err := fixture.bills.PullTrail(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, []provider.BillTrailEntry{{Step: "factor: sms", State: "factor", Chose: "Text message"}}, trail)

	fixture.bills.KeepSignInEnd(t.Context(), provider.BillSignInEnded{SessionID: uuid.NewString(), Detail: "a stranger's"})
	again, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, kept.LastPullError, again.LastPullError, "a sign-in nobody here started is kept nowhere")
}
