package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

type fakeUsers struct {
	byEmail map[string]store.User
	err     error
}

func (f *fakeUsers) GetUserByEmail(_ context.Context, email string) (store.User, error) {
	if f.err != nil {
		return store.User{}, f.err
	}
	user, ok := f.byEmail[strings.ToLower(email)]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return user, nil
}

const testPassword = "correct horse battery"

func usersForLogin(t *testing.T) *fakeUsers {
	t.Helper()
	hashed, err := HashPassword(testPassword)
	require.NoError(t, err)

	return &fakeUsers{byEmail: map[string]store.User{
		"known@example.test":   {ID: uuid.New(), Email: "known@example.test", HashedPassword: hashed, IsActive: true},
		"off@example.test":     {ID: uuid.New(), Email: "off@example.test", HashedPassword: hashed, IsActive: false},
		"passkey@example.test": {ID: uuid.New(), Email: "passkey@example.test", IsActive: true},
	}}
}

func TestEveryFirstFactorFailureIsTheSameRefusal(t *testing.T) {
	// Wrong password, unknown address, disabled account, passkey-only account.
	// One error value, so any handler that maps it produces one response body.
	// Anything finer-grained is a free list of valid addresses.
	users := usersForLogin(t)

	attempts := []struct {
		name, email, password string
	}{
		{"wrong password", "known@example.test", "wrong"},
		{"unknown address", "nobody@example.test", testPassword},
		{"disabled account", "off@example.test", testPassword},
		{"passkey-only account", "passkey@example.test", testPassword},
	}
	for _, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			_, err := AuthenticatePassword(context.Background(), users, attempt.email, attempt.password)
			require.Equal(t, ErrInvalidCredentials, err, "the failures are distinguishable")
		})
	}
}

func TestACorrectPasswordLogsIn(t *testing.T) {
	users := usersForLogin(t)
	user, err := AuthenticatePassword(context.Background(), users, "known@example.test", testPassword)
	require.NoError(t, err)
	require.Equal(t, "known@example.test", user.Email)
}

func TestEmailCaseDoesNotDecideWhoCanLogIn(t *testing.T) {
	users := usersForLogin(t)
	_, err := AuthenticatePassword(context.Background(), users, " Known@Example.TEST ", testPassword)
	require.NoError(t, err)
}

func TestADatabaseFailureIsNotACredentialFailure(t *testing.T) {
	// Swallowing it would turn an outage into "everybody's password is wrong",
	// and the operator would go looking in the wrong place.
	broken := errors.New("connection refused")
	users := &fakeUsers{err: broken}

	_, err := AuthenticatePassword(context.Background(), users, "known@example.test", testPassword)
	require.ErrorIs(t, err, broken)
	require.NotErrorIs(t, err, ErrInvalidCredentials)
}
