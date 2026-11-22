package egress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// proxiedClient is a client that sends everything through p, trusting the
// invented server's certificate.
func proxiedClient(t *testing.T, p *Proxy, server *httptest.Server) *http.Client {
	t.Helper()
	address, err := url.Parse(p.URL())
	require.NoError(t, err)
	transport := &http.Transport{Proxy: http.ProxyURL(address)}
	if server.TLS != nil {
		transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	}
	return &http.Client{Transport: transport}
}

func counting(hits *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "inside")
	})
}

// The server stands on loopback, which is the network the proxy keeps a page
// from: the request must fail and the server must never hear it.
func TestTheProxyRefusesATunnelToALoopbackAddress(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(counting(&hits))
	defer server.Close()
	p, err := StartProxy(nil)
	require.NoError(t, err)
	defer p.Close()

	_, err = proxiedClient(t, p, server).Get(server.URL)
	require.Error(t, err)
	require.Zero(t, hits.Load())
}

func TestTheProxyRefusesToForwardPlainHTTPToALoopbackAddress(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(counting(&hits))
	defer server.Close()
	p, err := StartProxy(nil)
	require.NoError(t, err)
	defer p.Close()

	answer, err := proxiedClient(t, p, server).Get(server.URL)
	require.NoError(t, err)
	defer answer.Body.Close()
	require.Equal(t, http.StatusForbidden, answer.StatusCode)
	require.Zero(t, hits.Load())
}

// With the address check told every address is public, both paths reach the
// server, and a tunnel carries the client's own TLS: the certificate it checks
// is the server's, not one the proxy made.
func TestAnAllowedAddressIsTunnelledAndForwardedUntouched(t *testing.T) {
	anything := func(netip.Addr) bool { return true }
	p, err := startProxy(dialWith(nil, ErrRefused, anything))
	require.NoError(t, err)
	defer p.Close()

	var hits atomic.Int32
	secure := httptest.NewTLSServer(counting(&hits))
	defer secure.Close()
	answer, err := proxiedClient(t, p, secure).Get(secure.URL)
	require.NoError(t, err)
	body, _ := io.ReadAll(answer.Body)
	_ = answer.Body.Close()
	require.Equal(t, "inside", string(body))
	require.NotNil(t, answer.TLS)
	require.Equal(t, secure.Certificate().Raw, answer.TLS.PeerCertificates[0].Raw)

	plain := httptest.NewServer(counting(&hits))
	defer plain.Close()
	answer, err = proxiedClient(t, p, plain).Get(plain.URL)
	require.NoError(t, err)
	body, _ = io.ReadAll(answer.Body)
	_ = answer.Body.Close()
	require.Equal(t, "inside", string(body))
	require.EqualValues(t, 2, hits.Load())
}
