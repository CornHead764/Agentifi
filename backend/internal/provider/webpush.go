package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Web Push (VAPID, RFC 8292; aes128gcm, RFC 8291/8188). The crypto is the
// library's; this file owns the endpoint SSRF check and the failure taxonomy.

// VapidKeys are base64url-encoded.
type VapidKeys struct {
	PrivateKey string
	PublicKey  string
}

type PushSubscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// ErrPushSubscriptionGone tells the caller to delete the row, not retry.
var ErrPushSubscriptionGone = errors.New("webpush: subscription is gone")

var ErrPushEndpointRejected = errors.New("webpush: endpoint rejected")

const DefaultPushTTL = 24 * time.Hour

type Push struct {
	Keys VapidKeys
	// Subject is the operator's mailto: or https: URL (RFC 8292).
	Subject    string
	TTL        time.Duration
	HTTPClient *http.Client
	Resolver   *net.Resolver

	// endpointGuard is unexported so only this package's tests can replace
	// the SSRF check; applications cannot switch it off.
	endpointGuard func(context.Context, string) error
}

// Send re-checks the endpoint every time rather than trusting the stored row.
func (p *Push) Send(ctx context.Context, sub PushSubscription, payload any) error {
	guard := p.endpointGuard
	if guard == nil {
		guard = func(ctx context.Context, endpoint string) error {
			return AssertEndpointAllowed(ctx, endpoint, p.Resolver)
		}
	}
	if err := guard(ctx, sub.Endpoint); err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webpush: encoding the notification: %w", err)
	}

	ttl := p.TTL
	if ttl <= 0 {
		ttl = DefaultPushTTL
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			// Re-checks at connect time against DNS rebinding.
			Transport: &http.Transport{DialContext: pinnedDial(p.Resolver, ErrPushEndpointRejected)},
			// A redirect would skip the endpoint check.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	// The library prefixes mailto: itself.
	subject := strings.TrimPrefix(p.Subject, "mailto:")

	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      client,
		Subscriber:      subject,
		TTL:             int(ttl.Seconds()),
		VAPIDPublicKey:  p.Keys.PublicKey,
		VAPIDPrivateKey: p.Keys.PrivateKey,
	})
	if err != nil {
		return fmt.Errorf("webpush: sending to the push service: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// Host only: the endpoint path is a bearer capability.
		return fmt.Errorf("%w: %s", ErrPushSubscriptionGone, endpointHost(sub.Endpoint))
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("webpush: %w", ErrRateLimited)
	case resp.StatusCode >= 400:
		return fmt.Errorf("webpush: push service returned %d", resp.StatusCode)
	}
	return nil
}

// AssertEndpointAllowed stops push delivery being an SSRF primitive: any user
// can register an endpoint, and a 404/410 pruning the row reveals whether a
// probe hit. HTTPS only, every resolved address publicly routable, and an
// unresolvable name is refused. The default client's pinnedDial repeats the
// address check at connect time.
func AssertEndpointAllowed(ctx context.Context, endpoint string, resolver *net.Resolver) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: %s is not a URL", ErrPushEndpointRejected, endpoint)
	}
	if parsed.Scheme != "https" {
		scheme := parsed.Scheme
		if scheme == "" {
			scheme = "no scheme"
		}
		return fmt.Errorf("%w: endpoint must use https, got %s", ErrPushEndpointRejected, scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("%w: endpoint has no host", ErrPushEndpointRejected)
	}

	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("%w: host %q does not resolve", ErrPushEndpointRejected, host)
	}
	for _, addr := range addrs {
		if !isPubliclyRoutable(addr) {
			return fmt.Errorf("%w: host %q resolves to non-public address %s",
				ErrPushEndpointRejected, host, addr)
		}
	}
	return nil
}

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

func isPubliclyRoutable(addr netip.Addr) bool {
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

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "(endpoint)"
	}
	return parsed.Scheme + "://" + parsed.Host
}

// pinnedDial checks addresses at connect time, closing the DNS-rebinding window
// a pre-request check leaves.
func pinnedDial(resolver *net.Resolver, refused error) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%w: address %q: %v", refused, addr, err)
		}
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		addrs, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addrs) == 0 {
			return nil, fmt.Errorf("%w: host %q does not resolve", refused, host)
		}
		for _, candidate := range addrs {
			if !isPubliclyRoutable(candidate) {
				return nil, fmt.Errorf("%w: %q resolved to non-public address %s at connect time",
					refused, host, candidate)
			}
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].String(), port))
	}
}
