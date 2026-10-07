package ofximport_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/importer/ofximport"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The subcommand's contract is its exit code: an operator's script reads it,
// and every importer here uses the same four. A dry run needs no database, so
// none of these open one — an `open` that is called at all fails the test.

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := ofximport.Run(t.Context(), args, &out, &errOut,
		func(context.Context) (*store.Store, service.Ingest, error) {
			t.Fatal("a dry run opened the database")
			return nil, service.Ingest{}, nil
		})
	return code, out.String(), errOut.String()
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestADryRunReportsAndWritesNothing(t *testing.T) {
	code, out, _ := run(t, write(t, "statement.qfx", sgml), "--account", "Everyday Checking", "--dry-run")
	require.Equal(t, 0, code)
	require.Contains(t, out, "OFX import — 1 statement(s), 2 transactions")
	require.Contains(t, out, "Everyday Checking")
	require.Contains(t, out, "Dry run: nothing was written.")
}

func TestAFileThatIsNotOfxExitsAsABadFile(t *testing.T) {
	// Exit 2 rather than 0-with-nothing-imported, so a script can tell
	// "wrong file" from "nothing new".
	code, _, errOut := run(t, write(t, "notes.txt", "Date,Amount\n2026-08-01,-3.00\n"), "--dry-run")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "could not be read")
}

func TestAMissingFileExitsAsABadFile(t *testing.T) {
	code, _, errOut := run(t, filepath.Join(t.TempDir(), "nope.ofx"), "--dry-run")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "cannot read")
}

func TestNoPathAtAllPrintsUsage(t *testing.T) {
	code, _, errOut := run(t, "--dry-run")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "usage: agentifi import-ofx")
}

func TestAStatementWithNoRowsIsRefusedRatherThanImportedAsNothing(t *testing.T) {
	empty := `<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<CURDEF>USD<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<LEDGERBAL><BALAMT>10.00<DTASOF>20260816</LEDGERBAL>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`
	code, _, errOut := run(t, write(t, "empty.ofx", empty), "--dry-run")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "no transactions")
}

func TestTheFlagsMayComeBeforeOrAfterThePath(t *testing.T) {
	// Go's flag package stops at the first non-flag argument, and the natural
	// way to type this puts the path first.
	path := write(t, "statement.qfx", sgml)
	for _, args := range [][]string{
		{path, "--dry-run", "--account", "Everyday Checking"},
		{"--dry-run", "--account", "Everyday Checking", path},
		{"--account=Everyday Checking", path, "--dry-run"},
	} {
		code, out, _ := run(t, args...)
		require.Equal(t, 0, code, "%v", args)
		require.Contains(t, out, "Everyday Checking", "%v", args)
	}
}

func TestTheFilesOwnCurrencyIsUsedWithoutBeingAsked(t *testing.T) {
	// Unlike the CSV, an OFX file states its currency. Making the operator
	// repeat it is how a file gets imported under the wrong one.
	code, out, _ := run(t, write(t, "statement.qfx", sgml), "--dry-run")
	require.Equal(t, 0, code)
	require.Contains(t, out, "transactions")
}
