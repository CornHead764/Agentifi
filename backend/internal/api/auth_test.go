package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Password login, the second factor, and signing out.
//
// The tests that matter most here are the negative ones. A login that works is
// easy; a login that fails *identically* for a wrong password, an unknown
// address and a disabled account is the property that stops the endpoint being
// a directory of who has an account.

const testPassword = "correct horse battery"

// Cache real bcrypt hashes for HTTP fixtures. TestMain lowers only the test
// work factor; internal/auth separately checks the production cost.
var hashes sync.Map // password string -> hash string

func hashFor(t *testing.T, password string) string {
	t.Helper()
	if cached, ok := hashes.Load(password); ok {
		return cached.(string)
	}
	hashed, err := auth.HashPassword(password)
	require.NoError(t, err)
	hashes.Store(password, hashed)
	return hashed
}

func makeUser(t *testing.T, password string, adjust ...func(*store.User)) store.User {
	t.Helper()
	user := &store.User{
		Email:    fmt.Sprintf("user-%s@example.test", uuid.NewString()),
		FullName: "Test Person",
		IsActive: true,
	}
	if password != "" {
		user.HashedPassword = hashFor(t, password)
	}
	for _, fn := range adjust {
		fn(user)
	}
	require.NoError(t, db(t).CreateUser(t.Context(), user))
	return *user
}

func login(c *client, email, password string) *response {
	return c.form("/auth/token", url.Values{"username": {email}, "password": {password}})
}

func TestPasswordLoginSetsAUsableSessionCookie(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)

	signedIn := login(c, user.Email, testPassword).requireStatus(http.StatusOK)
	require.NotContains(t, signedIn.Body.String(), signedIn.sessionCookie().Value,
		"the token is for the cookie alone, out of a script's reach")

	me := c.signedIn(signedIn).get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, user.Email, me["email"])
	require.Equal(t, true, me["has_password"])
	require.Equal(t, false, me["has_totp"])
}

func TestEveryFirstFactorFailureLooksTheSame(t *testing.T) {
	t.Parallel()
	// Wrong password, unknown address, disabled account, passkey-only account.
	c := newClient(t)
	known := makeUser(t, testPassword)
	disabled := makeUser(t, testPassword, func(u *store.User) { u.IsActive = false })
	passkeyOnly := makeUser(t, "")

	answers := map[string]bool{}
	for _, attempt := range []*response{
		login(c, known.Email, "wrong"),
		login(c, "nobody-"+uuid.NewString()+"@example.test", testPassword),
		login(c, disabled.Email, testPassword),
		login(c, passkeyOnly.Email, testPassword),
	} {
		require.Equal(t, http.StatusUnauthorized, attempt.Code)
		answers[attempt.Body.String()] = true
	}
	require.Len(t, answers, 1, "the failures are distinguishable: %v", answers)
}

func TestEmailCaseDoesNotDecideWhoCanLogIn(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)
	login(c, "ALEX-"+user.Email, testPassword).requireStatus(http.StatusUnauthorized)
	login(c, upperEmail(user.Email), testPassword).requireStatus(http.StatusOK)
}

func TestLogoutEndsTheSessionAndClearsItsCookie(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)
	c.signedIn(login(c, user.Email, testPassword).requireStatus(http.StatusOK))

	c.get("/auth/me").requireStatus(http.StatusOK)
	cleared := c.post("/auth/logout", nil).requireStatus(http.StatusNoContent).sessionCookie()
	require.NotNil(t, cleared)
	require.Negative(t, cleared.MaxAge)
	c.get("/auth/me").requireStatus(http.StatusUnauthorized)
}

func TestDeactivationEndsTheAccountsSessions(t *testing.T) {
	t.Parallel()
	// Turning the account back on must not bring a session from before back.
	c := newClient(t)
	admin := makeAdmin(t)
	user := makeUser(t, testPassword)
	c.as(user)
	held := c.token
	c.get("/auth/me").requireStatus(http.StatusOK)

	c.as(admin)
	path := "/admin/users/" + user.ID.String()
	c.patch(path, map[string]any{"is_active": false}).requireStatus(http.StatusOK)
	c.patch(path, map[string]any{"is_active": true}).requireStatus(http.StatusOK)

	c.token = held
	c.get("/auth/me").requireStatus(http.StatusUnauthorized)
}

func TestAnUnauthenticatedRequestIsRefusedWithAChallenge(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	response := c.get("/auth/me").requireStatus(http.StatusUnauthorized)
	require.Equal(t, "Bearer", response.Header().Get("WWW-Authenticate"))
}

func TestLoginIsMetered(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.env.Cfg.LoginMaxAttempts = 3
	makeUser(t, testPassword)

	codes := make([]int, 0, 5)
	for range 5 {
		codes = append(codes,
			login(c, "nobody-"+uuid.NewString()+"@example.test", "wrong").Code)
	}
	require.Equal(t, []int{401, 401, 401, 429, 429}, codes)
}

func TestLoginRateLimitIgnoresASpoofedForwardedHeader(t *testing.T) {
	t.Parallel()
	// With no trusted proxy configured, X-Forwarded-For must be ignored: an
	// attacker rotating it per request must not mint a fresh rate-limit bucket
	// each time and walk past the limit. The limiter meters the real TCP peer,
	// which httptest holds constant across these requests.
	c := newClient(t)
	c.env.Cfg.LoginMaxAttempts = 3
	makeUser(t, testPassword)

	codes := make([]int, 0, 5)
	for i := range 5 {
		r := httptest.NewRequest(http.MethodPost, "/auth/token",
			strings.NewReader(url.Values{
				"username": {"nobody-" + uuid.NewString() + "@example.test"},
				"password": {"wrong"},
			}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// A different forwarded address on every request, in each of the three
		// headers chi's RealIP would have believed.
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		r.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", i+101))
		r.Header.Set("True-Client-IP", fmt.Sprintf("203.0.113.%d", i+201))
		rec := httptest.NewRecorder()
		c.handler.ServeHTTP(rec, r)
		codes = append(codes, rec.Code)
	}
	require.Equal(t, []int{401, 401, 401, 429, 429}, codes,
		"a rotating forwarded header must not lift the per-IP limit")
}

func TestLoginIdentityMeterKeysOnABoundedHash(t *testing.T) {
	t.Parallel()
	// The per-account counter is keyed on what an unauthenticated caller
	// typed and is held for the whole attempt window, so the key is a
	// fixed-size hash of the folded identifier and an identifier no address
	// could be is refused before anything is stored under it.
	c := newClient(t)

	login(c, "Someone@Example.Test", "wrong").requireStatus(http.StatusUnauthorized)
	_, counted := c.env.Secrets.Get(identityMeterKey("someone@example.test"))
	require.True(t, counted, "the identity counter keys on the folded identifier's hash")

	held := c.env.Secrets.Len()
	login(c, strings.Repeat("a", maxIdentifierLength)+"@example.test", "wrong").
		requireStatus(http.StatusUnauthorized)
	require.Equal(t, held, c.env.Secrets.Len(),
		"an over-long identifier must not buy an entry in the secret store")
}

func TestLoginRateLimitBoundsGuessingAgainstOneAccount(t *testing.T) {
	t.Parallel()
	// Even from behind a trusted proxy that presents a fresh client IP each
	// request, one account cannot be guessed without bound: the per-account
	// ceiling is LoginMaxAttempts*10, above what a real person mistypes but
	// well below unlimited.
	c := newClient(t)
	c.env.Cfg.LoginMaxAttempts = 1 // per-account ceiling becomes 10
	// Trust a proxy range the forwarded client IPs below do not fall in, so the
	// forwarded value resolves to a distinct client IP every request and the
	// per-IP bucket never accumulates.
	c.env.Cfg.TrustedProxies = []string{"10.0.0.0/8"}
	c.handler = RouterFor(c.env)
	makeUser(t, testPassword)

	victim := "victim@example.test"
	trippedAt := 0
	for i := range 15 {
		r := httptest.NewRequest(http.MethodPost, "/auth/token",
			strings.NewReader(url.Values{"username": {victim}, "password": {"wrong"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		rec := httptest.NewRecorder()
		c.handler.ServeHTTP(rec, r)
		if rec.Code == http.StatusTooManyRequests {
			trippedAt = i + 1
			break
		}
	}
	require.NotZero(t, trippedAt, "guessing one account from rotating IPs must eventually be throttled")
	// It took more than the per-IP ceiling to trip, proving it was the
	// per-account bucket and not the per-IP one that fired.
	require.Greater(t, trippedAt, c.env.Cfg.LoginMaxAttempts+1)
}

// --- Second factor ------------------------------------------------------------

func enrolTOTPFor(t *testing.T, c *client, user store.User) (secret string, recovery []string) {
	t.Helper()
	c.as(user)
	secret = c.post("/auth/totp/enrol", nil).requireStatus(http.StatusOK).json()["secret"].(string)

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	confirmed := c.post("/auth/totp/confirm", map[string]any{"code": code}).
		requireStatus(http.StatusOK).json()
	for _, item := range confirmed["recovery_codes"].([]any) {
		recovery = append(recovery, item.(string))
	}
	return secret, recovery
}

func TestATOTPLockoutKeepsThePendingEnrolment(t *testing.T) {
	t.Parallel()
	// Confirming takes the pending secret out of the store to check a code
	// against it. A lockout is a throttle that clears on its own, so the
	// secret has to still be there afterwards — discarding it means starting
	// over with a QR code the user has already scanned.
	c := newClient(t)
	user := makeUser(t, testPassword)
	c.as(user)
	c.env.TOTP.MaxAttempts = 1

	secret := c.post("/auth/totp/enrol", nil).requireStatus(http.StatusOK).json()["secret"].(string)
	c.post("/auth/totp/confirm", map[string]any{"code": "000000"}).
		requireStatus(http.StatusBadRequest)
	c.post("/auth/totp/confirm", map[string]any{"code": "000000"}).
		requireStatus(http.StatusTooManyRequests)

	pending, held := c.env.TOTP.TakePendingSecret(user.ID)
	require.True(t, held, "a lockout must not discard the enrolment")
	require.Equal(t, secret, pending)
}

func TestASecondFactorTurnsLoginIntoAChallenge(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)
	secret, _ := enrolTOTPFor(t, c, user)

	c.token = ""
	challenged := login(c, user.Email, testPassword).requireStatus(http.StatusOK)
	require.Nil(t, challenged.sessionCookie(), "a password alone must not be a session")
	first := challenged.json()
	require.NotEmpty(t, first["mfa_token"])
	require.Equal(t, []any{"totp", "recovery_code"}, first["mfa_methods"])

	// The step the code was minted for is burned by the confirmation, so this
	// waits for the next one rather than replaying it.
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	require.NoError(t, err)
	verified := c.post("/auth/token/mfa", map[string]any{
		"mfa_token": first["mfa_token"], "code": code,
	}).requireStatus(http.StatusNoContent)
	c.signedIn(verified).get("/auth/me").requireStatus(http.StatusOK)
}

func TestARecoveryCodeIsAcceptedAtTheSamePromptAndOnlyOnce(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)
	_, recovery := enrolTOTPFor(t, c, user)
	require.NotEmpty(t, recovery)

	c.token = ""
	challenge := login(c, user.Email, testPassword).requireStatus(http.StatusOK).json()
	c.post("/auth/token/mfa", map[string]any{
		"mfa_token": challenge["mfa_token"], "code": recovery[0],
	}).requireStatus(http.StatusNoContent)

	second := login(c, user.Email, testPassword).requireStatus(http.StatusOK).json()
	c.post("/auth/token/mfa", map[string]any{
		"mfa_token": second["mfa_token"], "code": recovery[0],
	}).requireStatus(http.StatusBadRequest)
}

func TestDisablingTheSecondFactorNeedsACurrentCode(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)
	secret, _ := enrolTOTPFor(t, c, user)

	c.post("/auth/totp/disable", map[string]any{"code": "000000"}).
		requireStatus(http.StatusBadRequest)

	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	require.NoError(t, err)
	c.post("/auth/totp/disable", map[string]any{"code": code}).
		requireStatus(http.StatusNoContent)

	status := c.get("/auth/totp").requireStatus(http.StatusOK).json()
	require.Equal(t, false, status["enabled"])
}

func TestEnrollingTwiceIsRefused(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)
	enrolTOTPFor(t, c, user)
	c.post("/auth/totp/enrol", nil).requireStatus(http.StatusBadRequest)
}

func TestConfirmingWithoutEnrollingIsRefused(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.as(makeUser(t, testPassword))
	c.post("/auth/totp/confirm", map[string]any{"code": "123456"}).
		requireStatus(http.StatusBadRequest)
}

// --- OIDC ---------------------------------------------------------------------

func TestOIDCConfigIsOffUntilItIsConfigured(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	body := c.get("/auth/oidc/config").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["enabled"])
	require.Equal(t, "OIDC", body["provider_name"])
}

func TestOIDCLoginIsNotOfferedWhenItIsNotEnabled(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.get("/auth/oidc/login").requireStatus(http.StatusNotFound)
}

func upperEmail(email string) string {
	out := []rune(email)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
	}
	return string(out)
}

// --- Preferences -------------------------------------------------------------
//
// locale, theme and privacy_mode are columns /auth/me returns. Without a way
// to write them, a theme chosen on the laptop would not follow the phone.
// PATCH writes them; what these check is that it refuses what the client
// cannot render, and that a settings save cannot reach anything else.

func TestPreferencesRoamThroughTheAccount(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.as(makeUser(t, testPassword))

	saved := c.patch("/auth/me", map[string]any{
		"full_name":    "Renamed Person",
		"locale":       "en-GB",
		"theme":        "dark",
		"privacy_mode": true,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "Renamed Person", saved["full_name"])
	require.Equal(t, "en-GB", saved["locale"])
	require.Equal(t, "dark", saved["theme"])
	require.Equal(t, true, saved["privacy_mode"])

	// A second device reads the same answer, which is the whole point.
	me := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, "en-GB", me["locale"])
	require.Equal(t, "dark", me["theme"])
	require.Equal(t, true, me["privacy_mode"])
}

func TestAFreshAccountReportsThePreferenceDefaults(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.as(makeUser(t, testPassword))

	me := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, "en", me["locale"])
	require.Equal(t, "system", me["theme"])
	require.Equal(t, false, me["privacy_mode"])
	require.Equal(t, float64(160), me["animation_duration_ms"])
	require.Equal(t, float64(6000), me["toast_duration_ms"])
}

func TestAnAbsentPreferenceIsLeftAloneAndANullOneIsReset(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.as(makeUser(t, testPassword))
	c.patch("/auth/me", map[string]any{"locale": "fr-CA", "theme": "light", "privacy_mode": true}).
		requireStatus(http.StatusOK)

	// Absent: a client saving only the name must not wipe the rest.
	kept := c.patch("/auth/me", map[string]any{"full_name": "Someone"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "fr-CA", kept["locale"])
	require.Equal(t, "light", kept["theme"])
	require.Equal(t, true, kept["privacy_mode"])

	// Null: back to the default, the same answer full_name gives.
	reset := c.patch("/auth/me", map[string]any{
		"locale": nil, "theme": nil, "privacy_mode": nil,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "en", reset["locale"])
	require.Equal(t, "system", reset["theme"])
	require.Equal(t, false, reset["privacy_mode"])
}

func TestAThemeTheClientCannotRenderIsRefused(t *testing.T) {
	t.Parallel()
	// The set is the frontend's: a preference that roams has to arrive as
	// something the theme provider has a branch for.
	c := newClient(t)
	c.as(makeUser(t, testPassword))

	c.patch("/auth/me", map[string]any{"theme": "midnight"}).
		requireStatus(http.StatusUnprocessableEntity)
	c.patch("/auth/me", map[string]any{"locale": "not a language tag"}).
		requireStatus(http.StatusUnprocessableEntity)
	c.patch("/auth/me", map[string]any{"locale": strings.Repeat("a", 64)}).
		requireStatus(http.StatusUnprocessableEntity)

	me := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, "system", me["theme"])
	require.Equal(t, "en", me["locale"])
}

func TestTheSwipeActionsRoamAndAreRefusedWhenTheRegisterCannotPerformThem(t *testing.T) {
	t.Parallel()
	// The phone's register has no row menu and no checkboxes until it is asked
	// for them; a swipe is how a row is acted on there, so which action each
	// direction runs is the user's own choice and has to follow them to the
	// next phone.
	c := newClient(t)
	c.as(makeUser(t, testPassword))

	fresh := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, "menu", fresh["swipe_left_action"])
	require.Equal(t, "review", fresh["swipe_right_action"])

	saved := c.patch("/auth/me", map[string]any{
		"swipe_left_action":  "flag",
		"swipe_right_action": "exclude",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "flag", saved["swipe_left_action"])
	require.Equal(t, "exclude", saved["swipe_right_action"])

	me := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, "flag", me["swipe_left_action"])
	require.Equal(t, "exclude", me["swipe_right_action"])

	// Absent leaves them; null restores the direction's own default.
	kept := c.patch("/auth/me", map[string]any{"theme": "dark"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "flag", kept["swipe_left_action"])
	require.Equal(t, "exclude", kept["swipe_right_action"])

	reset := c.patch("/auth/me", map[string]any{
		"swipe_left_action": nil, "swipe_right_action": nil,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "menu", reset["swipe_left_action"])
	require.Equal(t, "review", reset["swipe_right_action"])

	// A name no row has a branch for is refused rather than stored, or the
	// swipe answers with nothing and no way to find out why.
	c.patch("/auth/me", map[string]any{"swipe_left_action": "explode"}).
		requireStatus(http.StatusUnprocessableEntity)
	c.patch("/auth/me", map[string]any{"swipe_right_action": "Review"}).
		requireStatus(http.StatusUnprocessableEntity)

	after := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, "menu", after["swipe_left_action"])
	require.Equal(t, "review", after["swipe_right_action"])
}

func TestTheAnimationDurationRoamsAndZeroMeansNoneOfIt(t *testing.T) {
	t.Parallel()
	// One duration drives every animated surface in the client, so this is the
	// whole of the setting — and zero is a value rather than the absence of
	// one: somebody who cannot look at movement turns it off and must not be
	// handed the default back.
	c := newClient(t)
	c.as(makeUser(t, testPassword))

	saved := c.patch("/auth/me", map[string]any{"animation_duration_ms": 0}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), saved["animation_duration_ms"])

	me := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), me["animation_duration_ms"])

	// Absent leaves it; null restores the default.
	kept := c.patch("/auth/me", map[string]any{"theme": "dark"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), kept["animation_duration_ms"])

	reset := c.patch("/auth/me", map[string]any{"animation_duration_ms": nil}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(160), reset["animation_duration_ms"])

	// A duration the next device could not recover from is refused rather than
	// stored: it roams, and a rail that takes ten seconds to open is between
	// the person and the screen that would fix it.
	c.patch("/auth/me", map[string]any{"animation_duration_ms": 10000}).
		requireStatus(http.StatusUnprocessableEntity)
	c.patch("/auth/me", map[string]any{"animation_duration_ms": -1}).
		requireStatus(http.StatusUnprocessableEntity)

	after := c.get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(160), after["animation_duration_ms"])
}

func TestTheToastDurationRoamsWithinItsBounds(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.as(makeUser(t, testPassword))

	saved := c.patch("/auth/me", map[string]any{"toast_duration_ms": 12000}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(12000), saved["toast_duration_ms"])

	kept := c.patch("/auth/me", map[string]any{"theme": "dark"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(12000), kept["toast_duration_ms"])

	c.patch("/auth/me", map[string]any{"toast_duration_ms": 0}).
		requireStatus(http.StatusUnprocessableEntity)
	c.patch("/auth/me", map[string]any{"toast_duration_ms": 600000}).
		requireStatus(http.StatusUnprocessableEntity)

	reset := c.patch("/auth/me", map[string]any{"toast_duration_ms": nil}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(6000), reset["toast_duration_ms"])
}

func TestSavingPreferencesCannotTouchACredential(t *testing.T) {
	t.Parallel()
	// The narrow write, from the endpoint's side: this handler holds a user
	// row read at the start of the request, and a whole-row save would carry
	// a stale password hash and a cleared session cutoff back with it.
	c := newClient(t)
	user := makeUser(t, testPassword)
	c.as(user)

	c.patch("/auth/me", map[string]any{"theme": "dark"}).requireStatus(http.StatusOK)

	after, err := db(t).GetUser(t.Context(), user.ID)
	require.NoError(t, err)
	require.Equal(t, user.HashedPassword, after.HashedPassword)
	require.True(t, after.IsActive)
	require.False(t, after.MustChangePassword)
}

func TestChangingThePasswordDoesNotLoseAConcurrentProfileSave(t *testing.T) {
	t.Parallel()
	// Two concurrent writes touch different columns, so whichever lands
	// second leaves the other standing.
	c := newClient(t)
	user := makeUser(t, testPassword)
	c.as(user)

	// The settings form loads the user, then the password changes underneath it.
	c.post("/auth/password", map[string]any{
		"current_password": testPassword,
		"new_password":     "a different long passphrase",
	}).requireStatus(http.StatusNoContent)

	c.patch("/auth/me", map[string]any{"theme": "dark"}).requireStatus(http.StatusOK)

	after, err := db(t).GetUser(t.Context(), user.ID)
	require.NoError(t, err)
	require.NotEqual(t, user.HashedPassword, after.HashedPassword)
	require.Equal(t, "dark", after.Theme)
}
