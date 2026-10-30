// Command seed is an explicitly development-only data generator.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"io"
	"os"
	"sort"
	"time"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out io.Writer) error {
	opts := seed.DefaultOptions()
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	flags.Int64Var(&opts.Seed, "seed", opts.Seed, "PCG seed")
	flags.IntVar(&opts.Households, "households", opts.Households, "households to create")
	flags.IntVar(&opts.Transactions, "transactions", opts.Transactions, "transactions per household (minimum 32)")
	today := flags.String("today", seed.DefaultToday, "fixed business date (YYYY-MM-DD)")
	data := flags.String("data", "./data", "SQLite data directory")
	force := flags.Bool("force", false, "allow appending to a database that already has households")
	storage := flags.String("storage-path", ".seed-documents", "directory for invented statement PDFs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("seed: unexpected positional arguments")
	}
	var err error
	opts.Today, err = time.Parse(time.DateOnly, *today)
	if err != nil {
		return fmt.Errorf("seed: invalid --today: %w", err)
	}
	st, err := store.OpenSQLite(ctx, *data, time.Now)
	if err != nil {
		return err
	}
	defer st.Close()
	cipher, err := store.NewCipher("seed-development-only-not-a-secret")
	if err != nil {
		return err
	}
	st = st.WithCipher(cipher)
	spaces, err := st.ListSpaces(ctx)
	if err != nil {
		return err
	}
	if len(spaces) > 0 && !*force {
		return fmt.Errorf("seed: refusing database containing %d households; use a fresh database or --force to append", len(spaces))
	}
	opts.Storage = &provider.LocalStorage{BasePath: *storage}
	if err := seed.Run(ctx, st, opts); err != nil {
		return err
	}
	counts, err := seed.Counts(ctx, st)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(out, "%-36s %d\n", name, counts[name])
	}
	return nil
}
