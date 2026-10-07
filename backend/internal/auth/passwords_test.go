package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTwoLongPassphrasesThatDifferOnlyAtTheEndAreDifferentPasswords(t *testing.T) {
	// The bug this proves absent: bcrypt truncates at 72 bytes, so without the
	// SHA-256 pre-hash these two hash identically and either one logs in as the
	// other. 103 characters is what a password manager produces without being
	// asked for anything unusual.
	prefix := strings.Repeat("correct horse battery staple ", 3) // 87 characters
	first := prefix + "aaaaaaaaaaaaaaaa"
	second := prefix + "bbbbbbbbbbbbbbbb"
	require.Len(t, first, 103)
	require.Len(t, second, 103)
	require.Equal(t, first[:72], second[:72], "they are identical inside bcrypt's window")

	hashed, err := HashPassword(first)
	require.NoError(t, err)

	require.True(t, VerifyPassword(first, hashed))
	require.False(t, VerifyPassword(second, hashed), "the pre-hash is missing or not applied")
}

func TestAPasswordWithANulByteIsNotTruncatedAtIt(t *testing.T) {
	// bcrypt stops at the first NUL. The digest is base64'd for this reason:
	// a raw digest can contain one.
	hashed, err := HashPassword("secret\x00tail")
	require.NoError(t, err)
	require.True(t, VerifyPassword("secret\x00tail", hashed))
	require.False(t, VerifyPassword("secret", hashed))
}

func TestAnAccountWithNoPasswordFailsRatherThanMatchingAnEmptyHash(t *testing.T) {
	require.False(t, VerifyPassword("", ""))
	require.False(t, VerifyPassword("anything", ""))
}

func TestACorruptStoredHashFailsClosed(t *testing.T) {
	require.False(t, VerifyPassword("anything", "not-a-bcrypt-hash"))
}

func TestTheSamePasswordHashesDifferentlyEachTime(t *testing.T) {
	first, err := HashPassword("hunter2")
	require.NoError(t, err)
	second, err := HashPassword("hunter2")
	require.NoError(t, err)
	require.NotEqual(t, first, second, "the salt is missing")
	require.True(t, VerifyPassword("hunter2", first))
	require.True(t, VerifyPassword("hunter2", second))
}
