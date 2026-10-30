package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/stretchr/testify/require"
)

func call[Req, Res any](c *client, procedure string, req *Req) (*Res, *connect.Error) {
	c.t.Helper()
	transport := &http.Client{Transport: handlerTransport{c.handler}}
	rpc := connect.NewClient[Req, Res](transport, "http://agentifi.test"+procedure, connect.WithProtoJSON())
	request := connect.NewRequest(req)
	if c.token != "" {
		request.Header().Set("Authorization", "Bearer "+c.token)
	}
	if c.spaceID != "" {
		request.Header().Set("X-Space-Id", c.spaceID)
	}
	res, err := rpc.CallUnary(c.t.Context(), request)
	if err != nil {
		var refused *connect.Error
		require.ErrorAs(c.t, err, &refused)
		return nil, refused
	}
	return res.Msg, nil
}

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, r)
	return recorder.Result(), nil
}

// problemIn is the Problem detail a Connect error carries.
func problemIn(t *testing.T, err *connect.Error) *agentifiv1.Problem {
	t.Helper()
	require.NotNil(t, err)
	for _, detail := range err.Details() {
		value, decodeErr := detail.Value()
		if problem, ok := value.(*agentifiv1.Problem); ok && decodeErr == nil {
			return problem
		}
	}
	t.Fatalf("%v carries no Problem", err)
	return nil
}

type rpcResponse struct{ *response }

func (c *client) rpc(path string, body any) *rpcResponse {
	c.t.Helper()
	var res *response
	if raw, ok := body.(string); ok {
		res = c.raw(http.MethodPost, path, raw)
	} else {
		res = c.post(path, body)
	}
	return &rpcResponse{res}
}

func (r *rpcResponse) requireCode(want connect.Code) *rpcResponse {
	r.t.Helper()
	var wire struct {
		Code string `json:"code"`
	}
	require.NoError(r.t, json.Unmarshal(r.Body.Bytes(), &wire))
	require.Equal(r.t, want.String(), wire.Code, "body: %s", r.Body.String())
	return r
}

func (r *rpcResponse) requireStatus(want int) *rpcResponse {
	r.t.Helper()
	if want >= 200 && want < 300 {
		require.Equal(r.t, http.StatusOK, r.Code, "body: %s", r.Body.String())
	} else {
		require.NotEqual(r.t, http.StatusOK, r.Code)
		require.Equal(r.t, want, problemFromWire(r.Code, r.Body.Bytes()).status, "body: %s", r.Body.String())
	}
	return r
}

func (r *rpcResponse) json() map[string]any {
	r.t.Helper()
	if r.Code == http.StatusOK {
		return r.response.json()
	}
	raw, err := json.Marshal(problemFromWire(r.Code, r.Body.Bytes()).body())
	require.NoError(r.t, err)
	var body map[string]any
	require.NoError(r.t, json.Unmarshal(raw, &body))
	return body
}

func TestConnectProblemFields(t *testing.T) {
	t.Parallel()
	coded := connectError(t.Context(), "/test", errConflictCode("invented_conflict", "cannot change this row"))
	require.Equal(t, connect.CodeFailedPrecondition, coded.Code())
	require.Equal(t, "invented_conflict", problemIn(t, coded).GetCode())
	invalid := connectError(t.Context(), "/test", errInvalid("missing", []string{"body", "name"}, "name is required"))
	require.Equal(t, connect.CodeInvalidArgument, invalid.Code())
	p := problemIn(t, invalid)
	require.Equal(t, int32(http.StatusUnprocessableEntity), p.GetStatus())
	require.Len(t, p.GetFields(), 1)
	require.Equal(t, []string{"body", "name"}, p.GetFields()[0].GetLoc())
	require.Equal(t, "missing", p.GetFields()[0].GetType())
}

func TestConnectCORSOrigin(t *testing.T) {
	t.Parallel()
	env := NewEnv(testConfig(), nil)
	handler := RouterFor(env)
	for _, origin := range []string{env.Cfg.FrontendURL, "https://other.example.invalid"} {
		req := httptest.NewRequest(http.MethodOptions, "/agentifi.v1.TagService/ListTags", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Headers", "connect-protocol-version,connect-timeout-ms,authorization,x-space-id,content-type")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if origin == env.Cfg.FrontendURL {
			require.Equal(t, http.StatusNoContent, res.Code)
			require.Contains(t, res.Header().Get("Access-Control-Allow-Headers"), "Connect-Protocol-Version")
			require.Contains(t, res.Header().Get("Access-Control-Allow-Headers"), "Connect-Timeout-Ms")
		} else {
			require.Empty(t, res.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}
