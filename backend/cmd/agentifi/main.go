// Command agentifi is the whole backend: one binary that carries its own
// schema and serves the API. The subcommands are listed in usage.
//
// Migration is a separate subcommand rather than done on serve startup, so two
// instances cannot race to migrate one database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	// The binary carries its own zoneinfo, so the daily sync window is read
	// in the server's TZ whatever the image installs.
	_ "time/tzdata"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		usage()
		return fmt.Errorf("agentifi: a subcommand is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Before the configuration: these commands must work in a container with
	// no database configured.
	switch os.Args[1] {
	case "browser-selftest":
		return browserSelftest(os.Stdout)
	case "probe-sign-in":
		return probeSignIn(os.Stdout, os.Args[2:])
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch os.Args[1] {
	case "serve":
		return serve(ctx, cfg)
	case "healthcheck":
		return healthcheck(cfg)
	case "migrate":
		return migrate(ctx, cfg, os.Args[2:])
	case "backup":
		return backupCommand(ctx, cfg, os.Args[2:], os.Stdout)
	case "restore":
		return restoreCommand(ctx, cfg, os.Args[2:], os.Stdin, os.Stdout)
	case "import":
		// The importer's exit code is its result (0 clean, 1 unrepresentable,
		// 2 unreadable file, 3 the write failed), so it exits here rather than
		// returning an error main would flatten to 1.
		//
		// signal.NotifyContext's stop cancels the context, so it must not be
		// called before the import runs. Ctrl-C then rolls the import back.
		os.Exit(importer.Run(ctx, os.Args[2:], os.Stdout, os.Stderr,
			func(ctx context.Context) (*store.Store, error) { return open(ctx, cfg) }))
		return nil
	case "import-csv":
		importCSV(ctx, cfg, os.Args[2:])
		return nil
	case "import-ofx":
		importOFX(ctx, cfg, os.Args[2:])
		return nil
	case "user":
		return userCommand(ctx, cfg, os.Args[2:])
	case "settle":
		return settleCommand(ctx, cfg, os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("agentifi: unknown subcommand %q", os.Args[1])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `agentifi — self-hosted personal finance

  agentifi migrate   back up, then apply the embedded schema migrations;
                     --skip-backup migrates without the backup
  agentifi serve     serve the API
  agentifi healthcheck  probe a running serve on HTTP_ADDR; exit 0 when healthy
  agentifi browser-selftest  launch the installed Chrome and report
  agentifi probe-sign-in <url>  report what a provider's sign-in page shows the
                     page reading a connector module is written against
  agentifi import    import a Simplifi export; --dry-run reports without writing
  agentifi import-csv  import a Simplifi CSV transaction export into a space
  agentifi import-ofx  import an OFX or QFX bank statement into a space
  agentifi settle    finish rows an import or sync wrote but failed to settle
  agentifi backup    take a backup set into BACKUP_DIR now; backup list lists them
  agentifi restore --from <set|day|latest> --identity <file|->
                     restore a set over the live database (needs --confirm
                     <set>), or --rehearse it in a scratch database
  agentifi user add|passwd  make an account or change its password; the
                     first on a fresh install is made with add -superuser
  agentifi user admin --email <address> --on|--off
                     let an account administer the server, or take that
                     away; the last active superuser keeps it
  agentifi user list  every account, whether it is active and a superuser

Configuration is read from the environment and from ./.env; DATABASE_URL is
the only setting with no workable default.
`)
}

func migrate(ctx context.Context, cfg *config.Config, args []string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	skipBackup := flags.Bool("skip-backup", false, "migrate without taking a backup first")
	if err := flags.Parse(args); err != nil {
		return err
	}
	db, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	if !*skipBackup {
		if err := backupBeforeMigrating(ctx, cfg, db, os.Stdout); err != nil {
			return err
		}
	}

	applied, err := db.Migrate(ctx)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("schema is up to date")
		return nil
	}
	for _, m := range applied {
		fmt.Printf("applied %d %s\n", m.Version, m.Source)
	}
	return nil
}

func open(ctx context.Context, cfg *config.Config) (*store.Store, error) {
	poolCfg, err := store.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	poolCfg.MaxConns = cfg.DatabaseMaxConns
	return store.OpenPool(ctx, poolCfg)
}
