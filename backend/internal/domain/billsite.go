package domain

import (
	"net"
	"net/url"
	"strings"
)

// privateHostSuffixes are names that resolve only inside a network. A signed-in
// browser is sent to a connection's site, so a site on one would point the
// server's browser at the household's own network.
var privateHostSuffixes = []string{
	".local", ".localhost", ".internal", ".lan", ".home", ".corp", ".home.arpa", ".intranet",
}

// SiteAddressOf is a portal address as a connection keeps it (catalogue
// SiteAddress): https, the host in lower case, and the first segment of the
// path, which is the application's root ("https://portal.example.org/Portal").
// Any page of the portal copied from the address bar gives the same answer,
// with or without its scheme. A host with no dot, an IP literal, a port other
// than 443, credentials in the address or a name only a private network
// resolves is refused.
func SiteAddressOf(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n\\") {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return "", false
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "https" && scheme != "http" {
		return "", false
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if !strings.Contains(host, ".") || net.ParseIP(host) != nil || !hostnameChars(host) {
		return "", false
	}
	for _, suffix := range privateHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return "", false
		}
	}
	root := "https://" + host
	segment, _, _ := strings.Cut(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/")
	if !pathSegmentChars(segment) {
		return "", false
	}
	if segment != "" {
		root += "/" + segment
	}
	return root, true
}

// pathSegmentChars is letters, digits, hyphens and underscores, which is every
// spelling of a portal's root ("Portal", "portal-prd") and nothing that
// could climb out of it.
func pathSegmentChars(segment string) bool {
	for _, r := range segment {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// hostnameChars is letters, digits, hyphens and dots, with no empty label.
func hostnameChars(host string) bool {
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
