package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Set string
	// Household is the one household restored, or empty for the whole set.
	Household   string
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
	return RehearseHousehold(ctx, set, identities, "")
}

// RehearseHousehold rehearses restoring one household of a set, or the whole
// set when household is empty.
func RehearseHousehold(ctx context.Context, set Set, identities []age.Identity, household string) (Report, error) {
	report := Report{Set: set.Name, Household: household}
	scratch, err := os.MkdirTemp("", "agentifi-rehearse-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(scratch)
	if report.Tables, err = unpackDatabases(ctx, set, identities, scratch, household); err != nil {
		return report, err
	}
	if report.Attachments, err = readAttachments(set, identities, "", household); err != nil {
		return report, err
	}
	if household != "" {
		return report, nil
	}
	report.Secrets, err = listSecrets(set, identities)
	return report, err
}

type RestoreOptions struct {
	Set        Set
	Identities []age.Identity
	DataDir    string
	// Household restores that one household's file and attachments and
	// leaves server.db and every other household as they are.
	Household    string
	KeepPrevious bool
	Now          time.Time
	Log          func(string)
	BackUp       func(context.Context) (Set, error)
	Migrate      func(context.Context) error
}

// Restore requires an offline server. It keeps the instance lock inode in
// place, including when DATA_DIR is a bind mount.
func Restore(ctx context.Context, opts RestoreOptions) (Report, error) {
	report := Report{Set: opts.Set.Name, Household: opts.Household}
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
	if opts.Household != "" {
		if !validHouseholdID(opts.Household) {
			return report, fmt.Errorf("backup: %q is not a household's id", opts.Household)
		}
		if err := liveHousehold(ctx, opts.DataDir, opts.Household); err != nil {
			return report, err
		}
	}
	stamp := opts.Now.UTC().Format("20060102T150405.000000000")
	scratch := filepath.Join(opts.DataDir, ".restore-"+stamp)
	if err := os.Mkdir(scratch, 0700); err != nil {
		return report, err
	}
	defer os.RemoveAll(scratch)
	staging := ""
	attachments := filepath.Join(opts.DataDir, "attachments")
	if _, ok := opts.Set.Files[PartAttachments]; ok {
		staging = filepath.Join(attachments, restoreStagingPrefix+stamp)
		defer os.RemoveAll(staging)
	}
	// The set is read under lockBackups so another install sharing the
	// directory cannot collect its objects half way. The lock is let go
	// before BackUp, whose Write takes it again.
	err = func() error {
		unlockSet, err := lockBackups(ctx, opts.Set.root())
		if err != nil {
			return err
		}
		defer unlockSet()
		if report.Tables, err = unpackDatabases(ctx, opts.Set, opts.Identities, scratch, opts.Household); err != nil {
			return err
		}
		if staging == "" {
			return nil
		}
		if err := os.MkdirAll(staging, 0700); err != nil {
			return err
		}
		report.Attachments, err = readAttachments(opts.Set, opts.Identities, staging, opts.Household)
		return err
	}()
	if err != nil {
		return report, err
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
	if opts.Household != "" {
		if err := publishHousehold(opts.DataDir, scratch, aside, opts.Household); err != nil {
			return report, err
		}
	} else if err := publishAll(opts.DataDir, scratch, aside); err != nil {
		return report, err
	}
	report.Previous = aside
	if staging != "" {
		attachmentsAside := filepath.Join(attachments, setAsidePrefix+stamp)
		var err error
		if opts.Household != "" {
			err = swapHouseholdAttachments(attachments, staging, attachmentsAside, opts.Household)
		} else {
			err = swapAttachments(attachments, staging, attachmentsAside)
		}
		if err != nil {
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

// liveHousehold refuses a household the live server.db does not have: the
// file alone would be served to no one. Restoring the whole set brings a
// deleted space back.
func liveHousehold(ctx context.Context, dataDir, id string) error {
	db, err := sqlitedb.Open(ctx, filepath.Join(dataDir, "server.db"), time.Now)
	if err != nil {
		return err
	}
	defer db.Close()
	var deleted int
	err = db.Read().QueryRowContext(ctx, `SELECT is_deleted FROM households WHERE id = ?`, id).Scan(&deleted)
	if err != nil || deleted != 0 {
		return fmt.Errorf("backup: this install has no household %s; restore the whole set to bring a deleted space back", id)
	}
	return nil
}

// publishAll moves the current contents of the data directory aside and the
// staged databases in.
func publishAll(dataDir, scratch, aside string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	var moved []string
	undo := func() {
		for _, name := range []string{"server.db", "households"} {
			_ = os.RemoveAll(filepath.Join(dataDir, name))
		}
		for _, name := range moved {
			_ = os.Rename(filepath.Join(aside, name), filepath.Join(dataDir, name))
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "server.lock" || name == "backup.lock" || name == "attachments" {
			continue
		}
		if err := os.Rename(filepath.Join(dataDir, name), filepath.Join(aside, name)); err != nil {
			for _, old := range moved {
				_ = os.Rename(filepath.Join(aside, old), filepath.Join(dataDir, old))
			}
			return err
		}
		moved = append(moved, name)
	}
	// Publish server.db last so incomplete staging can never look live.
	for _, name := range []string{"households", "server.db"} {
		from := filepath.Join(scratch, name)
		if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(from, filepath.Join(dataDir, name)); err != nil {
			undo()
			return err
		}
	}
	return nil
}

// publishHousehold moves one household's live file aside, with any WAL and
// shared-memory file beside it, which would otherwise be read into the
// restored file, and the staged file in.
func publishHousehold(dataDir, scratch, aside, id string) error {
	households := filepath.Join(dataDir, "households")
	asideHouseholds := filepath.Join(aside, "households")
	if err := os.MkdirAll(households, 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(asideHouseholds, 0o700); err != nil {
		return err
	}
	name := id + ".db"
	files := []string{name, name + "-wal", name + "-shm"}
	var moved []string
	// Every one of files was moved aside first, so whatever is under those
	// names now came from the staging directory: a restored WAL left beside
	// the original file would be read into it.
	undo := func() {
		for _, file := range files {
			_ = os.Remove(filepath.Join(households, file))
		}
		for _, file := range moved {
			_ = os.Rename(filepath.Join(asideHouseholds, file), filepath.Join(households, file))
		}
	}
	for _, file := range files {
		err := os.Rename(filepath.Join(households, file), filepath.Join(asideHouseholds, file))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			undo()
			return err
		}
		moved = append(moved, file)
	}
	for _, file := range []string{name + "-wal", name} {
		from := filepath.Join(scratch, "households", file)
		if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := rename(from, filepath.Join(households, file)); err != nil {
			undo()
			return err
		}
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

// readAttachments reads the set's attachments through, every one or one
// household's, and writes them into dir when one is given. It counts files.
func readAttachments(set Set, identities []age.Identity, dir, household string) (int, error) {
	if _, ok := set.Files[PartAttachments]; !ok {
		return 0, nil
	}
	if err := Checksum(set, PartAttachments); err != nil {
		return 0, err
	}
	var count int
	var err error
	if set.Format == FormatVersion {
		count, err = readAttachmentObjects(set, identities, dir, household)
	} else {
		count, err = readAttachmentsTar(set, identities, dir, household)
	}
	if err != nil {
		return count, fmt.Errorf("backup: reading the attachments: %w", err)
	}
	return count, nil
}

func inHousehold(rel, household string) bool {
	return household == "" || strings.HasPrefix(rel, household+"/")
}

func readAttachmentObjects(set Set, identities []age.Identity, dir, household string) (int, error) {
	plain, closeFile, err := openPart(set, PartAttachments, identities)
	if err != nil {
		return 0, err
	}
	var index []attachment
	gz, err := gzip.NewReader(plain)
	if err == nil {
		err = json.NewDecoder(gz).Decode(&index)
		_ = gz.Close()
	}
	closeFile()
	if err != nil {
		return 0, fmt.Errorf("the index: %w", err)
	}
	count := 0
	for _, entry := range index {
		clean := path.Clean(entry.Path)
		if clean != entry.Path || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || path.IsAbs(clean) {
			return count, fmt.Errorf("the index holds %q, outside attachments/", entry.Path)
		}
		if !inHousehold(clean, household) {
			continue
		}
		object, ok := set.Objects[entry.Object]
		if !ok {
			return count, fmt.Errorf("%s names object %s, which the manifest does not", entry.Path, entry.Object)
		}
		body, closeObject, err := openObject(set, entry.Object, object, identities)
		if err != nil {
			return count, err
		}
		err = writeAttachment(dir, clean, entry, body)
		closeObject()
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// writeAttachment checks one attachment against its recorded checksum while
// writing it into dir, or only reads it through when dir is empty.
func writeAttachment(dir, rel string, entry attachment, body io.Reader) error {
	sum := sha256.New()
	var out io.Writer = sum
	var file *os.File
	if dir != "" {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		var err error
		if file, err = os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err != nil {
			return err
		}
		out = io.MultiWriter(file, sum)
	}
	n, err := io.Copy(out, body)
	if file != nil {
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		return err
	}
	if n != entry.Bytes || hex.EncodeToString(sum.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("%s is not the file that was backed up", rel)
	}
	return nil
}

func readAttachmentsTar(set Set, identities []age.Identity, dir, household string) (int, error) {
	plain, closeFile, err := openPart(set, PartAttachments, identities)
	if err != nil {
		return 0, err
	}
	defer closeFile()
	count := 0
	err = walkTar(plain, "attachments", func(rel string, header *tar.Header, body io.Reader) error {
		if !inHousehold(rel, household) {
			return nil
		}
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
	return count, err
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

// swapHouseholdAttachments does what swapAttachments does for one
// household's directory alone.
func swapHouseholdAttachments(root, staging, aside, id string) error {
	if err := os.Mkdir(aside, 0o700); err != nil {
		return err
	}
	current := filepath.Join(root, id)
	if err := os.Rename(current, filepath.Join(aside, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	restored := filepath.Join(staging, id)
	if err := os.Rename(restored, current); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(staging)
}
