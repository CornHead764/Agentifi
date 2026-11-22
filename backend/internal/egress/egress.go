// Package egress decides where the server may connect when someone else
// chose the address: a push endpoint, a SimpleFIN bridge, a page a
// connector's browser loads. Only a publicly routable address is allowed, and
// the check is made on the address actually dialled.
package egress

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// reservedPrefixes is every IANA special-purpose range, including CGNAT and
// benchmarking, which look routable.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// Public says addr is publicly routable: in none of the reserved ranges.
func Public(addr netip.Addr) bool {
	// An IPv4-mapped address would otherwise miss every v4 range.
	addr = addr.Unmap()
	if !addr.IsValid() {
		return false
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// PublicName says host could be public by its spelling alone: a public IP
// literal, or a dotted name that is not localhost. A name that resolves to a
// private address passes; only a lookup catches that.
func PublicName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if addr, err := netip.ParseAddr(host); err == nil {
		return Public(addr)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	return strings.Contains(host, ".")
}

// Dial connects only to a public address, resolving host itself and dialling
// the address it checked, so a name that answers differently a second time
// cannot slip through. A refusal wraps refused.
func Dial(resolver *net.Resolver, refused error) func(context.Context, string, string) (net.Conn, error) {
	return dialWith(resolver, refused, Public)
}

func dialWith(
	resolver *net.Resolver, refused error, public func(netip.Addr) bool,
) func(context.Context, string, string) (net.Conn, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%w: address %q: %v", refused, addr, err)
		}
		addrs, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addrs) == 0 {
			return nil, fmt.Errorf("%w: host %q does not resolve", refused, host)
		}
		for _, candidate := range addrs {
			if !public(candidate) {
				return nil, fmt.Errorf("%w: %q resolved to non-public address %s at connect time",
					refused, host, candidate)
			}
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].String(), port))
	}
}
