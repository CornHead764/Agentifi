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

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5/middleware"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
//
// The OAuth2 form grant and the OIDC redirects stay plain HTTP; the rest are
// AuthService, PasskeyService and TotpService.

func init() {
	RegisterIdentity(Resource{Prefix: "/auth", Routes: func(rt *Routes) {
		rt.Public(http.MethodPost, "/token", issueToken)
		rt.Public(http.MethodGet, "/oidc/login", oidcLogin)
		rt.Public(http.MethodGet, "/oidc/callback", oidcCallback)
	}})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewAuthServiceHandler(authService{env}, opts...)
	})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewPasskeyServiceHandler(passkeyService{env}, opts...)
	})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewTotpServiceHandler(totpService{env}, opts...)
	})
}

type (
	authService    struct{ env *Env }
	passkeyService struct{ env *Env }
	totpService    struct{ env *Env }
)

// errOIDCRefused is every reason an OIDC identity may not become a session,
// said the same way, so the reason cannot reveal whether the address is
// registered.
var errOIDCRefused = errors.New("api: OIDC login is not available for this account")

// errPasswordChangeRequired is the 403 every route except passwordChangeExempt
// gives a caller whose password was set for them.
var errPasswordChangeRequired = errors.New("api: this account must set a new password")

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

// callRequest rebuilds the request a procedure was called with, for the
// helpers that read one: the login meter, the WebAuthn origin, the bearer.
func callRequest(ctx context.Context) *http.Request {
	header := http.Header{}
	if info, ok := connect.CallInfoForHandlerContext(ctx); ok {
		header = info.RequestHeader()
	}
	return requestFrom(ctx, header)
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
	answer, err := passwordLogin(r.Context(), env, r.PostFormValue("username"), r.PostFormValue("password"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, answer)
}

func (s authService) Login(ctx context.Context, req *agentifiv1.LoginRequest) (*agentifiv1.LoginResponse, error) {
	if err := meterLogin(s.env, callRequest(ctx)); err != nil {
		return nil, err
	}
	answer, err := passwordLogin(ctx, s.env, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.LoginResponse{
		AccessToken: answer.AccessToken, TokenType: answer.TokenType,
		MfaToken: answer.MfaToken, MfaMethods: answer.MfaMethods,
	}, nil
}

// passwordLogin is a first factor once the caller's address has been metered.
func passwordLogin(ctx context.Context, env *Env, email, password string) (LoginResponse, error) {
	// Bound guessing against this one account too, so a rotated source address
	// cannot make guesses against it free.
	if err := meterLoginIdentity(env, email); err != nil {
		return LoginResponse{}, err
	}
	user, err := auth.AuthenticatePassword(ctx, env.DB, email, password)
	if err != nil {
		return LoginResponse{}, err
	}

	if user.TOTPSecret == "" {
		token, err := issueSession(ctx, env, user)
		if err != nil {
			return LoginResponse{}, err
		}
		return LoginResponse{AccessToken: token, TokenType: "bearer", MfaMethods: []string{}}, nil
	}

	handle, err := env.Pending.Issue(user.ID)
	if err != nil {
		return LoginResponse{}, err
	}
	return LoginResponse{
		TokenType:  "bearer",
		MfaToken:   handle,
		MfaMethods: []string{"totp", "recovery_code"},
	}, nil
}

// VerifySecondFactor tries the code as a TOTP and as a recovery code, so the
// client need not say which.
func (s authService) VerifySecondFactor(ctx context.Context, req *agentifiv1.VerifySecondFactorRequest) (*agentifiv1.VerifySecondFactorResponse, error) {
	env := s.env
	if err := meterLogin(env, callRequest(ctx)); err != nil {
		return nil, err
	}

	userID, err := env.Pending.User(req.GetMfaToken())
	if err != nil {
		return nil, err
	}
	user, err := env.DB.GetUser(ctx, userID)
	if err != nil {
		if isNotFound(err) {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, err
	}
	if !user.IsActive || user.TOTPSecret == "" {
		return nil, auth.ErrInvalidCredentials
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return nil, err
	}
	seed, err := sealed.OpenTOTPSecret(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	accepted, err := env.TOTP.SubmitSecondFactor(ctx, env.Codes, user.ID, seed, req.GetCode())
	if err != nil {
		return nil, err
	}
	if !accepted {
		return nil, auth.ErrInvalidCode
	}

	// A wrong code leaves the challenge alive — a typo should not cost the
	// user their password entry — so it is spent only once one has verified.
	env.Pending.Spend(req.GetMfaToken())
	token, err := issueSession(ctx, env, user)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.VerifySecondFactorResponse{AccessToken: token, TokenType: "bearer"}, nil
}

func (s authService) GetCurrentUser(ctx context.Context, _ *agentifiv1.GetCurrentUserRequest) (*agentifiv1.GetCurrentUserResponse, error) {
	return &agentifiv1.GetCurrentUserResponse{User: userProto(userFrom(ctx))}, nil
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

// UpdateCurrentUser changes what a person may change about themselves here:
// the display name and presentation preferences. Absent leaves a field,
// cleared restores its default.
func (s authService) UpdateCurrentUser(ctx context.Context, req *agentifiv1.UpdateCurrentUserRequest) (*agentifiv1.UpdateCurrentUserResponse, error) {
	user := userFrom(ctx)
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	fullName := optOf(mask, "full_name", req.FullName)
	switch {
	case fullName.Cleared():
		user.FullName = ""
	case fullName.Present():
		// A name of spaces is no name.
		user.FullName = strings.TrimSpace(fullName.Value)
	}
	locale := optOf(mask, "locale", req.Locale)
	switch {
	case locale.Cleared():
		user.Locale = defaultLocale
	case locale.Present():
		value := strings.TrimSpace(locale.Value)
		if value == "" {
			value = defaultLocale
		}
		if !isLanguageTag(value) {
			return nil, errInvalid("invalid", []string{"body", "locale"},
				"%q is not a language tag", value)
		}
		user.Locale = value
	}
	theme := optOf(mask, "theme", req.Theme)
	switch {
	case theme.Cleared():
		user.Theme = defaultTheme
	case theme.Present():
		if !themes[theme.Value] {
			return nil, errInvalid("enum", []string{"body", "theme"},
				"%q is not a theme", theme.Value)
		}
		user.Theme = theme.Value
	}
	privacy := optOf(mask, "privacy_mode", req.PrivacyMode)
	switch {
	case privacy.Cleared():
		user.PrivacyMode = false
	case privacy.Present():
		user.PrivacyMode = privacy.Value
	}
	left, err := readSwipeAction(optOf(mask, "swipe_left_action", req.SwipeLeftAction),
		user.SwipeLeftAction, defaultSwipeLeft, "swipe_left_action")
	if err != nil {
		return nil, err
	}
	user.SwipeLeftAction = left
	right, err := readSwipeAction(optOf(mask, "swipe_right_action", req.SwipeRightAction),
		user.SwipeRightAction, defaultSwipeRight, "swipe_right_action")
	if err != nil {
		return nil, err
	}
	user.SwipeRightAction = right
	animation, err := readAnimationDuration(optOf(mask, "animation_duration_ms", req.AnimationDurationMs),
		user.AnimationDurationMs)
	if err != nil {
		return nil, err
	}
	user.AnimationDurationMs = animation
	toastMs, err := readToastDuration(optOf(mask, "toast_duration_ms", req.ToastDurationMs), user.ToastDurationMs)
	if err != nil {
		return nil, err
	}
	user.ToastDurationMs = toastMs

	// The narrow write: rewriting the whole row read at the start of the
	// request would undo a password change that landed in between.
	if err := s.env.DB.UpdateUserProfile(ctx, user.ID, store.UserProfile{
		FullName:            user.FullName,
		Locale:              user.Locale,
		Theme:               user.Theme,
		PrivacyMode:         user.PrivacyMode,
		SwipeLeftAction:     user.SwipeLeftAction,
		SwipeRightAction:    user.SwipeRightAction,
		AnimationDurationMs: user.AnimationDurationMs,
		ToastDurationMs:     user.ToastDurationMs,
	}); err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateCurrentUserResponse{User: userProto(user)}, nil
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
func readAnimationDuration(field Opt[int32], current int) (int, error) {
	switch {
	case field.Cleared():
		return defaultAnimationMs, nil
	case field.Present():
		if field.Value < 0 || field.Value > maxAnimationMs {
			return 0, errInvalid("out_of_range", []string{"body", "animation_duration_ms"},
				"%d is not a duration between 0 and %d milliseconds", field.Value, maxAnimationMs)
		}
		return int(field.Value), nil
	}
	return current, nil
}

// readToastDuration resolves the toast duration: absent leaves it, null
// restores the default, out of range is refused.
func readToastDuration(field Opt[int32], current int) (int, error) {
	switch {
	case field.Cleared():
		return defaultToastMs, nil
	case field.Present():
		if field.Value < minToastMs || field.Value > maxToastMs {
			return 0, errInvalid("out_of_range", []string{"body", "toast_duration_ms"},
				"%d is not a duration between %d and %d milliseconds",
				field.Value, minToastMs, maxToastMs)
		}
		return int(field.Value), nil
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

// Logout retires the presented token. A JWT the server forgot is still valid,
// so signing out leaves a mark that lives as long as the token would have.
func (s authService) Logout(ctx context.Context, _ *agentifiv1.LogoutRequest) (*agentifiv1.LogoutResponse, error) {
	if token := bearerToken(callRequest(ctx)); token != "" {
		if err := s.env.Tokens.Revoke(ctx, token); err != nil {
			return nil, err
		}
	}
	return &agentifiv1.LogoutResponse{}, nil
}

// --- Password ----------------------------------------------------------------

// ChangePassword replaces the caller's own password. The current password is
// required when there is one, since a session alone is what a borrowed laptop
// has. An account with no password sets one on the session alone.
//
// Changing a password ends every session including the caller's
// (store.User.SessionsValidFrom); the token in the answer is minted after the
// cutoff.
func (s authService) ChangePassword(ctx context.Context, req *agentifiv1.ChangePasswordRequest) (*agentifiv1.ChangePasswordResponse, error) {
	env, user := s.env, userFrom(ctx)
	if err := meterLogin(env, callRequest(ctx)); err != nil {
		return nil, err
	}

	if user.HashedPassword != "" {
		if !auth.VerifyPassword(req.GetCurrentPassword(), user.HashedPassword) {
			// Not a 401: the session is valid, and a client that signs out on
			// 401 would log the user out for a typo.
			return nil, errBadRequest("The current password is incorrect")
		}
	} else {
		// Nothing to compare against, but the time is spent anyway so that
		// "this account has no password" is not readable from the clock.
		auth.DummyVerify()
	}

	if err := auth.ValidatePassword(req.GetNewPassword()); err != nil {
		return nil, errBadRequest("%s", strings.TrimPrefix(err.Error(), "auth: "))
	}
	if auth.VerifyPassword(req.GetNewPassword(), user.HashedPassword) {
		return nil, errBadRequest("The new password is the same as the current one")
	}

	hashed, err := auth.HashPassword(req.GetNewPassword())
	if err != nil {
		return nil, err
	}

	// The next second: `iat` is a Unix second, so a cutoff inside the current
	// second would let a token issued moments ago survive.
	cutoff := env.now().UTC().Truncate(time.Second).Add(time.Second)
	// Only these columns: the row loaded before the bcrypt verify may be stale.
	if err := env.DB.SetUserPassword(ctx, user.ID, hashed, false, cutoff); err != nil {
		return nil, err
	}

	// Stamped at the cutoff so it is the one token that survives it.
	token, err := env.Tokens.IssueAt(user.ID, cutoff, env.Cfg.AccessTokenExpiry)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ChangePasswordResponse{AccessToken: token, TokenType: "bearer"}, nil
}

// --- Passkeys ----------------------------------------------------------------

func (s passkeyService) ListPasskeys(ctx context.Context, _ *agentifiv1.ListPasskeysRequest) (*agentifiv1.ListPasskeysResponse, error) {
	keys, err := s.env.Keys.ListPasskeys(ctx, userFrom(ctx).ID)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListPasskeysResponse{Passkeys: make([]*agentifiv1.Passkey, 0, len(keys))}
	for _, key := range keys {
		out.Passkeys = append(out.Passkeys, passkeyProto(key))
	}
	return out, nil
}

func (s passkeyService) StartPasskeyRegistration(ctx context.Context, req *agentifiv1.StartPasskeyRegistrationRequest) (*agentifiv1.StartPasskeyRegistrationResponse, error) {
	user := userFrom(ctx)
	wctx, err := s.env.Passkeys.ContextForRequest(callRequest(ctx))
	if err != nil {
		return nil, err
	}
	handle, options, err := s.env.Passkeys.BeginRegistration(
		ctx, wctx, user.ID, user.Email, req.GetName(), s.env.Keys)
	if err != nil {
		return nil, err
	}
	text, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.StartPasskeyRegistrationResponse{ChallengeId: handle, OptionsJson: string(text)}, nil
}

func (s passkeyService) FinishPasskeyRegistration(ctx context.Context, req *agentifiv1.FinishPasskeyRegistrationRequest) (*agentifiv1.FinishPasskeyRegistrationResponse, error) {
	key, err := s.env.Passkeys.FinishRegistration(ctx, userFrom(ctx).ID,
		req.GetChallengeId(), []byte(req.GetCredentialJson()), req.GetName(), s.env.Keys)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.FinishPasskeyRegistrationResponse{Passkey: passkeyProto(key)}, nil
}

func (s passkeyService) DeletePasskey(ctx context.Context, req *agentifiv1.DeletePasskeyRequest) (*agentifiv1.DeletePasskeyResponse, error) {
	id, err := idFrom(req.GetPasskeyId(), "Passkey")
	if err != nil {
		return nil, err
	}
	removed, err := s.env.Keys.DeletePasskey(ctx, userFrom(ctx).ID, id)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, errNotFound("Passkey")
	}
	return &agentifiv1.DeletePasskeyResponse{}, nil
}

// StartPasskeyAuthentication is unauthenticated and identity-free by design:
// no email in, no credential list out.
func (s passkeyService) StartPasskeyAuthentication(ctx context.Context, _ *agentifiv1.StartPasskeyAuthenticationRequest) (*agentifiv1.StartPasskeyAuthenticationResponse, error) {
	r := callRequest(ctx)
	if err := meterLogin(s.env, r); err != nil {
		return nil, err
	}
	wctx, err := s.env.Passkeys.ContextForRequest(r)
	if err != nil {
		return nil, err
	}
	handle, options, err := s.env.Passkeys.BeginAuthentication(wctx)
	if err != nil {
		return nil, err
	}
	text, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.StartPasskeyAuthenticationResponse{ChallengeId: handle, OptionsJson: string(text)}, nil
}

func (s passkeyService) FinishPasskeyAuthentication(ctx context.Context, req *agentifiv1.FinishPasskeyAuthenticationRequest) (*agentifiv1.FinishPasskeyAuthenticationResponse, error) {
	env := s.env
	if err := meterLogin(env, callRequest(ctx)); err != nil {
		return nil, err
	}
	key, err := env.Passkeys.FinishAuthentication(ctx, req.GetChallengeId(), []byte(req.GetCredentialJson()), env.Keys)
	if err != nil {
		return nil, err
	}
	user, err := env.DB.GetUser(ctx, key.UserID)
	if err != nil {
		if isNotFound(err) {
			return nil, auth.ErrInvalidPasskey
		}
		return nil, err
	}
	if !user.IsActive {
		return nil, auth.ErrInvalidPasskey
	}
	token, err := issueSession(ctx, env, user)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.FinishPasskeyAuthenticationResponse{AccessToken: token, TokenType: "bearer"}, nil
}

// --- TOTP --------------------------------------------------------------------

func (s totpService) GetTotpStatus(ctx context.Context, _ *agentifiv1.GetTotpStatusRequest) (*agentifiv1.GetTotpStatusResponse, error) {
	user := userFrom(ctx)
	remaining, err := s.env.TOTP.RemainingRecoveryCodes(ctx, s.env.Codes, user.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetTotpStatusResponse{
		Enabled:                user.TOTPSecret != "",
		RecoveryCodesRemaining: int32(remaining),
	}, nil
}

// EnrolTotp starts enrolment. The secret is not stored until a code confirms
// it, or closing the tab would lock the account out.
func (s totpService) EnrolTotp(ctx context.Context, _ *agentifiv1.EnrolTotpRequest) (*agentifiv1.EnrolTotpResponse, error) {
	user := userFrom(ctx)
	if user.TOTPSecret != "" {
		return nil, errBadRequest("Two-factor authentication is already on")
	}
	secret, uri, err := s.env.TOTP.BeginEnrolment(user.ID, user.Email)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.EnrolTotpResponse{Secret: secret, OtpauthUri: uri}, nil
}

// ConfirmTotp hands back the recovery codes, once. They are stored as
// digests, so this is the only time the plaintext exists.
func (s totpService) ConfirmTotp(ctx context.Context, req *agentifiv1.ConfirmTotpRequest) (*agentifiv1.ConfirmTotpResponse, error) {
	env, user := s.env, userFrom(ctx)
	pending, held := env.TOTP.TakePendingSecret(user.ID)
	if !held {
		return nil, auth.ErrNoPendingEnrolment
	}
	accepted, err := env.TOTP.VerifyCode(user.ID, pending, req.GetCode())
	if err != nil {
		// A lockout is not a failed enrolment either: the attempt window
		// clears on its own, and the secret has to still be here when it does.
		env.TOTP.HoldPendingSecret(user.ID, pending)
		return nil, err
	}
	if !accepted {
		// Put it back: one mistyped digit should not restart the enrolment.
		env.TOTP.HoldPendingSecret(user.ID, pending)
		return nil, auth.ErrInvalidCode
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return nil, err
	}
	if err := sealed.SetTOTPSecret(ctx, user.ID, pending); err != nil {
		return nil, err
	}
	codes, err := env.TOTP.IssueRecoveryCodes(ctx, env.Codes, user.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ConfirmTotpResponse{RecoveryCodes: codes}, nil
}

// DisableTotp turns the second factor off, which requires still holding it. A
// recovery code counts: the phone being gone is exactly when this is needed.
func (s totpService) DisableTotp(ctx context.Context, req *agentifiv1.DisableTotpRequest) (*agentifiv1.DisableTotpResponse, error) {
	env, user := s.env, userFrom(ctx)
	if user.TOTPSecret == "" {
		return nil, errBadRequest("Two-factor authentication is not on")
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return nil, err
	}
	seed, err := sealed.OpenTOTPSecret(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	accepted, err := env.TOTP.SubmitSecondFactor(ctx, env.Codes, user.ID, seed, req.GetCode())
	if err != nil {
		return nil, err
	}
	if !accepted {
		return nil, auth.ErrInvalidCode
	}

	if err := sealed.SetTOTPSecret(ctx, user.ID, ""); err != nil {
		return nil, err
	}
	if err := env.TOTP.DiscardRecoveryCodes(ctx, env.Codes, user.ID); err != nil {
		return nil, err
	}
	env.TOTP.CancelEnrolment(user.ID)
	return &agentifiv1.DisableTotpResponse{}, nil
}

// --- OIDC --------------------------------------------------------------------

// GetOidcConfig is what the login screen needs to decide whether to draw the
// provider button.
func (s authService) GetOidcConfig(context.Context, *agentifiv1.GetOidcConfigRequest) (*agentifiv1.GetOidcConfigResponse, error) {
	// From the live provider, so enabling OIDC from the admin screen needs no
	// restart.
	settings := s.env.OIDC.Settings()
	return &agentifiv1.GetOidcConfigResponse{
		Enabled:      settings.IsConfigured(),
		ProviderName: settings.ProviderName,
	}, nil
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

func userProto(user store.User) *agentifiv1.CurrentUser {
	out := &agentifiv1.CurrentUser{
		Id:          user.ID.String(),
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
		AnimationDurationMs: int32(user.AnimationDurationMs),
		ToastDurationMs:     int32(user.ToastDurationMs),

		MustChangePassword: user.MustChangePassword,

		HasPassword: user.HashedPassword != "",
		HasTotp:     user.TOTPSecret != "",
		HasOidc:     user.OIDCSubject != "",
	}
	if user.LastLoginAt != nil {
		out.LastLoginAt = timestamppb.New(*user.LastLoginAt)
	}
	return out
}

func passkeyProto(key auth.Passkey) *agentifiv1.Passkey {
	out := &agentifiv1.Passkey{
		Id:         key.ID.String(),
		Name:       key.Name,
		CreatedAt:  timestamppb.New(key.CreatedAt),
		Transports: key.Transports,
		RpId:       dbconv.NullText(key.RPID),
	}
	if key.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*key.LastUsedAt)
	}
	return out
}
