package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A route only a person may take is refused with a link that opens it, for
// the model to hand on — not with directions to a settings screen.
func TestAHumanOnlyRouteIsRefusedWithALinkToItsControl(t *testing.T) {
	const id = "8c6b1f33-0000-4000-8000-000000000001"
	cases := map[string]string{
		"/bills/connections/" + id + "/sign-in":            "(/settings/bills?sign-in=" + id + ")",
		"/bills/connections/" + id + "/credential":         "(/settings/bills?sign-in=" + id + ")",
		"/bills/connections/" + id + "/session":            "(/settings/bills)",
		"/bills/challenges/" + id + "/answer":              "(/settings/bills?challenge=" + id + ")",
		"/email/connections/" + id + "/sign-in":            "(/settings/email?sign-in=" + id + ")",
		"/email/connections/" + id + "/secret":             "(/settings/email)",
		"/merchants/amazon/accounts/" + id + "/sign-in":    "(/settings/merchants/amazon?sign-in=" + id + ")",
		"/merchants/costco/accounts/" + id + "/session":    "(/settings/merchants/costco)",
		"/bills/connections/not-an-id/sign-in":             "(/settings/bills)",
		"/merchants/amazon/accounts/" + id + "/credential": "[Sign in to Amazon]",
	}
	for path, want := range cases {
		err := refuseDeniedPath(path)
		require.Error(t, err, path)
		require.Contains(t, err.Error(), want, path)
		require.NotContains(t, err.Error(), "in Settings", path)
	}
}
