package api

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// storePasskeys adapts internal/store's passkey queries to auth.PasskeyStore.
// internal/auth imports internal/store, so the store cannot name auth.Passkey
// itself. *store.Store satisfies auth.RecoveryCodeStore directly.
type storePasskeys struct{ db *store.Store }

func (p storePasskeys) ListPasskeys(ctx context.Context, userID uuid.UUID) ([]auth.Passkey, error) {
	rows, err := p.db.ListPasskeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	keys := make([]auth.Passkey, len(rows))
	for i, row := range rows {
		keys[i] = authPasskey(row)
	}
	return keys, nil
}

// GetPasskeyByCredential passes store.ErrNotFound through unchanged: the
// ceremony's not-found check accepts either package's sentinel.
func (p storePasskeys) GetPasskeyByCredential(ctx context.Context, credentialID []byte) (auth.Passkey, error) {
	row, err := p.db.GetPasskeyByCredential(ctx, credentialID)
	if err != nil {
		return auth.Passkey{}, err
	}
	return authPasskey(row), nil
}

func (p storePasskeys) AddPasskey(ctx context.Context, key auth.Passkey) error {
	return p.db.AddPasskey(ctx, store.Passkey{
		ID:             key.ID,
		UserID:         key.UserID,
		CredentialID:   key.CredentialID,
		PublicKey:      key.PublicKey,
		SignCount:      key.SignCount,
		Name:           key.Name,
		Transports:     key.Transports,
		RPID:           key.RPID,
		IsDiscoverable: key.IsDiscoverable,
		BackupEligible: key.BackupEligible,
		CreatedAt:      key.CreatedAt,
		LastUsedAt:     key.LastUsedAt,
	})
}

func (p storePasskeys) UpdatePasskeyUse(ctx context.Context, id uuid.UUID, signCount uint32, backupEligible bool, usedAt time.Time) error {
	return p.db.UpdatePasskeyUse(ctx, id, signCount, backupEligible, usedAt)
}

func (p storePasskeys) DeletePasskey(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	return p.db.DeletePasskey(ctx, userID, id)
}

func authPasskey(row store.Passkey) auth.Passkey {
	return auth.Passkey{
		ID:             row.ID,
		UserID:         row.UserID,
		CredentialID:   row.CredentialID,
		PublicKey:      row.PublicKey,
		SignCount:      row.SignCount,
		Name:           row.Name,
		Transports:     row.Transports,
		RPID:           row.RPID,
		IsDiscoverable: row.IsDiscoverable,
		BackupEligible: row.BackupEligible,
		CreatedAt:      row.CreatedAt,
		LastUsedAt:     row.LastUsedAt,
	}
}

// Asserted here because this is the only package that can name both sides:
// internal/store cannot mention auth.Passkey without an import cycle.
var _ auth.PasskeyStore = storePasskeys{}
