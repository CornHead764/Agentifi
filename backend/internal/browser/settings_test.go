package browser

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/config"
)

var testUserAgent = UserAgentFor("140.0.7339.80")

func TestTheUserAgentIsTheInstalledChromesInItsReducedForm(t *testing.T) {
	want := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/140.0.0.0 Safari/537.36"
	if got := UserAgentFor("140.0.7339.80"); got != want {
		t.Fatalf("got %q", got)
	}
}

// testSettings is the browser settings the environment gives, as serve reads
// them, allowed onto the loopback the invented pages stand on.
func testSettings(t *testing.T) config.Browser {
	t.Helper()
	settings := guardedSettings(t)
	settings.AllowPrivate = true
	return settings
}

// guardedSettings is the browser settings exactly as serve reads them.
func guardedSettings(t *testing.T) config.Browser {
	t.Helper()
	settings, err := config.LoadBrowser()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}
