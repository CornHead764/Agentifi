package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The authentication surface: password, passkey, second factor and OIDC, all
// ending in a bearer token that says who the caller is and nothing about what
// they may do. The space is resolved per request, so revoking a membership
// takes effect on the next call.
//
// Nothing here answers "does this account exist": a wrong password, an unknown
// address and a disabled account share one 401, and every OIDC refusal reads
// the same. Password, second-factor and recovery-code endpoints are metered.

func init() {
	RegisterIdentity(Resource{Prefix: "/auth", Routes: func(rt *Routes) {
		rt.Public(http.MethodPost, "/token", issueToken)
		rt.Public(http.MethodPost, "/token/mfa", verifySecondFactor)
		rt.Public(http.MethodGet, "/first-account", readFirstAccount)
		rt.Public(http.MethodPost, "/first-account", createFirstAccount)
		rt.User(http.MethodGet, "/me", readCurrentUser)
		rt.User(http.MethodPatch, "/me", updateCurrentUser)
		rt.User(http.MethodPost, "/password", changePassword)
		rt.User(http.MethodPost, "/logout", logout)

		rt.User(http.MethodGet, "/passkeys", listPasskeys)
		rt.User(http.MethodPost, "/passkeys/register/options", passkeyRegisterOptions)
		rt.User(http.MethodPost, "/passkeys/register/verify", passkeyRegisterVerify)
		rt.User(http.MethodDelete, "/passkeys/{passkey_id}", deletePasskey)
		rt.Public(http.MethodPost, "/passkeys/authenticate/options", passkeyAuthenticateOptions)
		rt.Public(http.MethodPost, "/passkeys/authenticate/verify", passkeyAuthenticateVerify)

		rt.User(http.MethodGet, "/totp", totpStatus)
		rt.User(http.MethodPost, "/totp/enrol", enrolTOTP)
		rt.User(http.MethodPost, "/totp/confirm", confirmTOTP)
		rt.User(http.MethodPost, "/totp/disable", disableTOTP)

		rt.Public(http.MethodGet, "/oidc/config", oidcConfig)
		rt.Public(http.MethodGet, "/oidc/login", oidcLogin)
		rt.Public(http.MethodGet, "/oidc/callback", oidcCallback)
	}})
}

// errOIDCRefused is every reason an OIDC identity may not become a session,
// said the same way, so the reason cannot reveal whether the address is
// registered.
var errOIDCRefused = errors.New("api: OIDC login is not available for this account")

// errPasswordChangeRequired is the 403 every route except passwordChangeExempt
// gives a caller whose password was set for them.
var errPasswordChangeRequired = errors.New("api: this account must set a new password")

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// LoginResponse is the result of a first factor: a session or a second-factor
// challenge, never both.
type LoginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	// MfaToken means the password was right and a second factor is owed. It
	// authorizes nothing else.
	MfaToken   string   `json:"mfa_token"`
	MfaMethods []string `json:"mfa_methods"`
}

type UserResponse struct {
	ID uuid.UUID `json:"id"`
	// Not re-validated on the way out, so one odd stored address is not a 500.
	Email       string  `json:"email"`
	FullName    *string `json:"full_name"`
	IsActive    bool    `json:"is_active"`
	IsSuperuser bool    `json:"is_superuser"`
	IsVerified  bool    `json:"is_verified"`
	Locale      string  `json:"locale"`
	Theme       string  `json:"theme"`
	PrivacyMode bool    `json:"privacy_mode"`
	// What a swipe across a register row does on a phone. The names are the
	// register's own; `swipeActions` is the set.
	SwipeLeftAction  string `json:"swipe_left_action"`
	SwipeRightAction string `json:"swipe_right_action"`
	// How long anything on screen takes to move, in milliseconds. Zero is no
	// animation at all rather than a fast one.
	AnimationDurationMs int `json:"animation_duration_ms"`
	// How long a toast stays before it dismisses itself, in milliseconds.
	ToastDurationMs int        `json:"toast_duration_ms"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	// MustChangePassword is the only field here the client is obliged to act
	// on: while it is true the rest of the API refuses this caller.
	MustChangePassword bool `json:"must_change_password"`
	// Which credentials exist, never the credentials themselves. The UI needs
	// this to decide whether "remove password" is safe to offer.
	HasPassword bool `json:"has_password"`
	HasTOTP     bool `json:"has_totp"`
	HasOIDC     bool `json:"has_oidc"`
}

func issueToken(env *Env, w http.ResponseWriter, r *http.Request) error {
	if err := meterLogin(env, r); err != nil {
		return err
	}
	// The one form-body route, so it skips the JSON maxBodyBytes reader;
	// without this cap it is unauthenticated memory pressure.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		return errInvalid("form_invalid", []string{"body"}, "expected a form-encoded body")
	}
	// `username` is the email address: the field name is OAuth2's, and the
	// frontend posts what that spec calls for.
	username := r.PostFormValue("username")
	// Bound guessing against this one account too, so a rotated source address
	// cannot make guesses against it free.
	if err := meterLoginIdentity(env, username); err != nil {
		return err
	}
	user, err := auth.AuthenticatePassword(r.Context(), env.DB,
		username, r.PostFormValue("password"))
	if err != nil {
		return err
	}

	if user.TOTPSecret == "" {
		token, err := issueSession(r.Context(), env, user)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, LoginResponse{
			AccessToken: token, TokenType: "bearer", MfaMethods: []string{},
		})
	}

	handle, err := env.Pending.Issue(user.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, LoginResponse{
		TokenType:  "bearer",
		MfaToken:   handle,
		MfaMethods: []string{"totp", "recovery_code"},
	})
}

// verifySecondFactor exchanges a TOTP or recovery code for a session, trying
// both so the client need not say which.
func verifySecondFactor(env *Env, w http.ResponseWriter, r *http.Request) error {
	if err := meterLogin(env, r); err != nil {
		return err
	}
	var body struct {
		MfaToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	userID, err := env.Pending.User(body.MfaToken)
	if err != nil {
		return err
	}
	user, err := env.DB.GetUser(r.Context(), userID)
	if err != nil {
		if isNotFound(err) {
			return auth.ErrInvalidCredentials
		}
		return err
	}
	if !user.IsActive || user.TOTPSecret == "" {
		return auth.ErrInvalidCredentials
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return err
	}
	seed, err := sealed.OpenTOTPSecret(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accepted, err := env.TOTP.SubmitSecondFactor(r.Context(), env.Codes, user.ID, seed, body.Code)
	if err != nil {
		return err
	}
	if !accepted {
		return auth.ErrInvalidCode
	}

	// A wrong code leaves the challenge alive — a typo should not cost the
	// user their password entry — so it is spent only once one has verified.
	env.Pending.Spend(body.MfaToken)
	token, err := issueSession(r.Context(), env, user)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, TokenResponse{AccessToken: token, TokenType: "bearer"})
}

func readCurrentUser(_ *Env, w http.ResponseWriter, _ *http.Request, user store.User) error {
	return writeJSON(w, http.StatusOK, userResponse(user))
}

// UserUpdate is what a person may change about themselves here: the display
// name and presentation preferences. Every field is optional; absent leaves
// it, null restores the default.
type UserUpdate struct {
	FullName            Opt[string] `json:"full_name"`
	Locale              Opt[string] `json:"locale"`
	Theme               Opt[string] `json:"theme"`
	PrivacyMode         Opt[bool]   `json:"privacy_mode"`
	SwipeLeftAction     Opt[string] `json:"swipe_left_action"`
	SwipeRightAction    Opt[string] `json:"swipe_right_action"`
	AnimationDurationMs Opt[int]    `json:"animation_duration_ms"`
	ToastDurationMs     Opt[int]    `json:"toast_duration_ms"`
}

// The preference defaults, which are also what clearing a field restores.
// They match what internal/store writes on account creation.
const (
	defaultLocale     = "en"
	defaultTheme      = "system"
	defaultSwipeLeft  = "menu"
	defaultSwipeRight = "review"
	// Long enough to read as movement, short enough not to be a wait — and the
	// timing the frontend's hand-written transitions use.
	defaultAnimationMs = 160
	defaultToastMs     = 6000
)

// maxAnimationMs bounds the duration because the preference roams, and a
// device given an unusably slow rail could not reach the screen that fixes it.
// Zero means no animation.
const maxAnimationMs = 1000

// A toast shorter than a second cannot be read, and one longer than a minute
// is one the person dismisses by hand anyway.
const (
	minToastMs = 1000
	maxToastMs = 60000
)

// themes is the set the frontend renders; a roaming preference must not arrive
// as a value the client has no branch for.
var themes = map[string]bool{"light": true, "dark": true, "system": true}

// swipeActions is the set the register can perform, for the same reason.
var swipeActions = map[string]bool{
	"menu": true, "review": true, "flag": true, "exclude": true, "edit": true, "none": true,
}

// maxLocaleLength bounds a BCP 47 tag generously — "zh-Hant-HK-u-ca-chinese"
// and its relatives fit; a paragraph does not.
const maxLocaleLength = 35

func updateCurrentUser(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	var body UserUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	switch {
	case body.FullName.Cleared():
		user.FullName = ""
	case body.FullName.Present():
		// A name of spaces is no name.
		user.FullName = strings.TrimSpace(body.FullName.Value)
	}
	switch {
	case body.Locale.Cleared():
		user.Locale = defaultLocale
	case body.Locale.Present():
		locale := strings.TrimSpace(body.Locale.Value)
		if locale == "" {
			locale = defaultLocale
		}
		if !isLanguageTag(locale) {
			return errInvalid("invalid", []string{"body", "locale"},
				"%q is not a language tag", locale)
		}
		user.Locale = locale
	}
	switch {
	case body.Theme.Cleared():
		user.Theme = defaultTheme
	case body.Theme.Present():
		if !themes[body.Theme.Value] {
			return errInvalid("enum", []string{"body", "theme"},
				"%q is not a theme", body.Theme.Value)
		}
		user.Theme = body.Theme.Value
	}
	switch {
	case body.PrivacyMode.Cleared():
		user.PrivacyMode = false
	case body.PrivacyMode.Present():
		user.PrivacyMode = body.PrivacyMode.Value
	}
	left, err := readSwipeAction(body.SwipeLeftAction, user.SwipeLeftAction,
		defaultSwipeLeft, "swipe_left_action")
	if err != nil {
		return err
	}
	user.SwipeLeftAction = left
	right, err := readSwipeAction(body.SwipeRightAction, user.SwipeRightAction,
		defaultSwipeRight, "swipe_right_action")
	if err != nil {
		return err
	}
	user.SwipeRightAction = right
	animation, err := readAnimationDuration(body.AnimationDurationMs, user.AnimationDurationMs)
	if err != nil {
		return err
	}
	user.AnimationDurationMs = animation
	toastMs, err := readToastDuration(body.ToastDurationMs, user.ToastDurationMs)
	if err != nil {
		return err
	}
	user.ToastDurationMs = toastMs

	// The narrow write: rewriting the whole row read at the start of the
	// request would undo a password change that landed in between.
	if err := env.DB.UpdateUserProfile(r.Context(), user.ID, store.UserProfile{
		FullName:            user.FullName,
		Locale:              user.Locale,
		Theme:               user.Theme,
		PrivacyMode:         user.PrivacyMode,
		SwipeLeftAction:     user.SwipeLeftAction,
		SwipeRightAction:    user.SwipeRightAction,
		AnimationDurationMs: user.AnimationDurationMs,
		ToastDurationMs:     user.ToastDurationMs,
	}); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, userResponse(user))
}

// readSwipeAction resolves one direction: absent leaves it, null restores the
// default, and anything outside the set is refused rather than stored.
func readSwipeAction(field Opt[string], current, fallback, name string) (string, error) {
	switch {
	case field.Cleared():
		return fallback, nil
	case field.Present():
		if !swipeActions[field.Value] {
			return "", errInvalid("enum", []string{"body", name},
				"%q is not a swipe action", field.Value)
		}
		return field.Value, nil
	}
	return current, nil
}

// readAnimationDuration resolves the animation duration: absent leaves it, null
// restores the default, out of range is refused.
func readAnimationDuration(field Opt[int], current int) (int, error) {
	switch {
	case field.Cleared():
		return defaultAnimationMs, nil
	case field.Present():
		if field.Value < 0 || field.Value > maxAnimationMs {
			return 0, errInvalid("out_of_range", []string{"body", "animation_duration_ms"},
				"%d is not a duration between 0 and %d milliseconds", field.Value, maxAnimationMs)
		}
		return field.Value, nil
	}
	return current, nil
}

// readToastDuration resolves the toast duration: absent leaves it, null
// restores the default, out of range is refused.
func readToastDuration(field Opt[int], current int) (int, error) {
	switch {
	case field.Cleared():
		return defaultToastMs, nil
	case field.Present():
		if field.Value < minToastMs || field.Value > maxToastMs {
			return 0, errInvalid("out_of_range", []string{"body", "toast_duration_ms"},
				"%d is not a duration between %d and %d milliseconds",
				field.Value, minToastMs, maxToastMs)
		}
		return field.Value, nil
	}
	return current, nil
}

// isLanguageTag accepts the shape of a BCP 47 tag without the registry: two to
// eight letters, then any number of alphanumeric subtags.
func isLanguageTag(tag string) bool {
	if len(tag) > maxLocaleLength {
		return false
	}
	for i, part := range strings.Split(tag, "-") {
		if len(part) == 0 || len(part) > 8 {
			return false
		}
		for _, r := range part {
			letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			digit := r >= '0' && r <= '9'
			if !letter && !(digit && i > 0) {
				return false
			}
		}
		if i == 0 && len(part) < 2 {
			return false
		}
	}
	return true
}

// logout retires the presented token. A JWT the server forgot is still valid,
// so signing out leaves a mark that lives as long as the token would have.
func logout(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	if token := bearerToken(r); token != "" {
		if err := env.Tokens.Revoke(r.Context(), token); err != nil {
			return err
		}
	}
	return writeNoContent(w)
}

// --- Password ----------------------------------------------------------------

// ChangePasswordResponse carries the replacement session. Changing a password
// ends every session including the caller's (store.User.SessionsValidFrom);
// the new token is minted after the cutoff.
type ChangePasswordResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// changePassword replaces the caller's own password. The current password is
// required when there is one, since a session alone is what a borrowed laptop
// has. An account with no password sets one on the session alone.
func changePassword(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	if err := meterLogin(env, r); err != nil {
		return err
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	if user.HashedPassword != "" {
		if !auth.VerifyPassword(body.CurrentPassword, user.HashedPassword) {
			// Not a 401: the session is valid, and a client that signs out on
			// 401 would log the user out for a typo.
			return errBadRequest("The current password is incorrect")
		}
	} else {
		// Nothing to compare against, but the time is spent anyway so that
		// "this account has no password" is not readable from the clock.
		auth.DummyVerify()
	}

	if err := auth.ValidatePassword(body.NewPassword); err != nil {
		return errBadRequest("%s", strings.TrimPrefix(err.Error(), "auth: "))
	}
	if auth.VerifyPassword(body.NewPassword, user.HashedPassword) {
		return errBadRequest("The new password is the same as the current one")
	}

	hashed, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		return err
	}

	// The next second: `iat` is a Unix second, so a cutoff inside the current
	// second would let a token issued moments ago survive.
	cutoff := env.now().UTC().Truncate(time.Second).Add(time.Second)
	// Only these columns: the row loaded before the bcrypt verify may be stale.
	if err := env.DB.SetUserPassword(r.Context(), user.ID, hashed, false, cutoff); err != nil {
		return err
	}

	// Stamped at the cutoff so it is the one token that survives it.
	token, err := env.Tokens.IssueAt(user.ID, cutoff, env.Cfg.AccessTokenExpiry)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ChangePasswordResponse{AccessToken: token, TokenType: "bearer"})
}

// --- Passkeys ----------------------------------------------------------------

type PasskeyResponse struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Transports []string   `json:"transports"`
	// RPID is the domain it was registered on; a passkey does not work at
	// another origin.
	RPID *string `json:"rp_id"`
}

// PasskeyOptionsResponse is the challenge handle plus the options blob for the
// browser. The handle names the server's copy, the only one it verifies
// against.
type PasskeyOptionsResponse struct {
	ChallengeID string `json:"challenge_id"`
	Options     any    `json:"options"`
}

func listPasskeys(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	keys, err := env.Keys.ListPasskeys(r.Context(), user.ID)
	if err != nil {
		return err
	}
	out := make([]PasskeyResponse, 0, len(keys))
	for _, key := range keys {
		out = append(out, passkeyResponse(key))
	}
	return writeJSON(w, http.StatusOK, out)
}

func passkeyRegisterOptions(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeOptionalBody(r, &body); err != nil {
		return err
	}
	wctx, err := env.Passkeys.ContextForRequest(r)
	if err != nil {
		return err
	}
	handle, options, err := env.Passkeys.BeginRegistration(
		r.Context(), wctx, user.ID, user.Email, body.Name, env.Keys)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, PasskeyOptionsResponse{ChallengeID: handle, Options: options})
}

func passkeyRegisterVerify(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	var body struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
		Name        string          `json:"name"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	key, err := env.Passkeys.FinishRegistration(
		r.Context(), user.ID, body.ChallengeID, body.Credential, body.Name, env.Keys)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, passkeyResponse(key))
}

func deletePasskey(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	id, err := pathUUID(r, "passkey_id", "Passkey")
	if err != nil {
		return err
	}
	removed, err := env.Keys.DeletePasskey(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	if !removed {
		return errNotFound("Passkey")
	}
	return writeNoContent(w)
}

// passkeyAuthenticateOptions is unauthenticated and identity-free by design:
// no email in, no credential list out.
func passkeyAuthenticateOptions(env *Env, w http.ResponseWriter, r *http.Request) error {
	if err := meterLogin(env, r); err != nil {
		return err
	}
	wctx, err := env.Passkeys.ContextForRequest(r)
	if err != nil {
		return err
	}
	handle, options, err := env.Passkeys.BeginAuthentication(wctx)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, PasskeyOptionsResponse{ChallengeID: handle, Options: options})
}

func passkeyAuthenticateVerify(env *Env, w http.ResponseWriter, r *http.Request) error {
	if err := meterLogin(env, r); err != nil {
		return err
	}
	var body struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	key, err := env.Passkeys.FinishAuthentication(r.Context(), body.ChallengeID, body.Credential, env.Keys)
	if err != nil {
		return err
	}
	user, err := env.DB.GetUser(r.Context(), key.UserID)
	if err != nil {
		if isNotFound(err) {
			return auth.ErrInvalidPasskey
		}
		return err
	}
	if !user.IsActive {
		return auth.ErrInvalidPasskey
	}
	token, err := issueSession(r.Context(), env, user)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, TokenResponse{AccessToken: token, TokenType: "bearer"})
}

// --- TOTP --------------------------------------------------------------------

type TOTPStatusResponse struct {
	Enabled                bool `json:"enabled"`
	RecoveryCodesRemaining int  `json:"recovery_codes_remaining"`
}

type TOTPEnrolResponse struct {
	Secret     string `json:"secret"`
	OtpauthURI string `json:"otpauth_uri"`
}

// TOTPConfirmResponse hands back the recovery codes, once. They are stored as
// digests, so this is the only time the plaintext exists.
type TOTPConfirmResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func totpStatus(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	remaining, err := env.TOTP.RemainingRecoveryCodes(r.Context(), env.Codes, user.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, TOTPStatusResponse{
		Enabled:                user.TOTPSecret != "",
		RecoveryCodesRemaining: remaining,
	})
}

// enrolTOTP starts enrolment. The secret is not stored until a code confirms
// it, or closing the tab would lock the account out.
func enrolTOTP(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	if user.TOTPSecret != "" {
		return errBadRequest("Two-factor authentication is already on")
	}
	secret, uri, err := env.TOTP.BeginEnrolment(user.ID, user.Email)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, TOTPEnrolResponse{Secret: secret, OtpauthURI: uri})
}

func confirmTOTP(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	pending, held := env.TOTP.TakePendingSecret(user.ID)
	if !held {
		return auth.ErrNoPendingEnrolment
	}
	accepted, err := env.TOTP.VerifyCode(user.ID, pending, body.Code)
	if err != nil {
		// A lockout is not a failed enrolment either: the attempt window
		// clears on its own, and the secret has to still be here when it does.
		env.TOTP.HoldPendingSecret(user.ID, pending)
		return err
	}
	if !accepted {
		// Put it back: one mistyped digit should not restart the enrolment.
		env.TOTP.HoldPendingSecret(user.ID, pending)
		return auth.ErrInvalidCode
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return err
	}
	if err := sealed.SetTOTPSecret(r.Context(), user.ID, pending); err != nil {
		return err
	}
	codes, err := env.TOTP.IssueRecoveryCodes(r.Context(), env.Codes, user.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, TOTPConfirmResponse{RecoveryCodes: codes})
}

// disableTOTP turns the second factor off, which requires still holding it. A
// recovery code counts: the phone being gone is exactly when this is needed.
func disableTOTP(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if user.TOTPSecret == "" {
		return errBadRequest("Two-factor authentication is not on")
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return err
	}
	seed, err := sealed.OpenTOTPSecret(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accepted, err := env.TOTP.SubmitSecondFactor(r.Context(), env.Codes, user.ID, seed, body.Code)
	if err != nil {
		return err
	}
	if !accepted {
		return auth.ErrInvalidCode
	}

	if err := sealed.SetTOTPSecret(r.Context(), user.ID, ""); err != nil {
		return err
	}
	if err := env.TOTP.DiscardRecoveryCodes(r.Context(), env.Codes, user.ID); err != nil {
		return err
	}
	env.TOTP.CancelEnrolment(user.ID)
	return writeNoContent(w)
}

// --- OIDC --------------------------------------------------------------------

// OIDCConfigResponse is what the login screen needs to decide whether to draw
// the provider button.
type OIDCConfigResponse struct {
	Enabled      bool   `json:"enabled"`
	ProviderName string `json:"provider_name"`
}

func oidcConfig(env *Env, w http.ResponseWriter, _ *http.Request) error {
	// From the live provider, so enabling OIDC from the admin screen needs no
	// restart.
	settings := env.OIDC.Settings()
	return writeJSON(w, http.StatusOK, OIDCConfigResponse{
		Enabled:      settings.IsConfigured(),
		ProviderName: settings.ProviderName,
	})
}

// oidcBindingCookie binds an OIDC login to the browser that began it:
// BeginLogin stores its digest beside the nonce and the callback must present
// it, so a state URL an attacker completed against their own provider account
// fails in any other browser. There is no session yet, so a cookie is the only
// anchor.
const oidcBindingCookie = "agentifi_oidc_binding"

func setOIDCBindingCookie(env *Env, w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcBindingCookie,
		Value:    value,
		Path:     "/api/auth/oidc",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Plain-HTTP deployments exist, where a Secure cookie would never
		// come back and OIDC login would simply not complete.
		Secure: strings.HasPrefix(env.Cfg.FrontendURL, "https://"),
	})
}

func clearOIDCBindingCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcBindingCookie,
		Value:    "",
		Path:     "/api/auth/oidc",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func oidcLogin(env *Env, w http.ResponseWriter, r *http.Request) error {
	url, binding, err := env.OIDC.BeginLogin(r.Context())
	if err != nil {
		return err
	}
	setOIDCBindingCookie(env, w, r, binding)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	return nil
}

// oidcCallback is where the provider sends the browser back. The token goes in
// the URL fragment, which browsers do not send and proxies do not log.
func oidcCallback(env *Env, w http.ResponseWriter, r *http.Request) error {
	binding, err := r.Cookie(oidcBindingCookie)
	if err != nil {
		return auth.ErrOIDCLogin
	}
	clearOIDCBindingCookie(w)
	identity, err := env.OIDC.CompleteLogin(r.Context(),
		r.URL.Query().Get("code"), r.URL.Query().Get("state"), binding.Value)
	if err != nil {
		return err
	}
	user, created, err := loginWithOIDC(r.Context(), env, identity)
	if err != nil {
		return err
	}
	if created {
		if err := createPersonalSpace(r.Context(), env, user); err != nil {
			return err
		}
	}
	token, err := issueSession(r.Context(), env, user)
	if err != nil {
		return err
	}

	// A fragment does land in browser history. With no refresh-token rotation,
	// the value is a full-lifetime session (ACCESS_TOKEN_EXPIRE_MINUTES), so the
	// client must store it and replace the history entry immediately.
	frontend := strings.TrimRight(env.Cfg.FrontendURL, "/")
	http.Redirect(w, r, frontend+"/auth/oidc/callback#access_token="+token+"&token_type=bearer",
		http.StatusTemporaryRedirect)
	return nil
}

// loginWithOIDC resolves a provider identity to a local user, creating one
// only if allowed. Matching is on issuer and subject, never email alone, or
// account takeover would be a profile edit. Adopting a local account by
// address is off by default and needs a verified address.
func loginWithOIDC(ctx context.Context, env *Env, identity auth.OIDCIdentity) (store.User, bool, error) {
	user, err := env.DB.GetUserByOIDC(ctx, identity.Issuer, identity.Subject)
	if err == nil {
		if !user.IsActive {
			return store.User{}, false, errOIDCRefused
		}
		return user, false, nil
	}
	if !isNotFound(err) {
		return store.User{}, false, err
	}

	// Asked of the live provider: these are runtime settings.
	policy := env.OIDC.Settings()

	existing, err := env.DB.GetUserByEmail(ctx, identity.Email)
	if err == nil {
		switch {
		case existing.OIDCSubject != "",
			!policy.LinkExistingEmail,
			!existing.IsActive,
			!identity.EmailVerified:
			return store.User{}, false, errOIDCRefused
		}
		if err := env.DB.LinkOIDCIdentity(
			ctx, existing.ID, identity.Issuer, identity.Subject); err != nil {
			return store.User{}, false, err
		}
		existing.OIDCSubject, existing.OIDCIssuer = identity.Subject, identity.Issuer
		return existing, false, nil
	}
	if !isNotFound(err) {
		return store.User{}, false, err
	}

	if !policy.AutoRegister {
		return store.User{}, false, errOIDCRefused
	}
	if policy.RequireVerifiedEmail && !identity.EmailVerified {
		return store.User{}, false, errOIDCRefused
	}

	created := store.User{
		Email:       strings.ToLower(strings.TrimSpace(identity.Email)),
		FullName:    identity.FullName,
		OIDCSubject: identity.Subject,
		OIDCIssuer:  identity.Issuer,
		IsActive:    true,
		IsVerified:  identity.EmailVerified,
	}
	// The first account to arrive on an empty server administers it, as one
	// made on the first-run screen does.
	err = env.DB.InTx(ctx, func(tx *store.Store) error {
		first, err := tx.ClaimFirstAccount(ctx)
		if err != nil {
			return err
		}
		created.IsSuperuser = first
		return tx.CreateUser(ctx, &created)
	})
	if err != nil {
		return store.User{}, false, err
	}
	return created, true, nil
}

// createPersonalSpace is the space an account gets on creation, owned by the
// creator. The membership is accepted at creation, or the account would log in
// to nothing.
func createPersonalSpace(ctx context.Context, env *Env, user store.User) error {
	name := user.FullName
	if name == "" {
		name, _, _ = strings.Cut(user.Email, "@")
	}
	return env.DB.InTx(ctx, func(tx *store.Store) error {
		space := &store.Space{Name: name, PrimaryCurrency: env.Cfg.PrimaryCurrency}
		if err := tx.CreateSeededSpace(ctx, space); err != nil {
			return err
		}
		joined := env.now()
		return tx.CreateMembership(ctx, space.ID, &store.Membership{
			UserID:     user.ID,
			Role:       store.RoleOwner,
			InvitedAt:  &joined,
			AcceptedAt: &joined,
		})
	})
}

// issueSession stamps the login and mints the token. One column, through a
// narrow write: the user row was read before a bcrypt verify, and a whole-row
// write could undo a concurrent password change and its session cutoff.
func issueSession(ctx context.Context, env *Env, user store.User) (string, error) {
	moment := env.now()
	if err := env.DB.TouchLastLogin(ctx, user.ID, moment); err != nil {
		return "", err
	}
	return env.Tokens.Issue(user.ID)
}

// meterLogin bounds guess-and-check per client IP as resolved by the
// middleware; a forwarded header counts only from a configured trusted proxy,
// so it cannot be rotated to mint fresh buckets.
func meterLogin(env *Env, r *http.Request) error {
	cfg := env.Live()
	attempts := env.Secrets.Hit("login_attempts:"+clientKey(r), cfg.LoginAttemptWindow)
	if attempts > cfg.LoginMaxAttempts {
		return auth.ErrTooManyAttempts
	}
	return nil
}

// maxIdentifierLength is RFC 5321's longest address. The login form's
// `username` is an email address, so anything longer cannot match a row.
const maxIdentifierLength = 254

// meterLoginIdentity bounds guess-and-check against one account, whatever the
// source address. A throttle with a window, not a lock, so a third party can
// slow an attack but not lock a victim out. Keyed on a hash of the lowercased
// identifier, and an over-long one is refused before counting: the caller
// picks the key, and each entry is held for the whole window.
func meterLoginIdentity(env *Env, identifier string) error {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if identifier == "" {
		return nil
	}
	if len(identifier) > maxIdentifierLength {
		return auth.ErrInvalidCredentials
	}
	cfg := env.Live()
	attempts := env.Secrets.Hit(identityMeterKey(identifier), cfg.LoginAttemptWindow)
	if attempts > cfg.LoginMaxAttempts*10 {
		return auth.ErrTooManyAttempts
	}
	return nil
}

// identityMeterKey is the per-account counter's key: fixed size whatever the
// identifier was, and taking the folded form so casing does not split it.
func identityMeterKey(identifier string) string {
	sum := sha256.Sum256([]byte(identifier))
	return "login_attempts_id:" + hex.EncodeToString(sum[:])
}

// clientKey is the resolved client IP, falling back to the raw TCP peer, which
// a client cannot spoof.
func clientKey(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

// decodeOptionalBody accepts an absent body, for the requests whose fields are
// all optional — a passkey registration that does not name the key.
func decodeOptionalBody(r *http.Request, target any) error {
	if r.ContentLength == 0 {
		return nil
	}
	if err := decodeBody(r, target); err != nil {
		var schema *invalid
		if errors.As(err, &schema) && schema.Kind == "missing" {
			return nil
		}
		return err
	}
	return nil
}

func userResponse(user store.User) UserResponse {
	return UserResponse{
		ID:          user.ID,
		Email:       user.Email,
		FullName:    dbconv.NullText(user.FullName),
		IsActive:    user.IsActive,
		IsSuperuser: user.IsSuperuser,
		IsVerified:  user.IsVerified,
		Locale:      user.Locale,
		Theme:       user.Theme,
		PrivacyMode: user.PrivacyMode,

		SwipeLeftAction:     user.SwipeLeftAction,
		SwipeRightAction:    user.SwipeRightAction,
		AnimationDurationMs: user.AnimationDurationMs,
		ToastDurationMs:     user.ToastDurationMs,

		LastLoginAt: user.LastLoginAt,

		MustChangePassword: user.MustChangePassword,

		HasPassword: user.HashedPassword != "",
		HasTOTP:     user.TOTPSecret != "",
		HasOIDC:     user.OIDCSubject != "",
	}
}

func passkeyResponse(key auth.Passkey) PasskeyResponse {
	transports := key.Transports
	if transports == nil {
		transports = []string{}
	}
	return PasskeyResponse{
		ID:         key.ID,
		Name:       key.Name,
		CreatedAt:  key.CreatedAt,
		LastUsedAt: key.LastUsedAt,
		Transports: transports,
		RPID:       dbconv.NullText(key.RPID),
	}
}
