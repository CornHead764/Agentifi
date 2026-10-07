package browser

import (
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

// An invented jar: one cookie the provider dated, two it did not.
func testJar() []playwright.Cookie {
	return []playwright.Cookie{
		{Name: "session", Value: "invented", Domain: ".example.test", Path: "/", Expires: -1, HttpOnly: true, Secure: true},
		{Name: "device", Value: "invented", Domain: ".example.test", Path: "/", Expires: 1893456000},
		{Name: "gateway", Value: "invented", Domain: "sso.example.test", Path: "/auth", Expires: -1},
	}
}

func TestASessionCookieIsCopiedWithAnExpiryAndNothingElseChanged(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	pinned := PinnedCookies(testJar(), now, PinnedDays)
	require.Len(t, pinned, 2)

	first := pinned[0]
	require.Equal(t, "session", first.Name)
	require.Equal(t, "invented", first.Value)
	require.Equal(t, ".example.test", *first.Domain)
	require.Equal(t, "/", *first.Path)
	require.True(t, *first.HttpOnly)
	require.True(t, *first.Secure)
	require.InDelta(t, float64(now.Unix())+PinnedDays*86400, *first.Expires, 0.5)

	require.Equal(t, "gateway", pinned[1].Name)
	require.Equal(t, "/auth", *pinned[1].Path)
}

func TestACookieTheProviderDatedIsLeftOut(t *testing.T) {
	pinned := PinnedCookies(testJar(), time.Unix(1_700_000_000, 0), PinnedDays)
	for _, cookie := range pinned {
		require.NotEqual(t, "device", cookie.Name)
	}
}

func TestAnEmptyJarPinsNothing(t *testing.T) {
	require.Empty(t, PinnedCookies(nil, time.Now(), PinnedDays))
	require.Empty(t, PinnedCookies([]playwright.Cookie{}, time.Now(), PinnedDays))
}

func TestTheDayCountIsWhatSetsTheExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	pinned := PinnedCookies(testJar(), now, 1)
	require.InDelta(t, float64(now.Unix())+86400, *pinned[0].Expires, 0.5)
}
