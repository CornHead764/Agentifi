package provider

import (
	"net/url"
	"strconv"
	"strings"
)

// Institution logos are the favicon of the website SimpleFIN names; an empty
// URL is an ordinary answer.

const logoSize = 128

const faviconService = "https://www.google.com/s2/favicons"

// ExtractDomain is handed credential-bearing access URLs, so only the host
// survives (minus one leading "www.").
func ExtractDomain(website string) string {
	raw := strings.TrimSpace(website)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.TrimPrefix(host, "www.")
}

func FaviconURL(website string) string {
	domain := ExtractDomain(website)
	if domain == "" {
		return ""
	}
	return faviconService + "?domain=" + url.QueryEscape(domain) + "&sz=" + strconv.Itoa(logoSize)
}
