package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The loader reads the process environment, and these tests describe a bare
// one: a default secret refused, a .env read, a credentials directory
// honoured. CI exports SECRET_KEY and DATABASE_PATH for the packages that need
// a database, and a workstation shell may carry any of these, so each is
// cleared before the tests run rather than assumed absent.
func TestMain(m *testing.M) {
	for _, name := range []string{
		"SECRET_KEY", "DATABASE_PATH", "DEBUG", "CREDENTIALS_DIRECTORY",
		"CREDENTIAL_ENCRYPTION_KEY", "FRONTEND_URL", "HTTP_ADDR", "PRIMARY_CURRENCY", "CAMOUFOX_URL",
		"AGENTIFI_CHROME_PATH",
	} {
		os.Unsetenv(name)
	}
	os.Exit(m.Run())
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DEBUG", "true")
	t.Chdir(t.TempDir())

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "./data/agentifi.db", cfg.DatabasePath)
	require.Equal(t, ":8000", cfg.HTTPAddr)
	require.Equal(t, 24*time.Hour, cfg.AccessTokenExpiry)
	require.Equal(t, "USD", cfg.PrimaryCurrency)
	require.Contains(t, cfg.SupportedCurrencies, "EUR")
	require.Equal(t, []string{"openid", "email", "profile"}, cfg.OIDCScopes)
	// Where the compose file mounts the chrome volume.
	require.Equal(t, "/chrome/current/chrome", cfg.Browser.ChromePath)
}

// TestTheCostcoLookupNeedsAStore: there is no default warehouse, so the
// lookup stays off until all three parts are set.
func TestTheCostcoLookupNeedsAStore(t *testing.T) {
	t.Setenv("DEBUG", "true")
	t.Chdir(t.TempDir())

	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.MerchantCatalogEnabled)
	require.False(t, cfg.CostcoCatalog.Set())

	t.Setenv("COSTCO_CATALOG_SHOP_ID", "0000")
	t.Setenv("COSTCO_CATALOG_ZONE_ID", "0")
	cfg, err = Load()
	require.NoError(t, err)
	require.False(t, cfg.CostcoCatalog.Set())

	t.Setenv("COSTCO_CATALOG_POSTAL_CODE", "00000")
	cfg, err = Load()
	require.NoError(t, err)
	require.Equal(t, CostcoStore{ShopID: "0000", ZoneID: "0", PostalCode: "00000"}, cfg.CostcoCatalog)
	require.True(t, cfg.CostcoCatalog.Set())
}

// TestDefaultSecretIsRefused: a production instance signing tokens with the
// documented default would accept tokens minted by anyone who has read the
// documentation.
func TestDefaultSecretIsRefused(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := Load()
	require.ErrorContains(t, err, "SECRET_KEY")
}

// TestAShortSecretIsRefused: a six-character SECRET_KEY passes every other
// check in this file and looks like a configured deployment, which is exactly
// why the length is enforced rather than documented.
func TestAShortSecretIsRefused(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SECRET_KEY", "s3cret")

	_, err := Load()
	require.ErrorContains(t, err, "at least 32")

	// The documented recipe passes.
	t.Setenv("SECRET_KEY", strings.Repeat("a", minSecretKeyLength))
	_, err = Load()
	require.NoError(t, err)

	// DEBUG is the workstation escape hatch, the same one the default-value
	// check has.
	t.Setenv("SECRET_KEY", "s3cret")
	t.Setenv("DEBUG", "true")
	_, err = Load()
	require.NoError(t, err)
}

func TestMalformedValueIsAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DEBUG", "true")
	t.Setenv("SECRET_KEY", "s3cret")
	t.Setenv("ACCESS_TOKEN_EXPIRE_MINUTES", "two hours")

	_, err := Load()
	require.ErrorContains(t, err, "ACCESS_TOKEN_EXPIRE_MINUTES")
}

func TestDotEnvDoesNotOverrideEnvironment(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"# a comment\nSECRET_KEY=\"from-file\"\nexport HTTP_ADDR=:9000\nPRIMARY_CURRENCY=EUR\n"), 0o600))
	t.Chdir(dir)
	t.Setenv("DEBUG", "true")
	t.Setenv("PRIMARY_CURRENCY", "GBP")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "from-file", cfg.SecretKey)
	require.Equal(t, ":9000", cfg.HTTPAddr)
	require.Equal(t, "GBP", cfg.PrimaryCurrency, "the environment wins over a file baked into an image")
}

// TestSecretFromCredentialsDirectory covers the systemd and Compose path: a
// secret in a file is not visible in a process listing.
func TestSecretFromCredentialsDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SECRET_KEY"), []byte("from-credential\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vapid_private_key"), []byte("lowercase-too"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CAMOUFOX_URL"), []byte("ws://camoufox:9333/path"), 0o600))
	t.Chdir(t.TempDir())
	t.Setenv("DEBUG", "true")
	t.Setenv("CREDENTIALS_DIRECTORY", dir)
	// The compose file blanks this so a stray line in .env cannot repoint
	// it; an empty variable must fall through to the file.
	t.Setenv("CAMOUFOX_URL", "")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "from-credential", cfg.SecretKey)
	require.Equal(t, "lowercase-too", cfg.VAPIDPrivateKey)
	require.Equal(t, "ws://camoufox:9333/path", cfg.Browser.CamoufoxURL)

	// A value in the environment still wins over the file.
	t.Setenv("SECRET_KEY", "from-environment")
	cfg, err = Load()
	require.NoError(t, err)
	require.Equal(t, "from-environment", cfg.SecretKey)
}

// TestTheCredentialKeyIsNotTheSessionKey is the whole reason the setting
// exists: rotating SECRET_KEY is the documented way to log everyone out, and a
// deployment that did that must not also lose every stored bank connection.
func TestTheCredentialKeyIsNotTheSessionKey(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DEBUG", "true")
	t.Setenv("SECRET_KEY", "the-session-key")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotEqual(t, cfg.SecretKey, cfg.CredentialKey())
	require.Equal(t, DeriveCredentialKey("the-session-key"), cfg.CredentialKey())
}

func TestAnExplicitCredentialKeySurvivesASecretKeyRotation(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DEBUG", "true")
	t.Setenv("SECRET_KEY", "the-session-key")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "the-credential-key")

	before, err := Load()
	require.NoError(t, err)

	t.Setenv("SECRET_KEY", "a-brand-new-session-key")
	after, err := Load()
	require.NoError(t, err)
	require.Equal(t, before.CredentialKey(), after.CredentialKey())
}

func TestTheSyncWindowIsReadAsATimeOfDay(t *testing.T) {
	require.True(t, ParseTimeOfDay("04:00").Valid())
	require.Equal(t, 23, ParseTimeOfDay("23:30").Hour)
	require.Equal(t, 30, ParseTimeOfDay("23:30").Minute)
	require.Equal(t, "04:05", ParseTimeOfDay("4:5").String())
}

func TestAnUnreadableSyncWindowIsRefusedByName(t *testing.T) {
	// The raw string is kept so the error can quote what the operator wrote.
	// Reporting the zero value instead would say `got "00:00"`, which is both
	// wrong and a valid setting.
	for _, raw := range []string{"4pm", "25:00", "04:60", "0400", "", "-1:00"} {
		parsed := ParseTimeOfDay(raw)
		require.False(t, parsed.Valid(), "%q was accepted", raw)
		require.Equal(t, raw, parsed.Raw)
	}
}

// The frontend's currency picker mirrors this list. A drift between the two
// could offer a currency like SGD or ZAR, which no rate is ever stored for,
// or omit one like PLN or CZK, which are. A currency offered with no rate
// behind it is one whose amounts never convert, and whose transactions are
// then counted at face value in every total. Pinned here and in
// frontend/src/lib/currencies.test.ts so a drift is a failure rather than a
// silent wrong number.
func TestTheSupportedCurrencyDefaultIsTheListTheClientOffers(t *testing.T) {
	t.Setenv("DEBUG", "true")
	t.Chdir(t.TempDir())

	cfg, err := Load()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"USD", "EUR", "GBP", "CAD", "AUD", "CHF", "JPY", "MXN",
		"BRL", "INR", "SEK", "DKK", "NOK", "PLN", "CZK", "NZD",
	}, cfg.SupportedCurrencies)
}
