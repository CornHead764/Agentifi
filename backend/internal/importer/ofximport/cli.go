package ofximport

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/importer/csvimport"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Run reads an OFX or QFX file into an existing space: the `import-ofx`
// subcommand, answering one of importer's exit codes.
//
//	agentifi import-ofx statement.qfx --account "Everyday Checking" --dry-run
//
// Everything after reading the file (mapping, the idempotent write, resolving
// the space) is the CSV importer's.
func Run(ctx context.Context, args []string, out, errOut io.Writer,
	open csvimport.Opener) int {

	flags := flag.NewFlagSet("agentifi import-ofx", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dryRun := flags.Bool("dry-run", false, "read and report without writing; the way to run it first")
	account := flags.String("account", "",
		"the account to file every row under; without it the statement names itself")
	space := flags.String("space", "", "the space to import into, by id or by name")
	ownerEmail := flags.String("owner-email", "", "whose spaces to look in when --space is a name or absent")
	currency := flags.String("currency", "",
		"override the currency; the file's own CURDEF is used when this is empty")
	flags.Usage = func() {
		fmt.Fprintln(errOut, "usage: agentifi import-ofx <statement.ofx|.qfx> [--account name] "+
			"[--dry-run] [--space id-or-name] [--owner-email you@example.com] [--currency USD]")
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

	doc, err := Parse(file)
	if err != nil {
		fmt.Fprintf(errOut, "%s could not be read: %v\n", path, err)
		return importer.ExitBadFile
	}
	fmt.Fprint(out, RenderDocument(doc, *account))
	if doc.Count() == 0 {
		fmt.Fprintln(errOut, "\nThe file holds no transactions, so there is nothing to import.")
		return importer.ExitBadFile
	}

	report := importer.NewReport()
	mapped := csvimport.Map(report, Rows(doc, *account, report),
		csvimport.Options{Currency: currencyOf(doc, *currency), Source: filepath.Base(path)})
	fmt.Fprint(out, csvimport.RenderAs(mapped, "OFX"))

	return csvimport.Finish(ctx, open, *dryRun, *space, *ownerEmail, mapped, out, errOut,
		func(result csvimport.WriteResult, spaceName string, spaceID store.SpaceID) string {
			return fmt.Sprintf("\nWrote %d rows into %q (%s): %d new, %d already there.\n",
				result.Rows(), spaceName, spaceID,
				result.TransactionsWritten, result.TransactionsSkipped)
		})
}

// RenderDocument says what was in the file before anything is mapped, so the
// operator can check it is the statement they meant.
func RenderDocument(doc Document, named string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OFX import — %d statement(s), %d transactions\n\n",
		len(doc.Accounts), doc.Count())
	for _, account := range doc.Accounts {
		name := named
		if name == "" {
			name = AccountName(account)
		}
		fmt.Fprintf(&b, "  %-28s %4d rows", name, len(account.Transactions))
		if account.HasBalance {
			fmt.Fprintf(&b, "  balance %s", account.Balance)
		}
		if span := datesOf(account); span != "" {
			fmt.Fprintf(&b, "  %s", span)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b)
	return b.String()
}

func datesOf(account Account) string {
	if len(account.Transactions) == 0 {
		return ""
	}
	first, last := account.Transactions[0].Posted, account.Transactions[0].Posted
	for _, txn := range account.Transactions {
		if txn.Posted.Before(first) {
			first = txn.Posted
		}
		if txn.Posted.After(last) {
			last = txn.Posted
		}
	}
	return first.String() + " to " + last.String()
}

// currencyOf prefers the file's own declaration, because unlike the CSV an OFX
// file actually states one.
func currencyOf(doc Document, override string) string {
	if override != "" {
		return strings.ToUpper(override)
	}
	for _, account := range doc.Accounts {
		if account.Currency != "" {
			return strings.ToUpper(account.Currency)
		}
	}
	return csvimport.DefaultCurrency
}
