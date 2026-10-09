package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// The bills connector against a scripted engine. Every figure, id and session
// here is invented.

// fakeBillsAgent is a BillsAgent whose pulls are scripted in a provider
// module's shape (every figure a string) and read through provider.CoerceBills,
// as the real engine's pull is.
type fakeBillsAgent struct {
	mu sync.Mutex
	// answers is what the next pull returns, in order; the last is repeated.
	answers []map[string]any
	// resume is what resuming a parked pull returns.
	resume map[string]any
	// seen records the session_state each pull was given.
	seen []string
	// known records the known_documents each pull was given.
	known [][]string
	// codes records the codes answered into a parked sign-in.
	codes []string
	// document is the bytes a document reference is traded for.
	document []byte
	pulls    int
	// gate, when set, holds the next pull until it is closed, so a test can ask for
	// another while that one runs. One pull only: the scheduler's pass walks every
	// space, and the rest must not wait on it.
	gate chan struct{}
	// fail makes a pull an engine error rather than an answer.
	fail error
	// forgotten records the browser profiles deleted.
	forgotten []string
	// factors records the second factor each pull and each connect was given.
	factors []string
}

func newFakeBillsAgent(t *testing.T) *fakeBillsAgent {
	t.Helper()
	return &fakeBillsAgent{document: storetest.PDF()}
}

func (a *fakeBillsAgent) Available() bool { return true }

func (a *fakeBillsAgent) Health(context.Context) error { return nil }

func (a *fakeBillsAgent) Providers(context.Context) ([]provider.BillProvider, error) {
	return []provider.BillProvider{{ID: "alliant", Name: "Alliant Energy"}}, nil
}

func (a *fakeBillsAgent) Pull(
	ctx context.Context, request provider.BillPullRequest,
) (provider.BillPull, error) {
	a.mu.Lock()
	gate := a.gate
	a.gate = nil
	a.factors = append(a.factors, request.SecondFactor)
	a.mu.Unlock()
	if gate != nil {
		<-gate
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = append(a.seen, request.SessionState)
	a.known = append(a.known, append([]string{}, request.KnownDocuments...))
	if a.fail != nil {
		return provider.BillPull{}, a.fail
	}
	answer := a.answers[min(a.pulls, len(a.answers)-1)]
	a.pulls++
	return pullOf(answer), nil
}

// hangingBillsAgent is a fakeBillsAgent whose first hangs pulls each block
// until the pull's own context ends, as a provider's portal does when it
// never answers; the rest run as a normal fakeBillsAgent pull.
type hangingBillsAgent struct {
	fakeBillsAgent
	hangs int
}

func (a *hangingBillsAgent) Pull(
	ctx context.Context, request provider.BillPullRequest,
) (provider.BillPull, error) {
	a.mu.Lock()
	if a.hangs > 0 {
		a.hangs--
		a.mu.Unlock()
		<-ctx.Done()
		return provider.BillPull{}, ctx.Err()
	}
	a.mu.Unlock()
	return a.fakeBillsAgent.Pull(ctx, request)
}

func (a *fakeBillsAgent) ResumePull(ctx context.Context, sessionID string) (provider.BillPull, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return pullOf(a.resume), nil
}

func (a *fakeBillsAgent) AnswerConnect(
	ctx context.Context, sessionID, code string,
) (provider.BillConnectState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codes = append(a.codes, code)
	return provider.BillConnectState{
		SessionID: sessionID, Provider: "spectrum", State: provider.BillConnectSignedIn,
	}, nil
}

func (a *fakeBillsAgent) FetchDocument(ctx context.Context, ref string) ([]byte, string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.document, "application/pdf", "statement.pdf", nil
}

// The rest of the contract, answered blandly.
func (a *fakeBillsAgent) StartConnect(
	ctx context.Context, request provider.BillConnectStart,
) (provider.BillConnectState, error) {
	a.mu.Lock()
	a.factors = append(a.factors, request.SecondFactor)
	a.mu.Unlock()
	return provider.BillConnectState{SessionID: "connect-1", Provider: request.Provider,
		State: provider.BillConnectSignedIn}, nil
}

func (a *fakeBillsAgent) StartLiveConnect(
	ctx context.Context, providerID, profile, site string, width, height int,
) (provider.BillConnectState, error) {
	return provider.BillConnectState{SessionID: "live-1", Provider: providerID,
		State: provider.BillConnectInteractive}, nil
}

func (a *fakeBillsAgent) ConnectStatus(ctx context.Context, sessionID string) (provider.BillConnectState, error) {
	return provider.BillConnectState{SessionID: sessionID, State: provider.BillConnectSignedIn}, nil
}

func (a *fakeBillsAgent) ConnectTrail(ctx context.Context, sessionID string) ([]provider.BillTrailEntry, error) {
	return nil, nil
}

func (a *fakeBillsAgent) SteerTo(ctx context.Context, sessionID, address string) (provider.BillSignInSteer, error) {
	return provider.BillSignInSteer{URL: address}, nil
}

func (a *fakeBillsAgent) SteerClick(ctx context.Context, sessionID, text, selector string) (provider.BillSignInSteer, error) {
	return provider.BillSignInSteer{}, nil
}

func (a *fakeBillsAgent) SteerDOM(ctx context.Context, sessionID, selector string, limit int) (provider.BillSignInDOM, error) {
	return provider.BillSignInDOM{Count: limit}, nil
}

func (a *fakeBillsAgent) SteerFetch(
	ctx context.Context, sessionID string, request provider.BillSignInFetchRequest,
) (provider.BillSignInFetch, error) {
	return provider.BillSignInFetch{URL: request.URL}, nil
}

func (a *fakeBillsAgent) CancelSignIn(ctx context.Context, sessionID string) error { return nil }

func (a *fakeBillsAgent) CompleteConnect(ctx context.Context, sessionID string) (provider.BillConnectComplete, error) {
	return provider.BillConnectComplete{Provider: "alliant"}, nil
}

func (a *fakeBillsAgent) Keepalive(
	ctx context.Context, providerID, profile, site, sessionState string,
) (provider.BillKeepalive, error) {
	return provider.BillKeepalive{OK: true, SignedIn: true}, nil
}

func (a *fakeBillsAgent) ForgetProfile(ctx context.Context, profile string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forgotten = append(a.forgotten, profile)
	return nil
}

func (a *fakeBillsAgent) ReleaseProfile(
	ctx context.Context, profile string,
) (provider.BillProfileRelease, error) {
	return provider.BillProfileRelease{}, nil
}

// pullOf reads a scripted answer the way a module's own is read: an amount
// that does not read is a note and no bill, and the rest is coerced by
// provider.CoerceBills.
func pullOf(answer map[string]any) provider.BillPull {
	raw, _ := json.Marshal(answer)
	var wire struct {
		NeedsSignIn     bool            `json:"needs_sign_in"`
		PasswordRefused bool            `json:"password_refused"`
		CodeNeeded      bool            `json:"code_needed"`
		PageCheck       bool            `json:"page_check"`
		Reason          string          `json:"reason"`
		Image           string          `json:"image"`
		SessionState    json.RawMessage `json:"session_state"`
		Bills           []struct {
			Subaccount   string                    `json:"subaccount"`
			ExternalID   string                    `json:"external_id"`
			Invoice      string                    `json:"invoice"`
			IssuedOn     string                    `json:"issued_on"`
			DueOn        string                    `json:"due_on"`
			AmountDue    string                    `json:"amount_due"`
			MinimumDue   string                    `json:"minimum_due"`
			Currency     string                    `json:"currency"`
			Status       string                    `json:"status"`
			StatementURL string                    `json:"statement_url"`
			Document     *provider.BillDocumentRef `json:"document"`
		} `json:"bills"`
		Payments []struct {
			Subaccount string `json:"subaccount"`
			ExternalID string `json:"external_id"`
			PaidOn     string `json:"paid_on"`
			Amount     string `json:"amount"`
		} `json:"payments"`
		Notes     []string                     `json:"notes"`
		Challenge *provider.BillChallengeState `json:"challenge"`
		Trail     []provider.BillTrailEntry    `json:"trail"`
		Accounts  []provider.BillSubaccountRef `json:"subaccounts"`
	}
	_ = json.Unmarshal(raw, &wire)
	var read []provider.WireBill
	for _, one := range wire.Bills {
		amount, ok := domain.ParseMoneyText(one.AmountDue)
		if !ok {
			wire.Notes = append(wire.Notes, fmt.Sprintf("%q is not an amount", one.AmountDue))
			continue
		}
		bill := provider.WireBill{
			Subaccount: one.Subaccount, ExternalID: one.ExternalID, Invoice: one.Invoice, IssuedOn: one.IssuedOn,
			DueOn: one.DueOn, AmountDue: amount, Currency: one.Currency, Status: one.Status,
			StatementURL: one.StatementURL, Document: one.Document,
		}
		if one.MinimumDue != "" {
			bill.MinimumDue, bill.HasMinimumDue = domain.ParseMoneyText(one.MinimumDue)
		}
		read = append(read, bill)
	}
	bills, dropped := provider.CoerceBills(read)
	var paid []provider.WirePayment
	for _, one := range wire.Payments {
		amount, _ := domain.ParseMoneyText(one.Amount)
		paid = append(paid, provider.WirePayment{
			Subaccount: one.Subaccount, ExternalID: one.ExternalID, PaidOn: one.PaidOn, Amount: amount,
		})
	}
	payments, unpaid := provider.CoercePayments(paid)
	return provider.BillPull{
		NeedsSignIn: wire.NeedsSignIn, PasswordRefused: wire.PasswordRefused, CodeNeeded: wire.CodeNeeded, PageCheck: wire.PageCheck,
		Reason: wire.Reason, Image: wire.Image,
		SessionState: wire.SessionState, Bills: bills, Payments: payments,
		Notes: append(append(wire.Notes, dropped...), unpaid...), Challenge: wire.Challenge,
		Trail: wire.Trail, Subaccounts: wire.Accounts,
	}
}

type bridgeFixture struct {
	bills      *Bills
	agent      *fakeBillsAgent
	space      store.SpaceID
	user       store.User
	connection *store.BillConnection
	subaccount *store.BillSubaccount
	push       *pushbox
	root       string
}

const fakeBillSession = `{"token":"session-one"}`

func newBridgeFixture(t *testing.T, biller domain.BillerID) bridgeFixture {
	t.Helper()
	cipher, err := store.NewCipher("the-credential-key")
	require.NoError(t, err)
	sealed := db(t).WithCipher(cipher)

	space := newSpace(t)
	user := &store.User{
		Email:    fmt.Sprintf("alex-%s@example.test", uuid.NewString()),
		FullName: "Alex", IsActive: true,
	}
	require.NoError(t, sealed.CreateUser(t.Context(), user))
	accepted := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, sealed.CreateMembership(t.Context(), space, &store.Membership{
		UserID: user.ID, Role: store.RoleOwner, AcceptedAt: &accepted,
	}))
	require.NoError(t, sealed.SavePushSubscription(t.Context(), space, user.ID,
		&store.PushSubscription{
			Endpoint: "https://push.example.test/" + uuid.NewString(),
			P256dh:   "key", Auth: "auth", UserAgent: "a laptop",
		}))

	connection := &store.BillConnection{
		Biller: biller, Label: "Main account", CredentialSource: store.BillCredentialSession,
		AutopayRule: domain.AutopayNone, PullEnabled: true,
	}
	require.NoError(t, sealed.CreateBillConnection(t.Context(), space, connection))
	require.NoError(t, sealed.SaveBillConnectionSession(
		t.Context(), space, connection.ID, fakeBillSession, "profile-1", true))
	subaccount := &store.BillSubaccount{
		ConnectionID: connection.ID, ExternalID: "premise-7", Label: "Main account",
		IsSelected: true,
	}
	require.NoError(t, sealed.UpsertBillSubaccount(t.Context(), space, subaccount))

	agent := newFakeBillsAgent(t)
	push := &pushbox{}
	alerts := NewAlerts(sealed)
	alerts.Push = push

	root := t.TempDir()
	bills := NewBills(sealed)
	bills.Agent = agent
	bills.Documents = NewDocuments(sealed, &provider.LocalStorage{BasePath: root})
	bills.Alerts = alerts
	bills.Now = func() time.Time { return time.Date(2026, time.October, 3, 4, 0, 0, 0, time.UTC) }

	// Re-read, so the fixture holds the row as the sign-in left it.
	stored, err := sealed.GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)

	return bridgeFixture{
		bills: bills, agent: agent, space: space, user: *user, connection: &stored,
		subaccount: subaccount, push: push, root: root,
	}
}

func pulledStatement(external, dueOn, amount string) map[string]any {
	return map[string]any{
		"subaccount": "premise-7", "external_id": external,
		"issued_on": "", "due_on": dueOn, "amount_due": amount, "currency": "USD",
		"period_start": "", "period_end": "", "autopay_on": "",
		"status": "Open", "statement_url": "https://provider.example.test/bill/" + external,
	}
}

func okPull(session string, bills ...map[string]any) map[string]any {
	return map[string]any{
		"provider": "alliant", "needs_sign_in": false,
		"session_state": json.RawMessage(session), "bills": bills, "notes": []string{},
	}
}

func TestBillsPullSealsTheRolledSession(t *testing.T) {
	// The provider rotates its token on every call, so the row must hold the
	// snapshot the pull handed back.
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	fixture.agent.answers = []map[string]any{
		okPull(`{"token":"rolled-two"}`, pulledStatement("stmt-1", "2026-10-26", "120.00")),
		okPull(`{"token":"rolled-three"}`, pulledStatement("stmt-1", "2026-10-26", "120.00")),
	}

	first, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, first.Status)
	require.Equal(t, 1, first.New)

	kept, err := fixture.bills.store.BillConnectionSession(
		t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"rolled-two"}`, kept)

	second, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Zero(t, second.New, "the same cycle is the same bill")
	require.Equal(t, 1, second.Unchanged)

	fixture.agent.mu.Lock()
	defer fixture.agent.mu.Unlock()
	require.Len(t, fixture.agent.seen, 2)
	require.Contains(t, fixture.agent.seen[0], "session-one", "the first pull used the sign-in's session")
	require.Contains(t, fixture.agent.seen[1], "rolled-two", "the second used what the first was handed")
}

// What the provider showed during a pull that got in is kept on the
// connection, and the next pull's takes its place.
func TestBillsPullKeepsWhatTheProviderShowed(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	first := okPull(`{"token":"rolled-two"}`, pulledStatement("stmt-1", "2026-10-26", "120.00"))
	first["trail"] = []provider.BillTrailEntry{{Step: "read", State: "page", Note: "the statements list"}}
	fixture.agent.answers = []map[string]any{first, okPull(`{"token":"rolled-three"}`)}

	_, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	kept, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, kept.LastPullStatus)
	require.True(t, kept.HasTrail)
	trail, err := fixture.bills.PullTrail(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, []provider.BillTrailEntry{{Step: "read", State: "page", Note: "the statements list"}}, trail)

	_, err = fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	_, err = fixture.bills.PullTrail(t.Context(), fixture.space, fixture.connection.ID)
	require.ErrorIs(t, err, store.ErrNotFound, "a pull that showed nothing leaves nothing of the last one")
}

// A label the provider's page was misread for is put right by the next pull
// that reads it well; one somebody wrote stays theirs.
func TestBillsPullPutsRightALabelThatIsStillTheProvidersOwn(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerMyChart)
	misread := &store.BillSubaccount{
		ConnectionID: fixture.connection.ID, ExternalID: "00009999",
		Label: "Billing account ****9999 · Skip navigation to main content", IsSelected: true,
	}
	require.NoError(t, fixture.bills.store.UpsertBillSubaccount(t.Context(), fixture.space, misread))
	answer := okPull(`{"token":"rolled-two"}`)
	answer["subaccounts"] = []provider.BillSubaccountRef{
		{ExternalID: "00009999", Label: "Billing account ****9999 · Example Health", MaskedNumber: "••••9999"},
		{ExternalID: "premise-7", Label: "Billing account ****9999", MaskedNumber: "••••9999"},
		{ExternalID: "not-on-file", Label: "Billing account ****1234"},
	}
	fixture.agent.answers = []map[string]any{answer}

	_, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	held, err := fixture.bills.store.ListBillSubaccounts(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	labels := map[string]string{}
	for _, one := range held {
		labels[one.ExternalID] = one.Label
	}
	require.Equal(t, map[string]string{
		"00009999":  "Billing account ****9999 · Example Health",
		"premise-7": "Main account",
	}, labels, "a label somebody wrote is kept, and an account not on file is not added by its label alone")
}

func TestBillsPullStoresTheStatementOnceAcrossTwoPulls(t *testing.T) {
	// known_documents stops the engine fetching the statement again, and the
	// content hash stops it being stored twice.
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	withDocument := pulledStatement("stmt-9", "2026-10-26", "120.00")
	withDocument["document"] = map[string]any{
		"ref": "doc-ref-1", "content_type": "application/pdf",
		"size": len(storetest.PDF()), "filename": "october.pdf",
	}
	fixture.agent.answers = []map[string]any{
		okPull(`{"token":"rolled-two"}`, withDocument),
		// The second pull says nothing about a document, as an engine told the
		// statement is already on file answers.
		okPull(`{"token":"rolled-three"}`, pulledStatement("stmt-9", "2026-10-26", "120.00")),
	}

	first, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, first.Documents)

	bills, err := fixture.bills.store.ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Len(t, bills, 1)
	require.NotEqual(t, uuid.Nil, bills[0].DocumentID, "the bill names the statement it arrived with")
	documentID := bills[0].DocumentID

	second, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Zero(t, second.Documents)

	fixture.agent.mu.Lock()
	require.Equal(t, []string{"stmt-9"}, fixture.agent.known[1],
		"the second pull told the engine which statement it already held")
	fixture.agent.mu.Unlock()

	require.Len(t, storedFiles(t, fixture.root, fixture.space), 1)
	links, err := fixture.bills.store.ListDocumentLinks(t.Context(), fixture.space, documentID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	after, err := fixture.bills.store.ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Equal(t, documentID, after[0].DocumentID)
}

func TestAMedicalPullFilesTheStatementAPaymentSettledOnItsCardRow(t *testing.T) {
	// The payment arrives before the bank row; the daily pass pairs them.
	fixture := newBridgeFixture(t, domain.BillerMyChart)
	statement := pulledStatement("premise-7:2026-03-03", "2026-03-28", "150.00")
	statement["issued_on"] = "2026-03-03"
	statement["document"] = map[string]any{
		"ref": "doc-ref-1", "content_type": "application/pdf",
		"size": len(storetest.PDF()), "filename": "mychart-statement.pdf",
	}
	answer := okPull(`{"token":"rolled"}`, statement)
	answer["payments"] = []map[string]any{
		{"subaccount": "premise-7", "external_id": "premise-7:2026-03-10:150.00", "paid_on": "2026-03-10", "amount": "150.00"},
		{"subaccount": "premise-7", "external_id": "undated", "paid_on": "", "amount": "10.00"},
	}
	fixture.agent.answers = []map[string]any{answer}

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, result.Status)
	require.Contains(t, result.Notes, "Payments read from Main account: 1. Newly paired with a bank row: 0.")
	payments, err := fixture.bills.store.ListBillPayments(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Len(t, payments, 1, "the undated payment is a note")

	account := &store.Account{Name: "HSA Card", Type: "credit", Currency: "USD"}
	require.NoError(t, fixture.bills.store.CreateAccount(t.Context(), fixture.space, account))
	row := &store.Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, time.March, 12),
		Amount: domain.MustFromString("-150.00"), Currency: "USD",
		StatementName: "EXAMPLE HEALTH", Payee: "Example Health",
	}
	require.NoError(t, fixture.bills.store.CreateTransaction(t.Context(), fixture.space, row))
	paired, err := fixture.bills.store.MatchBillPayments(t.Context(), fixture.space)
	require.NoError(t, err)
	require.Equal(t, 1, paired)

	behind, err := fixture.bills.store.DocumentsBehindTransaction(t.Context(), fixture.space, row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, store.DocumentLinkReceipt, behind[0].Via)
}

func TestBillsPullFilesEachSameDayInvoiceWithItsOwnStatement(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerTruGreen)
	invoice := func(number, amount string) map[string]any {
		bill := pulledStatement("premise-7:"+number, "2026-05-04", amount)
		bill["invoice"], bill["status"] = number, "Paid"
		bill["document"] = map[string]any{
			"ref": "doc-" + number, "content_type": "application/pdf",
			"size": len(storetest.PDF()), "filename": number + ".pdf",
		}
		return bill
	}
	pull := okPull(`{"token":"rolled-two"}`, invoice("INV-7", "60.00"), invoice("INV-8", "35.00"))
	fixture.agent.answers = []map[string]any{pull}

	first, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 2, first.New)

	bills, err := fixture.bills.store.ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Len(t, bills, 2)
	for _, bill := range bills {
		require.NotEqual(t, uuid.Nil, bill.DocumentID, "invoice %s has its statement", bill.Invoice)
	}

	second, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Zero(t, second.New)
	require.Zero(t, second.Amended)
	require.Equal(t, 2, second.Unchanged)
}

func TestBillsPullDropsAnUnparseableAmountWithANote(t *testing.T) {
	// An unreadable figure is dropped with a note rather than stored as 0.00.
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	fixture.agent.answers = []map[string]any{okPull(`{"token":"rolled-two"}`,
		pulledStatement("stmt-1", "2026-10-26", "120.00"),
		pulledStatement("stmt-2", "2026-11-14", "see your statement"),
	)}

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, result.New, "only the bill that could be read")
	require.NotEmpty(t, result.Notes)
	require.Contains(t, strings.Join(result.Notes, " "), "is not an amount")

	bills, err := fixture.bills.store.ListBills(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Len(t, bills, 1)
	require.Equal(t, "120.00", bills[0].AmountDue.String())
}

func TestBillsPullParksAChallengeAndDeliversAPushWhenNothingAnswers(t *testing.T) {
	// The pull is unattended, so a code prompt becomes a row that outlives the
	// request, and a push fetches somebody to it.
	fixture := newBridgeFixture(t, domain.BillerSpectrum)
	fixture.agent.answers = []map[string]any{{
		"provider": "spectrum",
		"challenge": map[string]any{
			"session_id": "park-1", "state": "otp", "method": "sms",
			"prompt": "Enter the code we texted to the number ending 1234", "image": "",
		},
		"notes": []string{},
	}}

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullChallenge, result.Status)
	require.NotNil(t, result.Challenge)
	require.Equal(t, store.BillChallengeWaiting, result.Challenge.State)
	require.Equal(t, "sms", result.Challenge.Method)
	require.Equal(t, store.BillChallengeFromPull, result.Challenge.RaisedBy)
	require.Equal(t, fixture.bills.now().Add(BillChallengeTTL), result.Challenge.ExpiresAt.UTC())

	waiting, err := fixture.bills.store.ListBillChallenges(
		t.Context(), fixture.space, store.BillChallengeWaiting)
	require.NoError(t, err)
	require.Len(t, waiting, 1)

	connection, err := fixture.bills.store.GetBillConnection(
		t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullChallenge, connection.LastPullStatus)
	require.False(t, connection.NeedsSignIn, "the kept session is still good")

	require.Len(t, fixture.push.payloads, 1)
	require.Equal(t, "/settings/bills?challenge="+result.Challenge.ID.String(),
		fixture.push.payloads[0]["url"])
	require.Contains(t, fixture.push.payloads[0]["body"], "1234")

	feed, err := fixture.bills.store.ListNotifications(
		t.Context(), fixture.space, fixture.user.ID, store.NotificationQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, feed)
	require.Equal(t, domain.AlertBillChallenge, feed[0].AlertType)
}

func TestAnAutomaticAnswerFinishesTheChallengeWithNobodyTold(t *testing.T) {
	// With a stand-in for the mailbox reader: an answer found means no push, and
	// the row records which path answered.
	fixture := newBridgeFixture(t, domain.BillerSpectrum)
	fixture.agent.answers = []map[string]any{{
		"provider": "spectrum",
		"challenge": map[string]any{
			"session_id": "park-1", "state": "otp", "method": "email",
			"prompt": "Enter the code we emailed you", "image": "",
		},
		"notes": []string{},
	}}
	fixture.agent.resume = okPull(`{"token":"rolled-two"}`,
		pulledStatement("stmt-4", "2026-10-20", "90.00"))
	fixture.bills.Answerers = []BillChallengeAnswerer{
		func(context.Context, store.SpaceID, store.BillConnection, store.BillChallenge) (string, string, bool) {
			return "314159", store.BillChallengeByMailbox, true
		},
	}

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, result.Status, "the pull carried on as if it had never stopped")
	require.Equal(t, 1, result.New)
	require.Empty(t, fixture.push.sent, "nobody was interrupted")

	fixture.agent.mu.Lock()
	require.Equal(t, []string{"314159"}, fixture.agent.codes)
	fixture.agent.mu.Unlock()

	answered, err := fixture.bills.store.ListBillChallenges(
		t.Context(), fixture.space, store.BillChallengeAnswered)
	require.NoError(t, err)
	require.Len(t, answered, 1)
	require.Equal(t, store.BillChallengeByMailbox, answered[0].AnsweredBy)
	require.NotNil(t, answered[0].AnsweredAt)
}

func TestAnsweringAnExpiredChallengeSaysSoRatherThanFailing(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerSpectrum)
	challenge := &store.BillChallenge{
		ConnectionID: fixture.connection.ID, AgentSession: "park-1", Method: "sms",
		Prompt: "Enter the code", RaisedBy: store.BillChallengeFromPull,
		ExpiresAt: fixture.bills.now().Add(-time.Minute),
	}
	require.NoError(t, fixture.bills.store.CreateBillChallenge(t.Context(), fixture.space, challenge))

	_, err := fixture.bills.AnswerChallenge(t.Context(), fixture.space, challenge.ID, "314159")
	require.ErrorIs(t, err, ErrBillChallengeExpired)

	after, err := fixture.bills.store.GetBillChallenge(t.Context(), fixture.space, challenge.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillChallengeExpired, after.State)
}

func TestBillsPullDueSkipsSMSProvidersWithoutARelayOrAnHour(t *testing.T) {
	// A provider that texts a code is not pulled unattended; the connection is
	// told why, and pulled by Update now or at an hour the household names.
	texted := newBridgeFixture(t, domain.BillerSpectrum)
	require.True(t, mustBiller(t, domain.BillerSpectrum).NeedsAPersonForACode())

	quiet := &store.BillConnection{
		Biller: domain.BillerAlliant, Label: "the electricity",
		CredentialSource: store.BillCredentialSession, AutopayRule: domain.AutopayNone,
		PullEnabled: true,
	}
	require.NoError(t, texted.bills.store.CreateBillConnection(t.Context(), texted.space, quiet))
	require.NoError(t, texted.bills.store.SaveBillConnectionSession(
		t.Context(), texted.space, quiet.ID, fakeBillSession, "profile-2", true))
	texted.agent.answers = []map[string]any{okPull(`{"token":"rolled-two"}`)}

	// The window is open for both: only the provider's own second factor decides.
	// The pass walks every space, so only these two rows are asserted on.
	texted.bills.PullDue(t.Context(), texted.bills.now().Add(-time.Hour))

	ran, err := texted.bills.store.GetBillConnection(t.Context(), texted.space, quiet.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, ran.LastPullStatus, "the API provider was pulled")

	skipped, err := texted.bills.store.GetBillConnection(
		t.Context(), texted.space, texted.connection.ID)
	require.NoError(t, err)
	require.Nil(t, skipped.LastPulledAt, "a skipped connection was not marked as pulled")
	require.Contains(t, skipped.LastPullError, "code somebody has to read")
	require.Equal(t, "", skipped.LastPullStatus)

	// Said once: a second pass leaves the row exactly as it found it.
	before := skipped.UpdatedAt
	texted.bills.PullDue(t.Context(), texted.bills.now().Add(-time.Hour))
	again, err := texted.bills.store.GetBillConnection(
		t.Context(), texted.space, texted.connection.ID)
	require.NoError(t, err)
	require.Equal(t, before, again.UpdatedAt, "the same reason was not written twice")

	// An hour of its own is the household saying somebody will be awake.
	texted.connection.PullAt = "18:30"
	require.NoError(t, texted.bills.store.UpdateBillConnection(
		t.Context(), texted.space, texted.connection))
	texted.agent.answers = []map[string]any{{
		"provider": "spectrum",
		"challenge": map[string]any{
			"session_id": "park-1", "state": "otp", "method": "sms",
			"prompt": "Enter the code we texted you", "image": "",
		},
		"notes": []string{},
	}}
	texted.bills.Now = func() time.Time { return time.Date(2026, time.October, 3, 19, 0, 0, 0, time.UTC) }
	texted.bills.PullDue(t.Context(), texted.bills.now().Add(-time.Hour))

	awake, err := texted.bills.store.GetBillConnection(
		t.Context(), texted.space, texted.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullChallenge, awake.LastPullStatus,
		"with an hour set it is pulled, and the challenge finds somebody awake")
}

func TestANeedsSignInKeepsThePasswordAndARefusalKeepsTheSchedulerOffIt(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	require.NoError(t, fixture.bills.store.SaveBillConnectionCredential(t.Context(), fixture.space,
		fixture.connection.ID, store.BillCredential{Username: "alex", Password: "invented"}))
	row := func() store.BillConnection {
		one, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
		require.NoError(t, err)
		return one
	}
	// The pull stamp is the database's clock, not the fixture's.
	since := func() time.Time {
		if last := row().LastPulledAt; last != nil {
			return last.Add(time.Hour)
		}
		return time.Now().Add(time.Hour)
	}

	// The session lapsed and nothing tried the password: kept, and still due.
	fixture.agent.answers = []map[string]any{{
		"needs_sign_in": true, "reason": "Alliant Energy refused the kept session",
	}}
	lapsed, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullNeedsSignIn, lapsed.Status)
	require.Equal(t, "Alliant Energy refused the kept session", lapsed.Error)
	require.True(t, row().HasCredential)
	require.Equal(t, store.BillCredentialStored, row().CredentialSource)
	require.Nil(t, row().SignInPausedAt)
	// The pass walks every space, so this row's own stamp is asserted.
	before := *row().LastPulledAt
	fixture.bills.PullDue(t.Context(), since())
	require.True(t, row().LastPulledAt.After(before), "a kept password is what the next pull signs in with")

	// The engine tried it and was turned away: still kept, now marked, and
	// the scheduler leaves it alone.
	fixture.agent.answers = []map[string]any{{
		"needs_sign_in": true, "password_refused": true,
		"reason": "Alliant Energy did not accept the kept password (Invalid username or password)",
	}}
	refused, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullNeedsSignIn, refused.Status)
	require.Contains(t, refused.Error, "did not accept the kept password")
	require.Contains(t, refused.Error, "will not be tried on its own again")
	require.True(t, row().HasCredential)
	require.Equal(t, provider.SignInPausedPasswordRefused, row().SignInPausedFor)

	before = *row().LastPulledAt
	fixture.bills.PullDue(t.Context(), since())
	require.Equal(t, before, *row().LastPulledAt, "a refused password is not tried on a timer")

	// A person asking is one more try, and one that gets in clears the mark.
	fixture.agent.answers = []map[string]any{okPull(`{"token":"rolled-two"}`)}
	ok, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, ok.Status)
	require.Nil(t, row().SignInPausedAt)
	require.False(t, row().NeedsSignIn)
}

func TestACodeNobodyAnsweredPausesUnattendedSignInsUntilAPersonActs(t *testing.T) {
	// Once a code goes unanswered the scheduler waits, rather than text the
	// household on every unattended try.
	for name, answer := range map[string]map[string]any{
		// A browser provider's retry that got past the password to a code.
		"after the password": {
			"needs_sign_in": true, "code_needed": true,
			"reason": "Spectrum took the kept password and then asked for a code",
		},
		// A pull the engine parked, which nothing answered.
		"parked": {
			"challenge": map[string]any{
				"session_id": "park-1", "state": "otp", "method": "sms",
				"prompt": "Enter the code we texted you", "image": "",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newBridgeFixture(t, domain.BillerSpectrum)
			fixture.connection.PullAt = "18:30"
			require.NoError(t, fixture.bills.store.UpdateBillConnection(
				t.Context(), fixture.space, fixture.connection))
			require.NoError(t, fixture.bills.store.SaveBillConnectionCredential(t.Context(), fixture.space,
				fixture.connection.ID, store.BillCredential{Username: "alex", Password: "invented"}))
			row := func() store.BillConnection {
				one, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
				require.NoError(t, err)
				return one
			}

			fixture.agent.answers = []map[string]any{answer}
			_, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
			require.NoError(t, err)
			require.Equal(t, provider.SignInPausedCodeNeeded, row().SignInPausedFor)
			require.True(t, row().HasCredential, "a code is no reason to forget the password")

			// The next evening's pass leaves it alone (asserted on this row's stamp).
			fixture.bills.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
			before := *row().LastPulledAt
			fixture.bills.PullDue(t.Context(), time.Now().Add(time.Hour))
			require.Equal(t, before, *row().LastPulledAt, "a paused sign-in is not tried on a timer")

			// Update now is one more try; one that gets in lifts the pause.
			fixture.agent.answers = []map[string]any{okPull(`{"token":"rolled-two"}`)}
			ok, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
			require.NoError(t, err)
			require.Equal(t, store.BillPullOK, ok.Status)
			require.Equal(t, "", row().SignInPausedFor)
			require.Nil(t, row().SignInPausedAt)
		})
	}
}

func TestACheckOnlyAPersonCanTickPausesUnattendedSignInsUntilAPersonActs(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerSpectrum)
	fixture.connection.PullAt = "18:30"
	require.NoError(t, fixture.bills.store.UpdateBillConnection(t.Context(), fixture.space, fixture.connection))
	require.NoError(t, fixture.bills.store.SaveBillConnectionCredential(t.Context(), fixture.space,
		fixture.connection.ID, store.BillCredential{Username: "alex", Password: "invented"}))
	row := func() store.BillConnection {
		one, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
		require.NoError(t, err)
		return one
	}

	fixture.agent.answers = []map[string]any{{
		"needs_sign_in": true, "page_check": true,
		"reason": "Spectrum showed a check that only a person can tick",
	}}
	stopped, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullNeedsSignIn, stopped.Status)
	require.Contains(t, stopped.Error, "only a person can tick")
	require.Contains(t, stopped.Error, "wait until you sign in and tick it")
	require.Equal(t, provider.SignInPausedPageCheck, row().SignInPausedFor)
	require.True(t, row().HasCredential, "a check is no reason to forget the password")

	fixture.bills.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	before := *row().LastPulledAt
	fixture.bills.PullDue(t.Context(), time.Now().Add(time.Hour))
	require.Equal(t, before, *row().LastPulledAt, "a paused sign-in is not tried on a timer")
}

func mustBiller(t *testing.T, id domain.BillerID) domain.Biller {
	t.Helper()
	biller, known := domain.BillerByID(id)
	require.True(t, known)
	return biller
}

// The login's own second factor is kept when a sign-in starts, handed to the
// engine then and at every pull, and lets a provider that raises a text be
// pulled unattended when the login's codes come another way.
func TestALoginsSecondFactorIsKeptAndCarriedToEveryPull(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerSpectrum)
	require.True(t, mustBiller(t, domain.BillerSpectrum).NeedsAPersonForACode())

	_, err := fixture.bills.StartConnect(t.Context(), fixture.space, fixture.connection.ID,
		"alex@example.test", "invented", "", "", domain.SecondFactorEmail)
	require.NoError(t, err)
	kept, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, domain.SecondFactorEmail, kept.SecondFactor)

	fixture.agent.answers = []map[string]any{okPull(`{"token":"rolled-two"}`)}
	fixture.bills.PullDue(t.Context(), fixture.bills.now().Add(-time.Hour))

	ran, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, ran.LastPullStatus,
		"a login whose codes come by e-mail is not held back for a person")
	// The pass walks every space, so other rows are in the list too; this login's
	// connect and pull are the two that say e-mail.
	fixture.agent.mu.Lock()
	require.Equal(t, "email", fixture.agent.factors[0], "the connect")
	require.Contains(t, fixture.agent.factors[1:], "email", "the pull")
	fixture.agent.mu.Unlock()
}

// A provider's portal that never answers stops only its own connection's
// pull; the scheduled pass's lock is not held for the rest of the window, so
// the connection after it is still pulled in the same pass.
func TestABillPullThatNeverAnswersStopsItselfAndTheNextConnectionStillRuns(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	hanging := &hangingBillsAgent{
		fakeBillsAgent: fakeBillsAgent{answers: []map[string]any{okPull(`{"token":"rolled-two"}`)}},
		hangs:          1,
	}
	fixture.bills.Agent = hanging
	fixture.bills.PullTimeout = 20 * time.Millisecond

	second := &store.BillConnection{
		Biller: domain.BillerAlliant, Label: "second login",
		CredentialSource: store.BillCredentialSession, AutopayRule: domain.AutopayNone,
		PullEnabled: true,
	}
	require.NoError(t, fixture.bills.store.CreateBillConnection(t.Context(), fixture.space, second))
	require.NoError(t, fixture.bills.store.SaveBillConnectionSession(
		t.Context(), fixture.space, second.ID, fakeBillSession, "profile-2", true))

	ran := fixture.bills.PullDue(t.Context(), fixture.bills.now().Add(-time.Hour))
	require.Equal(t, 2, ran, "the hung connection and the one after it both ran")

	first, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	other, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, second.ID)
	require.NoError(t, err)

	byStatus := map[string]string{
		first.LastPullStatus: first.LastPullError, other.LastPullStatus: other.LastPullError,
	}
	require.Contains(t, byStatus, store.BillPullOK, "the connection after the hung one ran to completion")
	require.Contains(t, byStatus[store.BillPullFailed], "did not answer within",
		"the timeout is recorded as the pull's own stopped reason")
}

func (a *fakeBillsAgent) SignInInput(ctx context.Context, sessionID string, events []provider.BillLiveInput) error {
	return nil
}
