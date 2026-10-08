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
)

// A restore never writes into the live database. It decrypts the set's
// snapshot into a scratch file beside the live one, checks it, and renames it
// over the live file, so a set that fails half way leaves the install exactly
// as it was; and it unpacks the attachments beside the current ones before
// the swap, so the step after it is a handful of renames.

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
	// file and the directory the replaced attachments are in.
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

// Rehearse decrypts a set's database into a scratch file beside the live one,
// checks it and counts its rows, reads its archives through, and removes the
// scratch file. The live database is never touched, so a rehearsal runs
// under a live server.
func Rehearse(ctx context.Context, set Set, identities []age.Identity, db SQLite, now time.Time) (Report, error) {
	report := Report{Set: set.Name}
	if db.Path == "" {
		return report, errors.New("backup: DATABASE_PATH is not set")
	}
	scratch := scratchPath(db, "rehearse", now)
	defer func() { _ = removeDatabase(scratch) }()

	var err error
	if err = extractDatabase(set, identities, scratch); err != nil {
		return report, err
	}
	if report.Tables, err = inspect(ctx, scratch, set.SchemaVersion); err != nil {
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
	Database   SQLite
	// StoragePath is the attachments directory; empty leaves them alone.
	StoragePath string
	// KeepPrevious keeps the replaced database and attachments rather than
	// removing them once the restore has succeeded.
	KeepPrevious bool
	Now          time.Time
	Log          func(string)
	// BackUp takes the set of what is about to be replaced. It must leave no
	// connection open to the live database.
	BackUp func(context.Context) (Set, error)
	// Migrate brings the restored database up to this binary's schema.
	Migrate func(context.Context) error
}

// Restore puts a set's database and attachments in place of the live ones. The
// caller has checked PlanRestore and holds no connection to the live
// database. It refuses while any other process has the database open: a
// server left running would go on writing into the file it had open, which
// the rename leaves behind.
func Restore(ctx context.Context, opts RestoreOptions) (Report, error) {
	report := Report{Set: opts.Set.Name}
	say := opts.Log
	if say == nil {
		say = func(string) {}
	}
	live := opts.Database.Path
	if live == "" {
		return report, errors.New("backup: DATABASE_PATH is not set")
	}
	stamp := opts.Now.Format("20060102150405")
	scratch := scratchPath(opts.Database, "restore", opts.Now)
	previous := scratchPath(opts.Database, "before-restore", opts.Now)

	if err := quiet(ctx, opts.Database); err != nil {
		return report, err
	}

	var err error
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
	cleanUp := func() {
		_ = removeDatabase(scratch)
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
	}

	say("backing up the current database")
	before, err := opts.BackUp(ctx)
	if err != nil {
		cleanUp()
		return report, fmt.Errorf("backup: the backup taken before the restore failed, so nothing was changed: %w", err)
	}
	report.PreRestore = before.Name
	say("wrote " + before.Name)

	say("restoring into " + scratch)
	if err := extractDatabase(opts.Set, opts.Identities, scratch); err != nil {
		cleanUp()
		return report, fmt.Errorf("%w (the live database was not changed)", err)
	}
	if report.Tables, err = inspect(ctx, scratch, opts.Set.SchemaVersion); err != nil {
		cleanUp()
		return report, fmt.Errorf("%w (the live database was not changed)", err)
	}

	say("swapping " + scratch + " in as " + live)
	if err := quiet(ctx, opts.Database); err != nil {
		cleanUp()
		return report, err
	}
	if err := swapDatabases(live, scratch, previous); err != nil {
		cleanUp()
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
		if err := removeDatabase(previous); err != nil {
			say("could not remove " + previous + ": " + err.Error())
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

// scratchPath is a file beside the live database, on the same filesystem so
// that a rename can swap it in.
func scratchPath(db SQLite, what string, now time.Time) string {
	return db.Path + "." + what + "-" + now.Format("20060102150405")
}

// extractDatabase decrypts a set's snapshot into a new file at target.
func extractDatabase(set Set, identities []age.Identity, target string) error {
	if err := Checksum(set, PartDatabase); err != nil {
		return err
	}
	plain, closeFile, err := openPart(set, PartDatabase, identities)
	if err != nil {
		return err
	}
	defer closeFile()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, err = io.Copy(out, plain)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("backup: writing out the database: %w", err)
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
