package service

import (
	"context"
	"log/slog"

	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// DeleteSpace snapshots external resource keys before retiring the household
// file and deleting server metadata. Retirement is retryable, not cross-file
// atomic; external cleanup runs only after successful metadata deletion.
func DeleteSpace(
	ctx context.Context, db *store.Store, storage provider.StorageProvider, bills *Bills,
	spaceID store.SpaceID,
) error {
	var keys []string
	var connections []store.BillConnection
	err := db.InHouseholdTx(ctx, spaceID, func(tx *store.Store) error {
		var err error
		if keys, err = tx.ListDocumentStorageKeys(ctx, spaceID); err != nil {
			return err
		}
		if connections, err = tx.ListBillConnections(ctx, spaceID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := db.DeleteSpace(ctx, spaceID); err != nil {
		return err
	}

	if storage != nil {
		for _, key := range keys {
			if err := storage.Delete(ctx, key); err != nil {
				slog.Warn("deleting a space left an attachment behind", "space", spaceID, "error", err)
			}
		}
	}
	if bills != nil {
		for _, connection := range connections {
			bills.forgetBrowser(ctx, connection)
		}
	}
	return nil
}
