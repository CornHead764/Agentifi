package service

import (
	"context"
	"log/slog"

	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// DeleteSpace removes a space and everything in it, for good: every row in one
// transaction, then what the rows pointed at outside the database, the
// attachment files and the bill providers' browser profiles. Those go after
// the commit, so a delete that fails leaves the space whole; one that cannot
// be removed is logged and left behind, unreachable.
func DeleteSpace(
	ctx context.Context, db *store.Store, storage provider.StorageProvider, bills *Bills,
	spaceID store.SpaceID,
) error {
	var keys []string
	var connections []store.BillConnection
	err := db.InTx(ctx, func(tx *store.Store) error {
		var err error
		if keys, err = tx.ListDocumentStorageKeys(ctx, spaceID); err != nil {
			return err
		}
		if connections, err = tx.ListBillConnections(ctx, spaceID); err != nil {
			return err
		}
		return tx.DeleteSpace(ctx, spaceID)
	})
	if err != nil {
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
