package importer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/stretchr/testify/require"
)

// The command line's exit code is part of the interface: an operator scripting
// the migration reads it, and a dry run that found something unrepresentable
// must not look like a success.

func writeFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.json")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(t.Context(), args, &out, &errOut, storetest.NoDatabase)
	return code, out.String(), errOut.String()
}

func TestACleanExportExitsZeroAndPrintsTheReport(t *testing.T) {
	code, out, _ := run(t, writeFixture(t, fixtureJSON), "--dry-run")
	require.Equal(t, 0, code)
	require.Contains(t, out, "0 errors")
	require.Contains(t, out, "transactionStore")
	require.Contains(t, out, "Dry run: nothing was written.")
}

func TestAnExportWithAnUnrepresentableValueExitsOneAndNamesIt(t *testing.T) {
	broken := edited(t, "accountsStore", "a1", map[string]any{"subType": "CRYPTO_WALLET"})
	code, out, _ := run(t, writeExport(t, broken), "--dry-run")
	require.Equal(t, 1, code)
	require.Contains(t, out, "accountsStore a1 subType")
	require.Contains(t, out, "FAILED — nothing was written")
}

func TestAMissingFileExitsTwo(t *testing.T) {
	code, _, errOut := run(t, filepath.Join(t.TempDir(), "nope.json"), "--dry-run")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "cannot read")
}

func TestAFileThatIsNotAnExportExitsTwo(t *testing.T) {
	code, _, errOut := run(t, writeFixture(t, `{"hello": "world"}`), "--dry-run")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "not a Simplifi export")
}

func TestUnparseableJSONExitsTwo(t *testing.T) {
	code, _, errOut := run(t, writeFixture(t, `{"datasets":`), "--dry-run")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "cannot parse")
}

func TestOneDatasetNeedsNoChoosing(t *testing.T) {
	chosen, err := ChooseDataset(complete(t), "")
	require.NoError(t, err)
	require.Equal(t, fixtureDataset, chosen)
}

func TestSeveralDatasetsMakeTheOperatorSayWhich(t *testing.T) {
	export := complete(t)
	datasets := export.Raw["datasets"].(map[string]any)
	datasets["ds-2"] = map[string]any{}

	_, err := ChooseDataset(export, "")
	require.ErrorContains(t, err, "--dataset")

	chosen, err := ChooseDataset(export, "ds-2")
	require.NoError(t, err)
	require.Equal(t, "ds-2", chosen)
}

func TestADatasetThatIsNotThereIsNamed(t *testing.T) {
	_, err := ChooseDataset(complete(t), "ds-9")
	require.ErrorContains(t, err, "ds-9")
}

// writeExport serialises an edited fixture back to a file so the command line
// reads it through the decoder. json.Number re-encodes as the literal it was
// parsed from.
func writeExport(t *testing.T, export *Export) string {
	t.Helper()
	encoded, err := json.Marshal(export.Raw)
	require.NoError(t, err)
	return writeFixture(t, string(encoded))
}
