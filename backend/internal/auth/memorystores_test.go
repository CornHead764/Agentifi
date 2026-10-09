package auth

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryPasskeys is a process-local PasskeyStore for tests with no database.
type MemoryPasskeys struct {
	mu   sync.Mutex
	keys []Passkey
}

func (m *MemoryPasskeys) ListPasskeys(_ context.Context, userID uuid.UUID) ([]Passkey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Passkey
	for _, key := range m.keys {
		if key.UserID == userID {
			out = append(out, key)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryPasskeys) GetPasskeyByCredential(_ context.Context, credentialID []byte) (Passkey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range m.keys {
		if bytes.Equal(key.CredentialID, credentialID) {
			return key, nil
		}
	}
	return Passkey{}, ErrNotFound
}

func (m *MemoryPasskeys) AddPasskey(_ context.Context, key Passkey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys = append(m.keys, key)
	return nil
}

func (m *MemoryPasskeys) UpdatePasskeyUse(_ context.Context, id uuid.UUID, signCount uint32, backupEligible bool, usedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.keys {
		if m.keys[i].ID == id {
			m.keys[i].SignCount = signCount
			m.keys[i].BackupEligible = &backupEligible
			m.keys[i].LastUsedAt = &usedAt
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemoryPasskeys) DeletePasskey(_ context.Context, userID, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, key := range m.keys {
		if key.ID == id && key.UserID == userID {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

var _ PasskeyStore = (*MemoryPasskeys)(nil)

// MemoryRecoveryCodes is a process-local RecoveryCodeStore for tests with no
// database.
type MemoryRecoveryCodes struct {
	mu    sync.Mutex
	codes []memoryRecoveryCode
}

type memoryRecoveryCode struct {
	userID uuid.UUID
	digest string
	usedAt *time.Time
}

func (m *MemoryRecoveryCodes) ReplaceRecoveryCodes(_ context.Context, userID uuid.UUID, digests []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.codes[:0]
	for _, code := range m.codes {
		if code.userID != userID || code.usedAt != nil {
			kept = append(kept, code)
		}
	}
	m.codes = kept
	for _, digest := range digests {
		m.codes = append(m.codes, memoryRecoveryCode{userID: userID, digest: digest})
	}
	return nil
}

func (m *MemoryRecoveryCodes) CountUnusedRecoveryCodes(_ context.Context, userID uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, code := range m.codes {
		if code.userID == userID && code.usedAt == nil {
			count++
		}
	}
	return count, nil
}

func (m *MemoryRecoveryCodes) SpendRecoveryCode(_ context.Context, userID uuid.UUID, digest string, usedAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.codes {
		code := &m.codes[i]
		if code.userID == userID && code.usedAt == nil && ConstantTimeEquals(code.digest, digest) {
			code.usedAt = &usedAt
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryRecoveryCodes) DiscardRecoveryCodes(_ context.Context, userID uuid.UUID) error {
	return m.ReplaceRecoveryCodes(context.Background(), userID, nil)
}

var _ RecoveryCodeStore = (*MemoryRecoveryCodes)(nil)
