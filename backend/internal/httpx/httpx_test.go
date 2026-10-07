package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestGetJSONKeepsNumbersAsText(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`{"amount": 12.10}`))
	})
	var out map[string]any
	if _, err := GetJSON(context.Background(), server.Client(), server.URL, 0, &out); err != nil {
		t.Fatal(err)
	}
	if got, ok := out["amount"].(json.Number); !ok || got.String() != "12.10" {
		t.Fatalf("amount = %#v, want json.Number 12.10", out["amount"])
	}
}

func TestDoJSONStatusErrorQuotesTheAnswer(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("  upstream " + strings.Repeat("é", 400) + "\n"))
	})
	var out any
	_, err := GetJSON(context.Background(), server.Client(), server.URL, 0, &out)
	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("err = %v, want a *StatusError", err)
	}
	if status.Status != http.StatusBadGateway {
		t.Fatalf("status = %d", status.Status)
	}
	want := "upstream " + strings.Repeat("é", ExcerptRunes-len([]rune("upstream "))) + "…"
	if status.Excerpt != want {
		t.Fatalf("excerpt = %q, want %q", status.Excerpt, want)
	}
}

func TestReadRefusesAnAnswerOverTheLimit(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	})
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	if _, err := Read(server.Client(), req, 10); err != nil {
		t.Fatalf("an answer of exactly the limit: %v", err)
	}
	req, _ = http.NewRequest(http.MethodGet, server.URL, nil)
	if _, err := Read(server.Client(), req, 9); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestReadKeepsTheAnsweringURL(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/from" {
			http.Redirect(w, r, "/to", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/from", nil)
	response, err := Read(server.Client(), req, 0)
	if err != nil {
		t.Fatal(err)
	}
	if response.URL.Path != "/to" {
		t.Fatalf("URL = %s, want the redirect's target", response.URL)
	}
}

func TestRetryOnceOnAuth(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []int
		wantCalls int
		wantAuths int
		want      int
	}{
		{"accepted", []int{200}, 1, 0, 200},
		{"refused then accepted", []int{401, 200}, 2, 1, 200},
		{"forbidden then accepted", []int{403, 200}, 2, 1, 200},
		{"refused twice", []int{401, 401}, 2, 1, 401},
		{"a failure is not a refusal", []int{500}, 1, 0, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, auths := 0, 0
			response, err := RetryOnceOnAuth(context.Background(),
				func(context.Context) (Response, error) {
					calls++
					return Response{Status: tc.statuses[calls-1]}, nil
				},
				func(context.Context) error { auths++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.wantCalls || auths != tc.wantAuths || response.Status != tc.want {
				t.Fatalf("calls %d, auths %d, status %d; want %d, %d, %d",
					calls, auths, response.Status, tc.wantCalls, tc.wantAuths, tc.want)
			}
		})
	}
}

func TestRetryOnceOnAuthStopsWhenReauthFails(t *testing.T) {
	failed := errors.New("no fresh token")
	calls := 0
	_, err := RetryOnceOnAuth(context.Background(),
		func(context.Context) (Response, error) { calls++; return Response{Status: 401}, nil },
		func(context.Context) error { return failed })
	if !errors.Is(err, failed) || calls != 1 {
		t.Fatalf("err = %v after %d calls", err, calls)
	}
}
