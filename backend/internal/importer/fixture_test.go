package importer

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/require"
)

// A hand-built miniature Simplifi export with invented values: one record of
// every store the importer reads, wired together the way the real data is, so
// the steps' ordering is exercised end to end. A test that needs a broken
// record edits a copy.
//
// It is held as JSON text rather than as Go literals so every amount reaches
// the mapper through the decoder, where a float64 would creep in. The API's
// upload tests post the same file.

const fixtureDataset = "ds-1"

//go:embed testdata/invented-export.json
var fixtureJSON string

// complete decodes a fresh copy of the fixture, so a test that edits one
// record cannot change what the next test reads.
func complete(t *testing.T) *Export {
	t.Helper()
	export, err := ParseExport([]byte(fixtureJSON))
	require.NoError(t, err)
	return export
}

// storeOf reaches the live resourcesById map, not a copy, so a test can add
// or delete a whole record as well as edit the fields of one.
func storeOf(t *testing.T, export *Export, name string) map[string]any {
	t.Helper()
	stores, _ := export.Raw["datasets"].(map[string]any)[fixtureDataset].(map[string]any)
	wrapped, ok := stores[name].(map[string]any)
	require.Truef(t, ok, "no store %s in the fixture", name)
	data, _ := wrapped["data"].(map[string]any)
	records, ok := data["resourcesById"].(map[string]any)
	require.True(t, ok)
	return records
}

// recordOf is one record of a store, live.
func recordOf(t *testing.T, export *Export, name, recordID string) map[string]any {
	t.Helper()
	data, ok := storeOf(t, export, name)[recordID].(map[string]any)
	require.Truef(t, ok, "no record %s in %s", recordID, name)
	return data
}

// edited is a complete export with one record altered — the shape most tests
// want. A nil value deletes the field.
func edited(t *testing.T, name, recordID string, changes map[string]any) *Export {
	t.Helper()
	export := complete(t)
	data := recordOf(t, export, name, recordID)
	for field, value := range changes {
		if value == nil {
			delete(data, field)
			continue
		}
		data[field] = value
	}
	return export
}

func without(t *testing.T, names ...string) *Export {
	t.Helper()
	export := complete(t)
	stores, _ := export.Raw["datasets"].(map[string]any)[fixtureDataset].(map[string]any)
	for _, name := range names {
		delete(stores, name)
	}
	return export
}

// addStore drops one extra store into a copy of the fixture, for the cases
// that need a record the fixture deliberately does not carry.
func addStore(t *testing.T, export *Export, name string, records map[string]any) *Export {
	t.Helper()
	stores, _ := export.Raw["datasets"].(map[string]any)[fixtureDataset].(map[string]any)
	stores[name] = map[string]any{
		"version": num("1"),
		"data":    map[string]any{"resourcesById": records},
	}
	return export
}

func mapped(t *testing.T, export *Export) *Mapped {
	t.Helper()
	out, err := MapExport(export, "", Options{OwnerEmail: DefaultOwnerEmail})
	require.NoError(t, err)
	return out
}
