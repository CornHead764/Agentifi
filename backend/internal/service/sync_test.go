package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The sync, against a stand-in SimpleFIN server and a real Postgres. The bridge
// is faked over HTTP so the real connector does the parsing, chunking and errlist
// handling (where a dead Access URL is told apart from one bank needing
// reauthorization); only a database can prove a second sync changes nothing.

var syncDay = domain.NewDate(2026, time.March, 20)

// bridge is a stand-in SimpleFIN server whose accounts answer a test replaces
// between runs.
type bridge struct {
	server       *httptest.Server
	answer       func() (int, string)
	accountCalls int
	windows      []bridgeWindow
}

// bridgeWindow is one transaction read's range: [start, end), as the Bridge
// reads it.
type bridgeWindow struct {
	account    string
	start, end domain.Date
}

func (w bridgeWindow) days() int {
	return int(w.end.Time().Sub(w.start.Time()).Hours() / 24)
}

func newBridge(t *testing.T) *bridge {
	t.Helper()
	b := &bridge{answer: func() (int, string) { return http.StatusOK, payload("") }}
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/claim/"):
			fmt.Fprint(w, b.accessURL())
		case strings.HasSuffix(r.URL.Path, "/accounts"):
			b.accountCalls++
			if query := r.URL.Query(); query.Has("start-date") {
				b.windows = append(b.windows, bridgeWindow{
					account: query.Get("account"),
					start:   epochDay(query.Get("start-date")),
					end:     epochDay(query.Get("end-date")),
				})
			}
			status, body := b.answer()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(b.server.Close)
	return b
}

func epochDay(seconds string) domain.Date {
	var unix int64
	fmt.Sscan(seconds, &unix)
	return domain.DateOf(time.Unix(unix, 0).UTC())
}

func (b *bridge) accessURL() string {
	return strings.Replace(b.server.URL, "http://", "http://demo:secret@", 1) + "/simplefin"
}

func (b *bridge) setupToken() string {
	return base64.StdEncoding.EncodeToString([]byte(b.server.URL + "/simplefin/claim/abc123"))
}

func (b *bridge) serve(body string) {
	b.answer = func() (int, string) { return http.StatusOK, body }
}

func (b *bridge) serveStatus(code int) {
	b.answer = func() (int, string) { return code, "" }
}

func payload(errlist string, accounts ...string) string {
	return fmt.Sprintf(`{"errlist": [%s], "accounts": [%s]}`,
		errlist, strings.Join(accounts, ", "))
}

func bridgeAccount(id, name, balance string, txns ...string) string {
	return fmt.Sprintf(
		`{"id": %q, "name": %q, "currency": "USD", "balance": %q, "conn_id": "MX-MBR-1",
		  "org": {"name": "Big Bank", "domain": "bigbank.example"},
		  "transactions": [%s]}`,
		id, name, balance, strings.Join(txns, ", "))
}

func bridgeTxn(id string, on domain.Date, amount, description string) string {
	return fmt.Sprintf(`{"id": %q, "posted": %d, "amount": %q, "description": %q}`,
		id, on.Time().Unix(), amount, description)
}

type syncFixture struct {
	t       *testing.T
	space   store.SpaceID
	bridge  *bridge
	sync    *Sync
	sealed  *store.Store
	connect uuid.UUID
	// today is the day both the pipeline and the connector believe it is.
	today domain.Date
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	cipher, err := store.NewCipher("the-credential-key")
	require.NoError(t, err)
	sealed := db(t).WithCipher(cipher)

	f := &syncFixture{t: t, space: newSpace(t), bridge: newBridge(t), sealed: sealed, today: syncDay}
	clock := func() time.Time { return f.today.Time() }
	bank := &provider.SimpleFin{
		HTTPClient: f.bridge.server.Client(), AllowPrivate: true, Now: clock,
	}
	f.sync = NewSync(sealed, bank)
	f.sync.Now = clock
	return f
}

// claimPending runs the claim and stops where a real one stops: a credential
// stored, no accounts created, and the match screen owed.
func (f *syncFixture) claimPending() store.Connection {
	f.t.Helper()
	connection, err := f.sync.Claim(f.t.Context(), f.space, f.bridge.setupToken(), "")
	require.NoError(f.t, err)
	f.connect = connection.ID
	return connection
}

// claim is a connection whose account matching was answered with "none of
// these are mine", the default most tests start from.
func (f *syncFixture) claim() store.Connection {
	f.t.Helper()
	f.claimPending()
	return f.finish()
}

// finish answers the match screen with these choices; an account at the bank
// left out becomes a new account.
func (f *syncFixture) finish(choices ...LinkChoice) store.Connection {
	f.t.Helper()
	connection, err := f.sync.FinishLinking(f.t.Context(), f.space, f.connect, choices)
	require.NoError(f.t, err)
	return connection
}

func link(externalID string, accountID uuid.UUID) LinkChoice {
	return LinkChoice{ExternalID: externalID, Action: LinkToAccount, AccountID: accountID}
}

func ignore(externalID string) LinkChoice {
	return LinkChoice{ExternalID: externalID, Action: LinkIgnore}
}

func (f *syncFixture) run() SyncReport {
	f.t.Helper()
	report, err := f.sync.SyncConnection(f.t.Context(), f.space, f.connect)
	require.NoError(f.t, err)
	return report
}

func (f *syncFixture) connection() store.Connection {
	f.t.Helper()
	connection, err := f.sealed.GetConnection(f.t.Context(), f.space, f.connect)
	require.NoError(f.t, err)
	return connection
}

func (f *syncFixture) transactions() []store.Transaction {
	f.t.Helper()
	rows, err := db(f.t).ListTransactions(f.t.Context(), f.space, store.TransactionQuery{})
	require.NoError(f.t, err)
	return rows
}

func (f *syncFixture) accounts() []store.Account {
	f.t.Helper()
	rows, err := db(f.t).ListAccounts(f.t.Context(), f.space,
		store.AccountQuery{IncludeClosed: true, IncludeDeleted: true})
	require.NoError(f.t, err)
	return rows
}

func TestAClaimStoresTheConnectionAndCreatesNoAccounts(t *testing.T) {
	// A claim buys a credential and nothing else — see ConnectionPendingLink.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))

	connection := f.claimPending()
	require.Equal(t, store.ConnectionPendingLink, connection.Status)
	require.Empty(t, f.accounts(), "the claim created accounts before anybody matched them")

	opened, err := f.sealed.ConnectionAccessURL(t.Context(), f.space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, f.bridge.accessURL(), opened)
}

func TestAPendingConnectionRefusesToSync(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claimPending()

	before := f.bridge.accountCalls
	report := f.run()

	require.True(t, report.Skipped)
	require.Equal(t, before, f.bridge.accountCalls, "an unmatched connection was read anyway")
	require.Empty(t, f.accounts())
}

func TestDiscoveryShowsTheBanksAccountsWithoutCreatingThem(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claimPending()

	remote, err := f.sync.DiscoverAccounts(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Len(t, remote, 1)
	require.Equal(t, "acc-1", remote[0].ExternalID)
	require.Empty(t, f.accounts(), "discovery wrote an account")
}

func TestEverythingTheFeedSendsAboutARowIsStored(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		fmt.Sprintf(`{"id":"t-1","posted":%d,"transacted_at":%d,"amount":"-9.00",
		  "description":"SQ *XKCD4821","payee":"Square","memo":"CAFE MOCHA / TIP",
		  "extra":{"category":"Coffee Shops","mcc":"5814"}}`,
			syncDay.Time().Unix(), syncDay.AddDays(-2).Time().Unix()))))
	f.claimPending()
	f.finish()
	f.run()

	rows := f.transactions()
	require.Len(t, rows, 1)
	row := rows[0]

	require.Equal(t, "SQ *XKCD4821", row.StatementName)
	require.Equal(t, "CAFE MOCHA / TIP", row.Memo, "the line the category is actually readable from")
	require.Equal(t, syncDay, row.Date)
	require.Equal(t, syncDay.AddDays(-2), row.TransactedOn)

	var extra map[string]any
	require.NoError(t, json.Unmarshal(row.ProviderExtra, &extra))
	require.Equal(t,
		map[string]any{"category": "Coffee Shops", "mcc": "5814"}, extra["extra"])
}

func TestALinkedAccountIsAdoptedRatherThanDuplicated(t *testing.T) {
	// The household's own account keeps its id, name and history, and starts
	// receiving the feed.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140"))))
	f.claimPending()

	mine := store.Account{Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking"}
	require.NoError(t, f.sealed.CreateAccount(t.Context(), f.space, &mine))

	f.finish(link("acc-1", mine.ID))

	f.run()

	accounts := f.accounts()
	require.Len(t, accounts, 1, "the sync created a second account beside the one it was pointed at")
	require.Equal(t, mine.ID, accounts[0].ID)
	require.Equal(t, "Everyday Checking", accounts[0].Name, "the user's name was overwritten")
	require.Equal(t, "acc-1", accounts[0].ExternalID)
	require.Equal(t, f.connect, accounts[0].ConnectionID)
}

func TestAnUnmatchedProviderAccountBecomesANewOne(t *testing.T) {
	// Finishing without pairing creates the accounts on the next sync.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claimPending()

	f.finish()
	f.run()

	accounts := f.accounts()
	require.Len(t, accounts, 1)
	require.Equal(t, "Everyday Checking", accounts[0].Name)
	require.True(t, accounts[0].HasProviderBalance)
	require.Equal(t, "1200.00", accounts[0].ProviderBalance.String())
}

func TestARowOnANonSpendingAccountIsBornReviewed(t *testing.T) {
	// Only spending accounts queue their rows for review.
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		bridgeAccount("acc-1", "Everyday Checking", "1200.00",
			bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140")),
		bridgeAccount("acc-2", "Roth IRA", "50000.00",
			bridgeTxn("t-2", syncDay, "-500.00", "CONTRIBUTION")),
	))
	f.claimPending()

	ira := store.Account{Name: "Roth IRA", Kind: domain.KindInvestment, Type: "roth_ira"}
	require.NoError(t, f.sealed.CreateAccount(t.Context(), f.space, &ira))
	f.finish(link("acc-2", ira.ID))
	f.run()

	byExternal := map[string]store.Transaction{}
	for _, row := range f.transactions() {
		byExternal[row.ExternalID] = row
	}
	require.False(t, byExternal["t-1"].IsReviewed, "a spending-account row skipped the review queue")
	require.True(t, byExternal["t-2"].IsReviewed, "an investment row landed in the review queue")
}

func TestASecondSyncImportsNothingNew(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234 SPRINGFIELD ZZ"),
		bridgeTxn("t2", on(2026, time.March, 18), "-60.00", "SHELL OIL 574"),
	)))
	f.claim()

	first := f.run()
	require.Equal(t, 2, first.TransactionsImported)
	require.Len(t, f.transactions(), 2)

	callsAfterFirst := f.bridge.accountCalls
	second := f.run()
	require.Equal(t, 0, second.TransactionsImported)
	require.Len(t, f.transactions(), 2)
	require.Greater(t, f.bridge.accountCalls, callsAfterFirst, "the second sync never asked the bridge")
}

func TestASyncedRowKeepsTheBanksWordingAndSeedsTheDisplayName(t *testing.T) {
	// Ground rule 5: matching reads the immutable statement name, not the payee.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234 SPRINGFIELD ZZ"),
	)))
	f.claim()
	f.run()

	rows := f.transactions()
	require.Len(t, rows, 1)
	require.Equal(t, "SAFEWAY #1234 SPRINGFIELD ZZ", rows[0].StatementName)
	require.Equal(t, "SAFEWAY #1234 SPRINGFIELD ZZ", rows[0].Payee)
	require.Equal(t, domain.SourceSync, rows[0].Source)
	require.Equal(t, "t1", rows[0].ExternalID)
}

func TestASyncNeverCreatesARowBehindAnAccountsFloor(t *testing.T) {
	// The floor refuses creating rows in the days the bridge serves behind it;
	// reading those days is fine.
	f := newSyncFixture(t)
	floor := on(2026, time.March, 10)
	imported := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		SimpleFINAccountID: "acc-1", SyncFloorOn: floor,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, imported))
	newTransaction(t, f.space, imported, on(2026, time.March, 1), "-40.00",
		withStatementName("OLD IMPORTED ROW"), withSource(domain.SourceSimplifiImport))

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-old", on(2026, time.March, 1), "-40.00", "OLD IMPORTED ROW"),
		bridgeTxn("t-new", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()
	report := f.run()

	require.Equal(t, 1, report.TransactionsImported)
	rows := f.transactions()
	require.Len(t, rows, 2)
	for _, row := range rows {
		if row.Source != domain.SourceSync {
			continue
		}
		require.False(t, row.Date.Before(floor), "a row from before the floor was imported")
	}

	accounts := f.accounts()
	require.Len(t, accounts, 1)
	require.Equal(t, imported.ID, accounts[0].ID)
	require.Equal(t, floor, accounts[0].SyncFloorOn)
}

func TestARowBelowTheFloorIsStillCorrectedEvenThoughItIsNeverCreated(t *testing.T) {
	// The floor governs what may be written, not what may be read: a card posts a
	// charge under its swipe date, which can fall below the floor, and the pending
	// copy must still be settled.
	f := newSyncFixture(t)
	floor := syncDay.AddDays(-5)
	imported := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		SimpleFINAccountID: "acc-1", SyncFloorOn: floor,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, imported))
	// A pending authorization dated a day below the floor, and a posted row
	// below the floor that nothing here has any record of.
	pending := newTransaction(t, f.space, imported, floor.AddDays(-1), "-315.00",
		withStatementName("Sunset Tours"), withSource(domain.SourceSimplifiImport), withPending())

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-settled", floor.AddDays(-1), "-315.00", "SUNSET TOURS 800-555-0142"),
		bridgeTxn("t-never-seen", floor.AddDays(-2), "-61.00", "SOME OLD CHARGE"),
	)))
	f.claim()
	report := f.run()

	require.Equal(t, 0, report.TransactionsImported,
		"a row below the floor was created")
	rows := f.transactions()
	require.Len(t, rows, 1, "the charge below the floor was written a second time")
	require.Equal(t, pending.ID, rows[0].ID)
	require.False(t, rows[0].IsPending, "the authorization was never settled")
	require.Equal(t, "SUNSET TOURS 800-555-0142", rows[0].StatementName)
	require.Equal(t, "t-settled", rows[0].ExternalID)
}

func TestTheWindowCoversAWholeStatementSoALateCorrectionStillArrives(t *testing.T) {
	// A charge keeps changing after it first appears (a tip is added), and the
	// overlap must reach far enough back to pick that up.
	f := newSyncFixture(t)
	linked := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true, SimpleFINAccountID: "acc-1",
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, linked))
	early := newTransaction(t, f.space, linked, syncDay.AddDays(-25), "-40.00",
		withStatementName("TST*CORNER PUB"), withSource(domain.SourceSync),
		withExternalID("t-tip"))
	newTransaction(t, f.space, linked, syncDay, "-12.00",
		withStatementName("FUEL STOP"), withSource(domain.SourceSync),
		withExternalID("t-recent"))

	// Twenty-five days back, the bank restates the charge with the tip on it.
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-tip", syncDay.AddDays(-25), "-48.00", "TST*CORNER PUB"),
		bridgeTxn("t-recent", syncDay, "-12.00", "FUEL STOP"),
	)))
	f.claim()
	report := f.run()

	require.Equal(t, 0, report.TransactionsImported)
	require.Equal(t, 1, report.TransactionsUpdated, "the older charge was never revisited")
	corrected, err := db(t).GetTransaction(t.Context(), f.space, early.ID)
	require.NoError(t, err)
	require.Equal(t, "-48.00", corrected.Amount.String(), "the tip never reached the ledger")
}

func TestAForecastRowDoesNotSetTheSyncFloor(t *testing.T) {
	// Imported estimate rows can be dated a year out; only a day money actually
	// moved may set the floor.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claimPending()

	mine := store.Account{Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking"}
	require.NoError(t, f.sealed.CreateAccount(t.Context(), f.space, &mine))
	posted := syncDay.AddDays(-3)
	newTransaction(t, f.space, &mine, posted, "-40.00",
		withStatementName("FUEL STOP #1140"), withSource(domain.SourceSimplifiImport))
	newTransaction(t, f.space, &mine, syncDay.AddDays(365), "-2345.67",
		withStatementName("Mortgage"), withSource(domain.SourceSimplifiImport),
		withEstimate(store.ProjectedEstimate))

	f.finish(link("acc-1", mine.ID))
	linked, err := db(t).GetAccount(t.Context(), f.space, mine.ID)
	require.NoError(t, err)
	require.Equal(t, posted, linked.SyncFloorOn,
		"the floor was taken from a forecast rather than from posted history")
}

func TestAForecastRowDoesNotStopTheNextSyncFromImporting(t *testing.T) {
	// A floor taken from a forecast would put every resume window in the future,
	// importing nothing while reporting success.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-new", syncDay.AddDays(-1), "-25.00", "SAFEWAY #1234"),
	)))
	f.claimPending()

	mine := store.Account{Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking"}
	require.NoError(t, f.sealed.CreateAccount(t.Context(), f.space, &mine))
	newTransaction(t, f.space, &mine, syncDay.AddDays(-3), "-40.00",
		withStatementName("FUEL STOP #1140"), withSource(domain.SourceSimplifiImport))
	newTransaction(t, f.space, &mine, syncDay.AddDays(365), "-2345.67",
		withStatementName("Mortgage"), withSource(domain.SourceSimplifiImport),
		withEstimate(store.ProjectedEstimate))

	f.finish(link("acc-1", mine.ID))

	require.Equal(t, 1, f.run().TransactionsImported,
		"the bridge's row was dropped behind a floor taken from a forecast")
}

func TestAnImportedRowIsNotImportedAgainUnderTheBridgesID(t *testing.T) {
	// An imported row carries no SimpleFIN id, so the day, amount and folded
	// wording stand in for one.
	f := newSyncFixture(t)
	imported := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		SimpleFINAccountID: "acc-1", SyncFloorOn: on(2026, time.March, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, imported))
	newTransaction(t, f.space, imported, on(2026, time.March, 15), "-25.00",
		withStatementName("SAFEWAY #1234, SPRINGFIELD ZZ"), withSource(domain.SourceSimplifiImport))

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "Safeway #1234 Springfield ZZ"),
	)))
	f.claim()
	report := f.run()

	require.Equal(t, 0, report.TransactionsImported)
	require.Len(t, f.transactions(), 1)
}

func TestFourIdenticalChargesOnOneDayAreFourRows(t *testing.T) {
	// Four identical charges on one day are four purchases; a content check that
	// answered "already here" after the first would drop three.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-1", syncDay, "-45.00", "AIRLINE ANYTOWN"),
		bridgeTxn("t-2", syncDay, "-45.00", "AIRLINE ANYTOWN"),
		bridgeTxn("t-3", syncDay, "-45.00", "AIRLINE ANYTOWN"),
		bridgeTxn("t-4", syncDay, "-45.00", "AIRLINE ANYTOWN"),
	)))
	f.claim()

	require.Equal(t, 4, f.run().TransactionsImported)
	require.Len(t, f.transactions(), 4)

	// Syncing again adds none of them: each now carries the bank's own id.
	require.Equal(t, 0, f.run().TransactionsImported)
	require.Len(t, f.transactions(), 4)
}

func TestAnImportedPendingRowIsSettledByThePostedChargeRatherThanDuplicated(t *testing.T) {
	// An imported pending row has cleaned wording and no bank id, so when it posts
	// under the bank's wording neither id nor content matches; it must still be
	// settled rather than duplicated.
	f := newSyncFixture(t)
	imported := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		SimpleFINAccountID: "acc-1", SyncFloorOn: syncDay.AddDays(-30),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, imported))
	groceries := newCategory(t, f.space, "Home Services", uuid.Nil)
	pending := newTransaction(t, f.space, imported, syncDay.AddDays(-2), "-90.00",
		withStatementName("Greenlawn"), withPayee("GreenLawn"),
		withSource(domain.SourceSimplifiImport), withPending(),
		withCategory(groceries.ID))

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-posted", syncDay, "-90.00", "GREENLAWN *LOCKBOX SPRINGFIELD"),
	)))
	f.claim()
	report := f.run()

	require.Equal(t, 0, report.TransactionsImported, "the posted charge landed as a second row")
	rows := f.transactions()
	require.Len(t, rows, 1)

	// Adopted: same id, now settled, dated, worded and identified by the bank.
	require.Equal(t, pending.ID, rows[0].ID)
	require.False(t, rows[0].IsPending, "the row is still pending after the charge posted")
	require.Equal(t, syncDay, rows[0].Date)
	require.Equal(t, "GREENLAWN *LOCKBOX SPRINGFIELD", rows[0].StatementName)
	require.Equal(t, "t-posted", rows[0].ExternalID)
	require.Equal(t, "GreenLawn", rows[0].Payee)
	require.Equal(t, groceries.ID, rows[0].CategoryID)
}

func TestOnePendingRowAbsorbsOnlyOneOfSeveralIdenticalCharges(t *testing.T) {
	// One imported pending authorization and four posted charges of the same
	// amount: one settles the pending row, the other three are new.
	f := newSyncFixture(t)
	imported := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		SimpleFINAccountID: "acc-1", SyncFloorOn: syncDay.AddDays(-30),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, imported))
	newTransaction(t, f.space, imported, syncDay.AddDays(-1), "-45.00",
		withStatementName("Airline"), withSource(domain.SourceSimplifiImport), withPending())

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-1", syncDay, "-45.00", "AIRLINE 00000000000001 ANYTOWN"),
		bridgeTxn("t-2", syncDay, "-45.00", "AIRLINE 00000000000002 ANYTOWN"),
		bridgeTxn("t-3", syncDay, "-45.00", "AIRLINE 00000000000003 ANYTOWN"),
		bridgeTxn("t-4", syncDay, "-45.00", "AIRLINE 00000000000004 ANYTOWN"),
	)))
	f.claim()
	report := f.run()

	require.Equal(t, 3, report.TransactionsImported)
	rows := f.transactions()
	require.Len(t, rows, 4, "the pending row swallowed more than the one charge it was")
	for _, row := range rows {
		require.False(t, row.IsPending, "a pending row survived four posted charges")
	}
}

func TestAPendingRowTooOldToBeTheChargeIsLeftAlone(t *testing.T) {
	// The window keeps the amount from being the whole argument: pairing with an
	// authorization from three weeks back would delete a real transaction.
	f := newSyncFixture(t)
	imported := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		SimpleFINAccountID: "acc-1", SyncFloorOn: syncDay.AddDays(-60),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, imported))
	newTransaction(t, f.space, imported, syncDay.AddDays(-21), "-90.00",
		withStatementName("Greenlawn"), withSource(domain.SourceSimplifiImport), withPending())

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t-posted", syncDay, "-90.00", "GREENLAWN *LOCKBOX SPRINGFIELD"),
	)))
	f.claim()

	require.Equal(t, 1, f.run().TransactionsImported)
	require.Len(t, f.transactions(), 2)
}

func TestADeletedRowIsNotResurrectedByTheNextSync(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()
	f.run()

	rows := f.transactions()
	require.Len(t, rows, 1)
	require.NoError(t, db(t).DeleteTransaction(t.Context(), f.space, rows[0].ID))

	require.Equal(t, 0, f.run().TransactionsImported)
	require.Empty(t, f.transactions())
}

func TestAPendingRowThatPostsIsSettledRatherThanDuplicated(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		`{"id": "t1", "posted": `+
			fmt.Sprint(on(2026, time.March, 15).Time().Unix())+
			`, "amount": "-25.00", "description": "SAFEWAY #1234", "pending": true}`,
	)))
	f.claim()
	f.run()
	rows := f.transactions()
	require.Len(t, rows, 1)
	require.True(t, rows[0].IsPending)

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 16), "-26.00", "SAFEWAY #1234"),
	)))
	report := f.run()

	require.Equal(t, 0, report.TransactionsImported)
	require.Equal(t, 1, report.TransactionsUpdated)
	settled := f.transactions()
	require.Len(t, settled, 1)
	require.False(t, settled[0].IsPending)
	require.Equal(t, "-26.00", settled[0].Amount.String())
	require.Equal(t, on(2026, time.March, 16), settled[0].Date)
}

// A pending charge cannot be dated after today; a connector reporting a
// scheduled payment that way is describing an expectation.
func TestAPendingRowDatedInTheFutureIsNotWritten(t *testing.T) {
	f := newSyncFixture(t)
	future := domain.DateOf(time.Now()).AddDays(30)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		`{"id": "t1", "posted": `+fmt.Sprint(future.Time().Unix())+
			`, "amount": "-2345.67", "description": "MORTGAGE", "pending": true}`,
		bridgeTxn("t2", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()

	require.Equal(t, 1, f.run().TransactionsImported)
	rows := f.transactions()
	require.Len(t, rows, 1)
	require.Equal(t, "SAFEWAY #1234", rows[0].StatementName)
}

func TestASyncedCardChargeIsStampedWithItsStatementEffectiveDate(t *testing.T) {
	// A synced card charge gets the effective date of its bill's due day, which
	// reports and the spending plan read.
	f := newSyncFixture(t)
	card := func(txns string) string {
		return fmt.Sprintf(
			`{"id": "card-1", "name": "Rewards Card", "currency": "USD", "balance": "300.00",
			  "conn_id": "MX-MBR-1", "org": {"name": "Big Bank"},
			  "extra": {"account-type": "credit card"}, "transactions": [%s]}`, txns)
	}
	f.bridge.serve(payload("", card("")))
	f.claim()
	f.run()

	accounts := f.accounts()
	require.Len(t, accounts, 1)
	acc := accounts[0]
	require.Equal(t, domain.KindCreditCard, acc.Kind)

	// A card whose cycle closes on the 15th with payment due on the 5th.
	closeDay := int16(15)
	acc.StatementCloseDay = &closeDay
	acc.DueDate = on(2026, time.March, 5)
	require.NoError(t, db(t).UpdateAccount(t.Context(), f.space, &acc))

	f.bridge.serve(payload("", card(bridgeTxn("c1", on(2026, time.March, 10), "-40.00", "COFFEE"))))
	f.run()

	rows := f.transactions()
	require.Len(t, rows, 1)
	// Charged Mar 10, the cycle closes Mar 15, and the next payment due is Apr 5.
	require.Equal(t, on(2026, time.April, 5), rows[0].EffectiveDate)
}

func TestASyncedChargeIsLinkedToItsSeries(t *testing.T) {
	// Series matching runs inside the sync pipeline, so a synced recurring charge
	// advances its series.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()
	f.run()

	accounts := f.accounts()
	require.Len(t, accounts, 1)
	account := accounts[0]
	series := newSeries(t, f.space, &account)

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-15.00", "STREAMSVC.COM"))))
	f.run()

	rows := f.transactions()
	require.Len(t, rows, 1)
	require.Equal(t, series.ID, rows[0].SeriesID)
	require.Equal(t, on(2026, time.March, 15), rows[0].SeriesDueOn)
	require.Equal(t, on(2026, time.April, 15), loadSeries(t, f.space, series.ID).NextDueOn)
}

func TestASyncEditKeepsWhatTheUserOwns(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()
	f.run()

	row := f.transactions()[0]
	row.Payee = "Safeway"
	row.Notes = "check the receipt"
	require.NoError(t, db(t).UpdateTransaction(t.Context(), f.space, &row))

	f.run()
	after := reload(t, f.space, row.ID)
	require.Equal(t, "Safeway", after.Payee)
	require.Equal(t, "check the receipt", after.Notes)
	require.Equal(t, "SAFEWAY #1234", after.StatementName)
}

func TestASyncedTransferIsPairedOnTheWayIn(t *testing.T) {
	// Trap 3: pairing runs on synced rows, over the rows the run inserted.
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		bridgeAccount("acc-1", "Everyday Checking", "1200.00",
			bridgeTxn("t1", on(2026, time.March, 15), "-200.00", "ONLINE TRANSFER TO SAVINGS")),
		bridgeAccount("acc-2", "Savings", "5000.00",
			bridgeTxn("t2", on(2026, time.March, 15), "200.00", "ONLINE TRANSFER FROM CHECKING")),
	))
	f.claim()
	require.Equal(t, 2, f.run().TransactionsImported)

	for _, row := range f.transactions() {
		require.NotEqual(t, uuid.Nil, row.TransferPairID, "%s was not paired", row.StatementName)
	}
}

// byStatementName keys rows by the bank's wording, which the sync never
// changes.
func byStatementName(rows []store.Transaction) map[string]store.Transaction {
	out := make(map[string]store.Transaction, len(rows))
	for _, row := range rows {
		out[row.StatementName] = row
	}
	return out
}

func TestSyncedRowsLandWithARunningBalance(t *testing.T) {
	// The register's balance column is materialized, so the sync must write it.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234"),
		bridgeTxn("t2", on(2026, time.March, 18), "-10.00", "FUEL STOP #1140"),
	)))
	f.claim()
	f.run()

	rows := byStatementName(f.transactions())
	require.True(t, rows["SAFEWAY #1234"].HasBalance)
	require.True(t, rows["FUEL STOP #1140"].HasBalance)
	// The sequence is anchored on the bank's own figure, so the last row ends
	// on it and the one before is a step back up.
	require.Equal(t, "1210.00", rows["SAFEWAY #1234"].Balance.String())
	require.Equal(t, "1200.00", rows["FUEL STOP #1140"].Balance.String())
}

func TestAPendingRowThatPostsRestatesTheBalancesBehindIt(t *testing.T) {
	// A sync that inserts nothing can still move balances: a pending charge posting
	// at a different amount, in an account the settle did not insert into.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		`{"id": "t1", "posted": `+
			fmt.Sprint(on(2026, time.March, 15).Time().Unix())+
			`, "amount": "-25.00", "description": "SAFEWAY #1234", "pending": true}`,
		bridgeTxn("t2", on(2026, time.March, 18), "-10.00", "FUEL STOP #1140"),
	)))
	f.claim()
	f.run()
	require.Equal(t, "1200.00", byStatementName(f.transactions())["FUEL STOP #1140"].Balance.String())

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1199.00",
		bridgeTxn("t1", on(2026, time.March, 16), "-26.00", "SAFEWAY #1234"),
		bridgeTxn("t2", on(2026, time.March, 18), "-10.00", "FUEL STOP #1140"),
	)))
	report := f.run()
	require.Equal(t, 0, report.TransactionsImported)
	require.Equal(t, 1, report.TransactionsUpdated)

	rows := byStatementName(f.transactions())
	require.Equal(t, "1209.00", rows["SAFEWAY #1234"].Balance.String())
	require.Equal(t, "1199.00", rows["FUEL STOP #1140"].Balance.String())
}

func TestABankLevelWarningLeavesTheConnectionUsable(t *testing.T) {
	// One bank losing its authorization is not the connection failing: the Access
	// URL still works and every other bank still syncs.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()

	f.bridge.serve(payload(
		`{"code": "con.auth", "conn_id": "MBR-1", "msg": "Big Bank needs reauthorization"}`,
		bridgeAccount("acc-1", "Everyday Checking", "1200.00",
			bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234")),
	))
	report := f.run()

	require.Equal(t, store.ConnectionActive, report.Status)
	require.Equal(t, 1, report.TransactionsImported, "a bank warning stopped the import")
	require.Len(t, report.BankWarnings, 1)
	require.Equal(t, "Big Bank", report.BankWarnings[0].Institution)

	connection := f.connection()
	require.False(t, connection.NeedsSetupToken())
	require.NotNil(t, connection.LastSuccessfulSyncAt)
	require.Len(t, connection.SyncErrors, 1)
}

func TestAWarningThatHasBeenFixedStopsBeingReported(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()

	f.bridge.serve(payload(
		`{"code": "con.auth", "conn_id": "MBR-1", "msg": "Needs reauthorization"}`,
		bridgeAccount("acc-1", "Everyday Checking", "1200.00"),
	))
	require.Len(t, f.run().BankWarnings, 1)

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	require.Empty(t, f.run().BankWarnings)
	require.Empty(t, f.connection().SyncErrors)
}

func TestAGeneralAuthFailureAsksForAFreshSetupToken(t *testing.T) {
	// The Access URL itself is refused; only a new setup token fixes it.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	connection := f.claim()
	// A claim is not a sync.
	require.Nil(t, connection.LastSuccessfulSyncAt)

	f.bridge.serve(payload(`{"code": "gen.auth", "msg": "Reauthorize at SimpleFIN Bridge"}`))
	report := f.run()

	require.Equal(t, store.ConnectionCredentialsExpired, report.Status)
	require.Contains(t, report.StatusDetail, "Reauthorize")

	stored := f.connection()
	require.True(t, stored.NeedsSetupToken())
	require.Nil(t, stored.RetryNotBefore)
}

func TestARefusedAccessURLIsAlsoACredentialFailure(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()

	f.bridge.serveStatus(http.StatusForbidden)
	require.Equal(t, store.ConnectionCredentialsExpired, f.run().Status)
}

func TestReclaimingReplacesTheCredentialWithoutDuplicatingTheAccounts(t *testing.T) {
	// Reclaim keeps the connection: claiming again would duplicate every account.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 15), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()
	f.run()
	before := f.accounts()
	require.Len(t, before, 1)

	f.bridge.serve(payload(`{"code": "gen.auth", "msg": "Reauthorize"}`))
	require.Equal(t, store.ConnectionCredentialsExpired, f.run().Status)

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1310.00")))
	reclaimed, err := f.sync.Reclaim(t.Context(), f.space, f.connect, f.bridge.setupToken())
	require.NoError(t, err)
	require.Equal(t, store.ConnectionActive, reclaimed.Status)
	require.False(t, reclaimed.NeedsSetupToken())

	after := f.accounts()
	require.Len(t, after, 1, "the reclaim duplicated the accounts")
	require.Equal(t, before[0].ID, after[0].ID)
	require.Equal(t, "1310.00", after[0].ProviderBalance.String())
	require.Len(t, f.transactions(), 1)
}

func TestARateLimitParksTheConnectionWithoutBreakingIt(t *testing.T) {
	// The credential is fine and the user must not be told to reconnect. The
	// connection is parked until retry_not_before.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()

	f.bridge.serveStatus(http.StatusTooManyRequests)
	report := f.run()

	require.Equal(t, store.ConnectionRateLimited, report.Status)
	require.NotNil(t, report.RetryNotBefore)
	require.Equal(t, syncDay.Time().Add(syncRateLimitBackoff), report.RetryNotBefore.UTC())

	stored := f.connection()
	require.False(t, stored.NeedsSetupToken())
	opened, err := f.sealed.ConnectionAccessURL(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Equal(t, f.bridge.accessURL(), opened)
}

func TestARecoveredConnectionGoesBackToActive(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()

	f.bridge.serveStatus(http.StatusTooManyRequests)
	require.Equal(t, store.ConnectionRateLimited, f.run().Status)

	// Past the backoff: a sync asked for earlier is refused without reaching the
	// Bridge (see TestAParkedConnectionIsNotReadEvenWhenAskedDirectly).
	recovered := syncDay.Time().Add(syncRateLimitBackoff).Add(time.Minute)
	f.sync.Now = func() time.Time { return recovered }

	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	require.Equal(t, store.ConnectionActive, f.run().Status)
	stored := f.connection()
	require.Nil(t, stored.RetryNotBefore)
	require.Empty(t, stored.StatusDetail)
}

func TestADebtAccountsBalanceIsStoredNegative(t *testing.T) {
	// The Bridge reports what a card holds; storage wants what is owed, signed by
	// the account's kind.
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		`{"id": "card-1", "name": "Rewards Card", "currency": "USD", "balance": "300.00",
		  "org": {"name": "Big Bank"}, "extra": {"account-type": "credit card"},
		  "transactions": []}`))
	f.claim()
	// Accounts appear on the first sync, not on the claim.
	f.run()

	accounts := f.accounts()
	require.Len(t, accounts, 1)
	require.Equal(t, domain.KindCreditCard, accounts[0].Kind)
	require.Equal(t, "-300.00", accounts[0].ProviderBalance.String())
}

func TestASyncCannotReadAConnectionInAnotherSpace(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()

	_, err := f.sync.SyncConnection(t.Context(), newSpace(t), f.connect)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestAnUnreadableCredentialIsReportedRatherThanSyncedAround(t *testing.T) {
	// A rotated key must not look like "the bank refused us".
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()

	otherKey, err := store.NewCipher("a-different-key")
	require.NoError(t, err)
	stranded := NewSync(db(t).WithCipher(otherKey), &provider.SimpleFin{
		HTTPClient: f.bridge.server.Client(),
		Now:        func() time.Time { return syncDay.Time() },
	})

	_, err = stranded.SyncConnection(t.Context(), f.space, f.connect)
	require.ErrorIs(t, err, store.ErrCredentialUnreadable)
}

// The throttle, and the scheduler that respects it.

func TestAParkedConnectionIsNotReadEvenWhenAskedDirectly(t *testing.T) {
	// Sync now on a throttled connection must not reach the Bridge, which answers
	// a refused read with a longer wait.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acct-1", "Checking", "100.00")))
	f.claim()

	parked := f.connection()
	later := syncDay.Time().Add(2 * time.Hour)
	parked.Status = store.ConnectionRateLimited
	parked.StatusDetail = "the Bridge is throttling this connection"
	parked.RetryNotBefore = &later
	require.NoError(t, f.sealed.UpdateConnection(t.Context(), f.space, &parked))

	before := f.bridge.accountCalls
	report := f.run()

	require.True(t, report.Skipped, "a parked connection was synced anyway")
	require.Equal(t, before, f.bridge.accountCalls, "the Bridge was read while the connection was parked")
	require.Equal(t, store.ConnectionRateLimited, report.Status)
	require.NotNil(t, report.RetryNotBefore)
}

func TestTheParkExpiresAndTheConnectionSyncsAgain(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acct-1", "Checking", "100.00")))
	f.claim()

	parked := f.connection()
	passed := syncDay.Time().Add(-time.Minute)
	parked.Status = store.ConnectionRateLimited
	parked.RetryNotBefore = &passed
	require.NoError(t, f.sealed.UpdateConnection(t.Context(), f.space, &parked))

	report := f.run()
	require.False(t, report.Skipped)
	require.Equal(t, store.ConnectionActive, report.Status)
}

func TestTheSchedulerSyncsWhatIsDueAndNothingTwice(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acct-1", "Checking", "100.00",
		bridgeTxn("t-1", syncDay, "-12.50", "FUEL STOP #1140"))))
	f.claim()

	// The claim already stamped last_sync_at, so open the window after it: the
	// scheduler's first pass is the next day's run.
	now := syncDay.Time().Add(26 * time.Hour)
	f.sync.Now = func() time.Time { return now }
	scheduler := &Scheduler{
		Store: db(t),
		Sync:  f.sync,
		At:    SyncWindow{Hour: 4, Minute: 0},
		Now:   func() time.Time { return now },
	}

	// Counted on this fixture's own bridge: the scheduler's claim is global, so a
	// pass also picks up connections other tests left behind.
	before := f.bridge.accountCalls
	scheduler.RunDue(t.Context())
	require.Greater(t, f.bridge.accountCalls, before, "the due connection was not synced")

	synced := f.connection()
	require.Equal(t, store.ConnectionActive, synced.Status)
	require.NotNil(t, synced.LastSuccessfulSyncAt)

	after := f.bridge.accountCalls
	scheduler.RunDue(t.Context())
	require.Equal(t, after, f.bridge.accountCalls,
		"the same connection was read twice inside one window")
}

func TestAnIgnoredAccountIsNeverCreated(t *testing.T) {
	// Two SimpleFIN connections can reach the same bank account; the second can
	// ignore it instead of creating a copy that returns on every sync.
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		bridgeAccount("acc-1", "Everyday Checking", "1200.00"),
		bridgeAccount("acc-2", "Everyday Checking (again)", "1200.00")))
	f.claimPending()
	f.finish(ignore("acc-2"))
	f.run()

	accounts := f.accounts()
	require.Len(t, accounts, 1)
	require.Equal(t, "Everyday Checking", accounts[0].Name)
}

func TestAnIgnoredAccountStaysIgnoredOnTheNextSync(t *testing.T) {
	// A refusal must hold for later syncs, including the scheduled ones.
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		bridgeAccount("acc-1", "Everyday Checking", "1200.00"),
		bridgeAccount("acc-2", "Everyday Checking (again)", "1200.00")))
	f.claimPending()
	f.finish(ignore("acc-2"))

	f.run()
	f.run()

	names := []string{}
	for _, account := range f.accounts() {
		names = append(names, account.Name)
	}
	require.Equal(t, []string{"Everyday Checking"}, names)
}

func TestIgnoringAnAccountTheConnectionAlreadyMadeDetachesIt(t *testing.T) {
	// Ignoring after the fact stops the feed and takes the account out of the
	// lists; the account and its history stay, to be un-ignored.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claim()
	f.run()

	before := f.accounts()
	require.Len(t, before, 1)
	require.Equal(t, f.connect, before[0].ConnectionID)

	f.finish(ignore("acc-1"))
	f.run()

	after := f.accounts()
	require.Len(t, after, 1, "the account is detached, not deleted, and not duplicated")
	require.Equal(t, before[0].ID, after[0].ID)
	require.Equal(t, uuid.Nil, after[0].ConnectionID)
	require.Empty(t, after[0].SimpleFINAccountID)
	require.True(t, after[0].IsIgnored(), "refused, it would otherwise stay listed as a manual account")
}

func TestAnIgnoredAccountIsNotFedAndResumesWhenUnignored(t *testing.T) {
	// Ignoring the local account keeps its link, so un-ignoring resumes the feed on
	// the same row rather than leaving the next sync a duplicate to create.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Coin Wallet", "0.00")))
	f.claim()
	f.run()
	before := f.accounts()
	require.Len(t, before, 1)

	_, err := db(t).SetAccountsIgnored(t.Context(), f.space, []uuid.UUID{before[0].ID}, true)
	require.NoError(t, err)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Coin Wallet", "40.00",
		bridgeTxn("t-1", syncDay, "40.00", "DEPOSIT"))))
	f.run()

	ignored := f.accounts()
	require.Len(t, ignored, 1)
	require.True(t, ignored[0].IsIgnored())
	require.Equal(t, f.connect, ignored[0].ConnectionID, "the link is kept")
	require.Equal(t, "0.00", ignored[0].ProviderBalance.String(), "not updated")
	require.Empty(t, f.transactions(), "not fed")

	_, err = db(t).SetAccountsIgnored(t.Context(), f.space, []uuid.UUID{before[0].ID}, false)
	require.NoError(t, err)
	f.run()

	after := f.accounts()
	require.Len(t, after, 1, "the same account, not a second one")
	require.Equal(t, before[0].ID, after[0].ID)
	require.Equal(t, "40.00", after[0].ProviderBalance.String())
	require.Len(t, f.transactions(), 1)
}

func TestADeletedAccountStaysDeletedAndUnfed(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Coin Wallet", "0.00")))
	f.claim()
	f.run()
	before := f.accounts()
	require.Len(t, before, 1)

	require.NoError(t, db(t).DeleteAccount(t.Context(), f.space, before[0].ID))
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Coin Wallet", "40.00",
		bridgeTxn("t-1", syncDay, "40.00", "DEPOSIT"))))
	f.run()

	after := f.accounts()
	require.Len(t, after, 1, "no second account")
	require.True(t, after[0].IsDeleted)
	require.Equal(t, "0.00", after[0].ProviderBalance.String(), "not updated")
	var rows int
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM transactions WHERE account_id = $1`, before[0].ID).Scan(&rows))
	require.Zero(t, rows, "not fed")
}

func TestARestoredAccountIsCreatedByTheNextSync(t *testing.T) {
	// Un-ignoring creates nothing itself; the next sync does.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00")))
	f.claimPending()
	f.finish(ignore("acc-1"))
	f.run()
	require.Empty(t, f.accounts())

	ignored, err := db(t).ListIgnoredRemoteAccounts(t.Context(), f.space, f.connect)
	require.NoError(t, err)
	require.Len(t, ignored, 1)
	restored, err := db(t).RestoreRemoteAccount(t.Context(), f.space, f.connect, ignored[0].ID)
	require.NoError(t, err)
	require.True(t, restored)

	f.run()
	require.Len(t, f.accounts(), 1)
}

func TestAnAccountIsCreatedWithTheTypeItsNameNames(t *testing.T) {
	// The drawer groups crypto and a policy's cash value under headings of
	// their own, so the type the feed's words name is the one created.
	f := newSyncFixture(t)
	f.bridge.serve(payload("",
		bridgeAccount("acc-1", "Coinbase", "300.00"),
		bridgeAccount("acc-2", "Universal Life Policy", "5200.00"),
		bridgeAccount("acc-3", "Everyday Checking", "1200.00")))
	f.claim()
	f.run()

	types := map[string]string{}
	for _, account := range f.accounts() {
		if account.Type != "cash" {
			require.Equal(t, domain.KindInvestment, account.Kind, account.Name)
		}
		types[account.Name] = account.Type
	}
	require.Equal(t, map[string]string{
		"Coinbase": "crypto", "Universal Life Policy": "life_insurance", "Everyday Checking": "cash",
	}, types)
}

// --- The settle backlog ------------------------------------------------------
//
// Rows are inserted and committed before the settle runs, so a failed settle
// leaves them marked; the mark is what lets the next run find them, since it
// resumes past their dates.

func (f *syncFixture) unsettled() []uuid.UUID {
	f.t.Helper()
	ids, err := db(f.t).TransactionsNeedingSettle(f.t.Context(), f.space)
	require.NoError(f.t, err)
	return ids
}

// strand is the state a failed settle leaves behind: the rows are in the
// ledger, marked, and none of the settle's work has been done to them.
func (f *syncFixture) strand() {
	f.t.Helper()
	_, err := db(f.t).Pool().Exec(f.t.Context(), `
		UPDATE transactions SET needs_settle = true, balance = NULL
		WHERE space_id = $1 AND NOT is_deleted`, f.space.UUID())
	require.NoError(f.t, err)
}

func TestASuccessfulSyncLeavesNothingOwedASettle(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 18), "-25.00", "SAFEWAY #1234"),
		bridgeTxn("t2", on(2026, time.March, 19), "-11.00", "SHELL OIL"),
	)))
	f.claim()

	require.Equal(t, 2, f.run().TransactionsImported)
	require.Empty(t, f.unsettled(), "a settled row still carries the mark")
	for _, row := range f.transactions() {
		require.True(t, row.HasBalance, "the settle did not write a running balance")
	}
}

func TestRowsAnEarlierSettleLeftBehindAreSettledByTheNextSync(t *testing.T) {
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 18), "-25.00", "SAFEWAY #1234"),
		bridgeTxn("t2", on(2026, time.March, 19), "-11.00", "SHELL OIL"),
	)))
	f.claim()
	f.run()

	f.strand()
	require.Len(t, f.unsettled(), 2)

	// The next run asks for the same window and imports nothing new, so nothing
	// else would ever revisit these rows.
	report := f.run()
	require.Equal(t, 0, report.TransactionsImported)

	require.Empty(t, f.unsettled(), "the backlog survived a second sync")
	for _, row := range f.transactions() {
		require.True(t, row.HasBalance, "the backlog was not settled")
	}
}

func TestASettleBacklogIsRepairableOnItsOwn(t *testing.T) {
	// The same repair without a bank: a connection whose credential has expired
	// never syncs again.
	f := newSyncFixture(t)
	f.bridge.serve(payload("", bridgeAccount("acc-1", "Everyday Checking", "1200.00",
		bridgeTxn("t1", on(2026, time.March, 18), "-25.00", "SAFEWAY #1234"),
	)))
	f.claim()
	f.run()
	f.strand()

	settled, err := Ingest{}.SettleBacklog(t.Context(), db(t), f.space)
	require.NoError(t, err)
	require.Equal(t, 1, settled)
	require.Empty(t, f.unsettled())

	// Nothing owed, nothing done, no error: the sync calls this every run.
	settled, err = Ingest{}.SettleBacklog(t.Context(), db(t), f.space)
	require.NoError(t, err)
	require.Zero(t, settled)
}

func TestAnEstablishedBalanceDroppingToZeroIsHeldNotApplied(t *testing.T) {
	// A stable, material balance followed by the feed reporting 0.00 on a healthy
	// connection: the sync keeps the last-known balance, records the zero as
	// withheld, and warns.
	f := newSyncFixture(t)
	f.claim()

	f.bridge.serve(payload("", bridgeAccount("acct-1", "Savings", "42000.00")))
	f.run()

	accounts := f.accounts()
	require.Len(t, accounts, 1)
	account := accounts[0]
	require.True(t, account.HasProviderBalance)
	require.Equal(t, "42000.00", account.ProviderBalance.String())

	// Two snapshots twenty days apart make the non-zero history established.
	require.NoError(t, f.sealed.UpsertBalanceSnapshots(f.t.Context(), f.space,
		[]store.BalanceSnapshot{
			{AccountID: account.ID, AsOf: syncDay.AddDays(-30), Balance: domain.MustFromString("42000.00")},
			{AccountID: account.ID, AsOf: syncDay.AddDays(-10), Balance: domain.MustFromString("42000.00")},
		}))

	f.bridge.serve(payload("", bridgeAccount("acct-1", "Savings", "0.00")))
	report := f.run()

	held := f.accounts()[0]
	require.Equal(t, "42000.00", held.ProviderBalance.String(),
		"the last-known balance must stand, not the reported zero")
	require.True(t, held.HasWithheldBalance)
	require.Equal(t, "0.00", held.WithheldBalance.String())
	require.NotNil(t, held.WithheldBalanceAt)
	require.False(t, held.AcceptZeroBalance)

	// One warning per problem: the account row carries the hold and its reason,
	// and the connection reports no bank warning beside it.
	require.Equal(t, 1, report.BalancesHeld)
	require.Empty(t, report.BankWarnings)
	require.Empty(t, f.connection().SyncErrors)
	// The feed has never sent this account a transaction, so nothing could
	// explain the zero, and the warning says so.
	require.Equal(t,
		"SimpleFIN reported $0.00 for an account whose feed carries no transactions, so nothing "+
			"can explain the change from $42,000.00. Keeping $42,000.00 until you confirm the change.",
		held.WithheldBalanceReason)

	// Net worth reads the held balance, both in the chart's snapshot and in the
	// current account balance.
	_, err := f.sealed.SnapshotAccounts(f.t.Context(), f.space, syncDay)
	require.NoError(t, err)
	history, err := f.sealed.ListBalanceHistory(f.t.Context(), f.space, syncDay)
	require.NoError(t, err)
	balances := domain.BalancesOn(history, syncDay)
	worth := domain.NetWorthAt(
		store.DomainAccounts([]store.Account{held}), balances, syncDay)
	require.Equal(t, "42000.00", worth.Assets.String())
	require.Equal(t, "42000.00",
		domain.AccountBalance(store.DomainAccount(held), nil).String())
}

func TestAHeldBalanceSelfHealsWhenARealBalanceReturns(t *testing.T) {
	// The hold is not sticky: a later real balance clears it without confirmation.
	f := newSyncFixture(t)
	f.claim()

	f.bridge.serve(payload("", bridgeAccount("acct-1", "Savings", "42000.00")))
	f.run()
	account := f.accounts()[0]
	require.NoError(t, f.sealed.UpsertBalanceSnapshots(f.t.Context(), f.space,
		[]store.BalanceSnapshot{
			{AccountID: account.ID, AsOf: syncDay.AddDays(-30), Balance: domain.MustFromString("42000.00")},
			{AccountID: account.ID, AsOf: syncDay.AddDays(-10), Balance: domain.MustFromString("42000.00")},
		}))

	f.bridge.serve(payload("", bridgeAccount("acct-1", "Savings", "0.00")))
	f.run()
	require.True(t, f.accounts()[0].HasWithheldBalance)

	f.bridge.serve(payload("", bridgeAccount("acct-1", "Savings", "41500.00")))
	f.run()

	healed := f.accounts()[0]
	require.Equal(t, "41500.00", healed.ProviderBalance.String())
	require.False(t, healed.HasWithheldBalance)
	require.Nil(t, healed.WithheldBalanceAt)
}

// quietCard is a credit card owing 1,234.56 after a month with no activity,
// that balance established in history, and the answer a later sync serves for
// it.
func quietCard(t *testing.T) (*syncFixture, func(balance string, txns ...string) string) {
	t.Helper()
	f := newSyncFixture(t)
	card := func(balance string, txns ...string) string {
		all := append([]string{bridgeTxn("charge-1", syncDay.AddDays(-45), "-1234.56", "AIRLINE TICKETS")}, txns...)
		return payload("", fmt.Sprintf(
			`{"id": "card-9", "name": "Travel Card", "currency": "USD", "balance": %q,
			  "conn_id": "MX-MBR-1", "org": {"name": "Big Bank"},
			  "extra": {"account-type": "credit card"}, "transactions": [%s]}`,
			balance, strings.Join(all, ", ")))
	}
	f.bridge.serve(card("1234.56"))
	f.claim()
	f.run()

	account := f.accounts()[0]
	require.Equal(t, domain.KindCreditCard, account.Kind)
	require.Equal(t, "-1234.56", account.ProviderBalance.String())
	require.NoError(t, f.sealed.UpsertBalanceSnapshots(f.t.Context(), f.space,
		[]store.BalanceSnapshot{
			{AccountID: account.ID, AsOf: syncDay.AddDays(-40), Balance: domain.MustFromString("-1234.56")},
			{AccountID: account.ID, AsOf: syncDay.AddDays(-10), Balance: domain.MustFromString("-1234.56")},
		}))
	return f, card
}

func TestACardPaidInFullAfterAQuietMonthIsApplied(t *testing.T) {
	// The zero is the payment the same sync brought in: the balance before it
	// plus the payment comes to the reported zero, so nothing is held.
	f, card := quietCard(t)

	f.bridge.serve(card("0.00", bridgeTxn("payment-1", syncDay, "1234.56", "PAYMENT THANK YOU")))
	report := f.run()

	account := f.accounts()[0]
	require.Equal(t, "0.00", account.ProviderBalance.String())
	require.False(t, account.HasWithheldBalance)
	require.Nil(t, account.WithheldBalanceAt)
	require.Empty(t, account.WithheldBalanceReason)
	require.Empty(t, report.BankWarnings)
}

func TestAZeroNoTransactionExplainsIsHeld(t *testing.T) {
	f, card := quietCard(t)

	f.bridge.serve(card("0.00"))
	report := f.run()

	held := f.accounts()[0]
	require.Equal(t, "-1234.56", held.ProviderBalance.String())
	require.True(t, held.HasWithheldBalance)
	require.Equal(t, "0.00", held.WithheldBalance.String())
	require.Equal(t,
		"SimpleFIN reported $0.00; the last balance -$1,234.56 plus 0 new transactions ($0.00) "+
			"comes to -$1,234.56. Keeping -$1,234.56 until you confirm the change.",
		held.WithheldBalanceReason)
	require.Equal(t, 1, report.BalancesHeld)
	require.Empty(t, report.BankWarnings)
}

func TestAZeroAPartPaymentDoesNotReachIsHeldAtWhatTheRowsComeTo(t *testing.T) {
	// The payment arrived but covers only part of the balance: the zero is held,
	// and the balance kept is the old one carried forward by the payment.
	f, card := quietCard(t)

	f.bridge.serve(card("0.00", bridgeTxn("payment-1", syncDay, "1000.00", "PAYMENT THANK YOU")))
	f.run()

	held := f.accounts()[0]
	require.Equal(t, "-234.56", held.ProviderBalance.String())
	require.True(t, held.HasWithheldBalance)
	require.Equal(t,
		"SimpleFIN reported $0.00; the last balance -$1,234.56 plus 1 new transaction (+$1,000.00) "+
			"comes to -$234.56. Keeping -$234.56 until you confirm the change.",
		held.WithheldBalanceReason)
}

func TestAHeldZeroIsReleasedWhenALaterSyncBringsThePayment(t *testing.T) {
	// The balance dropped before the payment reached the feed: held on the first
	// sync, explained and applied on the one that brings the payment.
	f, card := quietCard(t)

	f.bridge.serve(card("0.00"))
	f.run()
	require.True(t, f.accounts()[0].HasWithheldBalance)

	f.bridge.serve(card("0.00", bridgeTxn("payment-1", syncDay, "1234.56", "PAYMENT THANK YOU")))
	report := f.run()

	released := f.accounts()[0]
	require.Equal(t, "0.00", released.ProviderBalance.String())
	require.False(t, released.HasWithheldBalance)
	require.Nil(t, released.WithheldBalanceAt)
	require.Empty(t, released.WithheldBalanceReason)
	require.Empty(t, report.BankWarnings)
}

func TestAZeroWhoseTransactionsCouldNotBeReadIsHeld(t *testing.T) {
	// The balance read worked and the transaction read failed: a payment that
	// would explain the zero was never seen, so the zero is held.
	f, card := quietCard(t)

	body := card("0.00", bridgeTxn("payment-1", syncDay, "1234.56", "PAYMENT THANK YOU"))
	first := f.bridge.accountCalls + 1
	f.bridge.answer = func() (int, string) {
		if f.bridge.accountCalls == first {
			return http.StatusOK, body
		}
		return http.StatusInternalServerError, ""
	}
	f.run()

	held := f.accounts()[0]
	require.Equal(t, "-1234.56", held.ProviderBalance.String())
	require.True(t, held.HasWithheldBalance)
	require.Equal(t,
		"SimpleFIN reported $0.00 without this account's transactions, so nothing explains the "+
			"change from -$1,234.56. Keeping -$1,234.56 until you confirm the change.",
		held.WithheldBalanceReason)
}

func TestAZeroFromABankNeedingReauthorizationIsHeld(t *testing.T) {
	// The Bridge flags the bank, so its figures are stale; a payment row in the
	// same answer explains nothing.
	f, card := quietCard(t)

	answer := card("0.00", bridgeTxn("payment-1", syncDay, "1234.56", "PAYMENT THANK YOU"))
	f.bridge.serve(strings.Replace(answer, `"errlist": []`,
		`"errlist": [{"code": "con.auth", "conn_id": "MBR-1", "msg": "Needs reauthorization"}]`, 1))
	report := f.run()

	held := f.accounts()[0]
	require.Equal(t, "-1234.56", held.ProviderBalance.String())
	require.True(t, held.HasWithheldBalance)
	require.Contains(t, held.WithheldBalanceReason, "without this account's transactions")

	// Two problems, one warning each: the bank's reauthorization on the
	// connection, the held balance on the account.
	require.Equal(t, 1, report.BalancesHeld)
	warnings := f.connection().SyncErrors
	require.Len(t, warnings, 1)
	require.NotContains(t, warnings[0].Message, "until you confirm the change")
}

func TestAnAlreadyZeroedBalanceIsRecoveredFromHistory(t *testing.T) {
	// An account already stuck at 0.00, with only snapshot history remembering the
	// balance: the next sync reporting 0 restores the last non-zero value from
	// history, warns, and offers confirm.
	f := newSyncFixture(t)
	f.claim()

	// The first sync brings the account in already at zero.
	f.bridge.serve(payload("", bridgeAccount("acct-1", "Mortgage", "0.00")))
	f.run()
	account := f.accounts()[0]
	require.Equal(t, "0.00", account.ProviderBalance.String())

	// History: months of the held balance, then the zero it has been stuck at.
	require.NoError(t, f.sealed.UpsertBalanceSnapshots(f.t.Context(), f.space,
		[]store.BalanceSnapshot{
			{AccountID: account.ID, AsOf: syncDay.AddDays(-40), Balance: domain.MustFromString("-400000.00")},
			{AccountID: account.ID, AsOf: syncDay.AddDays(-11), Balance: domain.MustFromString("-400000.00")},
			{AccountID: account.ID, AsOf: syncDay.AddDays(-10), Balance: domain.MustFromString("0")},
			{AccountID: account.ID, AsOf: syncDay.AddDays(-1), Balance: domain.MustFromString("0")},
		}))

	f.bridge.serve(payload("", bridgeAccount("acct-1", "Mortgage", "0.00")))
	report := f.run()

	recovered := f.accounts()[0]
	require.Equal(t, "-400000.00", recovered.ProviderBalance.String(),
		"the last non-zero balance must be recovered from history")
	require.True(t, recovered.HasWithheldBalance)
	require.Equal(t, "0.00", recovered.WithheldBalance.String())
	require.NotNil(t, recovered.WithheldBalanceAt)
	require.Equal(t, 1, report.BalancesHeld)
	require.Empty(t, report.BankWarnings)
}
