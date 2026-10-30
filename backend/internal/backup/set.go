// Package backup writes and restores the install's backups.
//
// A set is one directory under the backups directory: a SQLite snapshot of
// server.db, an index of the attachments and a tar of the secrets, each sealed
// with age, and a manifest.json that says what is there. Household snapshots
// and attachments live once in the objects store beside the sets, each sealed
// on its own, and a set names the objects it holds. A household whose file
// has not changed since the last set is not snapshotted again: the new set
// names the object the last one did. The manifest is plaintext and holds
// nothing secret: names, sizes, row counts, checksums of the ciphertext and
// the public recipients.
//
// A set is written under a ".partial-" name and renamed into place once every
// part is on disk, so a directory without that prefix is complete or was
// damaged afterwards, which List reports as not intact. Objects are written
// before the manifest that names them, and removed only when no set on disk
// names them.
package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	manifestName  = "manifest.json"
	partialPrefix = ".partial-"
	// FormatVersion is the manifest's layout. A reader refuses a newer one;
	// tarFormat, one sealed tar of every database, is still restored.
	FormatVersion = 3
	tarFormat     = 2
	objectsDir    = "objects"
	// nameLayout sorts a directory listing chronologically.
	nameLayout = "2006-01-02_150405"
	dateLayout = "2006-01-02"
)

// Trigger is what took a set.
type Trigger string

const (
	TriggerNightly Trigger = "nightly"
	TriggerManual  Trigger = "manual"
	// TriggerUpgrade is the set `agentifi migrate` takes before it changes the
	// schema.
	TriggerUpgrade Trigger = "upgrade"
	// TriggerRestore is the set a restore takes of what it is about to replace.
	TriggerRestore Trigger = "restore"
	// TriggerSpaceDelete is the set taken before a space is deleted.
	TriggerSpaceDelete Trigger = "space-delete"
)

// Part is one of the three things a set holds in its own directory.
type Part string

const (
	PartDatabase    Part = "database"
	PartAttachments Part = "attachments"
	PartSecrets     Part = "secrets"
)

// File is one part on disk.
type File struct {
	// Path is relative to the set's directory.
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	// SHA256 is of the file as stored (the ciphertext when encrypted), so it
	// can be checked without the identity.
	SHA256 string `json:"sha256,omitempty"`
	// Count is the number of files in a tar or an index.
	Count int `json:"count,omitempty"`
	// Names are the secrets a secrets tar holds; Skipped the ones the
	// process could not read.
	Names   []string `json:"names,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	// Tables is the rows in each table of a database snapshot.
	Tables map[string]int64 `json:"tables,omitempty"`
}

// Household is one household file in a format-3 set.
type Household struct {
	ID string `json:"id"`
	// Object is the snapshot's name in the objects store, which is also the
	// SHA-256 of its sealed bytes.
	Object        string `json:"object"`
	Bytes         int64  `json:"bytes"`
	SchemaVersion int64  `json:"schema_version"`
	// Fingerprint names the state of the live file the snapshot is of; a
	// later set that finds the file in the same state names this object
	// again rather than taking another.
	Fingerprint string `json:"fingerprint,omitempty"`
	// TakenIn is the set that took the snapshot.
	TakenIn  string           `json:"taken_in"`
	Verified bool             `json:"verified"`
	Tables   map[string]int64 `json:"tables"`
}

// Label is the household file's path inside a data directory.
func (h Household) Label() string {
	return "households/" + h.ID + ".db"
}

// Object is one sealed file in the objects store.
type Object struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Format    int       `json:"format"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Trigger   Trigger   `json:"trigger"`
	Encrypted bool      `json:"encrypted"`
	// Recipients are the public keys the parts were sealed to.
	Recipients    []string `json:"recipients,omitempty"`
	SchemaVersion int64    `json:"schema_version,omitempty"`
	// CredentialKeyID is KeyID of the key stored connections are
	// sealed with.
	CredentialKeyID string `json:"credential_key_id,omitempty"`
	// Verified means every SQLite snapshot passed PRAGMA integrity_check.
	Verified bool          `json:"verified"`
	Files    map[Part]File `json:"files"`
	// Households is every household file a format-3 set holds, and Objects
	// the attachments' objects by name.
	Households []Household       `json:"households,omitempty"`
	Objects    map[string]Object `json:"objects,omitempty"`
}

// Household finds one household in the set.
func (m Manifest) Household(id string) (Household, bool) {
	for _, household := range m.Households {
		if household.ID == id {
			return household, true
		}
	}
	return Household{}, false
}

// Set is a manifest as found on disk.
type Set struct {
	Manifest
	// Dir is the directory the manifest's paths are relative to.
	Dir string
	// Intact means every part and object is present at the size the
	// manifest recorded.
	Intact  bool
	Problem string
	// Bytes is everything a restore of the set reads, including objects
	// other sets share.
	Bytes int64
	// unknown is a set whose manifest cannot be read, so the objects it names
	// cannot be known either.
	unknown bool
}

// root is the backups directory the set is in, which holds the objects.
func (s Set) root() string {
	return filepath.Dir(s.Dir)
}

// Date is the calendar day the set was taken, in the zone of its name.
func (s Set) Date() string {
	if len(s.Name) >= len(dateLayout) {
		return s.Name[:len(dateLayout)]
	}
	return s.CreatedAt.Format(dateLayout)
}

// NewName is a set's directory name: its local time and what took it.
func NewName(at time.Time, trigger Trigger) string {
	return at.Format(nameLayout) + "_" + string(trigger)
}

// List reads every set in dir, newest first. A missing directory is no sets.
func List(dir string) ([]Set, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: reading %s: %w", dir, err)
	}
	var sets []Set
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.IsDir() || name == objectsDir {
			continue
		}
		set, ok := readSet(filepath.Join(dir, name))
		if ok {
			sets = append(sets, set)
		}
	}
	sort.SliceStable(sets, func(i, j int) bool {
		return sets[i].CreatedAt.After(sets[j].CreatedAt)
	})
	return sets, nil
}

func readSet(dir string) (Set, bool) {
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if errors.Is(err, os.ErrNotExist) {
		return Set{}, false
	}
	set := Set{Dir: dir}
	set.Name = filepath.Base(dir)
	if err != nil {
		set.Problem = "the manifest cannot be read: " + err.Error()
		set.unknown = true
		return set, true
	}
	if err := json.Unmarshal(data, &set.Manifest); err != nil {
		set.Problem = "the manifest is not valid JSON: " + err.Error()
		set.unknown = true
		return set, true
	}
	set.Name = filepath.Base(dir)
	if set.Format > FormatVersion {
		set.Problem = fmt.Sprintf("written by a newer version (format %d)", set.Format)
		set.unknown = true
		return set, true
	}
	if set.Format < tarFormat {
		set.Problem = legacyFormatError
		return set, true
	}
	set.check()
	return set, true
}

func (s *Set) check() {
	s.Intact = true
	s.Bytes = 0
	if _, ok := s.Files[PartDatabase]; !ok {
		s.Intact = false
		s.Problem = "there is no SQLite database archive"
		return
	}
	for _, part := range []Part{PartDatabase, PartAttachments, PartSecrets} {
		file, ok := s.Files[part]
		if !ok {
			continue
		}
		info, err := os.Stat(filepath.Join(s.Dir, file.Path))
		switch {
		case err != nil:
			s.Intact = false
			s.Problem = fmt.Sprintf("the %s file is missing", part)
		case info.Size() != file.Bytes:
			s.Intact = false
			s.Problem = fmt.Sprintf("the %s file is %d bytes, not the %d recorded", part, info.Size(), file.Bytes)
		default:
			s.Bytes += info.Size()
		}
	}
	if s.Format < FormatVersion || !s.Intact {
		return
	}
	for _, household := range s.Households {
		if !validHouseholdID(household.ID) {
			s.Intact = false
			s.Problem = fmt.Sprintf("the household %q is not a household file's name", household.ID)
			return
		}
		if !s.objectIntact(household.Object, household.Bytes, "the snapshot of "+household.Label()) {
			return
		}
	}
	names := make([]string, 0, len(s.Objects))
	for name := range s.Objects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !s.objectIntact(name, s.Objects[name].Bytes, "an attachment") {
			return
		}
	}
}

func (s *Set) objectIntact(name string, bytes int64, what string) bool {
	if !validObjectName(name) {
		s.Intact = false
		s.Problem = fmt.Sprintf("%s names no valid object", what)
		return false
	}
	info, err := os.Stat(objectPath(s.root(), name))
	switch {
	case err != nil:
		s.Intact = false
		s.Problem = fmt.Sprintf("%s (object %s) is missing", what, name)
		return false
	case info.Size() != bytes:
		s.Intact = false
		s.Problem = fmt.Sprintf("%s (object %s) is %d bytes, not the %d recorded", what, name, info.Size(), bytes)
		return false
	}
	s.Bytes += bytes
	return true
}

func (s Set) path(part Part) (string, bool) {
	file, ok := s.Files[part]
	if !ok {
		return "", false
	}
	return filepath.Join(s.Dir, file.Path), true
}

// remove deletes a set from disk.
func (s Set) remove() error {
	return os.RemoveAll(s.Dir)
}

// Prune deletes what PlanRetention picks and names what it deleted, then the
// objects no remaining set names. Called only after a set has been written
// and under Lock, so a failing night deletes nothing and no set is half
// written while the objects are collected.
func Prune(dir string, now time.Time, keepDays int) ([]string, error) {
	sets, err := List(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, set := range PlanRetention(sets, now, keepDays) {
		if err := set.remove(); err != nil {
			errs = append(errs, fmt.Errorf("backup: removing %s: %w", set.Name, err))
			continue
		}
		removed = append(removed, set.Name)
	}
	if _, err := CollectObjects(dir); err != nil {
		errs = append(errs, err)
	}
	return removed, errors.Join(errs...)
}

func writeManifest(dir string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manifestName), append(data, '\n'), 0o600)
}
