package main

import (
	"context"
	"os"

	"github.com/CornHead764/agentifi/backend/internal/api"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/importer/csvimport"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The `import-csv` subcommand. Unlike the IndexedDB `import`, which creates
// its space, a CSV import needs an existing one.
//
// Its exit code is its result (importer's Exit codes), so it exits the process.
// The signal context stays registered for the whole run: NotifyContext's stop
// cancels the context, and Ctrl-C should roll the import back.
func importCSV(ctx context.Context, cfg *config.Config, args []string) {
	os.Exit(csvimport.Run(ctx, args, os.Stdout, os.Stderr, openForIngest(cfg)))
}

// openForIngest opens the database with the ingest an upload runs, so rows a
// command writes are matched and queued for automations as uploaded ones are.
func openForIngest(cfg *config.Config) csvimport.Opener {
	return func(ctx context.Context) (*store.Store, service.Ingest, error) {
		db, err := open(ctx, cfg)
		if err != nil {
			return nil, service.Ingest{}, err
		}
		return db, api.NewIngest(api.NewEnv(cfg, db)), nil
	}
}
