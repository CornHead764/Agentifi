package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/pgimport"
	"io"
	"os"
)

func importPostgres(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("import-postgres", flag.ContinueOnError)
	storage := os.Getenv("STORAGE_PATH")
	if storage == "" {
		storage = "./data/attachments"
	}
	var o pgimport.Options
	o.Output = out
	flags.StringVar(&o.From, "from", "", "source PostgreSQL DSN")
	flags.StringVar(&o.Data, "data", "", "destination data directory")
	flags.StringVar(&o.StoragePath, "storage-path", storage, "old attachment storage directory (STORAGE_PATH)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("import-postgres: unexpected arguments")
	}
	_, err := pgimport.Import(ctx, o)
	return err
}
