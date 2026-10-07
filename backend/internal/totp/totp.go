// Package totp mints the six-digit code an authenticator app would show, so a
// bill provider demanding a second factor at every sign-in can be pulled
// unattended. The secret is the provider's setup key, sealed beside the
// password on the connection row. (internal/auth verifies codes; this mints.)
//
// RFC 6238 with the parameters every authenticator app assumes — HMAC-SHA1,
// thirty-second step, six digits — and nothing configurable.
package totp

import (
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/pquerna/otp"
	rfc6238 "github.com/pquerna/otp/totp"
)

const Step = 30 * time.Second

const Digits = otp.DigitsSix

// ErrNotBase32 is a secret that is not a setup key: the household pasted a
// code, a recovery phrase or a QR image's filename.
var ErrNotBase32 = errors.New("totp: the authenticator secret is not a base32 setup key")

// Normalize reduces a setup key as a provider shows it — lower case, groups of
// four, maybe padded — to the one spelling the decoder takes.
func Normalize(secret string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, secret))
}

// Valid reports whether a secret is a base32 setup key. The API checks before
// sealing, so a mistyped key is refused at the field.
func Valid(secret string) bool {
	return isSetupKey(Normalize(secret))
}

func Code(secret string, at time.Time) (string, error) {
	return code(secret, at, Digits)
}

func code(secret string, at time.Time, digits otp.Digits) (string, error) {
	key := strings.TrimRight(Normalize(secret), "=")
	if !isSetupKey(key) {
		return "", ErrNotBase32
	}
	return rfc6238.GenerateCodeCustom(key, at, rfc6238.ValidateOpts{
		Period:    uint(Step / time.Second),
		Digits:    digits,
		Algorithm: otp.AlgorithmSHA1,
	})
}

// Remaining is how much of the current step is left at at, so a caller can
// wait out a step rather than mint a code that expires in flight.
func Remaining(at time.Time) time.Duration {
	return Step - time.Duration(at.Unix()%int64(Step/time.Second))*time.Second
}

func isSetupKey(secret string) bool {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(secret, "="))
	return err == nil && len(key) > 0
}
