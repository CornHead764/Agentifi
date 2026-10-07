// Package csvimport reads a Simplifi CSV transaction export into an existing
// space.
//
//	agentifi import-csv data/csv/transactions.csv --dry-run
//
// Like the IndexedDB importer, every value the schema cannot hold is a named
// error, and a run with any error writes nothing. The CSV has no ids, account
// types, category types, series or filters, and one exclusion column where the
// schema has two, so this importer infers more and prints every inference.
//
// It writes into a space that already exists, and is idempotent: accounts,
// categories and tags are matched by name, transactions by a key derived from
// the row.
package csvimport

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Run is the `agentifi import-csv` subcommand, returning the process's exit
// code. open is called only when the run is not a dry run.
func Run(ctx context.Context, args []string, out, errOut io.Writer, open Opener) int {

	flags := flag.NewFlagSet("agentifi import-csv", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dryRun := flags.Bool("dry-run", false, "map and report without writing; the way to run it first")
	space := flags.String("space", "", "the space to import into, by id or by name")
	ownerEmail := flags.String("owner-email", "", "whose spaces to look in when --space is a name or absent")
	currency := flags.String("currency", DefaultCurrency, "the currency every amount in the file is in")
	flags.Usage = func() {
		fmt.Fprintln(errOut, "usage: agentifi import-csv <transactions.csv> [--dry-run] "+
			"[--space id-or-name] [--owner-email you@example.com] [--currency USD]")
		flags.PrintDefaults()
	}
	path, rest := importer.SplitPath(flags, args)
	if err := flags.Parse(rest); err != nil {
		return importer.ExitBadFile
	}
	if path == "" || flags.NArg() != 0 {
		flags.Usage()
		return importer.ExitBadFile
	}

	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(errOut, "cannot read %s: %v\n", path, err)
		return importer.ExitBadFile
	}
	defer file.Close()

	report := importer.NewReport()
	rows, readable := ReadRows(file, report)
	if !readable || (len(rows) == 0 && report.OK()) {
		fmt.Fprintf(errOut, "%s is not a Simplifi transaction export\n", path)
		for _, problem := range report.Errors {
			fmt.Fprintf(errOut, "  %s\n", problem.Render())
		}
		return importer.ExitBadFile
	}

	mapped := Map(report, rows, Options{Currency: *currency})
	fmt.Fprint(out, Render(mapped))

	return Finish(ctx, open, *dryRun, *space, *ownerEmail, mapped, out, errOut, renderResult)
}

// Opener opens the database a write goes into, with what follows a write of
// new rows into it.
type Opener func(context.Context) (*store.Store, service.Ingest, error)

// Finish is the tail every CSV-shaped command shares once its source is mapped
// and reported: refuse an unrepresentable mapping, stop at a dry run, or open
// the database and Commit.
func Finish(ctx context.Context, open Opener, dryRun bool,
	space, ownerEmail string, mapped *Mapped,
	out, errOut io.Writer, describe func(WriteResult, string, store.SpaceID) string) int {
	if !mapped.Report.OK() {
		return importer.ExitUnrepresentable
	}
	if dryRun {
		fmt.Fprintln(out, "\nDry run: nothing was written.")
		return importer.ExitOK
	}

	db, ingest, err := open(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "\nwrite failed, nothing committed: %v\n", err)
		return importer.ExitWriteFailed
	}
	defer db.Close()

	return Commit(ctx, db, ingest, space, ownerEmail, mapped, out, errOut, describe)
}

// Commit writes a mapped import into the space the operator named and runs
// what it wrote through the ingest: a row left unsettled has no rules,
// transfer pairing or running balance until the space's next sync.
func Commit(ctx context.Context, db *store.Store, ingest service.Ingest,
	space, ownerEmail string, mapped *Mapped,
	out, errOut io.Writer, describe func(WriteResult, string, store.SpaceID) string) int {
	spaceID, spaceName, err := ResolveSpace(ctx, db, space, ownerEmail)
	if err != nil {
		fmt.Fprintf(errOut, "\nwrite failed, nothing committed: %v\n", err)
		return importer.ExitWriteFailed
	}
	result, err := Write(ctx, db, spaceID, mapped)
	if err != nil {
		fmt.Fprintf(errOut, "\nwrite failed, nothing committed: %v\n", err)
		return importer.ExitWriteFailed
	}
	fmt.Fprint(out, describe(result, spaceName, spaceID))
	if _, err := ingest.AfterIngest(ctx, db, spaceID, result.TransactionIDs); err != nil {
		fmt.Fprintf(errOut, "\nthe rows were written and then failed to settle: %v\n"+
			"They are in the ledger without rules, transfer pairing or a running "+
			"balance; re-running this file will not settle them, because it adds "+
			"nothing the second time. They are marked, and `agentifi settle` "+
			"(or the space's next sync) finishes them.\n", err)
		return importer.ExitWriteFailed
	}
	return importer.ExitOK
}

// ResolveSpace decides which space to write into, and refuses to pick for the
// operator when there are several candidates.
func ResolveSpace(ctx context.Context, db *store.Store, requested, ownerEmail string) (store.SpaceID, string, error) {
	if requested != "" {
		if id, err := store.ParseSpaceID(requested); err == nil {
			found, err := db.GetSpace(ctx, id)
			if err != nil {
				return store.SpaceID{}, "", fmt.Errorf("csvimport: no space %s: %w", requested, err)
			}
			return found.ID, found.Name, nil
		}
	}

	if ownerEmail == "" {
		return store.SpaceID{}, "", errors.New(
			"csvimport: pass --space with a space id, or --owner-email to look up a space by name")
	}
	user, err := db.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(ownerEmail)))
	if err != nil {
		return store.SpaceID{}, "", fmt.Errorf("csvimport: no account for %s: %w", ownerEmail, err)
	}
	spaces, err := db.ListSpacesForUser(ctx, user.ID)
	if err != nil {
		return store.SpaceID{}, "", err
	}

	candidates := make([]store.Space, 0, len(spaces))
	for _, candidate := range spaces {
		if requested == "" || strings.EqualFold(candidate.Name, requested) {
			candidates = append(candidates, candidate)
		}
	}
	switch len(candidates) {
	case 0:
		return store.SpaceID{}, "", fmt.Errorf("csvimport: %s is a member of no space matching %q",
			ownerEmail, requested)
	case 1:
		return candidates[0].ID, candidates[0].Name, nil
	default:
		names := make([]string, len(candidates))
		for i, candidate := range candidates {
			names[i] = fmt.Sprintf("%q (%s)", candidate.Name, candidate.ID)
		}
		return store.SpaceID{}, "", fmt.Errorf(
			"csvimport: %s is a member of %d spaces; pass --space with one of %s",
			ownerEmail, len(candidates), strings.Join(names, ", "))
	}
}

// Render is the report the operator reads before trusting the run. Unlike
// importer.Report.Render, it prints every kind it guessed.
func Render(m *Mapped) string { return RenderAs(m, "Simplifi CSV") }

// guessedKinds reports whether any account's kind was inferred rather than read.
func guessedKinds(m *Mapped) bool {
	for _, account := range m.Accounts {
		if account.Guess.Kind != "" || account.Guess.Type != "" || account.Guess.Rule != "" {
			return true
		}
	}
	return false
}

// RenderAs is Render titled for another format read into the same mapping,
// such as OFX.
func RenderAs(m *Mapped, source string) string {
	report := m.Report
	var b strings.Builder
	fmt.Fprintf(&b, "%s import — %d errors, %d warnings\n\nRead\n", source,
		len(report.Errors), len(report.Warnings))
	writeCounts(&b, report.Read)
	b.WriteString("\nMapped\n")
	writeCounts(&b, report.Written)

	// Printed only when something was guessed: a source that states each
	// account's kind has nothing here for an operator to check.
	if guessedKinds(m) {
		b.WriteString("\nAccount kinds, inferred from the name — correct any of these before trusting a balance\n")
		for _, account := range m.Accounts {
			rule := account.Guess.Rule
			if rule == "" {
				rule = "NO RULE MATCHED — the fallback chose this"
			}
			fmt.Fprintf(&b, "  %-28s %-12s %-14s %s\n",
				account.Row.Name, account.Guess.Kind, account.Guess.Type, rule)
		}
	}

	b.WriteString("\nCategories that are not spending, inferred from the name\n")
	nonExpense := 0
	for _, category := range m.Categories {
		if category.Row.Kind == domain.CategoryExpense {
			continue
		}
		nonExpense++
		fmt.Fprintf(&b, "  %-44s %-9s %s\n", category.Path, category.Row.Kind, category.Guess.Rule)
	}
	fmt.Fprintf(&b, "  %d of %d categories; the rest were classified as spending\n",
		nonExpense, len(m.Categories))

	report.RenderGroups(&b)
	if len(report.Errors) > 0 {
		b.WriteString("\nErrors\n")
		for _, problem := range report.Errors {
			fmt.Fprintf(&b, "  %s\n", problem.Render())
		}
	}
	if report.OK() {
		b.WriteString("\nOK — nothing unrepresentable\n")
	} else {
		b.WriteString("\nFAILED — nothing was written\n")
	}
	return b.String()
}

func renderResult(r WriteResult, spaceName string, spaceID store.SpaceID) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nWrote %d rows into %q (%s)\n", r.Rows(), spaceName, spaceID)
	fmt.Fprintf(&b, "  accounts            %5d created, %5d already there\n",
		r.AccountsCreated, r.AccountsExisting)
	fmt.Fprintf(&b, "  categories          %5d created, %5d already there\n",
		r.CategoriesCreated, r.CategoriesExisting)
	fmt.Fprintf(&b, "  tags                %5d created, %5d already there\n",
		r.TagsCreated, r.TagsExisting)
	fmt.Fprintf(&b, "  transactions        %5d written, %5d already there\n",
		r.TransactionsWritten, r.TransactionsSkipped)
	fmt.Fprintf(&b, "  splits              %5d written\n", r.SplitsWritten)
	fmt.Fprintf(&b, "  tag links           %5d written\n", r.TagLinksWritten)
	return b.String()
}

func writeCounts(b *strings.Builder, counts map[string]int) {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(b, "  %-32s %7d\n", name, counts[name])
	}
}
