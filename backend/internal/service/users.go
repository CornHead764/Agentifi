package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Accounts are made by the operator, from the CLI or the administration
// screen, and both go through CreateAccount. The one sign-up is the first
// account on a server that has none, through CreateFirstAccount.

var ErrEmailTaken = errors.New("service: that address already has an account")

// ErrNotFirstAccount is a first-account sign-up on a server that already has
// an account.
var ErrNotFirstAccount = errors.New("service: this server already has an account")

var ErrNoPlacement = errors.New(
	"service: a new account needs either a space to create or a space to join")

// Placement is where a new account's membership goes: join SpaceID with Role,
// or create SpaceName and own it. An account is never made with no membership,
// since it would authenticate and then see nothing.
type Placement struct {
	SpaceID *store.SpaceID
	Role    store.Role

	SpaceName string
	Currency  string
}

type NewAccount struct {
	Email    string
	FullName string
	// Password is plaintext; CreateAccount validates and hashes it.
	Password string
	// MustChangePassword makes the API refuse this user everything but reading
	// themselves and replacing the password.
	MustChangePassword bool
	IsSuperuser        bool
	Placement          Placement
}

// CreateAccount makes the user, its space and its membership in one
// transaction. The email is lowercased because the unique index is on
// lower(email). The membership is accepted on the spot, since a pending
// invitation grants nothing.
func CreateAccount(
	ctx context.Context, db *store.Store, spec NewAccount, now time.Time,
) (store.User, error) {
	return createAccount(ctx, db, spec, now, false)
}

// CreateFirstAccount is the sign-up a server with no accounts offers: the
// account administers the server and owns a new space. Once any account
// exists it is ErrNotFirstAccount, decided under the lock that makes two
// sign-ups at once one first account and one refusal.
func CreateFirstAccount(
	ctx context.Context, db *store.Store, spec NewAccount, now time.Time,
) (store.User, error) {
	// Before anything else, so a closed sign-up never answers whether an
	// address has an account.
	switch exists, err := db.HasUsers(ctx); {
	case err != nil:
		return store.User{}, err
	case exists:
		return store.User{}, ErrNotFirstAccount
	}
	spec.Placement.SpaceID = nil
	return createAccount(ctx, db, spec, now, true)
}

func createAccount(
	ctx context.Context, db *store.Store, spec NewAccount, now time.Time, onlyFirst bool,
) (store.User, error) {
	email := strings.ToLower(strings.TrimSpace(spec.Email))
	if email == "" {
		return store.User{}, errors.New("service: an email address is required")
	}
	if err := auth.ValidatePassword(spec.Password); err != nil {
		return store.User{}, err
	}
	if spec.Placement.SpaceID == nil && strings.TrimSpace(spec.Placement.SpaceName) == "" {
		return store.User{}, ErrNoPlacement
	}

	switch _, err := db.GetUserByEmail(ctx, email); {
	case err == nil:
		return store.User{}, fmt.Errorf("%w: %s", ErrEmailTaken, email)
	case !errors.Is(err, store.ErrNotFound):
		return store.User{}, err
	}

	hashed, err := auth.HashPassword(spec.Password)
	if err != nil {
		return store.User{}, err
	}

	name := strings.TrimSpace(spec.FullName)
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}

	var created store.User
	err = db.InTx(ctx, func(tx *store.Store) error {
		superuser := spec.IsSuperuser
		if onlyFirst {
			first, err := tx.ClaimFirstAccount(ctx)
			if err != nil {
				return err
			}
			if !first {
				return ErrNotFirstAccount
			}
			superuser = true
		}
		user := store.User{
			Email:              email,
			HashedPassword:     hashed,
			FullName:           name,
			IsActive:           true,
			IsVerified:         true,
			IsSuperuser:        superuser,
			MustChangePassword: spec.MustChangePassword,
		}
		if err := tx.CreateUser(ctx, &user); err != nil {
			return err
		}
		created = user

		spaceID := spec.Placement.SpaceID
		role := spec.Placement.Role
		if spaceID == nil {
			space := store.Space{
				Name:            strings.TrimSpace(spec.Placement.SpaceName),
				PrimaryCurrency: spec.Placement.Currency,
			}
			if err := tx.CreateSeededSpace(ctx, &space); err != nil {
				return err
			}
			spaceID = &space.ID
			role = store.RoleOwner
		}
		stamped := now.UTC()
		return tx.CreateMembership(ctx, *spaceID, &store.Membership{
			UserID:     user.ID,
			Role:       role,
			InvitedAt:  &stamped,
			AcceptedAt: &stamped,
		})
	})
	if err != nil {
		return store.User{}, err
	}
	return created, nil
}

// SetAccountPassword replaces somebody's password and signs their sessions out
// with a stored cutoff, since token revocation is per-process. The cutoff is
// the next second because `iat` is a Unix second.
func SetAccountPassword(
	ctx context.Context, db *store.Store,
	userID uuid.UUID, password string, mustChange bool, now time.Time,
) error {
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	hashed, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	cutoff := now.UTC().Truncate(time.Second).Add(time.Second)
	return db.SetUserPassword(ctx, userID, hashed, mustChange, cutoff)
}

// TemporaryPassword mints a one-time password to be replaced at first sign-in.
func TemporaryPassword() (string, error) {
	return auth.RandomToken(24)
}
