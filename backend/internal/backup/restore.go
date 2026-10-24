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
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

// Restore validates standalone SQLite snapshots before publishing them. The
// replaced files remain available for recovery; attachments are staged before
// the swap as well.

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

// Rehearse verifies and counts a set in a temporary directory, then removes it.
func Rehearse(ctx context.Context, set Set, identities []age.Identity, dataDir string, now time.Time) (Report, error) {
	report := Report{Set: set.Name}
	scratch, err := os.MkdirTemp("", "agentifi-rehearse-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(scratch)
	if report.Tables, err = unpackDatabases(ctx, set, identities, scratch); err != nil {
		return report, err
	}
	if report.Attachments, err = readAttachments(set, identities, ""); err != nil {
		return report, err
	}
	report.Secrets, err = listSecrets(set, identities)
	return report, err
}

type RestoreOptions struct {
	Set          Set
	Identities   []age.Identity
	DataDir      string
	KeepPrevious bool
	Now          time.Time
	Log          func(string)
	BackUp       func(context.Context) (Set, error)
	Migrate      func(context.Context) error
}

// Restore requires an offline server. It keeps the instance lock inode in
// place, including when DATA_DIR is a bind mount.
func Restore(ctx context.Context, opts RestoreOptions) (Report, error) {
	report := Report{Set: opts.Set.Name}
	lock, err := sqlitedb.AcquireLock(filepath.Join(opts.DataDir, "server.lock"))
	if err != nil {
		return report, fmt.Errorf("backup: stop the server before restoring: %w", err)
	}
	defer lock.Close()
	unlock, err := Lock(ctx, opts.DataDir)
	if err != nil {
		return report, err
	}
	defer unlock()
	stamp := opts.Now.UTC().Format("20060102T150405.000000000")
	scratch := filepath.Join(opts.DataDir, ".restore-"+stamp)
	if err := os.Mkdir(scratch, 0700); err != nil {
		return report, err
	}
	defer os.RemoveAll(scratch)
	if report.Tables, err = unpackDatabases(ctx, opts.Set, opts.Identities, scratch); err != nil {
		return report, err
	}
	staging := ""
	attachments := filepath.Join(opts.DataDir, "attachments")
	if _, ok := opts.Set.Files[PartAttachments]; ok {
		staging = filepath.Join(attachments, restoreStagingPrefix+stamp)
		if err := os.MkdirAll(staging, 0700); err != nil {
			return report, err
		}
		defer os.RemoveAll(staging)
		if report.Attachments, err = readAttachments(opts.Set, opts.Identities, staging); err != nil {
			return report, err
		}
	}
	if opts.BackUp != nil {
		before, err := opts.BackUp(ctx)
		if err != nil {
			return report, fmt.Errorf("backup: pre-restore backup failed; nothing changed: %w", err)
		}
		report.PreRestore = before.Name
	}
	aside := filepath.Join(opts.DataDir, ".before-restore-"+stamp)
	if err := os.Mkdir(aside, 0700); err != nil {
		return report, err
	}
	entries, err := os.ReadDir(opts.DataDir)
	if err != nil {
		return report, err
	}
	var moved []string
	undo := func() {
		for _, name := range []string{"server.db", "households"} {
			_ = os.RemoveAll(filepath.Join(opts.DataDir, name))
		}
		for _, name := range moved {
			_ = os.Rename(filepath.Join(aside, name), filepath.Join(opts.DataDir, name))
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "server.lock" || name == "backup.lock" || name == "attachments" {
			continue
		}
		if err := os.Rename(filepath.Join(opts.DataDir, name), filepath.Join(aside, name)); err != nil {
			for _, old := range moved {
				_ = os.Rename(filepath.Join(aside, old), filepath.Join(opts.DataDir, old))
			}
			return report, err
		}
		moved = append(moved, name)
	}
	// Publish server.db last so incomplete staging can never look live.
	for _, name := range []string{"households", "server.db"} {
		from := filepath.Join(scratch, name)
		if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(from, filepath.Join(opts.DataDir, name)); err != nil {
			undo()
			return report, err
		}
	}
	report.Previous = aside
	if staging != "" {
		attachmentsAside := filepath.Join(attachments, setAsidePrefix+stamp)
		if err := swapAttachments(attachments, staging, attachmentsAside); err != nil {
			return report, fmt.Errorf("backup: databases restored; attachments failed; previous data kept in %s: %w", aside, err)
		}
		report.SetAside = attachmentsAside
	}
	// Migrations open their own store; the command must not acquire the same
	// instance lock recursively. The restore retains it until completion.
	if opts.Migrate != nil {
		if err := opts.Migrate(ctx); err != nil {
			return report, fmt.Errorf("backup: restored; migration failed; previous data kept in %s: %w", aside, err)
		}
	}
	if !opts.KeepPrevious && report.SetAside != "" {
		if err := os.RemoveAll(report.SetAside); err != nil {
			return report, err
		}
		report.SetAside = ""
	}
	return report, nil
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
