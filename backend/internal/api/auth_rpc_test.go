package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The identity services over Connect. The REST suite (auth_test.go,
// password_test.go, firstaccount_test.go) runs over the bridge; these hold the
// procedures themselves to the same refusals.

func loginRPC(c *client, email, password string) (*agentifiv1.LoginResponse, *connect.Error) {
	return call[agentifiv1.LoginRequest, agentifiv1.LoginResponse](c,
		agentifiv1connect.AuthServiceLoginProcedure,
		&agentifiv1.LoginRequest{Email: email, Password: password})
}

func TestLoginIssuesAUsableTokenOrAChallenge(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)

	answer, err := loginRPC(c, user.Email, testPassword)
	require.Nil(t, err)
	require.NotEmpty(t, answer.GetAccessToken())
	require.Equal(t, "bearer", answer.GetTokenType())
	require.Empty(t, answer.GetMfaToken())

	c.token = answer.GetAccessToken()
	me, err := call[agentifiv1.GetCurrentUserRequest, agentifiv1.GetCurrentUserResponse](c,
		agentifiv1connect.AuthServiceGetCurrentUserProcedure, &agentifiv1.GetCurrentUserRequest{})
	require.Nil(t, err)
	require.Equal(t, user.Email, me.GetUser().GetEmail())
	require.Equal(t, user.ID.String(), me.GetUser().GetId())

	secret, _ := enrolTOTPFor(t, c, user)
	require.NotEmpty(t, secret)
	c.token = ""
	challenge, err := loginRPC(c, user.Email, testPassword)
	require.Nil(t, err)
	require.Empty(t, challenge.GetAccessToken(), "a password alone must not be a session")
	require.NotEmpty(t, challenge.GetMfaToken())
	require.Equal(t, []string{"totp", "recovery_code"}, challenge.GetMfaMethods())
}

func TestEveryLoginFailureLooksTheSame(t *testing.T) {
	c := newClient(t)
	known := makeUser(t, testPassword)
	disabled := makeUser(t, testPassword, func(u *store.User) { u.IsActive = false })
	passkeyOnly := makeUser(t, "")

	answers := map[string]bool{}
	for _, attempt := range []struct{ email, password string }{
		{known.Email, "wrong"},
		{"nobody-" + uuid.NewString() + "@example.test", testPassword},
		{disabled.Email, testPassword},
		{passkeyOnly.Email, testPassword},
	} {
		refused := c.rpc(agentifiv1connect.AuthServiceLoginProcedure, map[string]any{
			"email": attempt.email, "password": attempt.password,
		}).requireCode(connect.CodeUnauthenticated)
		answers[refused.Body.String()] = true
	}
	require.Len(t, answers, 1, "the failures are distinguishable: %v", answers)
}

func TestLoginSharesTheTokenGrantsMeter(t *testing.T) {
	// One bucket per client address whichever door is used, or the procedure
	// would double what an attacker may guess.
	c := newClient(t)
	c.env.Cfg.LoginMaxAttempts = 3
	nobody := func() string { return "nobody-" + uuid.NewString() + "@example.test" }

	login(c, nobody(), "wrong").requireStatus(http.StatusUnauthorized)
	login(c, nobody(), "wrong").requireStatus(http.StatusUnauthorized)
	attempt := func() *response {
		return c.rpc(agentifiv1connect.AuthServiceLoginProcedure, map[string]any{"email": nobody(), "password": "wrong"})
	}
	attempt().requireCode(connect.CodeUnauthenticated)
	attempt().requireCode(connect.CodeResourceExhausted).requireStatus(http.StatusTooManyRequests)
	login(c, nobody(), "wrong").requireStatus(http.StatusTooManyRequests)
}

func TestLoginBoundsGuessingAgainstOneAccount(t *testing.T) {
	c := newClient(t)
	c.env.Cfg.LoginMaxAttempts = 1
	c.env.Cfg.TrustedProxies = []string{"10.0.0.0/8"}
	c.handler = RouterFor(c.env)

	// Over the per-account ceiling (LoginMaxAttempts*10) from fresh addresses.
	trippedAt := 0
	for i := range 15 {
		r := httptest.NewRequest(http.MethodPost, agentifiv1connect.AuthServiceLoginProcedure,
			strings.NewReader(`{"email": "victim@example.test", "password": "wrong"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		rec := httptest.NewRecorder()
		c.handler.ServeHTTP(rec, r)
		if strings.Contains(rec.Body.String(), `"resource_exhausted"`) {
			trippedAt = i + 1
			break
		}
	}
	require.NotZero(t, trippedAt, "guessing one account from rotating addresses must be throttled")
	require.Greater(t, trippedAt, c.env.Cfg.LoginMaxAttempts+1)
}

func TestTheIdentityProceduresRefuseWithoutASession(t *testing.T) {
	c := newClient(t)
	_, err := call[agentifiv1.GetCurrentUserRequest, agentifiv1.GetCurrentUserResponse](c,
		agentifiv1connect.AuthServiceGetCurrentUserProcedure, &agentifiv1.GetCurrentUserRequest{})
	require.Equal(t, connect.CodeUnauthenticated, err.Code())
	_, err = call[agentifiv1.ListPasskeysRequest, agentifiv1.ListPasskeysResponse](c,
		agentifiv1connect.PasskeyServiceListPasskeysProcedure, &agentifiv1.ListPasskeysRequest{})
	require.Equal(t, connect.CodeUnauthenticated, err.Code())
	_, err = call[agentifiv1.GetTotpStatusRequest, agentifiv1.GetTotpStatusResponse](c,
		agentifiv1connect.TotpServiceGetTotpStatusProcedure, &agentifiv1.GetTotpStatusRequest{})
	require.Equal(t, connect.CodeUnauthenticated, err.Code())
}

func TestAnAccountOwingAPasswordChangeReachesOnlyTheExemptProcedures(t *testing.T) {
	c := newClient(t)
	c.as(mustChangeUser(t))

	me, err := call[agentifiv1.GetCurrentUserRequest, agentifiv1.GetCurrentUserResponse](c,
		agentifiv1connect.AuthServiceGetCurrentUserProcedure, &agentifiv1.GetCurrentUserRequest{})
	require.Nil(t, err)
	require.True(t, me.GetUser().GetMustChangePassword())

	_, err = call[agentifiv1.ListPasskeysRequest, agentifiv1.ListPasskeysResponse](c,
		agentifiv1connect.PasskeyServiceListPasskeysProcedure, &agentifiv1.ListPasskeysRequest{})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, "password_change_required", problemIn(t, err).GetCode())
}

func TestTheProtoExemptionsMatchTheRegistrys(t *testing.T) {
	// passwordChangeExempt decides for a REST route and the method's option for
	// a procedure; while the bridge serves both, neither may be edited alone.
	for _, route := range RegisteredRoutes() {
		if route.procedure == "" || route.kind == kindPublic {
			continue
		}
		require.Equal(t, passwordChangeExempt(route), procedures[route.procedure].exempt,
			"%s %s and %s disagree about the password-change exemption",
			route.Method, route.Path(), route.procedure)
	}
}

func TestUpdateCurrentUserLeavesSetsAndClears(t *testing.T) {
	c := newClient(t)
	c.as(makeUser(t, testPassword))
	update := func(req *agentifiv1.UpdateCurrentUserRequest) *agentifiv1.CurrentUser {
		t.Helper()
		res, err := call[agentifiv1.UpdateCurrentUserRequest, agentifiv1.UpdateCurrentUserResponse](c,
			agentifiv1connect.AuthServiceUpdateCurrentUserProcedure, req)
		require.Nil(t, err)
		return res.GetUser()
	}
	dark, zero := "dark", int32(0)

	set := update(&agentifiv1.UpdateCurrentUserRequest{Theme: &dark, AnimationDurationMs: &zero})
	require.Equal(t, "dark", set.GetTheme())
	require.Equal(t, int32(0), set.GetAnimationDurationMs())

	// Not in the mask: left alone.
	left := update(&agentifiv1.UpdateCurrentUserRequest{
		FullName:   proto.String("Someone"),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"full_name"}},
	})
	require.Equal(t, "dark", left.GetTheme())
	require.Equal(t, "Someone", left.GetFullName())

	// In the mask and unset: back to the default.
	cleared := update(&agentifiv1.UpdateCurrentUserRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"theme", "animation_duration_ms", "full_name"}},
	})
	require.Equal(t, "system", cleared.GetTheme())
	require.Equal(t, int32(160), cleared.GetAnimationDurationMs())
	require.Nil(t, cleared.FullName)

	refused := c.rpc(agentifiv1connect.AuthServiceUpdateCurrentUserProcedure, `{"theme": "midnight"}`).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
	require.Equal(t, []string{"body", "theme"}, refused.problem().fields[0].Loc)
}

func TestLogoutRetiresTheCallingToken(t *testing.T) {
	c := newClient(t)
	c.as(makeUser(t, testPassword))
	_, err := call[agentifiv1.LogoutRequest, agentifiv1.LogoutResponse](c,
		agentifiv1connect.AuthServiceLogoutProcedure, &agentifiv1.LogoutRequest{})
	require.Nil(t, err)
	_, err = call[agentifiv1.GetCurrentUserRequest, agentifiv1.GetCurrentUserResponse](c,
		agentifiv1connect.AuthServiceGetCurrentUserProcedure, &agentifiv1.GetCurrentUserRequest{})
	require.Equal(t, connect.CodeUnauthenticated, err.Code())
}

func TestDeletingSomebodyElsesPasskeyIsNotFound(t *testing.T) {
	c := newClient(t)
	c.as(makeUser(t, testPassword))
	for _, id := range []string{uuid.NewString(), "not-an-id"} {
		_, err := call[agentifiv1.DeletePasskeyRequest, agentifiv1.DeletePasskeyResponse](c,
			agentifiv1connect.PasskeyServiceDeletePasskeyProcedure, &agentifiv1.DeletePasskeyRequest{PasskeyId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
	}
}

func TestAPasskeyCeremonyReadsTheCallersOrigin(t *testing.T) {
	// The relying party is the origin the call came from, so the procedure has
	// to see the request's headers; the bridge carries the options as the JSON
	// object the REST wire did.
	c := newClient(t)
	send := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://localhost:5173")
		if strings.HasPrefix(path, "/agentifi.v1.") {
			r.Header.Set("Connect-Protocol-Version", "1")
		}
		rec := httptest.NewRecorder()
		c.handler.ServeHTTP(rec, r)
		return rec
	}

	native := send(agentifiv1connect.PasskeyServiceStartPasskeyAuthenticationProcedure, `{}`)
	require.Equal(t, http.StatusOK, native.Code, native.Body.String())
	var answer struct {
		ChallengeID string `json:"challenge_id"`
		OptionsJSON string `json:"options_json"`
	}
	require.NoError(t, json.Unmarshal(native.Body.Bytes(), &answer))
	require.NotEmpty(t, answer.ChallengeID)
	var options map[string]any
	require.NoError(t, json.Unmarshal([]byte(answer.OptionsJSON), &options))
	require.Equal(t, "localhost", options["publicKey"].(map[string]any)["rpId"])

	bridged := send("/auth/passkeys/authenticate/options", "")
	require.Equal(t, http.StatusOK, bridged.Code, bridged.Body.String())
	var rest map[string]any
	require.NoError(t, json.Unmarshal(bridged.Body.Bytes(), &rest))
	require.Contains(t, rest, "options")
	require.NotContains(t, rest, "options_json")
	require.Equal(t, "localhost", rest["options"].(map[string]any)["publicKey"].(map[string]any)["rpId"])

	// An assertion the server never issued is refused, over either wire.
	refused := send("/auth/passkeys/authenticate/verify",
		`{"challenge_id": "`+answer.ChallengeID+`", "credential": {"id": "x", "type": "public-key"}}`)
	require.NotEqual(t, http.StatusOK, refused.Code, refused.Body.String())
	_, err := call[agentifiv1.FinishPasskeyAuthenticationRequest, agentifiv1.FinishPasskeyAuthenticationResponse](c,
		agentifiv1connect.PasskeyServiceFinishPasskeyAuthenticationProcedure,
		&agentifiv1.FinishPasskeyAuthenticationRequest{ChallengeId: "unknown", CredentialJson: `{}`})
	require.NotNil(t, err)
}

func TestTheTotpStatusIsTheCallersOwn(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)
	enrolTOTPFor(t, c, user)

	status, err := call[agentifiv1.GetTotpStatusRequest, agentifiv1.GetTotpStatusResponse](c,
		agentifiv1connect.TotpServiceGetTotpStatusProcedure, &agentifiv1.GetTotpStatusRequest{})
	require.Nil(t, err)
	require.True(t, status.GetEnabled())
	require.Positive(t, status.GetRecoveryCodesRemaining())

	c.as(makeUser(t, testPassword))
	other, err := call[agentifiv1.GetTotpStatusRequest, agentifiv1.GetTotpStatusResponse](c,
		agentifiv1connect.TotpServiceGetTotpStatusProcedure, &agentifiv1.GetTotpStatusRequest{})
	require.Nil(t, err)
	require.False(t, other.GetEnabled())
}
