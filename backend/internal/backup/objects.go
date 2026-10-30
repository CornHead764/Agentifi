package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"filippo.io/age"
	"github.com/google/uuid"
)

// The objects store, BACKUP_DIR/objects/<first two of name>/<name>, holds
// what sets share: household snapshots, named by the SHA-256 of their sealed
// bytes, and attachments, named by a keyed hash of their contents and the
// recipients they are sealed to. Keying the name keeps an attachment's own
// SHA-256, a confirmation of what it is, out of the plaintext manifest; folding
// in the recipients means a set never names an object sealed to keys it was
// not, so changing the keys seals every attachment again once.

// ObjectKey is the key attachment object names and household fingerprints
// are hashed with, from the install's credential key. A new credential key
// names every attachment afresh, which costs one more copy of each.
func ObjectKey(credentialKey string) []byte {
	sum := sha256.Sum256([]byte("agentifi:backup-object-key:" + credentialKey))
	return sum[:]
}

func keyedName(key []byte, parts ...string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(mac.Sum(nil))
}

// attachmentKey names an attachment's contents as sealed to these
// recipients. The object holding it is named by the key, or, when an object
// of that name is already on disk and is not one a set can use, by a name
// derived from it: an object is never overwritten, since sealing is not
// deterministic and a set that named the old one would no longer match it.
func attachmentKey(key []byte, recipients []string, contentSHA256 string) string {
	sorted := append([]string(nil), recipients...)
	sort.Strings(sorted)
	return keyedName(key, "attachment", strings.Join(sorted, "\n"), contentSHA256)
}

// freeObjectName is the first name for an attachment's key that no set
// records and nothing on disk is under. A name a set records is never given
// to other bytes, even once its object is gone, so that set goes on saying
// it is not intact rather than naming an object it cannot open. The caller
// holds lockBackups.
func freeObjectName(root, key string, recorded map[string]bool) string {
	name := key
	for i := 1; recorded[name] || exists(objectPath(root, name)); i++ {
		name = keyedName([]byte(key), "attachment-replacement", strconv.Itoa(i))
	}
	return name
}

// rename is os.Rename, which a test replaces to fail a step part way.
var rename = os.Rename

func objectPath(root, name string) string {
	return filepath.Join(root, objectsDir, name[:2], name)
}

func validObjectName(name string) bool {
	if len(name) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil && strings.ToLower(name) == name
}

// validHouseholdID is a household file's name as the store writes it, so a
// manifest can never name a path outside households/.
func validHouseholdID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

// putObject seals what fill writes into the objects store under name, or,
// when name is empty, under the SHA-256 of the sealed bytes. The object is
// renamed into place whole, so a reader never finds half of one, and never
// over an object already there. The caller holds lockBackups.
func putObject(root, name string, recipients []age.Recipient, fill func(io.Writer) error) (string, Object, error) {
	store := filepath.Join(root, objectsDir)
	if err := os.MkdirAll(store, 0o700); err != nil {
		return "", Object{}, fmt.Errorf("backup: %w", err)
	}
	out, err := os.CreateTemp(store, partialPrefix+"*")
	if err != nil {
		return "", Object{}, fmt.Errorf("backup: %w", err)
	}
	done := false
	defer func() {
		_ = out.Close()
		if !done {
			_ = os.Remove(out.Name())
		}
	}()
	sum := sha256.New()
	counted := &countingWriter{w: io.MultiWriter(out, sum)}
	sealed, err := seal(counted, recipients)
	if err != nil {
		return "", Object{}, fmt.Errorf("backup: %w", err)
	}
	if err := fill(sealed); err != nil {
		return "", Object{}, err
	}
	if err := sealed.Close(); err != nil {
		return "", Object{}, fmt.Errorf("backup: sealing an object: %w", err)
	}
	if err := out.Sync(); err != nil {
		return "", Object{}, fmt.Errorf("backup: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", Object{}, fmt.Errorf("backup: %w", err)
	}
	object := Object{Bytes: counted.n, SHA256: hex.EncodeToString(sum.Sum(nil))}
	if name == "" {
		name = object.SHA256
	}
	final := objectPath(root, name)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return "", Object{}, fmt.Errorf("backup: %w", err)
	}
	if exists(final) {
		if name == object.SHA256 && checksumFile(final, object.SHA256) == nil {
			return name, object, nil
		}
		return "", Object{}, fmt.Errorf("backup: object %s is already on disk", name)
	}
	if err := rename(out.Name(), final); err != nil {
		return "", Object{}, fmt.Errorf("backup: %w", err)
	}
	done = true
	return name, object, nil
}

// openObject checks an object against its recorded checksum, then returns
// its plaintext.
func openObject(set Set, name string, object Object, identities []age.Identity) (io.Reader, func(), error) {
	if !validObjectName(name) {
		return nil, nil, fmt.Errorf("backup: %q is not an object name", name)
	}
	path := objectPath(set.root(), name)
	if err := checksumFile(path, object.SHA256); err != nil {
		return nil, nil, fmt.Errorf("backup: object %s of %s: %w", name, set.Name, err)
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

var errChanged = errors.New("it has changed since it was written")

func checksumFile(path, want string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, in); err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != want {
		return errChanged
	}
	return nil
}

// CollectObjects removes every object no set in dir names, and what a run
// that died left half written, and says how many objects it removed. A set
// whose manifest cannot be read might name any object, so while one is on
// disk nothing is removed.
func CollectObjects(ctx context.Context, dir string) (int, error) {
	unlock, err := lockBackups(ctx, dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	return collectObjects(dir)
}

// collectObjects is CollectObjects for a caller that holds lockBackups.
func collectObjects(dir string) (int, error) {
	sets, err := List(dir)
	if err != nil {
		return 0, err
	}
	named := map[string]bool{}
	for _, set := range sets {
		if set.unknown {
			return 0, fmt.Errorf("backup: no object was removed: set %s cannot be read (%s), so what it names is unknown",
				set.Name, set.Problem)
		}
		for _, household := range set.Households {
			named[household.Object] = true
		}
		for name := range set.Objects {
			named[name] = true
		}
	}
	store := filepath.Join(dir, objectsDir)
	shards, err := os.ReadDir(store)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}
	removed := 0
	var errs []error
	for _, shard := range shards {
		path := filepath.Join(store, shard.Name())
		if !shard.IsDir() {
			if strings.HasPrefix(shard.Name(), partialPrefix) {
				errs = append(errs, os.Remove(path))
			}
			continue
		}
		objects, err := os.ReadDir(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		left := len(objects)
		for _, object := range objects {
			if named[object.Name()] || object.IsDir() {
				continue
			}
			if err := os.Remove(filepath.Join(path, object.Name())); err != nil {
				errs = append(errs, err)
				continue
			}
			removed++
			left--
		}
		if left == 0 {
			errs = append(errs, os.Remove(path))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return removed, fmt.Errorf("backup: removing unnamed objects: %w", err)
	}
	return removed, nil
}
