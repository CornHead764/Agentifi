package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/importer/csvimport"
)

// The `settle` subcommand: repairs rows that landed in the ledger without being
// settled (rules, transfer pairing, series match, running balance). A sync does
// this itself; a manual-only install or a file import whose settle failed has
// no other way, since re-running the file adds nothing.
func settleCommand(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("agentifi settle", flag.ContinueOnError)
	space := fs.String("space", "", "the space to settle, by id")
	ownerEmail := fs.String("owner-email", "", "find the space by its owner instead")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, ingest, err := openForIngest(cfg)(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	spaceID, spaceName, err := csvimport.ResolveSpace(ctx, db, *space, *ownerEmail)
	if err != nil {
		return err
	}
	settled, err := ingest.SettleBacklog(ctx, db, spaceID)
	if err != nil {
		return fmt.Errorf("settle %q: %w", spaceName, err)
	}
	if settled == 0 {
		fmt.Printf("nothing to settle in %q (%s)\n", spaceName, spaceID)
		return nil
	}
	fmt.Printf("settled %d rows in %q (%s)\n", settled, spaceName, spaceID)
	return nil
}
