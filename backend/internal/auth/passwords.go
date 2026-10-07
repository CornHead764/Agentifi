package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Password hashing: bcrypt over a base64'd SHA-256 pre-hash.
//
// bcrypt truncates at 72 bytes and stops at the first NUL, so without the
// pre-hash two long passphrases differing only past byte 72 log in as each
// other. The digest is base64'd because a raw digest can contain a NUL.
//
// This is bcrypt_sha256 as passlib defines it. A bare-bcrypt hash will not
// verify.

// bcryptCost is ~250ms on current hardware.
const bcryptCost = 12

// dummyHash is verified against when there is no real hash to check, so that
// an unknown address and a wrong password take the same time. Built on first
// use so commands that never verify a password do not pay for it at startup.
var dummyHash = sync.OnceValue(func() []byte {
	// The only error bcrypt returns here is an out-of-range cost, and the cost
	// is a constant in range.
	hash, _ := bcrypt.GenerateFromPassword(prepare(""), bcryptCost)
	return hash
})

func prepare(plain string) []byte {
	digest := sha256.Sum256([]byte(plain))
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(digest)))
	base64.StdEncoding.Encode(encoded, digest[:])
	return encoded
}

// HashPassword returns the stored form of a password.
func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(prepare(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hashing a password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword checks a password against a stored hash. An account with no
// password must fail against an empty hash in the same time as a real
// comparison; an unparseable hash fails closed.
func VerifyPassword(plain, hashed string) bool {
	if hashed == "" {
		DummyVerify()
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hashed), prepare(plain)) == nil
}

// DummyVerify spends the time a real verification would, on paths with no hash
// to check, so response time does not answer what the status code refuses to.
func DummyVerify() {
	_ = bcrypt.CompareHashAndPassword(dummyHash(), prepare(""))
}

// ConstantTimeEquals compares two secrets that are not bcrypt hashes —
// recovery-code digests, opaque handoff tokens — without leaking how far the
// comparison got.
func ConstantTimeEquals(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

// MinPasswordLength is the floor for a password chosen through the API. There
// is no email-based recovery, so the floor is ten. Length is the only rule.
const MinPasswordLength = 10

// MaxPasswordLength bounds what will be hashed, so a request body cannot make
// the server hash a megabyte.
const MaxPasswordLength = 1024

var (
	ErrPasswordTooShort = fmt.Errorf("auth: a password must be at least %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("auth: a password may be at most %d characters", MaxPasswordLength)
	ErrPasswordReused   = errors.New("auth: the new password is the same as the current one")
)

// ValidatePassword checks a password a user chose for themselves. Not applied
// to AuthenticatePassword: a stored password predating the rule must still
// verify.
func ValidatePassword(plain string) error {
	switch {
	case len([]rune(plain)) < MinPasswordLength:
		return ErrPasswordTooShort
	case len(plain) > MaxPasswordLength:
		return ErrPasswordTooLong
	}
	return nil
}
