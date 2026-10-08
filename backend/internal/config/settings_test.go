package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestASavedValueSitsBeneathTheEnvironment: the order the screen promises,
// environment, then .env, then saved, then default, read setting by setting.
func TestASavedValueSitsBeneathTheEnvironment(t *testing.T) {
	t.Setenv("DEBUG", "true")
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("EMAIL_LOOKBACK_DAYS=9\n"), 0o600))
	t.Setenv("SYNC_AT", "05:15")

	cfg, resolved, err := Resolve(map[string]string{
		"SYNC_AT":             "22:00",
		"EMAIL_LOOKBACK_DAYS": "3",
		"SIMPLEFIN_ENABLED":   "true",
	})
	require.NoError(t, err)

	require.Equal(t, Resolved{Value: "05:15", Default: "04:00", Source: FromEnvironment}, resolved["SYNC_AT"])
	require.Equal(t, 5, cfg.SyncAt.Hour)
	require.Equal(t, Resolved{Value: "9", Default: "30", Source: FromEnvironment}, resolved["EMAIL_LOOKBACK_DAYS"])
	require.Equal(t, 9, cfg.EmailLookbackDays)
	require.Equal(t, Resolved{Value: "true", Default: "false", Source: FromDatabase}, resolved["SIMPLEFIN_ENABLED"])
	require.True(t, cfg.SimpleFINEnabled)
	require.Equal(t, Resolved{Value: "true", Default: "true", Source: FromDefault}, resolved["SYNC_ENABLED"])
}

// TestASavedRowCannotStandInForASecret: only the closed list is read from the
// database, whatever a row is called.
func TestASavedRowCannotStandInForASecret(t *testing.T) {
	t.Setenv("DEBUG", "true")
	t.Chdir(t.TempDir())

	cfg, resolved, err := Resolve(map[string]string{
		"SECRET_KEY":    "a-saved-value-that-must-never-be-read-0123456789",
		"DATABASE_PATH": "/elsewhere/agentifi.db",
		"BACKUP_DIR":    "/elsewhere",
	})
	require.NoError(t, err)
	require.Equal(t, "change-me-in-production", cfg.SecretKey)
	require.NotEqual(t, "/elsewhere/agentifi.db", cfg.DatabasePath)
	require.Empty(t, cfg.BackupDir)
	require.NotContains(t, resolved, "SECRET_KEY")
	require.NotContains(t, resolved, "BACKUP_DIR")
}

// TestEverySettingIsOneLoadReads: the screen offers nothing the server does
// not read, and the list holds no secret.
func TestEverySettingIsOneLoadReads(t *testing.T) {
	t.Setenv("DEBUG", "true")
	t.Chdir(t.TempDir())

	_, resolved, err := Resolve(nil)
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, setting := range Settings {
		require.False(t, seen[setting.Key], "%s is listed twice", setting.Key)
		seen[setting.Key] = true
		require.Equal(t, FromDefault, resolved[setting.Key].Source, setting.Key)
		require.NotEmpty(t, setting.Label, setting.Key)
		require.NotEmpty(t, setting.Group, setting.Key)
	}
	for _, secret := range []string{
		"SECRET_KEY", "CREDENTIAL_ENCRYPTION_KEY", "DATABASE_PATH", "CAMOUFOX_URL",
		"OIDC_CLIENT_SECRET", "SMTP_PASSWORD", "VAPID_PRIVATE_KEY", "OPENEXCHANGERATES_APP_ID",
	} {
		require.False(t, seen[secret], "%s is a secret", secret)
	}
}

// TestAnUnreadableSavedValueIsRefusedByName: a save is checked by resolving
// it, so a value the next start would refuse is refused at the save.
func TestAnUnreadableSavedValueIsRefusedByName(t *testing.T) {
	t.Setenv("DEBUG", "true")
	t.Chdir(t.TempDir())

	for key, value := range map[string]string{
		"SYNC_AT":             "4pm",
		"SIMPLEFIN_ENABLED":   "maybe",
		"LOGIN_MAX_ATTEMPTS":  "0",
		"SMTP_PORT":           "70000",
		"TRUSTED_PROXY_CIDRS": "192.0.2.0/24, proxy.local",
		"AUTOMATION_WORKERS":  "0",
	} {
		_, _, err := Resolve(map[string]string{key: value})
		require.ErrorContains(t, err, key, value)
	}
}
