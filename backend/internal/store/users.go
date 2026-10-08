package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"
)

// User is a login. Not space-scoped (one person, several spaces), so these
// methods take no SpaceID.
type User struct {
	ID    uuid.UUID
	Email string
	// HashedPassword is empty for passkey- or OIDC-only accounts, stored NULL
	// rather than "".
	HashedPassword string
	FullName       string
	// OIDCSubject is unique only within an issuer; match on the pair.
	OIDCSubject string
	OIDCIssuer  string
	TOTPSecret  string

	IsActive    bool
	IsSuperuser bool
	IsVerified  bool
	// MustChangePassword is set when someone other than the holder chose the
	// password, as for every new account. The API refuses such a caller
	// everything but reading themselves and replacing the password.
	MustChangePassword bool
	// SessionsValidFrom rejects every token issued before it; nil (never set)
	// is not the same as a zero time.
	SessionsValidFrom *time.Time

	Locale      string
	Theme       string
	PrivacyMode bool
	// Swipe actions on a register row, in the frontend's action names;
	// internal/api validates them.
	SwipeLeftAction  string
	SwipeRightAction string
	// AnimationDurationMs: zero is no animation. New accounts take the
	// column's default, which CreateUser reads back.
	AnimationDurationMs int
	// ToastDurationMs: how long a toast stays before it dismisses itself.
	// New accounts take the column's default, which CreateUser reads back.
	ToastDurationMs int

	LastLoginAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const userColumns = `id, email, hashed_password, full_name, oidc_subject, oidc_issuer,
	totp_secret, is_active, is_superuser, is_verified, must_change_password,
	sessions_valid_from, locale, theme, privacy_mode, swipe_left_action, swipe_right_action,
	animation_duration_ms, toast_duration_ms, last_login_at, created_at, updated_at`

func (s *Store) CreateUser(ctx context.Context, user *User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	// Normalized so the unique index and lookup on lower(email) agree.
	user.Email = strings.ToLower(strings.TrimSpace(user.Email))
	if user.Locale == "" {
		user.Locale = "en"
	}
	if user.Theme == "" {
		user.Theme = "system"
	}
	if user.SwipeLeftAction == "" {
		user.SwipeLeftAction = "menu"
	}
	if user.SwipeRightAction == "" {
		user.SwipeRightAction = "review"
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO users (id, email, hashed_password, full_name, oidc_subject, oidc_issuer,
			totp_secret, is_active, is_superuser, is_verified, must_change_password,
			sessions_valid_from, locale, theme, privacy_mode, swipe_left_action,
			swipe_right_action, last_login_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING created_at, updated_at, animation_duration_ms, toast_duration_ms`,
		user.ID, user.Email, dbconv.NullText(user.HashedPassword), dbconv.NullText(user.FullName),
		dbconv.NullText(user.OIDCSubject), dbconv.NullText(user.OIDCIssuer), dbconv.NullText(user.TOTPSecret),
		user.IsActive, user.IsSuperuser, user.IsVerified, user.MustChangePassword,
		user.SessionsValidFrom, user.Locale, user.Theme, user.PrivacyMode,
		user.SwipeLeftAction, user.SwipeRightAction, user.LastLoginAt,
		// The animation and toast durations take the column defaults: zero is
		// a valid animation, so an unset field cannot mean "use the default".
	).Scan(&user.CreatedAt, &user.UpdatedAt, &user.AnimationDurationMs, &user.ToastDurationMs)
	return wrap("store: create user", err)
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	user, err := scanUser(row)
	return user, wrap("store: get user", err)
}

// GetUserByEmail matches case-insensitively, so one mailbox is one account.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`, email)
	user, err := scanUser(row)
	return user, wrap("store: get user by email", err)
}

// ListUsers is every account on the install, oldest first — a deployment
// question, served only behind a Superuser route.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	return queryAll(ctx, s.db, "store: list users", scanUser, `SELECT `+userColumns+` FROM users ORDER BY created_at`)
}

// HasUsers reports whether any account exists, which is the whole of what a
// caller with no session may learn about the accounts on this server.
func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists)
	return exists, wrap("store: has users", err)
}

// ClaimFirstAccount reports whether the account this transaction is about to
// create is the server's first. The transaction holds the write lock from
// its start, so two first accounts created at once cannot both find the table
// empty.
func (s *Store) ClaimFirstAccount(ctx context.Context) (bool, error) {
	if s.tx == nil {
		return false, errors.New("store: claiming the first account needs a transaction")
	}
	var empty bool
	err := s.db.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM users)`).Scan(&empty)
	return empty, wrap("store: claim first account", err)
}

// CountActiveSuperusers counts the accounts that can administer this server,
// excluding one: "if this one stops counting, is anybody left?"
func (s *Store) CountActiveSuperusers(ctx context.Context, excluding uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE is_superuser AND is_active AND id <> $1`,
		excluding).Scan(&count)
	return count, wrap("store: count superusers", err)
}

// GetUserByOIDC matches on issuer and subject together, never subject alone.
func (s *Store) GetUserByOIDC(ctx context.Context, issuer, subject string) (User, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE oidc_issuer = $1 AND oidc_subject = $2`,
		issuer, subject)
	user, err := scanUser(row)
	return user, wrap("store: get user by oidc identity", err)
}

// --- Narrow writes -----------------------------------------------------------

// Every write names only the columns its caller means to change, so two
// callers writing different columns cannot undo each other — e.g. a login
// stamping last_login_at must not restore an old hash over a concurrent
// password change.

// UserProfile is what a person may change about themselves. Deliberately not
// the password, second factor, role or is_active.
type UserProfile struct {
	FullName    string
	Locale      string
	Theme       string
	PrivacyMode bool
	// Swipe actions on a register row; internal/api validates them.
	SwipeLeftAction  string
	SwipeRightAction string
	// Milliseconds; zero is no animation. Bounded by internal/api.
	AnimationDurationMs int
	// Milliseconds a toast stays before it dismisses itself. Bounded by internal/api.
	ToastDurationMs int
}

func (s *Store) UpdateUserProfile(ctx context.Context, id uuid.UUID, profile UserProfile) error {
	return s.setUserColumns(ctx, "update user profile", id,
		`full_name = $2, locale = $3, theme = $4, privacy_mode = $5,
			swipe_left_action = $6, swipe_right_action = $7, animation_duration_ms = $8,
			toast_duration_ms = $9`,
		dbconv.NullText(profile.FullName), profile.Locale, profile.Theme, profile.PrivacyMode,
		profile.SwipeLeftAction, profile.SwipeRightAction, profile.AnimationDurationMs,
		profile.ToastDurationMs)
}

// TouchLastLogin stamps a successful login and touches nothing else.
func (s *Store) TouchLastLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	return s.setUserColumns(ctx, "stamp last login", id, `last_login_at = $2`, at)
}

// SetUserPassword replaces the hash and ends every session issued before the
// cutoff, together, so a password change signs out a stolen session.
func (s *Store) SetUserPassword(
	ctx context.Context, id uuid.UUID, hashed string, mustChange bool, cutoff time.Time,
) error {
	return s.setUserColumns(ctx, "set user password", id,
		`hashed_password = $2, must_change_password = $3, sessions_valid_from = $4`,
		dbconv.NullText(hashed), mustChange, cutoff)
}

// LinkOIDCIdentity attaches a provider identity to an existing account.
func (s *Store) LinkOIDCIdentity(ctx context.Context, id uuid.UUID, issuer, subject string) error {
	return s.setUserColumns(ctx, "link oidc identity", id,
		`oidc_issuer = $2, oidc_subject = $3`, dbconv.NullText(issuer), dbconv.NullText(subject))
}

func (s *Store) SetUserActive(ctx context.Context, id uuid.UUID, active bool) error {
	return s.setUserColumns(ctx, "set user active", id, `is_active = $2`, active)
}

// SetUserFullName writes the display name only, for an administrator's edit.
func (s *Store) SetUserFullName(ctx context.Context, id uuid.UUID, name string) error {
	return s.setUserColumns(ctx, "set user name", id, `full_name = $2`, dbconv.NullText(name))
}

// ErrLastSuperuser is a change that would leave no active account able to
// administer the server, which only a shell could then undo.
var ErrLastSuperuser = errors.New("store: the last active superuser")

// SetUserSuperuser grants or takes away the right to administer the server.
// Taking it from the last active superuser is ErrLastSuperuser. The decision
// runs inside a write transaction, so two demotions at once cannot each leave
// the other as the last.
func (s *Store) SetUserSuperuser(ctx context.Context, id uuid.UUID, superuser bool) error {
	if superuser {
		return s.setUserColumns(ctx, "set user superuser", id, `is_superuser = $2`, true)
	}
	return s.InTx(ctx, func(tx *Store) error {
		holders, err := queryAll(ctx, tx.db, "store: list superusers", func(row scanner) (uuid.UUID, error) {
			var held uuid.UUID
			err := row.Scan(&held)
			return held, err
		}, `SELECT id FROM users WHERE is_superuser AND is_active ORDER BY id`)
		if err != nil {
			return err
		}
		if len(holders) == 1 && holders[0] == id {
			return fmt.Errorf("store: set user superuser: %w", ErrLastSuperuser)
		}
		return tx.setUserColumns(ctx, "set user superuser", id, `is_superuser = $2`, false)
	})
}

// setUserColumns is the UPDATE shape the narrow writes share: the id is $1,
// and a missing row is ErrNotFound.
func (s *Store) setUserColumns(
	ctx context.Context, what string, id uuid.UUID, assignments string, args ...any,
) error {
	return s.execOne(ctx, "store: "+what,
		`UPDATE users SET `+assignments+`, updated_at = now() WHERE id = $1`,
		append([]any{id}, args...)...)
}

func scanUser(row scanner) (User, error) {
	var user User
	var password, fullName, subject, issuer, totp *string
	err := row.Scan(&user.ID, &user.Email, &password, &fullName, &subject, &issuer, &totp,
		&user.IsActive, &user.IsSuperuser, &user.IsVerified, &user.MustChangePassword,
		&user.SessionsValidFrom, &user.Locale, &user.Theme, &user.PrivacyMode,
		&user.SwipeLeftAction, &user.SwipeRightAction, &user.AnimationDurationMs,
		&user.ToastDurationMs, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return User{}, err
	}
	user.HashedPassword = Deref(password)
	user.FullName = Deref(fullName)
	user.OIDCSubject = Deref(subject)
	user.OIDCIssuer = Deref(issuer)
	// A sealed payload; only OpenTOTPSecret reads a seed.
	user.TOTPSecret = Deref(totp)
	return user, nil
}

// SetTOTPSecret writes the second-factor seed, sealed at rest (verification
// needs the plaintext, so it cannot be digested). An empty secret clears it.
func (s *Store) SetTOTPSecret(ctx context.Context, id uuid.UUID, secret string) error {
	if _, err := s.requireCipher(); err != nil {
		return err
	}
	// Cleared through NULL rather than an empty sealed payload.
	var value any
	if secret != "" {
		sealed, err := s.sealString(totpSecretContext(id), secret)
		if err != nil {
			return err
		}
		value = sealed
	}
	return s.execOne(ctx, "store: set totp secret",
		`UPDATE users SET totp_secret = $2, updated_at = now() WHERE id = $1`, id, value)
}

// OpenTOTPSecret returns the seed for verification, or "" when none is set.
func (s *Store) OpenTOTPSecret(ctx context.Context, id uuid.UUID) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	var stored *string
	err = s.db.QueryRow(ctx,
		`SELECT totp_secret FROM users WHERE id = $1`, id).Scan(&stored)
	if err != nil {
		return "", wrap("store: read totp secret", err)
	}
	if stored == nil {
		return "", nil
	}
	return cipher.Open(totpSecretContext(id), *stored)
}
