package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func sealedDB(t *testing.T) *Store {
	t.Helper()
	return db(t).WithCipher(newTestCipher(t, "the-credential-key"))
}

func newTestConnection(t *testing.T, spaceID SpaceID) *Connection {
	t.Helper()
	connection := &Connection{Name: "SimpleFIN"}
	require.NoError(t, sealedDB(t).CreateConnection(t.Context(), spaceID, connection, accessURL))
	return connection
}

func TestTheAccessURLReachesPostgresAsCiphertext(t *testing.T) {
	space := newSpace(t)
	connection := newTestConnection(t, space)

	var stored string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT access_url_encrypted FROM connections WHERE id = $1`, connection.ID).Scan(&stored))
	require.NotContains(t, stored, "example.invalid")
	require.NotContains(t, stored, "demo:demo@")

	opened, err := sealedDB(t).ConnectionAccessURL(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, accessURL, opened)
}

func TestAConnectionListingHasNowhereToPutACredential(t *testing.T) {
	// The struct has no field for the credential.
	space := newSpace(t)
	newTestConnection(t, space)

	connections, err := sealedDB(t).ListConnections(t.Context(), space, false)
	require.NoError(t, err)
	require.Len(t, connections, 1)

	encoded, err := json.Marshal(connections)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "example.invalid")
	require.NotContains(t, string(encoded), "demo:demo@")
}

func TestAConnectionInAnotherSpaceIsInvisible(t *testing.T) {
	space, stranger := newSpace(t), newSpace(t)
	connection := newTestConnection(t, space)

	_, err := sealedDB(t).GetConnection(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = sealedDB(t).ConnectionAccessURL(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAConnectionCarriesItsBankWarningsAndItsStatus(t *testing.T) {
	space := newSpace(t)
	connection := newTestConnection(t, space)

	at := time.Now().UTC().Truncate(time.Second)
	connection.Status = ConnectionCredentialsExpired
	connection.StatusDetail = "Reauthorize at SimpleFIN Bridge"
	connection.SyncErrors = []BankSyncError{{Institution: "Big Bank", Message: "Needs reauth", At: at}}
	require.NoError(t, sealedDB(t).UpdateConnection(t.Context(), space, connection))

	reloaded, err := sealedDB(t).GetConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.True(t, reloaded.NeedsSetupToken())
	require.Equal(t, "Reauthorize at SimpleFIN Bridge", reloaded.StatusDetail)
	require.Len(t, reloaded.SyncErrors, 1)
	require.Equal(t, "Big Bank", reloaded.SyncErrors[0].Institution)
	require.Equal(t, at, reloaded.SyncErrors[0].At.UTC())
}

func TestDeletingAConnectionKeepsTheLedgerAndMakesTheAccountsManual(t *testing.T) {
	space := newSpace(t)
	connection := newTestConnection(t, space)

	account := &Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		ConnectionID: connection.ID, ExternalID: "acc-1",
		ProviderBalance: domain.MustFromString("1200.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))

	txn := &Transaction{
		AccountID: account.ID, ExternalID: "txn-1",
		Date: domain.NewDate(2026, time.March, 15), Amount: domain.MustFromString("-25.00"),
		Currency: "USD", StatementName: "SAFEWAY #1234", Payee: "Safeway",
		Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, txn))

	require.NoError(t, sealedDB(t).DeleteConnection(t.Context(), space, connection.ID))

	surviving, err := db(t).GetAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, surviving.ConnectionID, "the account still points at a dead connection")
	require.False(t, surviving.IsDeleted)

	kept, err := db(t).GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.False(t, kept.IsDeleted)
	require.Equal(t, "-25.00", kept.Amount.String())
}

func TestAnAccountIsFoundByItsConnectionAndExternalID(t *testing.T) {
	space := newSpace(t)
	connection := newTestConnection(t, space)
	account := &Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		ConnectionID: connection.ID, ExternalID: "acc-1",
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))

	found, err := sealedDB(t).GetAccountByExternalID(t.Context(), space, connection.ID, "acc-1")
	require.NoError(t, err)
	require.Equal(t, account.ID, found.ID)

	_, err = sealedDB(t).GetAccountByExternalID(t.Context(), space, connection.ID, "acc-2")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAnImportedAccountAwaitingItsLinkIsFoundByTheSimpleFINID(t *testing.T) {
	// The match screen stamps the provider id and a sync floor, and the next
	// sync adopts the row rather than creating a second account.
	space := newSpace(t)
	imported := &Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		SimpleFINAccountID: "acc-1",
		SyncFloorOn:        domain.NewDate(2026, time.March, 10),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, imported))

	found, err := sealedDB(t).GetAccountAwaitingLink(t.Context(), space, "acc-1")
	require.NoError(t, err)
	require.Equal(t, imported.ID, found.ID)
	require.Equal(t, domain.NewDate(2026, time.March, 10), found.SyncFloorOn)
}

func TestUnlinkingAnAccountLeavesItManualWithItsRegister(t *testing.T) {
	space := newSpace(t)
	connection := newTestConnection(t, space)
	account := &Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		ConnectionID: connection.ID, ExternalID: "acc-1", SimpleFINAccountID: "acc-1",
		ProviderBalance: domain.MustFromString("1200.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	txn := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, time.March, 15),
		Amount: domain.MustFromString("-25.00"), Currency: "USD",
		StatementName: "SAFEWAY #1234", Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, txn))

	require.NoError(t, sealedDB(t).UnlinkAccount(t.Context(), space, account.ID))

	manual, err := db(t).GetAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, manual.ConnectionID)
	require.Empty(t, manual.ExternalID)
	require.Empty(t, manual.SimpleFINAccountID)
	require.False(t, manual.HasProviderBalance)

	kept, err := db(t).GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.False(t, kept.IsDeleted)
}

func TestOneInstitutionRowServesEveryAccountAtThatBank(t *testing.T) {
	space := newSpace(t)
	first, err := sealedDB(t).EnsureInstitution(t.Context(), space, "Big Bank", "https://logo.example/bb.png")
	require.NoError(t, err)
	second, err := sealedDB(t).EnsureInstitution(t.Context(), space, "big bank", "")
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestClaimTakesOnlyWhatIsDue(t *testing.T) {
	ctx := t.Context()
	space := newSpace(t)
	window := time.Now().Add(-2 * time.Hour)
	now := time.Now()

	never := newTestConnection(t, space)

	synced := newTestConnection(t, space)
	inWindow := window.Add(30 * time.Minute)
	synced.LastSyncAt = &inWindow
	require.NoError(t, sealedDB(t).UpdateConnection(ctx, space, synced))

	stale := newTestConnection(t, space)
	before := window.Add(-25 * time.Hour)
	stale.LastSyncAt = &before
	require.NoError(t, sealedDB(t).UpdateConnection(ctx, space, stale))

	due, err := db(t).ClaimConnectionsDueForSync(ctx, window, now)
	require.NoError(t, err)

	claimed := map[uuid.UUID]bool{}
	for _, one := range due {
		claimed[one.ID] = true
	}
	require.True(t, claimed[never.ID], "a connection that has never synced is due")
	require.True(t, claimed[stale.ID], "a connection last synced before the window is due")
	require.False(t, claimed[synced.ID], "a connection synced inside this window was claimed again")
}

func TestAClaimIsTakenOnlyOnce(t *testing.T) {
	// Stamp and read are one statement, so a second pass finds nothing.
	ctx := t.Context()
	space := newSpace(t)
	newTestConnection(t, space)
	window, now := time.Now().Add(-2*time.Hour), time.Now()

	first, err := db(t).ClaimConnectionsDueForSync(ctx, window, now)
	require.NoError(t, err)
	require.Len(t, first, 1)

	second, err := db(t).ClaimConnectionsDueForSync(ctx, window, now)
	require.NoError(t, err)
	require.Empty(t, second, "the same connection was claimed by a second pass")
}

func TestAParkedConnectionIsNotClaimedUntilItsTime(t *testing.T) {
	ctx := t.Context()
	space := newSpace(t)
	parked := newTestConnection(t, space)
	later := time.Now().Add(3 * time.Hour)
	parked.Status = ConnectionRateLimited
	parked.RetryNotBefore = &later
	require.NoError(t, sealedDB(t).UpdateConnection(ctx, space, parked))

	due, err := db(t).ClaimConnectionsDueForSync(ctx, time.Now().Add(-2*time.Hour), time.Now())
	require.NoError(t, err)
	require.Empty(t, due, "a throttled connection was claimed before its retry time")

	passed := time.Now().Add(-time.Minute)
	parked.RetryNotBefore = &passed
	require.NoError(t, sealedDB(t).UpdateConnection(ctx, space, parked))

	due, err = db(t).ClaimConnectionsDueForSync(ctx, time.Now().Add(-2*time.Hour), time.Now())
	require.NoError(t, err)
	require.Len(t, due, 1)
}

func TestARefusedCredentialIsNotRetriedOnASchedule(t *testing.T) {
	// Only a fresh setup token fixes it, so a scheduled retry learns nothing.
	ctx := t.Context()
	space := newSpace(t)
	dead := newTestConnection(t, space)
	dead.Status = ConnectionCredentialsExpired
	require.NoError(t, sealedDB(t).UpdateConnection(ctx, space, dead))

	due, err := db(t).ClaimConnectionsDueForSync(ctx, time.Now().Add(-2*time.Hour), time.Now())
	require.NoError(t, err)
	for _, one := range due {
		require.NotEqual(t, dead.ID, one.ID)
	}
}

func TestADeletedConnectionIsNeverClaimed(t *testing.T) {
	ctx := t.Context()
	space := newSpace(t)
	gone := newTestConnection(t, space)
	require.NoError(t, sealedDB(t).DeleteConnection(ctx, space, gone.ID))

	due, err := db(t).ClaimConnectionsDueForSync(ctx, time.Now().Add(-2*time.Hour), time.Now())
	require.NoError(t, err)
	for _, one := range due {
		require.NotEqual(t, gone.ID, one.ID)
	}
}

func TestTheClaimCarriesTheSpaceItBelongsTo(t *testing.T) {
	// The scheduler has no tenant; the sync takes the space from the claim.
	ctx := t.Context()
	first, second := newSpace(t), newSpace(t)
	mine := newTestConnection(t, first)
	theirs := newTestConnection(t, second)

	due, err := db(t).ClaimConnectionsDueForSync(ctx, time.Now().Add(-2*time.Hour), time.Now())
	require.NoError(t, err)

	spaces := map[uuid.UUID]SpaceID{}
	for _, one := range due {
		spaces[one.ID] = one.SpaceID
	}
	require.Equal(t, first, spaces[mine.ID])
	require.Equal(t, second, spaces[theirs.ID])
}
