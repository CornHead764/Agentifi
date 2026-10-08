package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
)

// Source is what a set is taken of.
type Source struct {
	Database SQLite
	// StoragePath is the attachments directory; empty or missing is an empty
	// archive.
	StoragePath string
	// SecretsDir holds one file per secret; empty leaves the part out.
	SecretsDir      string
	CredentialKeyID string
	SchemaVersion   int64
}

// Write takes one set into dir, sealed to recipients, or in plaintext when
// there are none. The caller holds Lock.
func Write(ctx context.Context, dir string, src Source, recipients []string, trigger Trigger, now time.Time) (Set, error) {
	sealTo, keys, err := parseRecipients(recipients)
	if err != nil {
		return Set{}, err
	}
	if err := Writable(dir); err != nil {
		return Set{}, err
	}
	tools := CheckTools(ctx, src.Database)
	if tools.Problem != "" {
		return Set{}, errors.New("backup: " + tools.Problem)
	}
	if err := clearPartials(dir); err != nil {
		return Set{}, err
	}
	if err := clearSnapshots(src.Database); err != nil {
		return Set{}, err
	}

	name := NewName(now, trigger)
	for i := 2; exists(filepath.Join(dir, name)); i++ {
		name = fmt.Sprintf("%s-%d", NewName(now, trigger), i)
	}
	partial := filepath.Join(dir, partialPrefix+name)
	if err := os.Mkdir(partial, 0o700); err != nil {
		return Set{}, fmt.Errorf("backup: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(partial)
		}
	}()

	manifest := Manifest{
		Format:          FormatVersion,
		Name:            name,
		CreatedAt:       now,
		Trigger:         trigger,
		Encrypted:       len(sealTo) > 0,
		Recipients:      keys,
		SchemaVersion:   src.SchemaVersion,
		CredentialKeyID: src.CredentialKeyID,
		Files:           map[Part]File{},
	}
	suffix := ""
	if manifest.Encrypted {
		suffix = ".age"
	}

	database, err := writePart(partial, "database.sqlite"+suffix, sealTo, func(w io.Writer) (File, error) {
		version, err := dumpDatabase(ctx, src.Database, w)
		manifest.ServerVersion = version
		return File{}, err
	})
	if err != nil {
		return Set{}, err
	}
	manifest.Files[PartDatabase] = database
	manifest.Verified = true

	attachments, err := writePart(partial, "attachments.tar.gz"+suffix, sealTo, func(w io.Writer) (File, error) {
		return tarAttachments(src.StoragePath, w)
	})
	if err != nil {
		return Set{}, err
	}
	manifest.Files[PartAttachments] = attachments

	if src.SecretsDir != "" {
		secrets, err := writePart(partial, "secrets.tar.gz"+suffix, sealTo, func(w io.Writer) (File, error) {
			return tarSecrets(src.SecretsDir, w)
		})
		if err != nil {
			return Set{}, err
		}
		manifest.Files[PartSecrets] = secrets
	}

	if err := writeManifest(partial, manifest); err != nil {
		return Set{}, fmt.Errorf("backup: writing the manifest: %w", err)
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(partial, final); err != nil {
		return Set{}, fmt.Errorf("backup: %w", err)
	}
	done = true
	set, _ := readSet(final)
	return set, nil
}

// Writable reports whether a set can be written into dir.
func Writable(dir string) error {
	if dir == "" {
		return errors.New("backup: BACKUP_DIR is not set")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("backup: the backup directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup: %s is not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, partialPrefix+"probe-")
	if err != nil {
		return fmt.Errorf("backup: this process cannot write to %s: %w", dir, err)
	}
	_ = probe.Close()
	return os.Remove(probe.Name())
}

// clearPartials removes what a run that died left behind. Only called under
// the lock, so nothing is still writing them.
func clearPartials(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), partialPrefix) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return fmt.Errorf("backup: clearing %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// writePart writes one part through the sealer and records its size and the
// checksum of what landed on disk.
func writePart(dir, name string, recipients []age.Recipient, fill func(io.Writer) (File, error)) (File, error) {
	path := filepath.Join(dir, name)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	defer out.Close()

	sum := sha256.New()
	counted := &countingWriter{w: io.MultiWriter(out, sum)}
	sealed, err := seal(counted, recipients)
	if err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	file, err := fill(sealed)
	if err != nil {
		return File{}, err
	}
	if err := sealed.Close(); err != nil {
		return File{}, fmt.Errorf("backup: sealing %s: %w", name, err)
	}
	if err := out.Sync(); err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	if err := out.Close(); err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	file.Path = name
	file.Bytes = counted.n
	file.SHA256 = hex.EncodeToString(sum.Sum(nil))
	return file, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Directories a restore leaves inside the attachments directory while it
// works, which a backup must not carry.
const (
	restoreStagingPrefix = ".restore-"
	setAsidePrefix       = ".before-restore-"
)

// tarAttachments archives the attachments as attachments/….
func tarAttachments(root string, w io.Writer) (File, error) {
	var file File
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if root != "" && exists(root) {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			if top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]; strings.HasPrefix(top, restoreStagingPrefix) ||
				strings.HasPrefix(top, setAsidePrefix) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return nil
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = "attachments/" + filepath.ToSlash(rel)
			header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
			if info.IsDir() {
				header.Name += "/"
				return tw.WriteHeader(header)
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if err := copyFile(tw, path); err != nil {
				return err
			}
			file.Count++
			return nil
		})
		if err != nil {
			return File{}, fmt.Errorf("backup: archiving the attachments: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	if err := gz.Close(); err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	return file, nil
}

// tarSecrets archives every secret file this process can read as secrets/…. A
// file it cannot read is named in the manifest rather than failing the run.
func tarSecrets(dir string, w io.Writer) (File, error) {
	var file File
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return File{}, fmt.Errorf("backup: reading %s: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			file.Skipped = append(file.Skipped, entry.Name())
			continue
		}
		header := &tar.Header{
			Name: "secrets/" + entry.Name(), Mode: 0o600, Size: int64(len(data)),
			ModTime: time.Now(), Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(header); err != nil {
			return File{}, fmt.Errorf("backup: %w", err)
		}
		if _, err := tw.Write(data); err != nil {
			return File{}, fmt.Errorf("backup: %w", err)
		}
		file.Names = append(file.Names, entry.Name())
		file.Count++
	}
	if err := tw.Close(); err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	if err := gz.Close(); err != nil {
		return File{}, fmt.Errorf("backup: %w", err)
	}
	return file, nil
}

func copyFile(w io.Writer, path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(w, in)
	return err
}

// Checksum re-reads a part and compares it with the manifest, which needs no
// identity: the checksum is of the ciphertext.
func Checksum(set Set, part Part) error {
	file, ok := set.Files[part]
	if !ok || file.SHA256 == "" {
		return nil
	}
	path, _ := set.path(part)
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, in); err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != file.SHA256 {
		return fmt.Errorf("backup: the %s file of %s has changed since it was written", part, set.Name)
	}
	return nil
}
