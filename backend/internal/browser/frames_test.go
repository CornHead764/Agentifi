package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The frame census's discretion rule, where a test can hold it.
//
// A sign-in page's frames mostly belong to advertisers, and what an ad server
// writes into an address is a cookie-sync identifier, a partner id and the
// host's own IP — after a `?`, after a `#`, and after a `;`, which is the one
// nobody expects.
func TestAFrameAddressCarriesNoIdentifiers(t *testing.T) {
	for _, one := range []struct {
		name, address, want string
	}{
		{
			"a query",
			"https://sync.example.test/100000.html?partner_uid=0123abcd4567&gdpr=0",
			"https://sync.example.test/100000.html",
		},
		{
			"matrix parameters after a semicolon",
			"https://12345678.fls.example.test/activityi;dc_pre=INVENTED;ipaddr=AAAAAAAA;ord=1234567890123",
			"https://12345678.fls.example.test/activityi",
		},
		{
			"a fragment",
			"https://tags.example.test/a/s.htm#dmn=portal.example.test&evid=176a092c",
			"https://tags.example.test/a/s.htm",
		},
		{
			"an address with nothing on it",
			"https://portal.example.test/signin/v2/",
			"https://portal.example.test/signin/v2/",
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			require.Equal(t, one.want, frameAddress(one.address))
		})
	}
}
