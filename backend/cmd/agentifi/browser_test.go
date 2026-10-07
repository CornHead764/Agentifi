package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

func TestTheSelftestSaysWhereChromeCameFrom(t *testing.T) {
	var out strings.Builder
	describeChrome(&out, browser.Chrome{
		Path:     "/chrome/current/chrome",
		Resolved: "/chrome/120.0.1.0-1/chrome",
		Origin: "package google-chrome-stable 120.0.1.0-1\n" +
			"from    https://dl.google.com/linux/chrome/deb/pool/main/g/google-chrome-stable/google-chrome-stable_120.0.1.0-1_amd64.deb\n" +
			"sha256  0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})

	require.Equal(t, ""+
		"chrome   /chrome/120.0.1.0-1/chrome\n"+
		"         through /chrome/current/chrome\n"+
		"         package google-chrome-stable 120.0.1.0-1\n"+
		"         from    https://dl.google.com/linux/chrome/deb/pool/main/g/google-chrome-stable/google-chrome-stable_120.0.1.0-1_amd64.deb\n"+
		"         sha256  0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n",
		out.String())
}

func TestTheSelftestSaysWhenChromeHasNoRecord(t *testing.T) {
	var out strings.Builder
	describeChrome(&out, browser.Chrome{
		Path:     "/opt/google/chrome/chrome",
		Resolved: "/opt/google/chrome/chrome",
	})

	require.Equal(t, ""+
		"chrome   /opt/google/chrome/chrome\n"+
		"         not installed by the chrome service; no record of where it came from\n",
		out.String())
}
