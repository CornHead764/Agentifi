package auth

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// WebAuthn requires the RP ID to be the origin's effective domain or a
// registrable suffix of it. A self-hosted box is reached several ways, so the
// RP ID and expected origin are resolved per request from the origin the
// browser used. Setting Passkeys.RPID pins it, and a request from outside that
// domain is refused.

// WebAuthnContext is the relying-party ID and browser origin one ceremony is
// bound to, stored with the challenge.
type WebAuthnContext struct {
	RPID   string
	Origin string
}

// RequestOrigin is the origin the browser is on. Without an Origin header it
// falls back to the addressed host, honouring the proxy's forwarded scheme,
// and refuses to guess beyond that.
func RequestOrigin(r *http.Request) (string, error) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		return origin, nil
	}

	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return "", &OriginError{
			Code:    OriginMissing,
			Message: "The request did not include an origin, so the passkey domain could not be determined.",
		}
	}

	scheme, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	scheme = strings.TrimSpace(scheme)
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host, nil
}

// SplitOrigin splits an origin into scheme, hostname and port. Hand-rolled so
// a missing scheme or an unterminated IPv6 literal is an error rather than an
// empty component.
func SplitOrigin(origin string) (scheme, hostname string, port int, err error) {
	malformed := &OriginError{
		Code:    OriginInvalid,
		Message: "The request origin could not be understood.",
	}

	scheme, remainder, found := strings.Cut(origin, "://")
	if !found || remainder == "" {
		return "", "", 0, malformed
	}

	host, _, _ := strings.Cut(remainder, "/")
	var portPart string
	if strings.HasPrefix(host, "[") { // IPv6 literal, e.g. [::1]:3000
		closing := strings.Index(host, "]")
		if closing == -1 {
			return "", "", 0, malformed
		}
		hostname = host[:closing+1]
		portPart = strings.TrimPrefix(host[closing+1:], ":")
	} else {
		hostname, portPart, _ = strings.Cut(host, ":")
	}

	if hostname == "" {
		return "", "", 0, malformed
	}
	if portPart != "" {
		if port, err = strconv.Atoi(portPart); err != nil {
			return "", "", 0, malformed
		}
	}
	return strings.ToLower(scheme), strings.ToLower(hostname), port, nil
}

// IsIPLiteral reports whether a hostname is an address rather than a name.
// Browsers refuse an RP ID that is an IP literal.
func IsIPLiteral(hostname string) bool {
	_, err := netip.ParseAddr(strings.Trim(hostname, "[]"))
	return err == nil
}

// isSecureOrigin mirrors the browser's secure-context rule: http://localhost
// is treated as secure, and every other plain-http origin fails that check
// before WebAuthn is reached at all.
func isSecureOrigin(scheme, hostname string) bool {
	if scheme == "https" {
		return true
	}
	return scheme == "http" && (hostname == "localhost" || strings.HasSuffix(hostname, ".localhost"))
}

func isRegistrableSuffix(hostname, rpID string) bool {
	return hostname == rpID || strings.HasSuffix(hostname, "."+rpID)
}

// ResolveContext is the RP ID and expected origin for one ceremony. Every
// refusal is an *OriginError the UI can act on.
func (p *Passkeys) ResolveContext(origin string) (WebAuthnContext, error) {
	scheme, hostname, _, err := SplitOrigin(origin)
	if err != nil {
		return WebAuthnContext{}, err
	}

	if IsIPLiteral(hostname) {
		return WebAuthnContext{}, &OriginError{
			Code: OriginIP,
			Message: "Passkeys cannot be used on an IP address. Open the app on a domain name " +
				"over HTTPS, or on http://localhost.",
		}
	}
	if !isSecureOrigin(scheme, hostname) {
		return WebAuthnContext{}, &OriginError{
			Code: OriginInsecure,
			Message: "Passkeys require a secure connection. Open the app over HTTPS, or on " +
				"http://localhost.",
		}
	}

	configured := strings.ToLower(strings.TrimSpace(p.RPID))
	if configured != "" && !isRegistrableSuffix(hostname, configured) {
		return WebAuthnContext{}, &OriginError{
			Code: OriginMismatch,
			Message: "This app is reached at '" + hostname + "', which is outside the configured " +
				"passkey domain '" + configured + "'. Update WEBAUTHN_RP_ID or open the app on that domain.",
		}
	}

	expected := strings.TrimSpace(p.Origin)
	if expected == "" {
		expected = origin
	}
	if configured == "" {
		configured = hostname
	}
	return WebAuthnContext{RPID: configured, Origin: expected}, nil
}

// ContextForRequest resolves the ceremony's relying party from the request.
func (p *Passkeys) ContextForRequest(r *http.Request) (WebAuthnContext, error) {
	origin, err := RequestOrigin(r)
	if err != nil {
		return WebAuthnContext{}, err
	}
	return p.ResolveContext(origin)
}
