package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

func newTOTP(clock *fakeClock) *TOTP {
	state := NewSecretStore()
	state.Now = clock.now
	return &TOTP{
		Issuer:            "Agentifi",
		ValidWindow:       1,
		MaxAttempts:       5,
		AttemptWindow:     5 * time.Minute,
		RecoveryCodeCount: 10,
		State:             state,
		Now:               clock.now,
	}
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period:    StepSeconds,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	require.NoError(t, err)
	return code
}

func TestEnrolmentDoesNotArmTheSecondFactorUntilItIsConfirmed(t *testing.T) {
	// The half-enrolled state is the one that locks people out of their own
	// account: a user row demanding a code nobody can produce. The secret goes
	// nowhere near the row until a code minted from it verifies, and this
	// package cannot write the row at all — only hand the secret back.
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	userID := uuid.New()

	secret, uri, err := second.BeginEnrolment(userID, "alex@example.test")
	require.NoError(t, err)
	require.NotEmpty(t, secret)
	require.Contains(t, uri, "otpauth://totp/")
	require.Contains(t, uri, "issuer=Agentifi")

	pending, ok := second.TakePendingSecret(userID)
	require.True(t, ok)
	require.Equal(t, secret, pending)

	_, ok = second.TakePendingSecret(userID)
	require.False(t, ok, "the pending secret is consumed, not left behind")
}

func TestAnAbandonedEnrolmentExpires(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	second.EnrolmentTTL = 10 * time.Minute
	userID := uuid.New()

	_, _, err := second.BeginEnrolment(userID, "alex@example.test")
	require.NoError(t, err)

	clock.advance(11 * time.Minute)
	_, ok := second.TakePendingSecret(userID)
	require.False(t, ok)
}

func TestACodeCannotBeReplayedInsideItsDriftWindow(t *testing.T) {
	// valid_window accepts the neighbouring steps for clock drift, so a code
	// read over someone's shoulder stays live for a minute and a half unless
	// the step it matched is burned on use.
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	userID := uuid.New()
	secret, _, err := second.BeginEnrolment(userID, "alex@example.test")
	require.NoError(t, err)

	code := codeAt(t, secret, clock.at)
	accepted, err := second.VerifyCode(userID, secret, code)
	require.NoError(t, err)
	require.True(t, accepted)

	accepted, err = second.VerifyCode(userID, secret, code)
	require.NoError(t, err)
	require.False(t, accepted, "the matched step was not burned")

	// And still refused from the far edge of the window, where a replay would
	// otherwise slip in.
	clock.advance(StepSeconds * time.Second)
	accepted, err = second.VerifyCode(userID, secret, code)
	require.NoError(t, err)
	require.False(t, accepted)
}

func TestANeighbouringStepStillVerifies(t *testing.T) {
	// Phones drift. One step either side, and no more.
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	userID := uuid.New()
	secret, _, err := second.BeginEnrolment(userID, "alex@example.test")
	require.NoError(t, err)

	accepted, err := second.VerifyCode(userID, secret, codeAt(t, secret, clock.at.Add(-StepSeconds*time.Second)))
	require.NoError(t, err)
	require.True(t, accepted)

	accepted, err = second.VerifyCode(userID, secret, codeAt(t, secret, clock.at.Add(3*StepSeconds*time.Second)))
	require.NoError(t, err)
	require.False(t, accepted, "three steps out is not drift, it is a stale code")
}

func TestGuessingIsCappedPerUserPerWindow(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	second.MaxAttempts = 3
	userID := uuid.New()
	secret, _, err := second.BeginEnrolment(userID, "alex@example.test")
	require.NoError(t, err)

	for range 3 {
		accepted, err := second.VerifyCode(userID, secret, "000000")
		require.NoError(t, err)
		require.False(t, accepted)
	}
	_, err = second.VerifyCode(userID, secret, "000000")
	require.ErrorIs(t, err, ErrTooManyAttempts)

	// Even a correct code is refused once the budget is spent, so a
	// locked-out attacker cannot hide behind one.
	_, err = second.VerifyCode(userID, secret, codeAt(t, secret, clock.at))
	require.ErrorIs(t, err, ErrTooManyAttempts)

	clock.advance(6 * time.Minute)
	accepted, err := second.VerifyCode(userID, secret, codeAt(t, secret, clock.at))
	require.NoError(t, err)
	require.True(t, accepted)
}

func TestAUserWithNoSecretStillCostsAnAttempt(t *testing.T) {
	// Otherwise this endpoint answers "does that account have a second
	// factor", which is the same oracle by another route.
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	second.MaxAttempts = 2
	userID := uuid.New()

	for range 2 {
		accepted, err := second.VerifyCode(userID, "", "000000")
		require.NoError(t, err)
		require.False(t, accepted)
	}
	_, err := second.VerifyCode(userID, "", "000000")
	require.ErrorIs(t, err, ErrTooManyAttempts)
}

func TestACorrectCodeClearsTheCounter(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	second.MaxAttempts = 3
	userID := uuid.New()
	secret, _, err := second.BeginEnrolment(userID, "alex@example.test")
	require.NoError(t, err)

	for range 2 {
		_, err := second.VerifyCode(userID, secret, "000000")
		require.NoError(t, err)
	}
	accepted, err := second.VerifyCode(userID, secret, codeAt(t, secret, clock.at))
	require.NoError(t, err)
	require.True(t, accepted)

	// Back to a full budget rather than one attempt from lockout.
	for range 3 {
		_, err := second.VerifyCode(userID, secret, "000000")
		require.NoError(t, err)
	}
}

func TestASpacedOutCodeIsStillTheCode(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	userID := uuid.New()
	secret, _, err := second.BeginEnrolment(userID, "alex@example.test")
	require.NoError(t, err)

	code := codeAt(t, secret, clock.at)
	accepted, err := second.VerifyCode(userID, secret, code[:3]+" "+code[3:])
	require.NoError(t, err)
	require.True(t, accepted)
}

// --- Recovery codes -----------------------------------------------------------

func TestARecoveryCodeWorksOnce(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	codes := &MemoryRecoveryCodes{}
	userID := uuid.New()

	issued, err := second.IssueRecoveryCodes(context.Background(), codes, userID)
	require.NoError(t, err)
	require.Len(t, issued, 10)

	spent, err := second.ConsumeRecoveryCode(context.Background(), codes, userID, issued[0])
	require.NoError(t, err)
	require.True(t, spent)

	spent, err = second.ConsumeRecoveryCode(context.Background(), codes, userID, issued[0])
	require.NoError(t, err)
	require.False(t, spent)

	remaining, err := second.RemainingRecoveryCodes(context.Background(), codes, userID)
	require.NoError(t, err)
	require.Equal(t, 9, remaining)
}

func TestReissuingReplacesTheUnusedCodesAndKeepsTheSpentOnes(t *testing.T) {
	// After a phone is lost the old set has to stop working, and "here are ten
	// more" leaves the compromised ones live. Spent rows stay so that "you
	// have already used that one" is still answerable.
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	codes := &MemoryRecoveryCodes{}
	userID := uuid.New()

	first, err := second.IssueRecoveryCodes(context.Background(), codes, userID)
	require.NoError(t, err)
	spent, err := second.ConsumeRecoveryCode(context.Background(), codes, userID, first[0])
	require.NoError(t, err)
	require.True(t, spent)

	if _, err := second.IssueRecoveryCodes(context.Background(), codes, userID); err != nil {
		require.NoError(t, err)
	}

	stillWorks, err := second.ConsumeRecoveryCode(context.Background(), codes, userID, first[1])
	require.NoError(t, err)
	require.False(t, stillWorks, "an old unused code survived the reissue")

	require.Len(t, codes.codes, 11, "the spent row was dropped along with the unused ones")
}

func TestOneUsersRecoveryCodeCannotBeSpentAgainstAnothersAccount(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	codes := &MemoryRecoveryCodes{}
	mine, theirs := uuid.New(), uuid.New()

	issued, err := second.IssueRecoveryCodes(context.Background(), codes, mine)
	require.NoError(t, err)
	_, err = second.IssueRecoveryCodes(context.Background(), codes, theirs)
	require.NoError(t, err)

	spent, err := second.ConsumeRecoveryCode(context.Background(), codes, theirs, issued[0])
	require.NoError(t, err)
	require.False(t, spent)
}

func TestARecoveryCodeIsStoredAsADigestAndReadForgivingly(t *testing.T) {
	code, err := GenerateRecoveryCode()
	require.NoError(t, err)
	require.Len(t, code, 19, "four groups of four, three dashes")
	require.NotContains(t, RecoveryAlphabet, "l")
	require.NotContains(t, RecoveryAlphabet, "0")

	digest := RecoveryDigest(code)
	require.Len(t, digest, 64, "SHA-256 as hex")
	require.NotContains(t, digest, strings.ReplaceAll(code, "-", ""))

	// Typed off a printout: case, dashes and spaces are noise.
	require.Equal(t, digest, RecoveryDigest(strings.ToUpper(code)))
	require.Equal(t, digest, RecoveryDigest(strings.ReplaceAll(code, "-", " ")))
}

func TestRecoveryGuessingIsCappedOnTheSameCounterAsCodes(t *testing.T) {
	// The two are alternatives at the same prompt; metering them separately
	// would double the budget.
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	second.MaxAttempts = 2
	codes := &MemoryRecoveryCodes{}
	userID := uuid.New()

	_, err := second.IssueRecoveryCodes(context.Background(), codes, userID)
	require.NoError(t, err)

	_, err = second.VerifyCode(userID, "", "000000")
	require.NoError(t, err)
	_, err = second.ConsumeRecoveryCode(context.Background(), codes, userID, "aaaa-bbbb-cccc-dddd")
	require.NoError(t, err)
	_, err = second.ConsumeRecoveryCode(context.Background(), codes, userID, "aaaa-bbbb-cccc-dddd")
	require.ErrorIs(t, err, ErrTooManyAttempts)
}

func TestDiscardingRetiresTheUnusedCodes(t *testing.T) {
	clock := &fakeClock{at: time.Now()}
	second := newTOTP(clock)
	codes := &MemoryRecoveryCodes{}
	userID := uuid.New()

	issued, err := second.IssueRecoveryCodes(context.Background(), codes, userID)
	require.NoError(t, err)
	require.NoError(t, second.DiscardRecoveryCodes(context.Background(), codes, userID))

	spent, err := second.ConsumeRecoveryCode(context.Background(), codes, userID, issued[0])
	require.NoError(t, err)
	require.False(t, spent)
}
