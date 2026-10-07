package store

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The page a connector's last run failed on: kept beside the error, replaced
// by the next failure, gone with the next success, and only in its own space.

func TestABillPullsFailureKeepsOnlyTheLatestPageUntilAPullGetsIn(t *testing.T) {
	space, stranger := newSpace(t), newSpace(t)
	connection := newBillConnection(t, space)
	saw := func() ([]byte, bool) {
		t.Helper()
		one, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
		require.NoError(t, err)
		shot, err := sealedDB(t).BillPullScreenshot(t.Context(), space, connection.ID)
		if err != nil {
			require.ErrorIs(t, err, ErrNotFound)
			require.False(t, one.HasFailureScreenshot)
			return nil, false
		}
		require.True(t, one.HasFailureScreenshot)
		return shot, true
	}

	_, kept := saw()
	require.False(t, kept, "never failed")

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed,
		"something covered the button", []byte("first page"), nil))
	shot, kept := saw()
	require.True(t, kept)
	require.Equal(t, []byte("first page"), shot)

	_, err := sealedDB(t).BillPullScreenshot(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound, "another space never sees it")

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed,
		"timeout", []byte("second page"), nil))
	shot, _ = saw()
	require.Equal(t, []byte("second page"), shot, "the next failure replaces it")

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed,
		"dial tcp: i/o timeout", nil, nil))
	_, kept = saw()
	require.False(t, kept, "a failure with no page leaves no page from an older one")

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed,
		"timeout", []byte("third page"), nil))
	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullOK, "", nil, nil))
	_, kept = saw()
	require.False(t, kept, "a pull that got in clears it")

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed,
		"timeout", []byte("fourth page"), nil))
	require.NoError(t, sealedDB(t).SaveBillConnectionSession(t.Context(), space, connection.ID,
		`{"cookies":[]}`, "", true))
	_, kept = saw()
	require.False(t, kept, "a person's sign-in clears it with the error")

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed,
		"timeout", []byte("fifth page"), nil))
	require.NoError(t, sealedDB(t).NoteBillPullSkipped(t.Context(), space, connection.ID, "paused"))
	_, kept = saw()
	require.False(t, kept, "a skipped run's note has no page")
}

func TestAPageTooLargeToKeepIsDroppedAndTheFailureStillRecorded(t *testing.T) {
	space := newSpace(t)
	connection := newBillConnection(t, space)
	tooLarge := bytes.Repeat([]byte{1}, MaxFailureScreenshotBytes+1)

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed,
		"timeout", tooLarge, nil))
	one, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "timeout", one.LastPullError)
	require.False(t, one.HasFailureScreenshot)
}

// A sign-in a person started that never landed takes the last pull's place
// with its page and its trail, and is cleared the way a pull's failure is;
// what the last pull said about scheduling stays as it was.
func TestAnUnfinishedSignInIsKeptBesideTheLastPullsFailure(t *testing.T) {
	space, stranger := newSpace(t), newSpace(t)
	connection := newBillConnection(t, space)
	trail := []byte(`[{"step":"factor","state":"factor"}]`)
	read := func() BillConnection {
		t.Helper()
		one, err := sealedDB(t).GetBillConnection(t.Context(), space, connection.ID)
		require.NoError(t, err)
		return one
	}

	_, err := sealedDB(t).BillPullTrail(t.Context(), space, connection.ID)
	require.ErrorIs(t, err, ErrNotFound, "nothing kept yet")
	before := read()

	require.NoError(t, sealedDB(t).MarkBillSignInEnded(t.Context(), space, connection.ID,
		"The sign-in was closed before it finished", []byte("the page"), trail))
	one := read()
	require.Equal(t, BillPullSignInFailed, one.LastPullStatus)
	require.Equal(t, "The sign-in was closed before it finished", one.LastPullError)
	require.True(t, one.HasFailureScreenshot)
	require.True(t, one.HasTrail)
	require.Equal(t, before.NeedsSignIn, one.NeedsSignIn, "whether it needs a sign-in is not this sign-in's to say")
	require.Equal(t, before.LastPulledAt, one.LastPulledAt, "nothing was pulled")
	kept, err := sealedDB(t).BillPullTrail(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(trail), string(kept))
	shot, err := sealedDB(t).BillPullScreenshot(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.Equal(t, []byte("the page"), shot, "one screenshot, served where a pull's is")
	_, err = sealedDB(t).BillPullTrail(t.Context(), stranger, connection.ID)
	require.ErrorIs(t, err, ErrNotFound, "another space never sees it")

	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullFailed, "timeout", nil, nil))
	require.False(t, read().HasTrail, "the next pull's failure takes its place")

	pulled := []byte(`[{"step":"read","state":"page","note":"the billing summary"}]`)
	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullOK, "", nil, pulled))
	require.True(t, read().HasTrail, "a pull that got in keeps what it read")
	kept, err = sealedDB(t).BillPullTrail(t.Context(), space, connection.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(pulled), string(kept))
	require.NoError(t, sealedDB(t).MarkBillPull(t.Context(), space, connection.ID, BillPullOK, "", nil,
		[]byte(`"`+strings.Repeat("x", maxTrailBytes)+`"`)))
	require.False(t, read().HasTrail, "a trail past the bound is not kept, nor is the last pull's left behind")

	require.NoError(t, sealedDB(t).MarkBillSignInEnded(t.Context(), space, connection.ID, "again", nil, trail))
	require.NoError(t, sealedDB(t).SaveBillConnectionSession(t.Context(), space, connection.ID,
		`{"cookies":[]}`, "", true))
	one = read()
	require.False(t, one.HasTrail, "a sign-in that lands clears it")
	require.Empty(t, one.LastPullStatus)

	require.NoError(t, sealedDB(t).MarkBillSignInEnded(t.Context(), space, connection.ID, "again", nil, trail))
	require.NoError(t, sealedDB(t).NoteBillPullSkipped(t.Context(), space, connection.ID, "paused"))
	require.False(t, read().HasTrail, "a skipped pull's note carries none")
}

func TestAMerchantPullsFailureKeepsOnlyTheLatestPageUntilAPullGetsIn(t *testing.T) {
	space, stranger := newSpace(t), newSpace(t)
	account := newSignedInMerchantAccount(t, space, "Alex")
	shotOf := func(space SpaceID) ([]byte, error) {
		return sealedDB(t).MerchantSyncScreenshot(t.Context(), space, account.ID)
	}

	_, err := shotOf(space)
	require.ErrorIs(t, err, ErrNotFound, "never failed")

	require.NoError(t, sealedDB(t).MarkMerchantSync(t.Context(), space, account.ID, MerchantSyncFailed,
		"the orders page never loaded", []byte("first page")))
	require.NoError(t, sealedDB(t).MarkMerchantSync(t.Context(), space, account.ID, MerchantSyncNeedsSignIn,
		"Amazon asked to sign in again", []byte("second page")))
	shot, err := shotOf(space)
	require.NoError(t, err)
	require.Equal(t, []byte("second page"), shot, "the next failure replaces it")
	listed, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.True(t, listed.HasFailureScreenshot)

	_, err = sealedDB(t).MerchantSyncScreenshot(t.Context(), stranger, account.ID)
	require.ErrorIs(t, err, ErrNotFound, "another space never sees it")

	require.NoError(t, sealedDB(t).MarkMerchantSync(t.Context(), space, account.ID, MerchantSyncOK, "", nil))
	_, err = shotOf(space)
	require.ErrorIs(t, err, ErrNotFound, "a pull that got in clears it")
	listed, err = sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.False(t, listed.HasFailureScreenshot)
}
