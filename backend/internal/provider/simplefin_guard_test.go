package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A setup token is a URL whoever adds a connection chooses, so the claim must
// not be a way to make this server call into its own network.

func TestAClaimToAPlainHTTPAddressIsRefusedBeforeAnythingIsSent(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	_, err := (&SimpleFin{}).Connect(context.Background(), encodeSetupToken(server.URL+"/claim/x"))

	require.ErrorIs(t, err, ErrSimpleFINAddressRefused)
	require.False(t, called)
}

func TestAClaimToANonPublicAddressIsRefusedAtConnectTime(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.8", "169.254.169.254", "[::1]", "localhost"} {
		t.Run(host, func(t *testing.T) {
			_, err := (&SimpleFin{}).Connect(context.Background(),
				encodeSetupToken("https://"+host+":9/simplefin/claim/x"))
			require.Error(t, err)
			require.Contains(t, err.Error(), ErrSimpleFINAddressRefused.Error())
		})
	}
}

func TestAFailedClaimNamesTheStatusAndNeverEchoesTheBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "internal service banner: do not echo")
	}))
	defer server.Close()

	bank := &SimpleFin{HTTPClient: server.Client(), AllowPrivate: true}
	_, err := bank.Connect(context.Background(), encodeSetupToken(server.URL+"/claim/x"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "502")
	require.False(t, strings.Contains(err.Error(), "banner"))
}

func TestAClaimMayNotHandBackAnAccessURLThatIsNotHTTPS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "http://demo:secret@bridge.example.test/simplefin")
	}))
	defer server.Close()

	// The TLS test server is on loopback, so only its client may reach it; the
	// access URL is still checked.
	bank := &SimpleFin{HTTPClient: server.Client()}
	_, err := bank.Connect(context.Background(), encodeSetupToken(server.URL+"/claim/x"))

	require.ErrorIs(t, err, ErrSimpleFINAddressRefused)
}
