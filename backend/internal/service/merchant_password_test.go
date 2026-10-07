package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A merchant login's kept password, against a scripted engine: sealed only
// once a sign-in lands, kept nowhere when the person says not to, handed to a
// pull and paused when the pull cannot get in without a person. Every login
// and session here is invented.

// fakeMerchantAgent is a MerchantAgent whose sign-in and pull are scripted.
type fakeMerchantAgent struct {
	mu sync.Mutex
	// signIn is the state a typed sign-in stops at.
	signIn string
	// lapsed makes a fetch meet a sign-in screen; paused is how a fetch that
	// was handed a credential then ends ("" signs in again and pulls).
	lapsed bool
	paused string
	// credentials is the kept login each fetch was handed, and days how far
	// back each was asked to read.
	credentials []*provider.MerchantCredential
	days        []int
	// gate, when set, holds the next fetch until it is closed.
	gate chan struct{}
	// orders and invoices are what a fetch reads; skipped and invoiced are
	// the orders each fetch was told were read in full and already have an
	// invoice on file.
	orders   []merchantimport.Order
	invoices []provider.MerchantInvoice
	skipped  [][]string
	invoiced [][]string
	// backfilled is the orders each backfill was handed; blank are the orders
	// whose invoice page gives nothing; stopAt, when stop is set, is the
	// order a backfill meets a sign-in at; backfillGate holds the next
	// backfill until it is closed.
	backfilled   [][]string
	blank        map[string]bool
	stop         string
	stopAt       int
	backfillGate chan struct{}
	// laidOut is each stored purchase laid out as a receipt, and layOutFails
	// makes laying one out fail.
	laidOut     []provider.MerchantReceipt
	layOutFails bool
}

func (a *fakeMerchantAgent) LayOutReceipt(receipt provider.MerchantReceipt) (string, []byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.layOutFails {
		return "", nil, errors.New("this browser cannot print")
	}
	a.laidOut = append(a.laidOut, receipt)
	return "costco-receipt-" + receipt.Number + ".pdf", []byte("%PDF-1.4 receipt " + receipt.Number), nil
}

func (a *fakeMerchantAgent) Available() bool                                 { return true }
func (a *fakeMerchantAgent) Health(context.Context, domain.MerchantID) error { return nil }

func (a *fakeMerchantAgent) StartSignIn(
	ctx context.Context, merchant domain.MerchantID, email, password string,
) (provider.MerchantSignInState, error) {
	return provider.MerchantSignInState{SessionID: "session-" + password, State: a.signIn}, nil
}

func (a *fakeMerchantAgent) AnswerSignIn(
	ctx context.Context, sessionID, code string,
) (provider.MerchantSignInState, error) {
	return provider.MerchantSignInState{SessionID: sessionID, State: provider.MerchantSignInSignedIn}, nil
}

func (a *fakeMerchantAgent) SignInStatus(
	ctx context.Context, sessionID string,
) (provider.MerchantSignInState, error) {
	return provider.MerchantSignInState{SessionID: sessionID, State: provider.MerchantSignInSignedIn}, nil
}

func (a *fakeMerchantAgent) CompleteSignIn(
	ctx context.Context, sessionID string,
) (json.RawMessage, string, error) {
	return json.RawMessage(`{"cookies":[{"name":"session","value":"` + sessionID + `"}]}`), "Alex", nil
}

func (a *fakeMerchantAgent) Fetch(
	ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage,
	sinceDays int, skipDetails, invoiced []string, credential *provider.MerchantCredential,
) (provider.MerchantFetchResult, error) {
	a.mu.Lock()
	gate := a.gate
	a.gate = nil
	a.mu.Unlock()
	if gate != nil {
		<-gate
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.credentials = append(a.credentials, credential)
	a.days = append(a.days, sinceDays)
	a.skipped = append(a.skipped, skipDetails)
	a.invoiced = append(a.invoiced, invoiced)
	if a.lapsed && !credential.Usable() {
		return provider.MerchantFetchResult{NeedsSignIn: true, Reason: "Amazon asked to sign in again"}, nil
	}
	if a.lapsed && a.paused != "" {
		reason := "Amazon did not accept the kept password"
		if a.paused == provider.SignInPausedCodeNeeded {
			reason = "Amazon asked for a code; sign in to answer it"
		}
		return provider.MerchantFetchResult{NeedsSignIn: true, Paused: a.paused, Reason: reason}, nil
	}
	return provider.MerchantFetchResult{
		StorageState: json.RawMessage(`{"cookies":[{"name":"session","value":"rolled"}]}`),
		Parsed:       &merchantimport.Parsed{Orders: a.orders},
		Invoices:     a.invoices,
	}, nil
}

func (a *fakeMerchantAgent) BackfillInvoices(
	ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage, orders []string,
	each func(orderNumber string, invoice *provider.MerchantInvoice),
) (provider.MerchantBackfillResult, error) {
	a.mu.Lock()
	gate := a.backfillGate
	a.backfillGate = nil
	a.backfilled = append(a.backfilled, orders)
	blank, stop, stopAt := a.blank, a.stop, a.stopAt
	a.mu.Unlock()
	if gate != nil {
		<-gate
	}
	for n, order := range orders {
		if stop != "" && n == stopAt {
			return provider.MerchantBackfillResult{Stopped: stop, NeedsSignIn: true}, nil
		}
		if blank[order] {
			each(order, nil)
			continue
		}
		each(order, &provider.MerchantInvoice{
			OrderNumber: order, Filename: "amazon-invoice-" + order + ".pdf", PDF: []byte("%PDF-1.4 " + order),
		})
	}
	return provider.MerchantBackfillResult{
		StorageState: json.RawMessage(`{"cookies":[{"name":"session","value":"backfilled"}]}`),
	}, nil
}

// backfills is the orders each backfill was handed.
func (a *fakeMerchantAgent) backfills() [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.backfilled
}

// handed is the kept login the last fetch was given.
func (a *fakeMerchantAgent) handed() *provider.MerchantCredential {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.credentials[len(a.credentials)-1]
}

func merchantsWithAgent(t *testing.T, agent *fakeMerchantAgent) (*Merchants, *store.Store, store.SpaceID, store.MerchantAccount) {
	t.Helper()
	cipher, err := store.NewCipher("the-credential-key")
	require.NoError(t, err)
	sealed := db(t).WithCipher(cipher)
	space := newSpace(t)
	account := store.MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Alex"}
	require.NoError(t, sealed.CreateMerchantAccount(t.Context(), space, &account))
	merchants := NewMerchants(sealed)
	merchants.Agent = agent
	return merchants, sealed, space, account
}

func accountNow(t *testing.T, st *store.Store, space store.SpaceID, account store.MerchantAccount) store.MerchantAccount {
	t.Helper()
	one, err := st.GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	return one
}

func TestAKeptPasswordIsSealedOnlyOnceTheSignInLands(t *testing.T) {
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInOTP}
	merchants, st, space, account := merchantsWithAgent(t, agent)

	state, err := merchants.StartSignIn(t.Context(), space, account.ID,
		"alex@example.com", "a-password", "JBSWY3DPEHPK3PXP", "")
	require.NoError(t, err)
	require.False(t, accountNow(t, st, space, account).HasPassword,
		"a sign-in waiting on a code has not got in, and keeps nothing yet")

	_, err = merchants.AnswerSignIn(t.Context(), space, account.ID, state.SessionID, "123456")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, "alex@example.com"))

	kept := accountNow(t, st, space, account)
	require.True(t, kept.HasSession)
	require.True(t, kept.HasPassword)
	require.True(t, kept.HasTOTP)
	login, err := st.MerchantCredentialOf(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Equal(t, store.MerchantCredential{
		Username: "alex@example.com", Password: "a-password", TOTPSecret: "JBSWY3DPEHPK3PXP",
	}, login)
}

func TestASignInThatFailsKeepsNoPassword(t *testing.T) {
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInFailed}
	merchants, st, space, account := merchantsWithAgent(t, agent)

	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "wrong", "", "")
	require.NoError(t, err)
	_, held := takeCredential(state.SessionID)
	require.False(t, held, "a password the merchant turned down is not held for sealing")
	require.False(t, accountNow(t, st, space, account).HasPassword)
}

func TestAPullIsHandedTheKeptPasswordAndARefusalPausesIt(t *testing.T) {
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
	merchants, st, space, account := merchantsWithAgent(t, agent)
	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept", "", "")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))

	// A lapsed session the kept password signs past: the pull carries on.
	agent.lapsed = true
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Equal(t, &provider.MerchantCredential{Email: "alex@example.com", Password: "kept"}, agent.handed())
	require.Equal(t, store.MerchantSyncOK, accountNow(t, st, space, account).LastSyncStatus)

	// The merchant turns the password down: kept, paused, and said so.
	agent.paused = provider.SignInPausedPasswordRefused
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.ErrorIs(t, err, ErrMerchantNeedsSignIn)
	refused := accountNow(t, st, space, account)
	require.True(t, refused.HasPassword, "a refused password is kept")
	require.True(t, refused.NeedsSignIn)
	require.NotNil(t, refused.SignInPausedAt)
	require.Equal(t, provider.SignInPausedPasswordRefused, refused.SignInPausedFor)
	require.Contains(t, refused.LastSyncError, "did not accept the kept password")
	require.Contains(t, refused.LastSyncError, "will not sign in with it on its own again")

	// The scheduler does not hand a paused password to a pull.
	require.NoError(t, scheduledPull(t, merchants, space, account))
	require.Nil(t, agent.handed(), "a paused password is not tried on a timer")

	// A person pressing Update now tries it once more, and a pull that gets
	// in lifts the pause.
	agent.paused = ""
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.NotNil(t, agent.handed())
	lifted := accountNow(t, st, space, account)
	require.Nil(t, lifted.SignInPausedAt)
	require.False(t, lifted.NeedsSignIn)
}

func TestACodeNobodyCanAnswerPausesThePasswordWithoutCallingItRefused(t *testing.T) {
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
	merchants, st, space, account := merchantsWithAgent(t, agent)
	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept", "", "")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))

	agent.lapsed, agent.paused = true, provider.SignInPausedCodeNeeded
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.ErrorIs(t, err, ErrMerchantNeedsSignIn)
	paused := accountNow(t, st, space, account)
	require.Equal(t, provider.SignInPausedCodeNeeded, paused.SignInPausedFor)
	require.Contains(t, paused.LastSyncError, "Amazon asked for a code; sign in to answer it")

	// Signing in again lifts it.
	state, err = merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept", "", "")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))
	require.Nil(t, accountNow(t, st, space, account).SignInPausedAt)
}

func TestForgettingThePasswordLeavesTheSessionAndHandsAPullNothing(t *testing.T) {
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
	merchants, st, space, account := merchantsWithAgent(t, agent)
	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept",
		"JBSWY3DPEHPK3PXP", "")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))

	require.NoError(t, merchants.ForgetPassword(t.Context(), space, account.ID))
	forgotten := accountNow(t, st, space, account)
	require.False(t, forgotten.HasPassword)
	require.False(t, forgotten.HasTOTP)
	require.True(t, forgotten.HasSession)

	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Nil(t, agent.handed())
}

// scheduledPull is the pull the scheduler runs, whose needs-sign-in answer is
// the point rather than a failure.
func scheduledPull(t *testing.T, a *Merchants, space store.SpaceID, account store.MerchantAccount) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := a.pull(ctx, space, account.ID, 30, false)
	if err != nil && !errors.Is(err, ErrMerchantNeedsSignIn) {
		return err
	}
	return nil
}

func TestUpdateNowStampsTheAccountItPulledEvenWhenThePageLeaves(t *testing.T) {
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
	merchants, st, space, account := merchantsWithAgent(t, agent)
	other := store.MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Sam"}
	require.NoError(t, st.CreateMerchantAccount(t.Context(), space, &other))
	for _, one := range []store.MerchantAccount{account, other} {
		state, err := merchants.StartSignIn(t.Context(), space, one.ID, "someone@example.com", "kept", "", "")
		require.NoError(t, err)
		require.NoError(t, merchants.CompleteSignIn(t.Context(), space, one.ID, state.SessionID, ""))
	}
	gate := make(chan struct{})
	agent.mu.Lock()
	agent.gate = gate
	agent.mu.Unlock()

	request, leave := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := merchants.Pull(request, space, account.ID, 0)
		done <- err
	}()
	leave()
	close(gate)
	require.NoError(t, <-done)

	pulled := accountNow(t, st, space, account)
	require.NotNil(t, pulled.LastSyncedAt, "the pull finished, so its account says when")
	require.Equal(t, store.MerchantSyncOK, pulled.LastSyncStatus)
	require.Nil(t, accountNow(t, st, space, other).LastSyncedAt, "the other login was not pulled")
}
