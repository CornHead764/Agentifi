package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/pgimport"
)

// The `import-postgres` subcommand: copies every row of a database written by
// the Postgres-backed release into the SQLite file at DATABASE_PATH, which
// must not exist yet or be freshly migrated and empty. The copy is one
// transaction, so a failure leaves the file as empty as it found it.
func importPostgres(ctx context.Context, cfg *config.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentifi import-postgres", flag.ContinueOnError)
	from := fs.String("from", "", "the Postgres database to copy, as a postgres:// URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("import-postgres: --from <postgres URL> is required")
	}

	src, err := pgx.Connect(ctx, *from)
	if err != nil {
		return fmt.Errorf("import-postgres: connecting to Postgres: %w", err)
	}
	defer src.Close(context.Background())

	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o700); err != nil {
		return fmt.Errorf("creating the database directory: %w", err)
	}
	dst, err := pgimport.OpenTarget(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("import-postgres: %w", err)
	}
	defer dst.Close()

	report, err := pgimport.Import(ctx, src, dst)
	if err != nil {
		return fmt.Errorf("import-postgres: nothing was written: %w", err)
	}

	var moneyColumns int
	for _, t := range report.Tables {
		moneyColumns += len(t.Money)
		if t.Rows == 0 {
			continue
		}
		fmt.Fprintf(out, "  %-36s %8d rows", t.Table, t.Rows)
		if len(t.Money) > 0 {
			fmt.Fprintf(out, ", %d money totals equal", len(t.Money))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "copied %d rows across %d tables into %s; every row count and all %d money totals match Postgres\n",
		report.Rows(), len(report.Tables), cfg.DatabasePath, moneyColumns)
	return nil
}
