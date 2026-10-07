package csvimport

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// The exit code is part of the interface: a dry run that found something
// unrepresentable must not look like a success.

func writeFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transactions.csv")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(t.Context(), args, &out, &errOut,
		func(ctx context.Context) (*store.Store, service.Ingest, error) {
			db, err := storetest.NoDatabase(ctx)
			return db, service.Ingest{}, err
		})
	return code, out.String(), errOut.String()
}

func TestACleanFileExitsZeroAndPrintsTheReport(t *testing.T) {
	code, out, _ := runCLI(t, writeFixture(t, file(ledger()...)), "--dry-run")
	require.Equal(t, importer.ExitOK, code)
	require.Contains(t, out, "0 errors")
	require.Contains(t, out, "Account kinds")
	require.Contains(t, out, "Dry run: nothing was written.")
}

func TestAFileWithAnUnrepresentableValueExitsOneAndNamesTheLine(t *testing.T) {
	code, out, _ := runCLI(t,
		writeFixture(t, file(spend("Everyday Checking", "A", "Shopping", " twelve"))), "--dry-run")
	require.Equal(t, importer.ExitUnrepresentable, code)
	require.Contains(t, out, "line 2 Amount")
	require.Contains(t, out, "FAILED — nothing was written")
}

func TestAMissingFileExitsTwo(t *testing.T) {
	code, _, errOut := runCLI(t, filepath.Join(t.TempDir(), "nope.csv"), "--dry-run")
	require.Equal(t, importer.ExitBadFile, code)
	require.Contains(t, errOut, "cannot read")
}

func TestAFileThatIsNotATransactionExportExitsTwo(t *testing.T) {
	code, _, errOut := runCLI(t, writeFixture(t, "hello,world\n1,2\n"), "--dry-run")
	require.Equal(t, importer.ExitBadFile, code)
	require.Contains(t, errOut, "is not a Simplifi transaction export")
}

func TestNoPathAtAllExitsTwo(t *testing.T) {
	code, _, _ := runCLI(t, "--dry-run")
	require.Equal(t, importer.ExitBadFile, code)
}

// Go's flag package stops at the first non-flag argument.
func TestThePathMayComeBeforeOrAfterTheFlags(t *testing.T) {
	path := writeFixture(t, file(ledger()...))
	before, _, _ := runCLI(t, path, "--dry-run")
	after, _, _ := runCLI(t, "--dry-run", path)
	require.Equal(t, importer.ExitOK, before)
	require.Equal(t, importer.ExitOK, after)
}

// --space takes a value, so the value must not be mistaken for the path.
func TestAFlagValueIsNotMistakenForThePath(t *testing.T) {
	path := writeFixture(t, file(ledger()...))
	code, _, _ := runCLI(t, "--space", "Household", path, "--dry-run")
	require.Equal(t, importer.ExitOK, code)
}

// The space is not something a CSV can name.
func TestResolveSpaceRefusesToChooseBetweenSeveral(t *testing.T) {
	ctx := t.Context()
	handle := db(t)

	user := store.User{Email: "csv-import-test@example.com", IsActive: true}
	require.NoError(t, handle.CreateUser(ctx, &user))
	first := storetest.NewOwnedSpace(t, "Household", user.ID)
	storetest.NewOwnedSpace(t, "Rental", user.ID)

	_, _, err := ResolveSpace(ctx, handle, "", user.Email)
	require.ErrorContains(t, err, "--space")

	id, name, err := ResolveSpace(ctx, handle, "Household", user.Email)
	require.NoError(t, err)
	require.Equal(t, first, id)
	require.Equal(t, "Household", name)

	byID, _, err := ResolveSpace(ctx, handle, first.String(), "")
	require.NoError(t, err)
	require.Equal(t, first, byID)
}
