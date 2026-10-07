package totp

import (
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/stretchr/testify/require"
)

// The RFC 6238 Appendix B vectors, SHA-1: the seed is the ASCII digits
// "12345678901234567890", which is this base32 key. Expected values come from
// the RFC, not from this implementation.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTheRFC6238VectorsMint(t *testing.T) {
	for _, one := range []struct {
		at       int64
		expected string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		minted, err := code(rfcSecret, time.Unix(one.at, 0), otp.DigitsEight)
		require.NoError(t, err)
		require.Equal(t, one.expected, minted, "T = %d", one.at)
	}
}

func TestACodeIsSixDigitsOfTheSameAnswer(t *testing.T) {
	code, err := Code(rfcSecret, time.Unix(59, 0))
	require.NoError(t, err)
	require.Equal(t, "287082", code)

	// The step is what a code belongs to: another second inside it is the same
	// code, and the step after it is a different one.
	same, err := Code(rfcSecret, time.Unix(30, 0))
	require.NoError(t, err)
	require.Equal(t, code, same)
	next, err := Code(rfcSecret, time.Unix(60, 0))
	require.NoError(t, err)
	require.NotEqual(t, code, next)
}

func TestASetupKeyIsReadAsThePortalShowsIt(t *testing.T) {
	spaced, err := Code("gezd gnbv gy3t qojq gezd gnbv gy3t qojq", time.Unix(59, 0))
	require.NoError(t, err)
	require.Equal(t, "287082", spaced)

	padded, err := Code("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ====", time.Unix(59, 0))
	require.NoError(t, err)
	require.Equal(t, "287082", padded)
}

func TestSomethingThatIsNotASetupKeyIsRefused(t *testing.T) {
	for _, one := range []string{"", "   ", "182931", "1234567890", "not a key!", "GEZD1"} {
		require.False(t, Valid(one), "%q", one)
		_, err := Code(one, time.Unix(59, 0))
		require.ErrorIs(t, err, ErrNotBase32, "%q", one)
	}
	require.True(t, Valid(rfcSecret))
}

func TestRemainingRunsOutWithTheStep(t *testing.T) {
	require.Equal(t, 30*time.Second, Remaining(time.Unix(60, 0)))
	require.Equal(t, time.Second, Remaining(time.Unix(89, 0)))
}
