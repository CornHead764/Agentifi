// Package importer reads a Simplifi IndexedDB export and writes Agentifi's
// schema.
//
//	agentifi import data/simplifi/export.json --dry-run
//
// It takes the JSON that tools/extractors/extract-simplifi.js downloads; the
// steps and their order come from docs/importing.md.
//
// It is deliberately strict about what it reads: every unknown enum value and
// unresolvable foreign key is a named error rather than a guess, and a run
// with any error writes nothing. A field or store it was never taught is noted
// and left out, because a newer exporter adds them and one the importer does
// not read changes no row it writes. Simplifi's stored calculated* figures are
// imported verbatim, never recomputed, so our engine can be diffed against
// them.
package importer

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// DefaultOwnerEmail is used when the operator does not name one.
const DefaultOwnerEmail = "owner@agentifi.local"

// MapExport maps one dataset of an export to rows, without a database.
func MapExport(export *Export, datasetID string, options Options) (*Mapped, error) {
	chosen, err := ChooseDataset(export, datasetID)
	if err != nil {
		return nil, err
	}
	if options.OwnerEmail == "" {
		options.OwnerEmail = DefaultOwnerEmail
	}
	return NewMapper(export, chosen, options).Run(), nil
}

// ChooseDataset decides which dataset to import, and refuses to pick for the
// operator when the export holds several.
func ChooseDataset(export *Export, datasetID string) (string, error) {
	available := export.DatasetIDs()
	if len(available) == 0 {
		return "", fmt.Errorf("importer: the export holds no datasets")
	}
	if datasetID != "" {
		for _, id := range available {
			if id == datasetID {
				return datasetID, nil
			}
		}
		return "", fmt.Errorf("importer: no dataset %q; the export holds %v", datasetID, available)
	}
	if len(available) > 1 {
		return "", &SeveralDatasetsError{IDs: available}
	}
	return available[0], nil
}

// SeveralDatasetsError is an export holding more than one dataset with none
// chosen; IDs are the choices.
type SeveralDatasetsError struct{ IDs []string }

func (e *SeveralDatasetsError) Error() string {
	return fmt.Sprintf("importer: the export holds %d datasets; pass --dataset from %v", len(e.IDs), e.IDs)
}

// Prepare is everything an import does before the database: it parses the
// export, chooses the dataset, adds the separately saved transaction rules
// (nil when there are none) and maps the result. The command line and the
// upload in the app both start here.
func Prepare(data, transactionRules []byte, datasetID string, options Options) (*Mapped, error) {
	export, err := ParseExport(data)
	if err != nil {
		return nil, err
	}
	if transactionRules != nil {
		chosen, err := ChooseDataset(export, datasetID)
		if err != nil {
			return nil, err
		}
		if err := export.AddTransactionRules(chosen, transactionRules); err != nil {
			return nil, err
		}
	}
	return MapExport(export, datasetID, options)
}

// SplitPath takes the first argument that is neither a flag nor a non-boolean
// flag's value, and returns it with everything else: Go's flag package stops
// at the first non-flag argument, and the path usually comes first.
func SplitPath(flags *flag.FlagSet, args []string) (string, []string) {
	rest := make([]string, 0, len(args))
	path := ""
	skip := false
	for _, arg := range args {
		switch {
		case skip:
			skip = false
			rest = append(rest, arg)
		case strings.HasPrefix(arg, "-"):
			skip = takesValue(flags, arg)
			rest = append(rest, arg)
		case path == "":
			path = arg
		default:
			rest = append(rest, arg)
		}
	}
	return path, rest
}

func takesValue(flags *flag.FlagSet, arg string) bool {
	found := flags.Lookup(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"))
	if found == nil {
		return false
	}
	boolean, ok := found.Value.(interface{ IsBoolFlag() bool })
	return !ok || !boolean.IsBoolFlag()
}

// Exit codes, shared by every importer here; they are part of the interface.
const (
	ExitOK = 0
	// ExitUnrepresentable means the source holds something the schema cannot
	// hold. Nothing was written.
	ExitUnrepresentable = 1
	// ExitBadFile means the source could not be read, parsed, or is not what
	// the importer reads at all.
	ExitBadFile = 2
	// ExitWriteFailed means the mapping was clean and the database refused
	// it, which commits nothing, or the rows landed and settling them
	// afterwards failed, which the importer says.
	ExitWriteFailed = 3
)

// Run is the `agentifi import` subcommand, returning the process's exit code.
// open is called only when the run is not a dry run.
func Run(ctx context.Context, args []string, out, errOut io.Writer, open func(context.Context) (*store.Store, error)) int {
	flags := flag.NewFlagSet("agentifi import", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dryRun := flags.Bool("dry-run", false, "map and report without writing; the way to run it first")
	dataset := flags.String("dataset", "", "which dataset, if the export holds several")
	ownerEmail := flags.String("owner-email", DefaultOwnerEmail, "the user the import writes and scopes alert rules to")
	spaceName := flags.String("space-name", "", "overrides the dataset's own name")
	rulesOnly := flags.Bool("rules-only", false, "write only the rules, into the space a full import already wrote")
	spaceID := flags.String("space-id", "", "with --rules-only: the space to write into, if the owner and name do not find it")
	transactionRules := flags.String("transaction-rules", "",
		"Simplifi's transaction rules, the GET transaction-rules response saved from the browser")
	flags.Usage = func() {
		fmt.Fprintln(errOut, "usage: agentifi import <export.json> [--dry-run] [--dataset id] [--owner-email you@example.com] [--space-name name] [--transaction-rules rules.json]")
		fmt.Fprintln(errOut, "       agentifi import <export.json> --rules-only [--dry-run] [--owner-email you@example.com] [--space-name name | --space-id id] [--transaction-rules rules.json]")
		flags.PrintDefaults()
	}
	path, rest := SplitPath(flags, args)
	if err := flags.Parse(rest); err != nil {
		return ExitBadFile
	}
	if path == "" || flags.NArg() != 0 {
		flags.Usage()
		return ExitBadFile
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(errOut, "cannot read %s: %v\n", path, err)
		return ExitBadFile
	}
	var rules []byte
	if *transactionRules != "" {
		if rules, err = os.ReadFile(*transactionRules); err != nil {
			fmt.Fprintf(errOut, "cannot read %s: %v\n", *transactionRules, err)
			return ExitBadFile
		}
	}
	mapped, err := Prepare(data, rules, *dataset, Options{
		OwnerEmail: *ownerEmail,
		SpaceName:  *spaceName,
	})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitBadFile
	}

	fmt.Fprint(out, mapped.Report.Render())
	if !mapped.Report.OK() {
		return ExitUnrepresentable
	}
	if *rulesOnly {
		return runRulesOnly(ctx, mapped, *spaceID, *dryRun, out, errOut, open)
	}
	if *dryRun {
		fmt.Fprintln(out, "\nDry run: nothing was written.")
		return ExitOK
	}

	db, err := open(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "\nwrite failed, nothing committed: %v\n", err)
		return ExitWriteFailed
	}
	defer db.Close()

	if err := Write(ctx, db, mapped); err != nil {
		fmt.Fprintf(errOut, "\nwrite failed, nothing committed: %v\n", err)
		return ExitWriteFailed
	}
	fmt.Fprintf(out, "\nWrote %d rows into space %s.\n", mapped.RowCount(), mapped.SpaceID)
	return ExitOK
}

// runRulesOnly writes the mapped rules into an existing space. Unlike the full
// import, its dry run needs the database: what it would write depends on what
// the space already holds.
func runRulesOnly(ctx context.Context, mapped *Mapped, spaceText string, dryRun bool, out, errOut io.Writer,
	open func(context.Context) (*store.Store, error)) int {
	db, err := open(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "\nwrite failed, nothing committed: %v\n", err)
		return ExitWriteFailed
	}
	defer db.Close()

	var space uuid.UUID
	if spaceText != "" {
		space, err = uuid.Parse(spaceText)
		if err != nil {
			fmt.Fprintf(errOut, "--space-id: %v\n", err)
			return ExitBadFile
		}
	} else if space, err = FindImportedSpace(ctx, db, mapped); err != nil {
		fmt.Fprintln(errOut, err)
		return ExitWriteFailed
	}

	outcome, err := WriteRules(ctx, db, mapped, space, dryRun)
	if outcome != nil {
		fmt.Fprintf(out, "\nRules into space %s\n%s", space, outcome.Render())
	}
	if errors.Is(err, ErrRefused) {
		fmt.Fprintln(errOut, err)
		return ExitUnrepresentable
	}
	if err != nil {
		fmt.Fprintf(errOut, "\nwrite failed, nothing committed: %v\n", err)
		return ExitWriteFailed
	}
	if dryRun {
		fmt.Fprintln(out, "\nDry run: nothing was written.")
	}
	return ExitOK
}
