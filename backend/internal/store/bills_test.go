package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// billSession is an invented storage-state snapshot of a browser provider's
// shape.
const billSession = `{"cookies":[{"name":"SESSIONID","value":"tok-4a91f2"}]}`

func newBillConnection(t *testing.T, spaceID SpaceID) *BillConnection {
	t.Helper()
	connection := &BillConnection{
		Biller: domain.BillerSpectrum, Label: "Main account",
		CredentialSource: BillCredentialSession, AutopayRule: domain.AutopayNone,
		PullEnabled: true,
	}
	require.NoError(t, sealedDB(t).CreateBillConnection(t.Context(), spaceID, connection))
	return connection
}

func TestAKeptProviderSessionReachesPostgresAsCiphertext(t *testing.T) {
	space := newSpace(t)
	connection := newBillConnection(t, space)

	require.NoError(t, sealedDB(t).SaveBillConnectionSession(
		t.Context(), space, connection.ID, billSession, "", true))

	var stored string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT session_state FROM bill_connections WHERE id = $1`, connection.ID).Scan(&stored))
	require.NotContains(t, stored, "SESSIONID")
	require.NotContains(t, stored, "tok-4a91f2")

	opened, err := sealedDB(t).BillConnectionSession(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, billSession, opened)
}

func TestABillConnectionListingHasNowhereToPutASession(t *testing.T) {
	// The struct has no field for the ciphertext.
	space := newSpace(t)
	connection := newBillConnection(t, space)
	require.NoError(t, sealedDB(t).SaveBillConnectionSession(
		t.Context(), space, connection.ID, billSession, "", true))

	connections, err := sealedDB(t).ListBillConnections(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	require.True(t, connections[0].HasSession, "the listing still says one is on file")

	encoded, err := json.Marshal(connections)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "SESSIONID")
	require.NotContains(t, string(encoded), "tok-4a91f2")
}

func TestASessionSealedForOneConnectionWillNotOpenOnAnother(t *testing.T) {
	// The row binding stops a payload copied onto another row from opening.
	space := newSpace(t)
	mine := newBillConnection(t, space)
	theirs := &BillConnection{
		Biller: domain.BillerSpectrum, Label: "Second account",
		CredentialSource: BillCredentialSession, AutopayRule: domain.AutopayNone,
	}
	require.NoError(t, sealedDB(t).CreateBillConnection(t.Context(), space, theirs))
	require.NoError(t, sealedDB(t).SaveBillConnectionSession(
		t.Context(), space, mine.ID, billSession, "", true))

	var sealed string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT session_state FROM bill_connections WHERE id = $1`, mine.ID).Scan(&sealed))
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE bill_connections SET session_state = $2 WHERE id = $1`, theirs.ID, sealed)
	require.NoError(t, err)

	_, err = sealedDB(t).BillConnectionSession(t.Context(), space, theirs.ID)
	require.ErrorIs(t, err, ErrCredentialUnreadable)
}

func TestABillConnectionInAnotherSpaceIsInvisible(t *testing.T) {
	space, stranger := newSpace(t), newSpace(t)
	connection := newBillConnection(t, space)

	_, err := sealedDB(t).GetBillConnection(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = sealedDB(t).BillConnectionSession(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAConnectionIsDueOnlyOncePerWindow(t *testing.T) {
	space := newSpace(t)
	connection := newBillConnection(t, space)
	require.NoError(t, sealedDB(t).SaveBillConnectionSession(
		t.Context(), space, connection.ID, billSession, "", true))

	since := time.Now().UTC().Add(-time.Hour)
	billID := func(c BillConnection) uuid.UUID { return c.ID }
	due, err := db(t).ListBillConnectionsDue(t.Context(), since)
	require.NoError(t, err)
	require.True(t, containsID(due, connection.ID, billID), "never pulled, so it is due")

	require.NoError(t, db(t).MarkBillPull(t.Context(), space, connection.ID, BillPullOK, "", nil, nil))
	due, err = db(t).ListBillConnectionsDue(t.Context(), since)
	require.NoError(t, err)
	require.False(t, containsID(due, connection.ID, billID), "pulled inside the window")
}

func TestASecondPullOfOneCycleIsTheSameBill(t *testing.T) {
	// The same subaccount, due date and invoice is one row however many pulls
	// describe it, enforced by a constraint.
	space := newSpace(t)
	connection := newBillConnection(t, space)
	subaccount := &BillSubaccount{
		ConnectionID: connection.ID, ExternalID: "premise-1",
		Label: "Electric", IsSelected: true,
	}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), space, subaccount))

	dueOn := domain.NewDate(2026, time.October, 26)
	first := &Bill{
		SubaccountID: subaccount.ID, DueOn: dueOn, AmountDue: domain.MustFromString("80.00"),
		Currency: "USD", Status: domain.BillOpen, Source: BillSourceProvider,
		FetchedAt: time.Now().UTC(),
	}
	require.NoError(t, db(t).UpsertBill(t.Context(), space, first, false))

	second := &Bill{
		SubaccountID: subaccount.ID, DueOn: dueOn, AmountDue: domain.MustFromString("151.00"),
		Currency: "USD", Status: domain.BillOpen, Source: BillSourceProvider,
		FetchedAt: time.Now().UTC(),
	}
	require.NoError(t, db(t).UpsertBill(t.Context(), space, second, true))
	require.Equal(t, first.ID, second.ID, "one cycle, one row")
	require.NotNil(t, second.AmendedAt, "and the correction is stamped")

	bills, err := db(t).ListBills(t.Context(), space, subaccount.ID)
	require.NoError(t, err)
	require.Len(t, bills, 1)
	require.Equal(t, "151.00", bills[0].AmountDue.String())
}

func TestTwoInvoicesOfOneDayAreTwoBills(t *testing.T) {
	space := newSpace(t)
	connection := newBillConnection(t, space)
	subaccount := &BillSubaccount{
		ConnectionID: connection.ID, ExternalID: "customer-1",
		Label: "Lawn", IsSelected: true,
	}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), space, subaccount))

	dueOn := domain.NewDate(2026, time.May, 4)
	invoice := func(number, amount string) *Bill {
		return &Bill{
			SubaccountID: subaccount.ID, DueOn: dueOn, Invoice: number,
			AmountDue: domain.MustFromString(amount), Currency: "USD",
			Status: domain.BillPaid, Source: BillSourceProvider, FetchedAt: time.Now().UTC(),
		}
	}
	lawn, shrubs := invoice("INV-7", "60.00"), invoice("INV-8", "35.00")
	require.NoError(t, db(t).UpsertBill(t.Context(), space, lawn, false))
	require.NoError(t, db(t).UpsertBill(t.Context(), space, shrubs, false))
	require.NotEqual(t, lawn.ID, shrubs.ID)

	again := invoice("INV-7", "60.00")
	require.NoError(t, db(t).UpsertBill(t.Context(), space, again, false))
	require.Equal(t, lawn.ID, again.ID, "one invoice, one row")

	bills, err := db(t).ListBills(t.Context(), space, subaccount.ID)
	require.NoError(t, err)
	require.Len(t, bills, 2)
	require.Equal(t, "INV-7", bills[0].Invoice)
	require.Equal(t, "60.00", bills[0].AmountDue.String())
	require.Equal(t, "INV-8", bills[1].Invoice)
	require.Equal(t, "35.00", bills[1].AmountDue.String())
}

func TestALinkReleasesWhateverEitherSideHeld(t *testing.T) {
	space := newSpace(t)
	connection := newBillConnection(t, space)
	first := &BillSubaccount{ConnectionID: connection.ID, ExternalID: "premise-1", Label: "Electric"}
	second := &BillSubaccount{ConnectionID: connection.ID, ExternalID: "premise-2", Label: "Gas"}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), space, first))
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), space, second))
	series := newSeriesRow(t, space)

	_, err := db(t).LinkSeriesBill(t.Context(), space, series, first.ID)
	require.NoError(t, err)
	// Re-linking the same series to a second subaccount must not fail on the
	// constraint: the person cannot see the row they would have to release.
	_, err = db(t).LinkSeriesBill(t.Context(), space, series, second.ID)
	require.NoError(t, err)

	links, err := db(t).ListSeriesBillLinks(t.Context(), space, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, second.ID, links[0].SubaccountID)
}

// newSeriesRow inserts the minimum series the link's foreign key needs;
// service owns that table, so there is no store query for it.
func newSeriesRow(t *testing.T, spaceID SpaceID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	account := newAccount(t, spaceID, "Everyday Checking")
	_, err := db(t).Pool().Exec(t.Context(),
		`INSERT INTO series (id, space_id, account_id, description, amount, currency, alias,
		                     frequency, interval, start_on, kind, reminder_days, match_criteria)
		 VALUES ($1, $2, $3, 'ELECTRIC CO', -80.00, 'USD', 'monthly', 'monthly', 1,
		         '2026-01-14', 'bill', 3, 'description')`,
		id, spaceID.UUID(), account.ID)
	require.NoError(t, err)
	return id
}

func TestAKeptProviderPasswordReachesPostgresAsCiphertextBoundToItsRow(t *testing.T) {
	space := newSpace(t)
	connection := newBillConnection(t, space)
	login := BillCredential{Username: "alex", Password: "hunter2"}

	require.NoError(t, sealedDB(t).SaveBillConnectionCredential(t.Context(), space, connection.ID, login))

	var stored string
	var source string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT credential_sealed, credential_source FROM bill_connections WHERE id = $1`,
		connection.ID).Scan(&stored, &source))
	require.NotContains(t, stored, "hunter2")
	require.NotContains(t, stored, "alex")
	require.Equal(t, BillCredentialStored, source)

	opened, err := sealedDB(t).BillConnectionCredential(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, login, opened)

	rows, err := sealedDB(t).ListBillConnections(t.Context(), space)
	require.NoError(t, err)
	require.True(t, rows[0].HasCredential)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hunter2")

	theirs := &BillConnection{
		Biller: domain.BillerSpectrum, Label: "Second account",
		CredentialSource: BillCredentialSession, AutopayRule: domain.AutopayNone,
	}
	require.NoError(t, sealedDB(t).CreateBillConnection(t.Context(), space, theirs))
	_, err = db(t).Pool().Exec(t.Context(),
		`UPDATE bill_connections SET credential_sealed = $2 WHERE id = $1`, theirs.ID, stored)
	require.NoError(t, err)
	_, err = sealedDB(t).BillConnectionCredential(t.Context(), space, theirs.ID)
	require.ErrorIs(t, err, ErrCredentialUnreadable)

	require.NoError(t, sealedDB(t).ClearBillConnectionCredential(t.Context(), space, connection.ID))
	_, err = sealedDB(t).BillConnectionCredential(t.Context(), space, connection.ID)
	require.ErrorIs(t, err, ErrNotFound)
	after, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, BillCredentialSession, after.CredentialSource)
	require.False(t, after.HasCredential)
}

func TestAnAuthenticatorKeyIsSealedWithThePasswordAndForgottenWithIt(t *testing.T) {
	// The key rides inside the sealed blob; the column beside it says only
	// that there is one.
	space := newSpace(t)
	connection := newBillConnection(t, space)
	login := BillCredential{Username: "alex", Password: "hunter2", TOTPSecret: "JBSWY3DPEHPK3PXP"}

	require.NoError(t, sealedDB(t).SaveBillConnectionCredential(t.Context(), space, connection.ID, login))

	var stored string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT credential_sealed FROM bill_connections WHERE id = $1`,
		connection.ID).Scan(&stored))
	require.NotContains(t, stored, login.TOTPSecret)

	opened, err := sealedDB(t).BillConnectionCredential(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, login, opened)

	with, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.True(t, with.HasTOTP)

	// Sealing a password without a key clears any earlier key.
	require.NoError(t, sealedDB(t).SaveBillConnectionCredential(t.Context(), space, connection.ID,
		BillCredential{Username: "alex", Password: "hunter2"}))
	without, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.True(t, without.HasCredential)
	require.False(t, without.HasTOTP)

	require.NoError(t, sealedDB(t).SaveBillConnectionCredential(t.Context(), space, connection.ID, login))
	require.NoError(t, sealedDB(t).ClearBillConnectionCredential(t.Context(), space, connection.ID))
	forgotten, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.False(t, forgotten.HasCredential)
	require.False(t, forgotten.HasTOTP)
}

func TestAConnectionThatKeepsItsPasswordIsDueEvenWhenItNeedsASignIn(t *testing.T) {
	// needs_sign_in follows a refused refresh; a kept password signs in anyway.
	space := newSpace(t)
	connection := newBillConnection(t, space)
	require.NoError(t, sealedDB(t).ClearBillConnectionSession(t.Context(), space, connection.ID))

	// The due list spans every space, so the check is on this row alone.
	isDue := func() bool {
		due, err := sealedDB(t).ListBillConnectionsDue(t.Context(), time.Now())
		require.NoError(t, err)
		for _, one := range due {
			if one.ID == connection.ID {
				return true
			}
		}
		return false
	}
	require.False(t, isDue(), "nothing to sign in with")

	require.NoError(t, sealedDB(t).SaveBillConnectionCredential(t.Context(), space, connection.ID,
		BillCredential{Username: "alex", Password: "hunter2"}))
	require.True(t, isDue())

	// A paused sign-in is not retried on a timer until something lifts the
	// pause.
	for _, why := range []string{provider.SignInPausedPasswordRefused, provider.SignInPausedCodeNeeded} {
		t.Run(why, func(t *testing.T) {
			pause := func() {
				require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID,
					BillPullNeedsSignIn, "only a person can carry on", nil, nil))
				require.NoError(t, sealedDB(t).PauseBillSignIn(t.Context(), space, connection.ID, why))
				_, err := sealedDB(t).Pool().Exec(t.Context(),
					`UPDATE bill_connections SET last_pulled_at = ts_add(now(), '-2 days') WHERE id = $1`,
					connection.ID)
				require.NoError(t, err)
				require.False(t, isDue(), "a paused sign-in waits for a person")
				paused, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
				require.NoError(t, err)
				require.Equal(t, why, paused.SignInPausedFor)
			}
			lifted := func(because string) {
				t.Helper()
				one, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
				require.NoError(t, err)
				require.Nil(t, one.SignInPausedAt, because)
				require.Equal(t, "", one.SignInPausedFor, because)
			}

			pause()
			require.NoError(t, sealedDB(t).SaveBillConnectionCredential(t.Context(), space, connection.ID,
				BillCredential{Username: "alex", Password: "a-new-one"}))
			require.True(t, isDue(), "a changed password")
			lifted("a changed password")

			pause()
			require.NoError(t, sealedDB(t).SaveBillConnectionSession(t.Context(), space, connection.ID,
				`{"cookies":[]}`, "", true))
			require.True(t, isDue(), "a person's sign-in")
			lifted("a person's sign-in")

			pause()
			require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullOK, "", nil, nil))
			lifted("a pull that got in")

			pause()
			require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed, "timeout", nil, nil))
			still, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
			require.NoError(t, err)
			require.NotNil(t, still.SignInPausedAt, "a failed pull is not a pull that got in")

			require.NoError(t, sealedDB(t).ClearBillConnectionCredential(t.Context(), space, connection.ID))
			lifted("a forgotten password")
			require.NoError(t, sealedDB(t).SaveBillConnectionCredential(t.Context(), space, connection.ID,
				BillCredential{Username: "alex", Password: "hunter2"}))
		})
	}
}

// `site` must survive an UPDATE that does not name it: a column forgotten in
// the UPDATE reads back correctly until a rename empties it. The slugs are
// invented.
func TestTheDeploymentAConnectionIsAtSurvivesASettingsSave(t *testing.T) {
	space := newSpace(t)
	connection := &BillConnection{
		Biller: domain.BillerCommunityConnect, Label: "Main account",
		Site:             "exampletown",
		CredentialSource: BillCredentialSession, AutopayRule: domain.AutopayNone,
		PullEnabled: true,
	}
	require.NoError(t, sealedDB(t).CreateBillConnection(t.Context(), space, connection))

	read, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "exampletown", read.Site)

	read.Label = "Main account, renamed"
	require.NoError(t, sealedDB(t).UpdateBillConnection(t.Context(), space, &read))
	again, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "exampletown", again.Site, "a rename is not a move")

	again.Site = "fair-haven"
	require.NoError(t, sealedDB(t).UpdateBillConnection(t.Context(), space, &again))
	moved, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "fair-haven", moved.Site)

	// A shared provider has none, and null reads as empty.
	shared := newBillConnection(t, space)
	require.Equal(t, "", shared.Site)
	listed, err := sealedDB(t).ListBillConnections(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, listed, 2)
}

func TestABillConnectionIsCalledByItsNameOrItsProvider(t *testing.T) {
	require.Equal(t, "Erie Insurance", BillConnection{Biller: domain.BillerErie}.DisplayName())
	require.Equal(t, "Erie Insurance", BillConnection{Biller: domain.BillerErie, Label: "  "}.DisplayName())
	require.Equal(t, "Water",
		BillConnection{Biller: domain.BillerCommunityConnect, Label: "Water"}.DisplayName())
	require.Equal(t, "nothing-like-it", BillConnection{Biller: "nothing-like-it"}.DisplayName())
	require.Equal(t, "Our Community Connect",
		BillConnection{Biller: domain.BillerCommunityConnect, Label: "Water"}.ProviderName())
}

// A title names the connection beside the provider only when it was given a
// name, never "Provider ()", and a generic provider's name is left out.
func TestABillConnectionsTitleTellsTwoConnectionsApart(t *testing.T) {
	require.Equal(t, "Erie Insurance", BillConnection{Biller: domain.BillerErie}.Title())
	require.Equal(t, "Erie Insurance", BillConnection{Biller: domain.BillerErie, Label: " "}.Title())
	require.Equal(t, "Our Community Connect (Water)",
		BillConnection{Biller: domain.BillerCommunityConnect, Label: "Water"}.Title())
	require.Equal(t, "Example Water",
		BillConnection{Biller: domain.BillerEmailOnly, Label: "Example Water"}.Title())
	require.Equal(t, "Emailed bills", BillConnection{Biller: domain.BillerEmailOnly}.Title())
}
