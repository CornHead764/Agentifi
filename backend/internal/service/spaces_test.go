package service

import (
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

// spaceRows counts the rows of every table with a space_id that belong to one
// space, by table.
func spaceRows(t *testing.T, space store.SpaceID) map[string]int {
	t.Helper()
	ctx := t.Context()
	tables, err := db(t).Pool().Query(ctx, `
		SELECT m.name FROM sqlite_master m, pragma_table_info(m.name) c
		WHERE m.type = 'table' AND c.name = 'space_id'`)
	require.NoError(t, err)
	var names []string
	for tables.Next() {
		var name string
		require.NoError(t, tables.Scan(&name))
		names = append(names, name)
	}
	tables.Close()
	require.NotEmpty(t, names)

	counts := map[string]int{}
	for _, name := range names {
		var count int
		require.NoError(t, db(t).Pool().QueryRow(ctx,
			`SELECT count(*) FROM `+name+` WHERE space_id = $1`, space.UUID()).Scan(&count))
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
	before := spaceRows(t, kept)
	for _, table := range []string{"transactions", "accounts", "rules", "envelopes", "watchlists",
		"holdings", "categories", "memberships", "documents", "bill_connections", "connections"} {
		require.Positivef(t, spaceRows(t, doomed)[table], "the fixture puts rows in %s", table)
	}

	require.NoError(t, DeleteSpace(ctx, db(t), storage, bills, doomed))

	require.Empty(t, spaceRows(t, doomed))
	_, err := db(t).GetSpace(ctx, doomed)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.NoFileExists(t, files[doomed])
	require.Equal(t, []string{connections[doomed].String()}, agent.forgotten)

	require.Equal(t, before, spaceRows(t, kept))
	require.FileExists(t, files[kept])
}

func TestDeletingASpaceThatIsNotThereIsNotFound(t *testing.T) {
	err := DeleteSpace(t.Context(), db(t), nil, nil, store.NewSpaceID())
	require.ErrorIs(t, err, store.ErrNotFound)
}
