package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
)

// A restore never writes into the live database. It restores into a new one,
// all or nothing, and swaps the names, so a dump that fails half way leaves
// the install exactly as it was; and it unpacks the attachments beside the
// current ones before the swap, so the step after it is a handful of renames.

// TableCount is one table's rows after a restore.
type TableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// Report is what a restore or rehearsal did.
type Report struct {
	Set         string
	Tables      []TableCount
	Attachments int
	Secrets     []string
	// PreRestore is the set taken of the database a restore replaced.
	PreRestore string
	// Previous and SetAside are what a restore kept: the replaced database's
	// name and the directory the replaced attachments are in.
	Previous string
	SetAside string
}

// Rows is the total across every table.
func (r Report) Rows() int64 {
	var total int64
	for _, table := range r.Tables {
		total += table.Rows
	}
	return total
}

// Rehearse restores a set into a scratch database beside the live one, counts
// its rows, reads its archives through, and drops the scratch database. The
// live database is never touched.
func Rehearse(ctx context.Context, set Set, identities []age.Identity, db Postgres, now time.Time) (Report, error) {
	report := Report{Set: set.Name}
	live, err := db.Database()
	if err != nil {
		return report, err
	}
	scratch := scratchName(live, "rehearse", now)

	maintenance, err := connectMaintenance(ctx, db)
	if err != nil {
		return report, err
	}
	defer maintenance.Close(context.Background())

	if err := createDatabase(ctx, maintenance, scratch); err != nil {
		return report, err
	}
	defer func() { _ = dropDatabase(context.WithoutCancel(ctx), maintenance, scratch) }()

	if err := restoreDatabase(ctx, set, identities, db.On(scratch)); err != nil {
		return report, err
	}
	if report.Tables, err = countRows(ctx, db.On(scratch)); err != nil {
		return report, err
	}
	if report.Attachments, err = readAttachments(set, identities, ""); err != nil {
		return report, err
	}
	if report.Secrets, err = listSecrets(set, identities); err != nil {
		return report, err
	}
	return report, nil
}

// RestoreOptions is a restore over the live database.
type RestoreOptions struct {
	Set        Set
	Identities []age.Identity
	Database   Postgres
	// StoragePath is the attachments directory; empty leaves them alone.
	StoragePath string
	// KeepPrevious keeps the replaced database and attachments rather than
	// dropping them once the restore has succeeded.
	KeepPrevious bool
	// Disconnect ends other sessions on the live database rather than
	// refusing while there are any.
	Disconnect bool
	Now        time.Time
	Log        func(string)
	// BackUp takes the set of what is about to be replaced. It must leave no
	// connection open to the live database.
	BackUp func(context.Context) (Set, error)
	// Migrate brings the restored database up to this binary's schema.
	Migrate func(context.Context) error
}

// Restore puts a set's database and attachments in place of the live ones. The
// caller has checked PlanRestore and holds no connection to the live
// database.
func Restore(ctx context.Context, opts RestoreOptions) (Report, error) {
	report := Report{Set: opts.Set.Name}
	say := opts.Log
	if say == nil {
		say = func(string) {}
	}
	live, err := opts.Database.Database()
	if err != nil {
		return report, err
	}
	stamp := opts.Now.Format("20060102150405")
	scratch := scratchName(live, "restore", opts.Now)
	previous := scratchName(live, "before_restore", opts.Now)

	maintenance, err := connectMaintenance(ctx, opts.Database)
	if err != nil {
		return report, err
	}
	defer maintenance.Close(context.Background())
	if err := quiet(ctx, maintenance, live, opts.Disconnect); err != nil {
		return report, err
	}

	staging := ""
	if _, ok := opts.Set.Files[PartAttachments]; ok && opts.StoragePath != "" {
		staging = filepath.Join(opts.StoragePath, restoreStagingPrefix+stamp)
		say("unpacking the attachments into " + staging)
		if err := os.Mkdir(staging, 0o700); err != nil {
			return report, fmt.Errorf("backup: %w", err)
		}
		if report.Attachments, err = readAttachments(opts.Set, opts.Identities, staging); err != nil {
			_ = os.RemoveAll(staging)
			return report, err
		}
	}
	cleanStaging := func() {
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
	}

	say("backing up the current database")
	before, err := opts.BackUp(ctx)
	if err != nil {
		cleanStaging()
		return report, fmt.Errorf("backup: the backup taken before the restore failed, so nothing was changed: %w", err)
	}
	report.PreRestore = before.Name
	say("wrote " + before.Name)

	say("restoring into " + scratch)
	if err := createDatabase(ctx, maintenance, scratch); err != nil {
		cleanStaging()
		return report, err
	}
	if err := restoreDatabase(ctx, opts.Set, opts.Identities, opts.Database.On(scratch)); err != nil {
		_ = dropDatabase(context.WithoutCancel(ctx), maintenance, scratch)
		cleanStaging()
		return report, fmt.Errorf("%w (the live database was not changed)", err)
	}
	if report.Tables, err = countRows(ctx, opts.Database.On(scratch)); err != nil {
		_ = dropDatabase(context.WithoutCancel(ctx), maintenance, scratch)
		cleanStaging()
		return report, err
	}

	say("swapping " + scratch + " in as " + live)
	if err := quiet(ctx, maintenance, live, opts.Disconnect); err != nil {
		_ = dropDatabase(context.WithoutCancel(ctx), maintenance, scratch)
		cleanStaging()
		return report, err
	}
	if err := swapDatabases(ctx, maintenance, live, scratch, previous); err != nil {
		_ = dropDatabase(context.WithoutCancel(ctx), maintenance, scratch)
		cleanStaging()
		return report, err
	}
	// From here the restored database is live, and the replaced one is
	// kept until the end so every failure below can be undone by hand.
	report.Previous = previous

	if staging != "" {
		aside := filepath.Join(opts.StoragePath, setAsidePrefix+stamp)
		say("setting the current attachments aside in " + aside)
		if err := swapAttachments(opts.StoragePath, staging, aside); err != nil {
			return report, fmt.Errorf("backup: the database was restored, but the attachments were not: %w; "+
				"the replaced database is kept as %s", err, previous)
		}
		report.SetAside = aside
	}

	say("applying migrations")
	if err := opts.Migrate(ctx); err != nil {
		return report, fmt.Errorf("backup: the set was restored, but migrating it failed: %w; "+
			"the replaced database is kept as %s", err, previous)
	}

	if !opts.KeepPrevious {
		if err := dropDatabase(ctx, maintenance, previous); err != nil {
			say("could not drop " + previous + ": " + err.Error())
		} else {
			report.Previous = ""
		}
		if report.SetAside != "" {
			if err := os.RemoveAll(report.SetAside); err != nil {
				say("could not remove " + report.SetAside + ": " + err.Error())
			} else {
				report.SetAside = ""
			}
		}
	}
	return report, nil
}

func scratchName(live, what string, now time.Time) string {
	name := live + "_" + what + "_" + now.Format("20060102150405")
	if len(name) > 63 {
		name = name[len(name)-63:]
	}
	return name
}

// connectMaintenance connects to a database other than the live one, since
// a database cannot be renamed or dropped by a session connected to it.
func connectMaintenance(ctx context.Context, db Postgres) (*pgx.Conn, error) {
	var firstErr error
	for _, name := range []string{"postgres", "template1"} {
		conn, err := db.On(name).connect(ctx)
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

const quietGrace = 2 * time.Second

// quiet refuses while anything else is connected to the live database, or
// ends those sessions when asked to.
func quiet(ctx context.Context, conn *pgx.Conn, live string, disconnect bool) error {
	if disconnect {
		_, err := conn.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()`, live)
		if err != nil {
			return fmt.Errorf("backup: ending the sessions on %s: %w", live, err)
		}
		return nil
	}
	// A session whose client has just closed, such as the pre-restore backup's
	// own pg_dump, stays in pg_stat_activity for a moment after it is gone.
	settle := time.Now().Add(quietGrace)
	for {
		var others int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()`, live).Scan(&others); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if others == 0 {
			return nil
		}
		if time.Now().After(settle) {
			return fmt.Errorf("backup: %d other sessions are connected to %s; stop the application first "+
				"(docker compose stop agentifi), or pass --disconnect", others, live)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("backup: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func createDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()+` TEMPLATE template0`); err != nil {
		return fmt.Errorf("backup: creating the database %s: %w", name, err)
	}
	return nil
}

func dropDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()); err != nil {
		return fmt.Errorf("backup: dropping the database %s: %w", name, err)
	}
	return nil
}

// swapDatabases renames live to previous and scratch to live. If the second
// rename fails the first is undone, so live always names a whole database.
func swapDatabases(ctx context.Context, conn *pgx.Conn, live, scratch, previous string) error {
	rename := func(from, to string) error {
		_, err := conn.Exec(ctx, `ALTER DATABASE `+pgx.Identifier{from}.Sanitize()+
			` RENAME TO `+pgx.Identifier{to}.Sanitize())
		return err
	}
	if err := rename(live, previous); err != nil {
		return fmt.Errorf("backup: setting %s aside (is something still connected to it?): %w", live, err)
	}
	if err := rename(scratch, live); err != nil {
		if undo := rename(previous, live); undo != nil {
			return fmt.Errorf("backup: renaming %s to %s failed (%v), and so did putting %s back: %w; "+
				"rename it by hand", scratch, live, err, previous, undo)
		}
		return fmt.Errorf("backup: renaming %s to %s: %w (the live database was put back)", scratch, live, err)
	}
	return nil
}

// restoreDatabase streams a set's dump, decrypted in memory, into pg_restore
// in one transaction.
func restoreDatabase(ctx context.Context, set Set, identities []age.Identity, target Postgres) error {
	if err := Checksum(set, PartDatabase); err != nil {
		return err
	}
	plain, closeFile, err := openPart(set, PartDatabase, identities)
	if err != nil {
		return err
	}
	defer closeFile()

	cmd, err := target.command(ctx, "pg_restore",
		"--no-owner", "--no-acl", "--single-transaction", "--exit-on-error")
	if err != nil {
		return err
	}
	var stderr tail
	cmd.Stderr = &stderr
	cmd.Stdin = plain
	if err := cmd.Run(); err != nil {
		return toolError("pg_restore", err, &stderr)
	}
	return nil
}

func openPart(set Set, part Part, identities []age.Identity) (io.Reader, func(), error) {
	path, ok := set.path(part)
	if !ok {
		return nil, nil, fmt.Errorf("backup: set %s has no %s part", set.Name, part)
	}
	in, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("backup: %w", err)
	}
	plain, err := open(in, set.Encrypted, identities)
	if err != nil {
		_ = in.Close()
		return nil, nil, err
	}
	return plain, func() { _ = in.Close() }, nil
}

// countRows counts every table outside the system schemas.
func countRows(ctx context.Context, db Postgres) ([]TableCount, error) {
	conn, err := db.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(ctx, `
		SELECT n.nspname, c.relname
		  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relkind IN ('r', 'p')
		   AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		   AND n.nspname NOT LIKE 'pg_toast%'
		 ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, fmt.Errorf("backup: listing tables: %w", err)
	}
	type table struct{ schema, name string }
	tables, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (table, error) {
		var one table
		err := row.Scan(&one.schema, &one.name)
		return one, err
	})
	if err != nil {
		return nil, fmt.Errorf("backup: listing tables: %w", err)
	}
	out := make([]TableCount, 0, len(tables))
	for _, one := range tables {
		var count int64
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{one.schema, one.name}.Sanitize()).
			Scan(&count); err != nil {
			return nil, fmt.Errorf("backup: counting %s: %w", one.name, err)
		}
		label := one.name
		if one.schema != "public" {
			label = one.schema + "." + one.name
		}
		out = append(out, TableCount{Table: label, Rows: count})
	}
	return out, nil
}

// readAttachments reads the attachments archive through, and unpacks it into
// dir when one is given. It counts regular files.
func readAttachments(set Set, identities []age.Identity, dir string) (int, error) {
	if _, ok := set.Files[PartAttachments]; !ok {
		return 0, nil
	}
	if err := Checksum(set, PartAttachments); err != nil {
		return 0, err
	}
	plain, closeFile, err := openPart(set, PartAttachments, identities)
	if err != nil {
		return 0, err
	}
	defer closeFile()
	count := 0
	err = walkTar(plain, "attachments", func(rel string, header *tar.Header, body io.Reader) error {
		target := ""
		if dir != "" {
			target = filepath.Join(dir, filepath.FromSlash(rel))
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if target != "" {
				return os.MkdirAll(target, 0o700)
			}
		case tar.TypeReg:
			count++
			if target == "" {
				_, err := io.Copy(io.Discard, body)
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o600|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, body); err != nil {
				_ = out.Close()
				return err
			}
			return out.Close()
		}
		return nil
	})
	if err != nil {
		return count, fmt.Errorf("backup: reading the attachments archive: %w", err)
	}
	return count, nil
}

// listSecrets names the files in the secrets archive without writing them.
func listSecrets(set Set, identities []age.Identity) ([]string, error) {
	if _, ok := set.Files[PartSecrets]; !ok {
		return nil, nil
	}
	if err := Checksum(set, PartSecrets); err != nil {
		return nil, err
	}
	plain, closeFile, err := openPart(set, PartSecrets, identities)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, nil
		}
		return nil, err
	}
	defer closeFile()
	var names []string
	err = walkTar(plain, "secrets", func(rel string, header *tar.Header, _ io.Reader) error {
		if header.Typeflag == tar.TypeReg {
			names = append(names, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup: reading the secrets archive: %w", err)
	}
	return names, nil
}

// walkTar visits each entry under top/ in a gzipped tar, with its path
// relative to top. An entry that would land outside top is refused, not
// skipped: a set is not supposed to hold one.
func walkTar(r io.Reader, top string, visit func(rel string, header *tar.Header, body io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, "./")
		clean := path.Clean(name)
		if clean == top {
			continue
		}
		if !strings.HasPrefix(clean, top+"/") || strings.Contains(clean, "..") || path.IsAbs(clean) {
			return fmt.Errorf("the archive holds %q, outside %s/", header.Name, top)
		}
		if err := visit(strings.TrimPrefix(clean, top+"/"), header, tr); err != nil {
			return err
		}
	}
}

// swapAttachments moves what is in root into aside, then what is in staging
// into root. root itself is usually a bind mount, which cannot be renamed.
func swapAttachments(root, staging, aside string) error {
	if err := os.Mkdir(aside, 0o700); err != nil {
		return err
	}
	current, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range current {
		name := entry.Name()
		if strings.HasPrefix(name, restoreStagingPrefix) || strings.HasPrefix(name, setAsidePrefix) {
			continue
		}
		if err := os.Rename(filepath.Join(root, name), filepath.Join(aside, name)); err != nil {
			return err
		}
	}
	restored, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, entry := range restored {
		if err := os.Rename(filepath.Join(staging, entry.Name()), filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(staging)
}
