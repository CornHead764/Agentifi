package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The credential cipher needs no database.

const accessURL = "https://demo:demo@bridge.example.invalid/simplefin"

// testContext stands in for a row binding; the real ones are in encryption.go.
const testContext = "connection.access_url:space:row"

func newTestCipher(t *testing.T, key string) *Cipher {
	t.Helper()
	cipher, err := NewCipher(key)
	require.NoError(t, err)
	return cipher
}

func TestASealedCredentialRoundTrips(t *testing.T) {
	cipher := newTestCipher(t, "the-credential-key")

	sealed, err := cipher.Seal(testContext, accessURL)
	require.NoError(t, err)
	require.NotContains(t, sealed, "example.invalid")
	require.NotContains(t, sealed, "demo:demo@")

	opened, err := cipher.Open(testContext, sealed)
	require.NoError(t, err)
	require.Equal(t, accessURL, opened)
}

func TestEverySealUsesItsOwnNonce(t *testing.T) {
	// Equal ciphertexts would also reveal which connections share a credential.
	cipher := newTestCipher(t, "the-credential-key")

	first, err := cipher.Seal(testContext, accessURL)
	require.NoError(t, err)
	second, err := cipher.Seal(testContext, accessURL)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestTheWrongKeyFailsLoudlyRatherThanReturningNothing(t *testing.T) {
	// An empty Access URL would get a 401 and blame the connection instead of
	// the key.
	sealed, err := newTestCipher(t, "the-credential-key").Seal(testContext, accessURL)
	require.NoError(t, err)

	opened, err := newTestCipher(t, "a-different-key").Open(testContext, sealed)
	require.ErrorIs(t, err, ErrCredentialUnreadable)
	require.Empty(t, opened)
	require.Contains(t, err.Error(), "CREDENTIAL_ENCRYPTION_KEY")
}

func TestATamperedCredentialIsRefused(t *testing.T) {
	cipher := newTestCipher(t, "the-credential-key")
	sealed, err := cipher.Seal(testContext, accessURL)
	require.NoError(t, err)

	flipped := strings.TrimSuffix(sealed, "A") + "B"
	if flipped == sealed {
		flipped = sealed[:len(sealed)-1] + "A"
	}
	_, err = cipher.Open(testContext, flipped)
	require.Error(t, err)
}

func TestAPayloadThatNamesNoSchemeIsRefused(t *testing.T) {
	// The version prefix lets the scheme change without guessing at rows.
	cipher := newTestCipher(t, "the-credential-key")

	_, err := cipher.Open(testContext, "not-a-sealed-value")
	require.ErrorContains(t, err, "scheme")

	_, err = cipher.Open(testContext, "v9:AAAA")
	require.ErrorContains(t, err, "v9")
}

func TestASealedValueDoesNotOpenInAnotherRow(t *testing.T) {
	// Without additional data, a credential copied between rows would decrypt.
	cipher := newTestCipher(t, "the-credential-key")

	sealed, err := cipher.Seal(connectionCredentialContext(newTestSpaceID(t), uuid.New()), accessURL)
	require.NoError(t, err)

	opened, err := cipher.Open(connectionCredentialContext(newTestSpaceID(t), uuid.New()), sealed)
	require.ErrorIs(t, err, ErrCredentialUnreadable)
	require.Empty(t, opened)
}

func TestEveryColumnBindsToADifferentContext(t *testing.T) {
	// Colliding contexts would let a value move between columns.
	space := newTestSpaceID(t)
	id := uuid.New()
	contexts := map[string]bool{
		connectionCredentialContext(space, id):    true,
		assistantKeyContext(space):                true,
		serverSettingContext(OIDCClientIDSetting): true,
		totpSecretContext(id):                     true,
	}
	require.Len(t, contexts, 4)
}

func TestAFreshSealIsAlwaysTheCurrentScheme(t *testing.T) {
	cipher := newTestCipher(t, "the-credential-key")
	sealed, err := cipher.Seal(testContext, accessURL)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(sealed, cipherVersion+":"))

	// The same bytes under a scheme this build does not read are refused by
	// name, not handed to GCM to fail as a wrong key.
	_, body, _ := strings.Cut(sealed, ":")
	_, err = cipher.Open(testContext, "v2:"+body)
	require.ErrorContains(t, err, `scheme "v2"`)
}

func TestAnEmptyKeyIsRefusedAtConstruction(t *testing.T) {
	_, err := NewCipher("   ")
	require.ErrorContains(t, err, "empty")
}

func TestAStoreWithNoCipherWillNotWriteACredential(t *testing.T) {
	// Refusing beats storing an Access URL in the clear or an empty one.
	space := newSpace(t)
	connection := &Connection{Name: "SimpleFIN"}

	err := db(t).CreateConnection(t.Context(), space, connection, accessURL)
	require.ErrorContains(t, err, "WithCipher")
}

func newTestSpaceID(t *testing.T) SpaceID {
	t.Helper()
	id, err := ParseSpaceID(uuid.NewString())
	require.NoError(t, err)
	return id
}

func TestAGeneratedVAPIDKeypairIsMadeOnceAndKeptSealed(t *testing.T) {
	sealed := db(t).WithCipher(newTestCipher(t, "the-credential-key"))
	_, err := sealed.Pool().Exec(t.Context(), `DELETE FROM server_settings WHERE key = $1`, VAPIDKeysSetting)
	require.NoError(t, err)

	made := 0
	generate := func() (string, string, error) {
		made++
		return "private-half", "public-half", nil
	}
	private, public, err := sealed.EnsureVAPIDKeys(t.Context(), generate)
	require.NoError(t, err)
	require.Equal(t, []string{"private-half", "public-half"}, []string{private, public})

	// A later process opens the same row with its own cipher over the same key.
	again := db(t).WithCipher(newTestCipher(t, "the-credential-key"))
	private, public, err = again.EnsureVAPIDKeys(t.Context(), generate)
	require.NoError(t, err)
	require.Equal(t, []string{"private-half", "public-half"}, []string{private, public})
	require.Equal(t, 1, made)

	var raw string
	require.NoError(t, sealed.Pool().QueryRow(t.Context(),
		`SELECT value_encrypted FROM server_settings WHERE key = $1`, VAPIDKeysSetting).Scan(&raw))
	require.NotContains(t, raw, "private-half")
}
