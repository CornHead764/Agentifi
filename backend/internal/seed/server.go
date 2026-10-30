package seed

import (
	"context"
	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"time"
)

func seedServer(ctx context.Context, st *store.Store, user store.User, householdID string, today time.Time) error {
	if err := st.SetServerSettings(ctx, []string{"seed.dataset"}, map[string]string{"seed.dataset": "invented deterministic fixture"}); err != nil {
		return err
	}
	run, err := st.StartBackupRun(ctx, "manual", today)
	if err != nil {
		return err
	}
	run.Status = store.BackupRunSucceeded
	run.FinishedAt = &today
	run.SetName = "seed-backup"
	run.Bytes = 128
	if err := st.FinishBackupRun(ctx, run); err != nil {
		return err
	}
	if err := st.UpsertCatalogItem(ctx, &store.CatalogItem{Merchant: "amazon", SKU: "SEED-MUG", Status: "found", Title: "Café mug ☕", Brand: "Invented household goods", Price: money("45.45"), HasPrice: true, Source: "seed", LookedUpAt: today, FoundAt: &today}); err != nil {
		return err
	}
	if err := st.AddPasskey(ctx, store.Passkey{UserID: user.ID, CredentialID: []byte("seed-credential-not-valid-" + householdID), PublicKey: []byte("invented-public-key"), Name: "Seed nonfunctional passkey", RPID: "example.invalid", CreatedAt: today}); err != nil {
		return err
	}
	if err := st.ReplaceRecoveryCodes(ctx, user.ID, []string{auth.RecoveryDigest("seed-not-a-real-code")}); err != nil {
		return err
	}
	return st.RevokeToken(ctx, "seed-revoked-token", today.AddDate(10, 0, 0))
}
