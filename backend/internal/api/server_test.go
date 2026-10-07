package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/config"
)

// Server admin's build and settings. The precedence itself is
// internal/config's tests; these are what the screen is told and what a save
// may change.

func clearSavedSettings(t *testing.T, c *client) {
	t.Helper()
	// The settings are read the way `serve` reads them, from this process's
	// environment, which has no SECRET_KEY under test.
	t.Setenv("DEBUG", "true")
	remove := func() {
		_, err := c.env.DB.Pool().Exec(context.Background(),
			`DELETE FROM server_settings WHERE key = ANY($1)`, settingKeys())
		require.NoError(t, err)
	}
	remove()
	t.Cleanup(remove)
}

func settingIn(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	for _, one := range body["settings"].([]any) {
		setting := one.(map[string]any)
		if setting["key"] == key {
			return setting
		}
	}
	t.Fatalf("%s is not in the settings", key)
	return nil
}

func TestServerInfoNamesTheSchema(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))

	body := c.get("/admin/server").requireStatus(http.StatusOK).json()
	require.Greater(t, body["schema_version"], float64(0))
	require.Contains(t, body, "commit")
	require.Contains(t, body, "built_at")
}

func TestASavedSettingAppliesAndTheEnvironmentStaysReadOnly(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearSavedSettings(t, c)
	t.Setenv("SYNC_AT", "05:00")
	_, started, err := config.Resolve(nil)
	require.NoError(t, err)
	c.env.started = started

	body := c.get("/admin/server/settings").requireStatus(http.StatusOK).json()
	simplefin := settingIn(t, body, "SIMPLEFIN_ENABLED")
	require.Equal(t, "false", simplefin["value"])
	require.Equal(t, "default", simplefin["source"])
	require.Equal(t, true, simplefin["live"])
	syncAt := settingIn(t, body, "SYNC_AT")
	require.Equal(t, "05:00", syncAt["value"])
	require.Equal(t, "04:00", syncAt["default"])
	require.Equal(t, "environment", syncAt["source"])

	saved := c.put("/admin/server/settings", map[string]any{"values": map[string]string{
		"SIMPLEFIN_ENABLED":  "true",
		"SYNC_CHECK_MINUTES": "7",
	}}).requireStatus(http.StatusOK).json()
	simplefin = settingIn(t, saved, "SIMPLEFIN_ENABLED")
	require.Equal(t, "true", simplefin["value"])
	require.Equal(t, "database", simplefin["source"])
	require.Equal(t, false, simplefin["pending_restart"])
	require.True(t, c.env.Live().SimpleFINEnabled, "SimpleFIN applies without a restart")
	check := settingIn(t, saved, "SYNC_CHECK_MINUTES")
	require.Equal(t, "7", check["value"])
	require.Equal(t, true, check["pending_restart"])

	// The environment wins, so saving beneath it is refused rather than
	// silently ignored.
	c.put("/admin/server/settings", map[string]any{"values": map[string]string{"SYNC_AT": "06:00"}}).
		requireStatus(http.StatusUnprocessableEntity)
	// Nothing outside the list, and nothing the next start would refuse.
	c.put("/admin/server/settings", map[string]any{"values": map[string]string{"SECRET_KEY": "x"}}).
		requireStatus(http.StatusUnprocessableEntity)
	c.put("/admin/server/settings", map[string]any{"values": map[string]string{"LOGIN_MAX_ATTEMPTS": "0"}}).
		requireStatus(http.StatusUnprocessableEntity)
	require.True(t, c.env.Live().SimpleFINEnabled, "a refused save changes nothing")

	// Left out of the form is cleared, back to the default.
	cleared := c.put("/admin/server/settings", map[string]any{"values": map[string]string{}}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "default", settingIn(t, cleared, "SIMPLEFIN_ENABLED")["source"])
	require.False(t, c.env.Live().SimpleFINEnabled)
	require.Equal(t, false, settingIn(t, cleared, "SYNC_CHECK_MINUTES")["pending_restart"])
}

func TestTheSettingsAreForAdministratorsOnly(t *testing.T) {
	c := newClient(t).as(makeUser(t, testPassword))
	c.get("/admin/server/settings").requireStatus(http.StatusForbidden)
	c.get("/admin/server").requireStatus(http.StatusForbidden)
}
