package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Time-based second factor and recovery codes.
//
//   - The secret is not written to the user row until a code proves it works,
//     or the account demands a factor nobody can produce. Only the caller of
//     TakePendingSecret writes the row.
//   - A code that verified once cannot verify again: the step it matched is
//     burned, since the drift window keeps a shoulder-surfed code live.
//   - Attempts are counted per user and window, spent by wrong answers.

// StepSeconds is one TOTP step. Every authenticator app assumes thirty.
const StepSeconds = 30

const DefaultEnrolmentTTL = 10 * time.Minute

// TOTP verifies second factors for one instance.
type TOTP struct {
	// Issuer is the label the authenticator app shows.
	Issuer string
	// ValidWindow is steps either side of the current one that still verify.
	ValidWindow int
	// MaxAttempts and AttemptWindow bound guessing, per user.
	MaxAttempts   int
	AttemptWindow time.Duration
	// EnrolmentTTL is zero for DefaultEnrolmentTTL.
	EnrolmentTTL time.Duration
	// RecoveryCodeCount is how many codes an enrolment mints.
	RecoveryCodeCount int
	State             *SecretStore
	Now               func() time.Time
}

// BeginEnrolment generates a secret and holds it aside until a code confirms
// it; it is not written to the user row here.
func (t *TOTP) BeginEnrolment(userID uuid.UUID, email string) (secret, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: t.issuer(), AccountName: email})
	if err != nil {
		return "", "", fmt.Errorf("auth: generating a TOTP secret: %w", err)
	}
	t.HoldPendingSecret(userID, key.Secret())
	return key.Secret(), key.URL(), nil
}

// ProvisioningURI rebuilds the otpauth:// URI for a secret already in hand.
func (t *TOTP) ProvisioningURI(secret, email string) (string, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("auth: the TOTP secret is not base32: %w", err)
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      t.issuer(),
		AccountName: email,
		Secret:      raw,
	})
	if err != nil {
		return "", fmt.Errorf("auth: building the provisioning URI: %w", err)
	}
	return key.URL(), nil
}

// HoldPendingSecret parks a secret that is not yet the user's, refreshing its
// expiry, so one mistyped confirmation does not restart the enrolment.
func (t *TOTP) HoldPendingSecret(userID uuid.UUID, secret string) {
	ttl := t.EnrolmentTTL
	if ttl <= 0 {
		ttl = DefaultEnrolmentTTL
	}
	t.State.Set(enrolmentKey(userID), secret, ttl)
}

// TakePendingSecret consumes the secret from an in-flight enrolment.
func (t *TOTP) TakePendingSecret(userID uuid.UUID) (string, bool) {
	return TakeAs[string](t.State, enrolmentKey(userID))
}

func (t *TOTP) CancelEnrolment(userID uuid.UUID) {
	t.State.Delete(enrolmentKey(userID))
}

// VerifyCode checks one code, spending an attempt and burning the step it
// matched. A user with no secret still costs an attempt and returns false, so
// this cannot reveal whether an account has a second factor.
func (t *TOTP) VerifyCode(userID uuid.UUID, secret, code string) (bool, error) {
	if t.State.Hit(attemptKey(userID), t.attemptWindow()) > t.maxAttempts() {
		return false, ErrTooManyAttempts
	}
	if secret == "" {
		return false, nil
	}

	step, matched := t.matchingStep(secret, normalizeDigits(code))
	if !matched {
		return false, nil
	}
	// Held for the whole span a code stays acceptable, so a replay cannot slip
	// in at the far edge of the window.
	ttl := time.Duration(StepSeconds*(2*t.ValidWindow+2)) * time.Second
	if !t.State.Claim(replayKey(userID, step), ttl) {
		return false, nil
	}
	t.State.Delete(attemptKey(userID))
	return true, nil
}

// matchingStep returns the step a code belongs to. Replay protection needs to
// know which step matched; totp.Validate only answers yes or no.
func (t *TOTP) matchingStep(secret, code string) (int64, bool) {
	moment := now(t.Now).Unix()
	opts := totp.ValidateOpts{
		Period:    StepSeconds,
		Skew:      0,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	}
	for offset := -t.ValidWindow; offset <= t.ValidWindow; offset++ {
		at := moment + int64(offset)*StepSeconds
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(at, 0), opts)
		if err != nil {
			return 0, false
		}
		if ConstantTimeEquals(expected, code) {
			return at / StepSeconds, true
		}
	}
	return 0, false
}

func (t *TOTP) issuer() string {
	if t.Issuer == "" {
		return "Agentifi"
	}
	return t.Issuer
}

func (t *TOTP) maxAttempts() int {
	if t.MaxAttempts <= 0 {
		return 5
	}
	return t.MaxAttempts
}

func (t *TOTP) attemptWindow() time.Duration {
	if t.AttemptWindow <= 0 {
		return 5 * time.Minute
	}
	return t.AttemptWindow
}

func enrolmentKey(userID uuid.UUID) string { return "totp_enrol:" + userID.String() }
func attemptKey(userID uuid.UUID) string   { return "totp_attempts:" + userID.String() }
func replayKey(userID uuid.UUID, step int64) string {
	return fmt.Sprintf("totp_step:%s:%d", userID, step)
}

// normalizeDigits strips the spaces authenticator apps put in the middle of a
// code and users copy along with it.
func normalizeDigits(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, code)
}

// --- Recovery codes ----------------------------------------------------------

// RecoveryAlphabet excludes the characters people misread when copying off a
// printout: no i, l, o, 0, 1.
const RecoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

const (
	recoveryGroups    = 4
	recoveryGroupSize = 4
)

// RecoveryCodeStore is persistence for the break-glass codes.
type RecoveryCodeStore interface {
	// ReplaceRecoveryCodes deletes the user's unused codes and inserts these
	// digests. Spent rows are left alone — see IssueRecoveryCodes.
	ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, digests []string) error
	CountUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error)
	// SpendRecoveryCode marks one unused code used, matching on the user and
	// the digest together. False means no such unused code.
	SpendRecoveryCode(ctx context.Context, userID uuid.UUID, digest string, usedAt time.Time) (bool, error)
	// DiscardRecoveryCodes retires the unused codes, keeping the spent ones.
	DiscardRecoveryCodes(ctx context.Context, userID uuid.UUID) error
}

// RecoveryDigest is what gets stored: SHA-256, not a password KDF. A recovery
// code is ~79 bits of machine-chosen randomness, so slowing guesses gains
// nothing, and a per-row KDF when matching is a self-inflicted DoS. The digest
// keeps a database copy from handing over working codes.
//
// Dashes, spaces and case are dropped first.
func RecoveryDigest(code string) string {
	normalized := strings.ToLower(strings.ReplaceAll(normalizeDigits(code), "-", ""))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// GenerateRecoveryCode returns one code: four groups of four characters from a
// 31-symbol alphabet, so a shade over 79 bits.
func GenerateRecoveryCode() (string, error) {
	groups := make([]string, recoveryGroups)
	for g := range groups {
		var group strings.Builder
		for range recoveryGroupSize {
			index, err := rand.Int(rand.Reader, big.NewInt(int64(len(RecoveryAlphabet))))
			if err != nil {
				return "", fmt.Errorf("auth: generating a recovery code: %w", err)
			}
			group.WriteByte(RecoveryAlphabet[index.Int64()])
		}
		groups[g] = group.String()
	}
	return strings.Join(groups, "-"), nil
}

// IssueRecoveryCodes replaces the user's unused codes and returns the plaintext
// once. Replacing rather than appending retires a compromised set; spent codes
// are kept so "already used" is answerable.
func (t *TOTP) IssueRecoveryCodes(ctx context.Context, codes RecoveryCodeStore, userID uuid.UUID) ([]string, error) {
	count := t.RecoveryCodeCount
	if count <= 0 {
		count = 10
	}
	plain := make([]string, count)
	digests := make([]string, count)
	for i := range plain {
		code, err := GenerateRecoveryCode()
		if err != nil {
			return nil, err
		}
		plain[i] = code
		digests[i] = RecoveryDigest(code)
	}
	if err := codes.ReplaceRecoveryCodes(ctx, userID, digests); err != nil {
		return nil, err
	}
	return plain, nil
}

// RemainingRecoveryCodes is how many are left unused.
func (t *TOTP) RemainingRecoveryCodes(ctx context.Context, codes RecoveryCodeStore, userID uuid.UUID) (int, error) {
	return codes.CountUnusedRecoveryCodes(ctx, userID)
}

// ConsumeRecoveryCode spends one code. The lookup is by digest and user, and
// attempts share TOTP's per-user counter, since the two are alternatives at the
// same prompt.
func (t *TOTP) ConsumeRecoveryCode(ctx context.Context, codes RecoveryCodeStore, userID uuid.UUID, code string) (bool, error) {
	if t.State.Hit(attemptKey(userID), t.attemptWindow()) > t.maxAttempts() {
		return false, ErrTooManyAttempts
	}
	spent, err := codes.SpendRecoveryCode(ctx, userID, RecoveryDigest(code), now(t.Now))
	if err != nil {
		return false, err
	}
	if !spent {
		return false, nil
	}
	t.State.Delete(attemptKey(userID))
	return true, nil
}

// SubmitSecondFactor is one submission at the second-factor prompt: a TOTP code
// or a recovery code, tried in that order and metered once.
func (t *TOTP) SubmitSecondFactor(
	ctx context.Context, codes RecoveryCodeStore, userID uuid.UUID, secret, code string,
) (bool, error) {
	if t.State.Hit(attemptKey(userID), t.attemptWindow()) > t.maxAttempts() {
		return false, ErrTooManyAttempts
	}

	accepted, err := t.verifyMetered(userID, secret, code)
	if err != nil || accepted {
		return accepted, err
	}
	spent, err := codes.SpendRecoveryCode(ctx, userID, RecoveryDigest(code), now(t.Now))
	if err != nil {
		return false, err
	}
	if !spent {
		return false, nil
	}
	t.State.Delete(attemptKey(userID))
	return true, nil
}

// verifyMetered is VerifyCode without its own attempt charge: the caller has
// already metered this submission.
func (t *TOTP) verifyMetered(userID uuid.UUID, secret, code string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	step, matched := t.matchingStep(secret, normalizeDigits(code))
	if !matched {
		return false, nil
	}
	// Held for the whole span a code stays acceptable, so a replay cannot slip
	// in at the far edge of the window.
	ttl := time.Duration(StepSeconds*(2*t.ValidWindow+2)) * time.Second
	if !t.State.Claim(replayKey(userID, step), ttl) {
		return false, nil
	}
	t.State.Delete(attemptKey(userID))
	return true, nil
}

// DiscardRecoveryCodes retires the unused codes, for when the second factor is
// turned off.
func (t *TOTP) DiscardRecoveryCodes(ctx context.Context, codes RecoveryCodeStore, userID uuid.UUID) error {
	return codes.DiscardRecoveryCodes(ctx, userID)
}
