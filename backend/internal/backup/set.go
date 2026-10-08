// Package backup writes and restores the install's backups.
//
// A set is one directory under the backups directory: a snapshot of the
// SQLite database, a tar of the attachments and a tar of the secrets, each sealed
// with age, and a manifest.json that says what is there. The manifest is
// plaintext and holds nothing secret: names, sizes, checksums of the
// ciphertext and the public recipients.
//
// A set is written under a ".partial-" name and renamed into place once every
// part is on disk, so a directory without that prefix is complete or was
// damaged afterwards, which List reports as not intact.
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
	// FormatVersion is the manifest's layout. A reader refuses a newer one,
	// and an older one, whose database part is a Postgres dump.
	FormatVersion = 2
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

// Part is one of the three things a set holds.
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
	// Count is the number of files in a tar.
	Count int `json:"count,omitempty"`
	// Names are the secrets a secrets tar holds; Skipped the ones the
	// process could not read.
	Names   []string `json:"names,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
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
	// ServerVersion is the version of the SQLite library that took the
	// snapshot.
	ServerVersion string `json:"server_version,omitempty"`
	// CredentialKeyID is KeyID of the key the database's stored connections
	// are sealed with.
	CredentialKeyID string `json:"credential_key_id,omitempty"`
	// Verified means the snapshot passed SQLite's integrity check before it
	// was sealed.
	Verified bool          `json:"verified"`
	Files    map[Part]File `json:"files"`
}

// Set is a manifest as found on disk.
type Set struct {
	Manifest
	// Dir is the directory the manifest's paths are relative to.
	Dir string
	// Intact means every part is present at the size the manifest recorded.
	Intact  bool
	Problem string
	Bytes   int64
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
		if strings.HasPrefix(name, ".") || !entry.IsDir() {
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
		return set, true
	}
	if err := json.Unmarshal(data, &set.Manifest); err != nil {
		set.Problem = "the manifest is not valid JSON: " + err.Error()
		return set, true
	}
	set.Name = filepath.Base(dir)
	if set.Format > FormatVersion {
		set.Problem = fmt.Sprintf("written by a newer version (format %d)", set.Format)
		return set, true
	}
	if set.Format < FormatVersion {
		set.Problem = fmt.Sprintf("holds a Postgres dump (format %d), which this version cannot restore", set.Format)
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
		s.Problem = "there is no database snapshot"
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

// Prune deletes what PlanRetention picks and names what it deleted. Called
// only after a set has been written, so a failing night deletes nothing.
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
	return removed, errors.Join(errs...)
}

func writeManifest(dir string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manifestName), append(data, '\n'), 0o600)
}
