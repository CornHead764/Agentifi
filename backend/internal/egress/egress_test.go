package egress

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// The ranges are the IANA special-purpose address registries (RFC 6890 and
// its updates); the public examples are well-known public resolvers.
func TestTheReservedRangesThatLookRoutableAreStillRefused(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.169.254",
		"100.64.0.1", // CGNAT, inside a hosting provider's own network
		"198.18.0.1", // benchmarking
		"::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
	} {
		require.False(t, Public(netip.MustParseAddr(addr)), "%s was allowed", addr)
	}
	for _, addr := range []string{"8.8.8.8", "34.107.221.82", "2606:4700:4700::1111"} {
		require.True(t, Public(netip.MustParseAddr(addr)), "%s was refused", addr)
	}
}

// A name is judged by spelling alone: an IP literal by its range, localhost
// and a single label (a compose service, "agentifi") refused.
func TestAHostThatNamesThePrivateNetworkBySpellingIsRefused(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1", "10.0.0.5", "[::1]", "169.254.169.254", "localhost", "app.localhost",
		"agentifi", "camoufox", "",
	} {
		require.False(t, PublicName(host), "%q was allowed", host)
	}
	for _, host := range []string{"portal.example.org", "8.8.8.8", "[2606:4700:4700::1111]", "Portal.Example.org."} {
		require.True(t, PublicName(host), "%q was refused", host)
	}
}

func TestADialToALoopbackAddressIsRefusedBeforeConnecting(t *testing.T) {
	_, err := Dial(nil, ErrRefused)(context.Background(), "tcp", "127.0.0.1:1")
	require.ErrorIs(t, err, ErrRefused)
	_, err = Dial(nil, ErrRefused)(context.Background(), "tcp", "[::ffff:169.254.169.254]:80")
	require.ErrorIs(t, err, ErrRefused)
}
