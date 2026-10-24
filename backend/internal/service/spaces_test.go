package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// importedSpace is a space filled from the invented Simplifi export, which
// reaches nearly every table, the restricted ones among them.
func importedSpace(t *testing.T) store.SpaceID {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "importer", "testdata", "invented-export.json"))
	require.NoError(t, err)
	export, err := importer.ParseExport(raw)
	require.NoError(t, err)
	out, err := importer.MapExport(export, "", importer.Options{
		OwnerEmail: "owner-" + uuid.NewString()[:8] + "@example.test",
	})
	require.NoError(t, err)
	require.True(t, out.Report.OK(), out.Report.Render())
	out.Space.Name += " " + uuid.NewString()[:8]
	require.NoError(t, importer.Write(t.Context(), db(t), out))
	return out.SpaceID
}

// spaceRows counts every household table and the server membership rows.
func spaceRows(t *testing.T, space store.SpaceID) map[string]int {
	t.Helper()
	ctx := t.Context()
	counts := map[string]int{}
	if _, err := db(t).GetSpace(ctx, space); errors.Is(err, store.ErrNotFound) {
		h, err := db(t).ServerDB()
		require.NoError(t, err)
		var count int
		require.NoError(t, h.Read().QueryRowContext(ctx, `SELECT count(*) FROM memberships WHERE household_id = ?`, space.UUID()).Scan(&count))
		require.Zero(t, count)
		return counts
	}
	h := householdSQL(t, space)
	tables, err := h.QueryContext(ctx, `
		SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	require.NoError(t, err)
	var names []string
	for tables.Next() {
		var name string
		require.NoError(t, tables.Scan(&name))
		names = append(names, name)
	}
	tables.Close()
	require.NotEmpty(t, names)

	server, err := db(t).ServerDB()
	require.NoError(t, err)
	var memberships int
	require.NoError(t, server.Read().QueryRowContext(ctx, `SELECT count(*) FROM memberships WHERE household_id = ?`, space.UUID()).Scan(&memberships))
	if memberships > 0 {
		counts["memberships"] = memberships
	}
	for _, name := range names {
		var count int
		require.NoError(t, h.QueryRowContext(ctx,
			`SELECT count(*) FROM "`+name+`"`).Scan(&count))
		if count > 0 {
			counts[name] = count
		}
	}
	return counts
}

func TestDeletingASpaceRemovesEverythingInItAndNothingElse(t *testing.T) {
	ctx := t.Context()
	doomed, kept := importedSpace(t), importedSpace(t)

	// What lives outside the database: an uploaded file and a bill provider's
	// browser, in each space.
	storage := &provider.LocalStorage{BasePath: t.TempDir()}
	docs := NewDocuments(db(t), storage)
	agent := &fakeBillsAgent{}
	bills := NewBills(db(t))
	bills.Agent = agent
	files := map[store.SpaceID]string{}
	connections := map[store.SpaceID]uuid.UUID{}
	for _, space := range []store.SpaceID{doomed, kept} {
		account := newAccount(t, space, "Checking")
		txn := newTransaction(t, space, account, domain.NewDate(2026, 1, 5), "-12.34")
		document, _, err := docs.Store(ctx, space, DocumentUpload{
			Bytes: append(storetest.PDF(), []byte(space.String())...), Filename: "receipt.pdf",
			Source: store.DocumentSourceUpload,
			Link:   store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: txn.ID},
		})
		require.NoError(t, err)
		files[space] = filepath.Join(storage.BasePath, document.StorageKey)
		require.FileExists(t, files[space])

		connection := &store.BillConnection{
			Biller: domain.BillerSpectrum, Label: "Internet", CredentialSource: store.BillCredentialSession,
			AutopayRule: domain.AutopayNone,
		}
		require.NoError(t, db(t).CreateBillConnection(ctx, space, connection))
		connections[space] = connection.ID
		newConnection(t, space)
	}
	paths := map[store.SpaceID]string{}
	for _, space := range []store.SpaceID{doomed, kept} {
		var sequence int
		var name string
		var path string
		require.NoError(t, householdSQL(t, space).QueryRowContext(ctx, `PRAGMA database_list`).Scan(&sequence, &name, &path))
		paths[space] = path
	}
	before := spaceRows(t, kept)
	for _, table := range []string{"transactions", "accounts", "rules", "envelopes", "watchlists",
		"holdings", "categories", "memberships", "documents", "bill_connections", "connections"} {
		require.Positivef(t, spaceRows(t, doomed)[table], "the fixture puts rows in %s", table)
	}

	require.NoError(t, DeleteSpace(ctx, db(t), storage, bills, doomed))

	require.Empty(t, spaceRows(t, doomed))
	_, err := db(t).GetSpace(ctx, doomed)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = db(t).HouseholdDB(ctx, doomed)
	require.Error(t, err, "the retired household must not be readable or recreated")
	spaces, err := db(t).ListSpaces(ctx)
	require.NoError(t, err)
	for _, space := range spaces {
		require.NotEqual(t, doomed, space.ID, "deleted spaces must not be listed")
	}
	require.NoFileExists(t, paths[doomed])
	archives, err := filepath.Glob(filepath.Join(filepath.Dir(paths[doomed]), "deleted", doomed.String()+".*.db"))
	require.NoError(t, err)
	require.Len(t, archives, 1, "the retired household is retained for recovery")
	require.NoFileExists(t, files[doomed])
	require.Equal(t, []string{connections[doomed].String()}, agent.forgotten)

	require.Equal(t, before, spaceRows(t, kept))
	require.FileExists(t, files[kept])
	require.FileExists(t, paths[kept])
}

func TestDeletingASpaceThatIsNotThereIsNotFound(t *testing.T) {
	err := DeleteSpace(t.Context(), db(t), nil, nil, store.NewSpaceID())
	require.ErrorIs(t, err, store.ErrNotFound)
}
