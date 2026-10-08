package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/CornHead764/agentifi/backend/internal/api"
	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// backupCommand takes a set now, or lists the sets on disk.
func backupCommand(ctx context.Context, cfg *config.Config, args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "list" {
		return listBackups(cfg, out)
	}
	if len(args) > 0 {
		return fmt.Errorf("agentifi backup: takes no arguments but list")
	}
	db, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := checkSchema(ctx, db); err != nil {
		return err
	}
	set, err := api.NewBackups(cfg, db).Take(ctx, backup.TriggerManual)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (%d bytes, %s)\n", set.Name, set.Bytes, encryptedWord(set.Encrypted))
	return nil
}

func listBackups(cfg *config.Config, out io.Writer) error {
	if cfg.BackupDir == "" {
		return errors.New("agentifi backup: BACKUP_DIR is not set")
	}
	sets, err := backup.List(cfg.BackupDir)
	if err != nil {
		return err
	}
	if len(sets) == 0 {
		fmt.Fprintf(out, "no backup sets in %s\n", cfg.BackupDir)
	}
	for _, set := range sets {
		state := "intact"
		if !set.Intact {
			state = "NOT INTACT: " + set.Problem
		}
		fmt.Fprintf(out, "%-36s %-9s %12d bytes  %-11s %s\n",
			set.Name, set.Trigger, set.Bytes, encryptedWord(set.Encrypted), state)
	}
	return nil
}

func encryptedWord(encrypted bool) string {
	if encrypted {
		return "encrypted"
	}
	return "UNENCRYPTED"
}

// backupBeforeMigrating takes a set before migrate changes a schema that
// holds data, so every upgrade can be undone. A set that cannot be taken
// holds the upgrade.
func backupBeforeMigrating(ctx context.Context, cfg *config.Config, db *store.Store, out io.Writer) error {
	version, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		return err
	}
	switch {
	case version == 0 || version >= latest:
		return nil
	case cfg.BackupDir == "":
		fmt.Fprintln(out, "BACKUP_DIR is not set; migrating without a backup")
		return nil
	}
	set, err := api.NewBackups(cfg, db).Take(ctx, backup.TriggerUpgrade)
	if err != nil {
		return fmt.Errorf("agentifi migrate: the backup before migrating failed, so nothing was migrated "+
			"(fix it, or run migrate --skip-backup to go ahead without one): %w", err)
	}
	fmt.Fprintf(out, "backed up to %s (%s) before migrating from %d to %d\n",
		set.Name, encryptedWord(set.Encrypted), version, latest)
	return nil
}

// restoreCommand restores a set over the live database, or rehearses it in a
// scratch file. A restore runs with the application stopped, and refuses while
// anything else has the database open.
func restoreCommand(ctx context.Context, cfg *config.Config, args []string, stdin io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(out)
	from := flags.String("from", "latest", "the set: its name, a day (YYYY-MM-DD) for that day's newest, or latest")
	identityPath := flags.String("identity", "",
		"the age identity file or SSH private key, or - to read it from standard input; "+
			"an encrypted SSH key's passphrase is read from BACKUP_IDENTITY_PASSPHRASE or asked for on the terminal")
	rehearse := flags.Bool("rehearse", false, "restore into a scratch file, check it and count its rows, remove it")
	confirm := flags.String("confirm", "", "the set's name, typed back: required to replace the live database")
	keepPrevious := flags.Bool("keep-previous", false, "keep the replaced database and attachments")
	ignoreKey := flags.Bool("ignore-key-mismatch", false,
		"restore a set whose stored connections were sealed with another key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if cfg.BackupDir == "" {
		return errors.New("agentifi restore: BACKUP_DIR is not set")
	}

	sets, err := backup.List(cfg.BackupDir)
	if err != nil {
		return err
	}
	set, err := backup.Resolve(sets, *from)
	if err != nil {
		return err
	}
	identities, err := readIdentityFlag(*identityPath, stdin)
	if err != nil {
		return err
	}

	storage := ""
	if *rehearse {
		storage = cfg.StoragePath
	} else if err := writableDir(cfg.StoragePath); err == nil {
		storage = cfg.StoragePath
	}
	currentKey := backup.KeyID(cfg.CredentialKey())
	plan, err := backup.PlanRestore(backup.RestoreRequest{
		Set: set, HasIdentity: len(identities) > 0, Rehearse: *rehearse,
		CurrentKeyID: currentKey, IgnoreKeyMismatch: *ignoreKey,
		Confirm: *confirm, HasStorage: storage != "",
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "set %s, taken %s (%s)\n", set.Name, set.CreatedAt.Format(time.RFC3339), set.Trigger)
	for i, step := range plan.Steps {
		fmt.Fprintf(out, "  %d. %s\n", i+1, step)
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(out, "  note: %s\n", warning)
	}
	database := backup.SQLite{Path: cfg.DatabasePath}
	now := time.Now()

	if *rehearse {
		report, err := backup.Rehearse(ctx, set, identities, database, now)
		if err != nil {
			return err
		}
		printReport(out, report)
		fmt.Fprintln(out, "rehearsal complete; the live database was not touched")
		return nil
	}

	// The keys the pre-restore set is encrypted to are read first, from the
	// database about to be overwritten, and the handle closed: the swap
	// refuses while anything has the file open. An empty database, on a new
	// host, has no saved keys and only the environment's.
	db, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	backups := api.NewBackups(cfg, db)
	source := backups.Source
	keys := cfg.BackupRecipients
	source.SchemaVersion, err = db.SchemaVersion(ctx)
	if err == nil && source.SchemaVersion > 0 {
		var settings service.BackupSettings
		if settings, err = backups.Settings(ctx); err == nil {
			keys = settings.Keys()
		}
	}
	db.Close()
	if err != nil {
		return err
	}

	report, err := backup.Restore(ctx, backup.RestoreOptions{
		Set: set, Identities: identities, Database: database, StoragePath: storage,
		KeepPrevious: *keepPrevious, Now: now,
		Log: func(line string) { fmt.Fprintln(out, "restore: "+line) },
		BackUp: func(ctx context.Context) (backup.Set, error) {
			unlock, err := backup.Lock(ctx, database)
			if err != nil {
				return backup.Set{}, err
			}
			defer unlock()
			return backup.Write(ctx, cfg.BackupDir, source, keys, backup.TriggerRestore, now)
		},
		Migrate: func(ctx context.Context) error {
			restored, err := open(ctx, cfg)
			if err != nil {
				return err
			}
			defer restored.Close()
			applied, err := restored.Migrate(ctx)
			for _, m := range applied {
				fmt.Fprintf(out, "restore: applied %d %s\n", m.Version, m.Source)
			}
			return err
		},
	})
	if report.PreRestore != "" {
		fmt.Fprintf(out, "the database as it was before the restore is in set %s\n", report.PreRestore)
	}
	if err != nil {
		return err
	}
	printReport(out, report)
	if report.Previous != "" {
		fmt.Fprintf(out, "the replaced database is kept as %s\n", report.Previous)
	}
	if report.SetAside != "" {
		fmt.Fprintf(out, "the replaced attachments are kept in %s\n", report.SetAside)
	}
	fmt.Fprintln(out, "restore complete; start the application (docker compose up -d) and check a number, not a page")
	return nil
}

func readIdentityFlag(path string, stdin io.Reader) ([]backup.Identity, error) {
	if path == "" {
		return nil, nil
	}
	var in io.Reader = stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("agentifi restore: %w", err)
		}
		defer file.Close()
		in = file
	}
	identities, err := backup.ReadIdentities(in, identityPassphrase)
	if errors.Is(err, backup.ErrPassphraseRequired) {
		return nil, fmt.Errorf("agentifi restore: %w: set BACKUP_IDENTITY_PASSPHRASE, "+
			"or run with a terminal to be asked for it", err)
	}
	return identities, err
}

// identityPassphrase opens an encrypted SSH private key, from
// BACKUP_IDENTITY_PASSPHRASE or else asked for on /dev/tty, since standard
// input may be carrying the key itself. With neither it gives none.
func identityPassphrase() ([]byte, error) {
	if value, ok := os.LookupEnv("BACKUP_IDENTITY_PASSPHRASE"); ok {
		return []byte(value), nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil
	}
	defer tty.Close()
	fmt.Fprint(tty, "passphrase for the SSH private key: ")
	raw, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	return raw, err
}

func writableDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("no directory")
	}
	probe, err := os.CreateTemp(dir, ".probe-")
	if err != nil {
		return err
	}
	_ = probe.Close()
	return os.Remove(probe.Name())
}

func printReport(out io.Writer, report backup.Report) {
	fmt.Fprintf(out, "%d rows in %d tables\n", report.Rows(), len(report.Tables))
	for _, table := range report.Tables {
		if table.Rows > 0 {
			fmt.Fprintf(out, "  %-40s %d\n", table.Table, table.Rows)
		}
	}
	fmt.Fprintf(out, "%d attachments\n", report.Attachments)
	if len(report.Secrets) > 0 {
		fmt.Fprintf(out, "secrets in the set: %s\n", strings.Join(report.Secrets, ", "))
	}
}
