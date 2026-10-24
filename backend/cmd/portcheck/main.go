// Command portcheck compares authenticated API snapshots across storage engines.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/portcheck"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

var different = errors.New("snapshots differ")

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		if !errors.Is(err, different) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: portcheck snapshot --data DIR --out FILE [--today YYYY-MM-DD] | portcheck diff A B")
	}
	switch args[0] {
	case "diff":
		if len(args) != 3 {
			return fmt.Errorf("usage: portcheck diff A B")
		}
		snapshots := make([]portcheck.Snapshot, 2)
		for i, path := range args[1:] {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			dec := json.NewDecoder(f)
			dec.UseNumber()
			err = dec.Decode(&snapshots[i])
			_ = f.Close()
			if err != nil {
				return err
			}
			if snapshots[i].FormatVersion != 1 {
				return fmt.Errorf("unsupported snapshot format %d", snapshots[i].FormatVersion)
			}
		}
		differences := portcheck.Diff(&snapshots[0], &snapshots[1])
		portcheck.PrintDiff(out, differences, 100)
		if len(differences) > 0 {
			return different
		}
		return nil
	case "snapshot":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("portcheck snapshot", flag.ContinueOnError)
		data := fs.String("data", cfg.DataDir, "SQLite data directory (default DATA_DIR/config)")
		output := fs.String("out", "", "snapshot JSON file (contains private household data)")
		today := fs.String("today", time.Now().UTC().Format(time.DateOnly), "fixed business date (YYYY-MM-DD)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *output == "" {
			return fmt.Errorf("snapshot requires --out FILE and no positional arguments")
		}
		date, err := time.Parse(time.DateOnly, *today)
		if err != nil {
			return err
		}
		db, err := store.OpenSQLite(ctx, *data, time.Now)
		if err != nil {
			return err
		}
		defer db.Close()
		cipher, err := store.NewCipher(cfg.CredentialKey())
		if err != nil {
			return err
		}
		db = db.WithCipher(cipher)
		cfg.DataDir = *data
		started := time.Now()
		snapshot, err := portcheck.Capture(ctx, db, cfg, date)
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		f, err := os.OpenFile(*output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := f.Chmod(0600); err != nil {
			_ = f.Close()
			return err
		}
		_, err = f.Write(raw)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		ids := make([]string, 0, len(snapshot.Households))
		for id := range snapshot.Households {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(out, "household %s: %d requests\n", id, len(snapshot.Households[id]))
			keys := make([]string, 0, len(snapshot.Skipped[id]))
			for k := range snapshot.Skipped[id] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(out, "  skipped %s: %s\n", k, snapshot.Skipped[id][k])
			}
		}
		fmt.Fprintf(out, "%d bytes; %s\n", len(raw), time.Since(started).Round(time.Millisecond))
		return nil
	default:
		return fmt.Errorf("unknown portcheck command %q", args[0])
	}
}
