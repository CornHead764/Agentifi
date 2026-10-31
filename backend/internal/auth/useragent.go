package auth

import "strings"

// SessionName is how a browser session is listed: the browser and operating
// system read from its User-Agent, as in "Firefox on Linux". Order matters,
// since most browsers also claim to be Chrome or Safari.
func SessionName(userAgent string) string {
	browser := firstMatch(userAgent, []namedMarker{
		{"Edg/", "Edge"}, {"EdgiOS/", "Edge"}, {"OPR/", "Opera"},
		{"Firefox/", "Firefox"}, {"FxiOS/", "Firefox"},
		{"Chrome/", "Chrome"}, {"CriOS/", "Chrome"}, {"Safari/", "Safari"},
	})
	system := firstMatch(userAgent, []namedMarker{
		{"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Android", "Android"},
		{"CrOS", "ChromeOS"}, {"Windows", "Windows"}, {"Mac OS X", "macOS"}, {"Linux", "Linux"},
	})
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "":
		return "A browser on " + system
	}
	return "A browser"
}

type namedMarker struct{ marker, name string }

func firstMatch(userAgent string, markers []namedMarker) string {
	for _, m := range markers {
		if strings.Contains(userAgent, m.marker) {
			return m.name
		}
	}
	return ""
}
