package main

import (
	"context"
	"os"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/importer/ofximport"
)

// The `import-ofx` subcommand. An OFX file names an account number rather than
// accounts, so it needs `--account`. Exit codes and signal handling are the CSV
// importer's.
func importOFX(ctx context.Context, cfg *config.Config, args []string) {
	os.Exit(ofximport.Run(ctx, args, os.Stdout, os.Stderr, openForIngest(cfg)))
}
