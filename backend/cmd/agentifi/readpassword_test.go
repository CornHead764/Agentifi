package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
)

// readLine is what a scripted install depends on: the CLI reads exactly one
// line per prompt, so a password and its repeat can be piped in.
func TestAPipedPasswordIsReadOneLineAtATime(t *testing.T) {
	piped := strings.NewReader("first-secret\nsecond-secret\n")

	first, err := readLine(piped)
	require.NoError(t, err)
	require.Equal(t, "first-secret", first)

	second, err := readLine(piped)
	require.NoError(t, err)
	require.Equal(t, "second-secret", second)
}

func TestAPipedPasswordKeepsEveryCharacterOfIt(t *testing.T) {
	// The failure this catches is a password stored truncated or with the
	// carriage return still on it: `user add` reports success and the account
	// then cannot sign in, which looks like the login being broken.
	for _, password := range []string{
		"McpSelftest-1234567890-Xy",
		"has spaces and $ymbols!",
		"trailing-cr",
	} {
		line, err := readLine(strings.NewReader(password + "\r\n"))
		require.NoError(t, err)
		require.Equal(t, password, line)
	}
}

func TestAPasswordTheAppWouldRefuseIsRefusedHere(t *testing.T) {
	// A CLI that hashed whatever it was given would let an operator set a
	// password its owner could never re-enter when asked to change it. One
	// rule, in one place.
	require.Error(t, auth.ValidatePassword("short"))
	require.NoError(t, auth.ValidatePassword("long-enough-to-pass"))
}

func TestAnEmptyPipedLineIsRefusedRatherThanHashed(t *testing.T) {
	// Stdin that never arrived — a closed pipe, a `docker exec` without
	// `-i` — reads as an empty line, and `user add` must not report success
	// having created an account with an empty password that nobody could ever
	// sign into.
	line, err := readLine(strings.NewReader("\n"))
	require.NoError(t, err)
	require.Empty(t, line, "an empty line is what arrives; refusing it is readPassword's job")
}
