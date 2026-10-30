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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
)

// Source is what a set is taken of.
type Source struct {
	// DataDir holds the SQLite databases and attachments/; a missing attachments
	// directory is no attachments.
	DataDir string
	// SecretsDir holds one file per secret; empty leaves the part out.
	SecretsDir      string
	CredentialKeyID string
	// ObjectKey is ObjectKey of the credential key.
	ObjectKey     []byte
	SchemaVersion int64
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
	unlock, err := lockBackups(ctx, dir)
	if err != nil {
		return Set{}, err
	}
	defer unlock()
	if err := clearPartials(dir); err != nil {
		return Set{}, err
	}
	sets, err := List(dir)
	if err != nil {
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
	scratch, err := os.MkdirTemp("", "agentifi-snapshots-")
	if err != nil {
		return Set{}, err
	}
	defer os.RemoveAll(scratch)

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
		Households:      []Household{},
		Objects:         map[string]Object{},
	}
	suffix := ""
	if manifest.Encrypted {
		suffix = ".age"
	}

	server := filepath.Join(scratch, "server.db")
	if err := snapshotFile(ctx, filepath.Join(src.DataDir, "server.db"), server); err != nil {
		return Set{}, fmt.Errorf("backup: snapshotting server.db: %w", err)
	}
	counts, err := inspectFile(ctx, server, "server.db")
	if err != nil {
		return Set{}, err
	}
	database, err := writePart(partial, "server.db.gz"+suffix, sealTo, func(w io.Writer) (File, error) {
		return File{Count: 1, Tables: tables(counts, "server.db")}, gzipFile(w, server)
	})
	if err != nil {
		return Set{}, err
	}
	manifest.Files[PartDatabase] = database

	reusable := reusableHouseholds(sets, manifest)
	ids, err := householdIDs(src.DataDir)
	if err != nil {
		return Set{}, err
	}
	manifest.Verified = true
	for _, id := range ids {
		household, err := backUpHousehold(ctx, src, dir, scratch, id, sealTo, name, reusable)
		if err != nil {
			return Set{}, err
		}
		manifest.Verified = manifest.Verified && household.Verified
		manifest.Households = append(manifest.Households, household)
	}

	index, err := backUpAttachments(filepath.Join(src.DataDir, "attachments"), dir, src.ObjectKey, sealTo, keys,
		knownObjects(sets), recordedObjects(sets), manifest.Objects)
	if err != nil {
		return Set{}, err
	}
	attachments, err := writePart(partial, "attachments.json.gz"+suffix, sealTo, func(w io.Writer) (File, error) {
		gz := gzip.NewWriter(w)
		if err := json.NewEncoder(gz).Encode(index); err != nil {
			return File{}, err
		}
		return File{Count: len(index)}, gz.Close()
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
	written, _ := readSet(partial)
	if !written.Intact {
		return Set{}, fmt.Errorf("backup: the set just written is not intact: %s", written.Problem)
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(partial, final); err != nil {
		return Set{}, fmt.Errorf("backup: %w", err)
	}
	done = true
	set, _ := readSet(final)
	return set, nil
}

// reusableHouseholds is every household snapshot an intact set sealed to the
// same recipients holds, by fingerprint. A snapshot sealed to other keys is
// never named again: the new set would need a key it does not list.
func reusableHouseholds(sets []Set, next Manifest) map[string]Household {
	out := map[string]Household{}
	for _, set := range sets {
		if set.Format != FormatVersion || !set.Intact || set.Encrypted != next.Encrypted || !sameKeys(set.Recipients, next.Recipients) {
			continue
		}
		for _, household := range set.Households {
			if household.Fingerprint == "" || !household.Verified {
				continue
			}
			if _, seen := out[household.Fingerprint]; !seen {
				out[household.Fingerprint] = household
			}
		}
	}
	return out
}

func sameKeys(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}

// knownObject is an attachment object a set records.
type knownObject struct {
	Name string
	Object
}

// knownObjects is every attachment object the sets on disk record, by key,
// newest set first: the same key can name a replacement written after the
// object first named for it went missing.
func knownObjects(sets []Set) map[string][]knownObject {
	out := map[string][]knownObject{}
	seen := map[string]bool{}
	for _, set := range sets {
		if set.Format != FormatVersion {
			continue
		}
		for name, object := range set.Objects {
			if object.Key == "" || seen[name] {
				continue
			}
			seen[name] = true
			out[object.Key] = append(out[object.Key], knownObject{Name: name, Object: object})
		}
	}
	return out
}

// recordedObjects is every attachment object name a set on disk records.
func recordedObjects(sets []Set) map[string]bool {
	out := map[string]bool{}
	for _, set := range sets {
		for name := range set.Objects {
			out[name] = true
		}
	}
	return out
}

// usable reports whether an object is on disk as its record says, checksum
// and all, so a set may name it again.
func usable(root, name string, object Object) bool {
	if !validObjectName(name) {
		return false
	}
	info, err := os.Stat(objectPath(root, name))
	return err == nil && info.Size() == object.Bytes && checksumFile(objectPath(root, name), object.SHA256) == nil
}

// backUpHousehold names the earlier snapshot of a household whose file is as
// it was then, or else snapshots it, checks the snapshot and seals it into
// the objects store.
func backUpHousehold(ctx context.Context, src Source, root, scratch, id string, sealTo []age.Recipient,
	setName string, reusable map[string]Household) (Household, error) {
	live := filepath.Join(src.DataDir, "households", id+".db")
	current, err := liveFingerprint(ctx, src.ObjectKey, id, live)
	if err != nil {
		return Household{}, fmt.Errorf("backup: reading households/%s.db: %w", id, err)
	}
	if earlier, ok := reusable[current]; ok && current != "" && earlier.ID == id {
		if usable(root, earlier.Object, Object{Bytes: earlier.Bytes, SHA256: earlier.Object}) {
			return earlier, nil
		}
	}

	household := Household{ID: id, TakenIn: setName}
	snapshot := filepath.Join(scratch, "households", id+".db")
	if err := snapshotFile(ctx, live, snapshot); err != nil {
		return Household{}, fmt.Errorf("backup: snapshotting %s: %w", household.Label(), err)
	}
	defer os.Remove(snapshot)
	info, err := os.Stat(live)
	if err != nil {
		return Household{}, err
	}
	state, version, err := householdState(ctx, snapshot)
	if err != nil {
		return Household{}, err
	}
	counts, err := inspectFile(ctx, snapshot, household.Label())
	if err != nil {
		return Household{}, err
	}
	household.Fingerprint = fingerprint(src.ObjectKey, id, state, info)
	household.SchemaVersion = version
	household.Tables = tables(counts, household.Label())
	household.Verified = true
	name, object, err := putObject(root, "", sealTo, func(w io.Writer) error { return gzipFile(w, snapshot) })
	if err != nil {
		return Household{}, err
	}
	household.Object, household.Bytes = name, object.Bytes
	return household, nil
}

// attachment is one entry of a set's attachments index, which is sealed:
// it holds each file's own SHA-256.
type attachment struct {
	// Path is relative to DATA_DIR/attachments.
	Path   string `json:"path"`
	Object string `json:"object"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// backUpAttachments seals every attachment not already in the objects store,
// adds each object the set names to objects, and returns the index.
func backUpAttachments(source, root string, key []byte, sealTo []age.Recipient, keys []string,
	known map[string][]knownObject, recorded map[string]bool, objects map[string]Object) ([]attachment, error) {
	index := []attachment{}
	named := map[string]string{}
	err := walkAttachments(source, func(rel, path string, info fs.FileInfo) error {
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		entry := attachment{Path: rel, Bytes: info.Size(), SHA256: sum}
		contents := attachmentKey(key, keys, sum)
		if name, ok := named[contents]; ok {
			entry.Object = name
			index = append(index, entry)
			return nil
		}
		for _, earlier := range known[contents] {
			if usable(root, earlier.Name, earlier.Object) {
				entry.Object = earlier.Name
				named[contents], objects[earlier.Name] = earlier.Name, earlier.Object
				index = append(index, entry)
				return nil
			}
		}
		entry.Object = freeObjectName(root, contents, recorded)
		_, object, err := putObject(root, entry.Object, sealTo, func(w io.Writer) error {
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			copied := sha256.New()
			if _, err := io.Copy(io.MultiWriter(w, copied), in); err != nil {
				return err
			}
			if hex.EncodeToString(copied.Sum(nil)) != sum {
				return fmt.Errorf("backup: attachments/%s changed while it was being backed up", rel)
			}
			return nil
		})
		if err != nil {
			return err
		}
		object.Key = contents
		named[contents], objects[entry.Object] = entry.Object, object
		index = append(index, entry)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup: backing up the attachments: %w", err)
	}
	return index, nil
}

func hashFile(path string) (string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, in); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// walkAttachments visits every regular file under root, with its path
// relative to root, leaving out what a restore keeps there while it works.
func walkAttachments(root string, visit func(rel, path string, info fs.FileInfo) error) error {
	if root == "" || !exists(root) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
		if !info.Mode().IsRegular() {
			return nil
		}
		return visit(filepath.ToSlash(rel), path, info)
	})
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

// tarSecrets archives every secret file this process can read as secrets/…. A
// file it cannot read is named in the manifest rather than failing the run:
// its value is usually in another (POSTGRES_PASSWORD is inside DATABASE_URL).
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
	if err := checksumFile(path, file.SHA256); err != nil {
		if errors.Is(err, errChanged) {
			return fmt.Errorf("backup: the %s file of %s has changed since it was written", part, set.Name)
		}
		return err
	}
	return nil
}
