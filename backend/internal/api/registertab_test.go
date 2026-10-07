package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Which register tab an account opens on: stored on the account, read back on
// the row and the listing, cleared by null.

func TestAnAccountOpensOnTheRowsUntilItIsToldOtherwise(t *testing.T) {
	l := buildLedger(t)
	row := listedAccountByID(t, l, l.id("checking"))
	require.Contains(t, row, "default_register_tab")
	require.Nil(t, row["default_register_tab"])
}

func TestAnAccountKeepsTheTabItWasToldToOpenOn(t *testing.T) {
	l := buildLedger(t)
	path := "/accounts/" + l.str("checking")

	body := l.alex.patch(path, map[string]any{"default_register_tab": "spending"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "spending", body["default_register_tab"])
	require.Equal(t, "spending", listedAccountByID(t, l, l.id("checking"))["default_register_tab"])

	// A patch about something else leaves it alone.
	l.alex.patch(path, map[string]any{"name": "Everyday Checking"}).requireStatus(http.StatusOK)
	require.Equal(t, "spending", listedAccountByID(t, l, l.id("checking"))["default_register_tab"])

	body = l.alex.patch(path, map[string]any{"default_register_tab": nil}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, body["default_register_tab"])
}

func TestAnAccountRefusesATabTheRegisterDoesNotHave(t *testing.T) {
	l := buildLedger(t)
	l.alex.patch("/accounts/"+l.str("checking"), map[string]any{"default_register_tab": "budget"}).
		requireStatus(http.StatusUnprocessableEntity)
	require.Nil(t, listedAccountByID(t, l, l.id("checking"))["default_register_tab"])
}
