package browser

import (
	"fmt"
	"time"

	"github.com/playwright-community/playwright-go"
)

// PinnedDays is longer than any provider's idle session, so what ends a
// sign-in is the provider rather than the browser closing.
const PinnedDays = 30

// PinnedCookies is the session cookies of a jar, copied with an expiry on them.
// A cookie with no expiry dies with the browser process, and a kept profile's
// browser closes after every pull, so a sign-in carried in session cookies
// (Azure AD B2C, F5 gateways) would never survive. A dated cookie is left as
// it is.
func PinnedCookies(cookies []playwright.Cookie, now time.Time, days int) []playwright.OptionalCookie {
	expires := float64(now.Unix()) + float64(days)*86400
	pinned := make([]playwright.OptionalCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Expires >= 0 {
			continue
		}
		pinned = append(pinned, playwright.OptionalCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   playwright.String(cookie.Domain),
			Path:     playwright.String(cookie.Path),
			Expires:  playwright.Float(expires),
			HttpOnly: playwright.Bool(cookie.HttpOnly),
			Secure:   playwright.Bool(cookie.Secure),
			SameSite: cookie.SameSite,
		})
	}
	return pinned
}

// PinSessionCookies is called on a signed-in context just before it closes.
// The note carries only the count: a cookie's name is as private as its value.
func PinSessionCookies(context playwright.BrowserContext, notes *[]string) (int, error) {
	if context == nil {
		return 0, nil
	}
	cookies, err := context.Cookies()
	if err != nil {
		return 0, err
	}
	pinned := PinnedCookies(cookies, time.Now(), PinnedDays)
	if len(pinned) == 0 {
		return 0, nil
	}
	if err := context.AddCookies(pinned); err != nil {
		return 0, err
	}
	if notes != nil {
		plural := "s"
		if len(pinned) == 1 {
			plural = ""
		}
		*notes = append(*notes, fmt.Sprintf(
			"%d session cookie%s pinned to last %d days, so closing the browser does not end the sign-in",
			len(pinned), plural, PinnedDays))
	}
	return len(pinned), nil
}
