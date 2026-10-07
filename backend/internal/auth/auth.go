// Package auth answers two questions and keeps them apart: who the caller is,
// and which space this request is about.
//
// A bearer token says who, and nothing more. Space membership is resolved per
// request, so revoking it takes effect on the next call.
//
// No refusal tells the caller something they did not already know. A wrong
// password, an unknown address, a disabled account and a passkey-only account
// all produce ErrInvalidCredentials, in comparable time. A space the caller is
// not a member of produces ErrNoSpace, never a permission error. Every passkey
// ceremony failure, clone detection included, produces ErrInvalidPasskey.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The refusals this package makes. Several are deliberately the same answer to
// different questions.
var (
	// ErrInvalidCredentials is the one first-factor refusal. Anything
	// finer-grained is a free list of valid addresses.
	ErrInvalidCredentials = errors.New("auth: incorrect email or password")

	// ErrInactiveUser is for a caller who authenticated and whose account has
	// since been disabled. Distinct from ErrInvalidCredentials because it is
	// raised after the token was accepted, where there is nothing left to
	// enumerate.
	ErrInactiveUser = errors.New("auth: inactive user")

	// ErrNoSpace covers both "there is no such space" and "you are not a
	// member of it". See ResolveSpace.
	ErrNoSpace = errors.New("auth: space not found")

	// ErrReadOnly is a viewer attempting a write. Safe to distinguish: the
	// caller is already known to be a member.
	ErrReadOnly = errors.New("auth: this space is read-only for you")

	ErrTooManyAttempts  = errors.New("auth: too many verification attempts")
	ErrInvalidCode      = errors.New("auth: invalid verification code")
	ErrInvalidChallenge = errors.New("auth: invalid or expired challenge")

	// ErrInvalidPasskey is every passkey refusal, including a cloned
	// authenticator; the caller must not learn what was noticed.
	ErrInvalidPasskey = errors.New("auth: invalid passkey")

	// ErrPasskeyRegistered is safe to distinguish: the caller is already
	// authenticated and is enrolling their own credential.
	ErrPasskeyRegistered = errors.New("auth: passkey is already registered")

	// ErrNoPendingEnrolment means confirm was called without enrol, or the
	// pending secret expired.
	ErrNoPendingEnrolment = errors.New("auth: start the enrolment first")

	// ErrNotFound is what the credential stores return for a missing row.
	ErrNotFound = errors.New("auth: not found")

	ErrOIDCDisabled = errors.New("auth: OIDC login is not enabled")

	// ErrOIDCLogin is every way the exchange with the provider can fail;
	// which half failed is not the browser holder's business.
	ErrOIDCLogin = errors.New("auth: OIDC login could not be completed")

	// ErrOIDCUnreachable is separate because it is an operator problem, not a
	// caller problem: it maps to a 502 and belongs in the logs.
	ErrOIDCUnreachable = errors.New("auth: the OIDC provider could not be read")
)

// OriginError is a passkey ceremony refused before it began, because the
// origin the browser is on can never support one. Code tells the UI which of
// HTTPS, a domain name or WEBAUTHN_RP_ID to fix, instead of the browser's
// opaque SecurityError.
type OriginError struct {
	Code    string
	Message string
}

func (e *OriginError) Error() string { return fmt.Sprintf("auth: %s: %s", e.Code, e.Message) }

// The Code values an OriginError can carry. The frontend switches on these.
const (
	OriginInvalid  = "passkey_origin_invalid"
	OriginMissing  = "passkey_origin_missing"
	OriginIP       = "passkey_origin_ip"
	OriginInsecure = "passkey_origin_insecure"
	OriginMismatch = "passkey_origin_mismatch"
)

// isNotFound covers both this package's missing-row sentinel and
// internal/store's.
func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, store.ErrNotFound)
}

// now reads the clock a struct was configured with; nil is the real clock.
func now(hook func() time.Time) time.Time {
	if hook == nil {
		return time.Now()
	}
	return hook()
}
