package auth

import (
	"context"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// UserFinder is the slice of internal/store a password login needs.
type UserFinder interface {
	GetUserByEmail(ctx context.Context, email string) (store.User, error)
}

// AuthenticatePassword checks an email and password.
//
// Every credential failure returns ErrInvalidCredentials, and the
// unknown-address and no-password paths still pay for a bcrypt comparison, so
// neither the status nor a stopwatch enumerates accounts. A database error is
// returned as itself.
func AuthenticatePassword(ctx context.Context, users UserFinder, email, password string) (store.User, error) {
	user, err := users.GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		DummyVerify()
		if isNotFound(err) {
			return store.User{}, ErrInvalidCredentials
		}
		return store.User{}, err
	}
	if !VerifyPassword(password, user.HashedPassword) {
		return store.User{}, ErrInvalidCredentials
	}
	if !user.IsActive {
		return store.User{}, ErrInvalidCredentials
	}
	return user, nil
}
