package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestABareDomainNeedsNoScheme(t *testing.T) {
	require.Equal(t, "chase.com", ExtractDomain("chase.com"))
}

func TestWwwIsStrippedSoTwoSpellingsShareALogo(t *testing.T) {
	require.Equal(t, ExtractDomain("https://www.chase.com/personal"), ExtractDomain("chase.com"))
}

func TestUserinfoAndPortDoNotLeakIntoTheDomain(t *testing.T) {
	// A SimpleFIN Access URL carries Basic Auth credentials, and this function
	// is called with one.
	domain := ExtractDomain("https://user:secret@bridge.simplefin.org:8443/simplefin")
	require.Equal(t, "bridge.simplefin.org", domain)
	require.NotContains(t, FaviconURL("https://user:secret@bridge.simplefin.org:8443/simplefin"), "secret")
}

func TestABlankWebsiteHasNoDomain(t *testing.T) {
	require.Empty(t, ExtractDomain(""))
	require.Empty(t, ExtractDomain("   "))
}

func TestTheLogoURLCarriesTheDomainAndASize(t *testing.T) {
	require.Equal(t,
		"https://www.google.com/s2/favicons?domain=chase.com&sz=128",
		FaviconURL("https://www.chase.com"))
}

func TestNoWebsiteMeansNoLogoRatherThanABrokenOne(t *testing.T) {
	require.Empty(t, FaviconURL(""))
}
