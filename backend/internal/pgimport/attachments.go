package pgimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Keys are relative to the data directory, not to the old STORAGE_PATH.
// Trails are JSON diagnostic artifacts, not images; documents has no source
// or content-type CHECK, so bill_pull / application/json preserves them.
func (s *source) attach(ctx context.Context, t *target, data map[string][]row, storage string) error {
	_ = ctx
	byHash := map[string]row{}
	put := func(bytes []byte, hash string) (string, error) {
		sum := sha256.Sum256(bytes)
		actual := hex.EncodeToString(sum[:])
		if hash != "" && actual != hash {
			return "", fmt.Errorf("attachment SHA-256 mismatch: expected %s got %s", hash, actual)
		}
		key := filepath.ToSlash(filepath.Join("attachments", t.id, actual))
		dest := filepath.Join(t.dir, filepath.FromSlash(key))
		if e := os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return "", e
		}
		if _, e := os.Stat(dest); os.IsNotExist(e) {
			if e = os.WriteFile(dest, bytes, 0600); e != nil {
				return "", e
			}
			t.attachments++
		} else if e != nil {
			return "", e
		}
		copied, e := os.ReadFile(dest)
		if e != nil {
			return "", e
		}
		check := sha256.Sum256(copied)
		if check != sum || len(copied) != len(bytes) {
			return "", fmt.Errorf("copied attachment verification failed %s", key)
		}
		return key, nil
	}
	for _, r := range data["documents"] {
		old := val(r, "storage_key")
		if storage == "" {
			return fmt.Errorf("document %s requires --storage-path", val(r, "id"))
		}
		clean := filepath.Clean(filepath.FromSlash(old))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("document %s unsafe storage_key %q", val(r, "id"), old)
		}
		bytes, e := os.ReadFile(filepath.Join(storage, clean))
		if e != nil {
			return fmt.Errorf("document %s: %w", val(r, "id"), e)
		}
		if strconv.Itoa(len(bytes)) != val(r, "size_bytes") {
			return fmt.Errorf("document %s size mismatch", val(r, "id"))
		}
		key, e := put(bytes, val(r, "content_sha256"))
		if e != nil {
			return fmt.Errorf("document %s: %w", val(r, "id"), e)
		}
		r["storage_key"] = ptr(key)
		byHash[val(r, "content_sha256")] = r
	}
	for _, spec := range []struct{ table, col, source string }{{"bill_connections", "last_pull_screenshot", "bill_pull"}, {"bill_connections", "last_pull_trail", "bill_pull"}, {"merchant_accounts", "last_sync_screenshot", "merchant_pull"}} {
		for _, r := range data[spec.table] {
			if r[spec.col] == nil {
				continue
			}
			var bytes []byte
			var e error
			ct, filename := "application/json", spec.col+".json"
			if strings.HasSuffix(spec.col, "screenshot") {
				bytes, e = hex.DecodeString(strings.TrimPrefix(val(r, spec.col), `\x`))
				if e != nil {
					return e
				}
				ct = http.DetectContentType(bytes)
				filename = spec.col + ".png"
			} else {
				bytes = []byte(val(r, spec.col))
			}
			sum := sha256.Sum256(bytes)
			hash := hex.EncodeToString(sum[:])
			doc := byHash[hash]
			if doc == nil {
				key, e := put(bytes, hash)
				if e != nil {
					return e
				}
				doc = row{"id": ptr(uuid.NewSHA1(uuid.NameSpaceOID, []byte(t.id+"/"+hash)).String()), "content_sha256": ptr(hash), "content_type": ptr(ct), "size_bytes": ptr(strconv.Itoa(len(bytes))), "filename": ptr(filename), "storage_key": ptr(key), "source": ptr(spec.source), "source_ref": ptr(spec.table + "/" + val(r, "id") + "/" + spec.col), "created_at": r["created_at"], "updated_at": r["updated_at"]}
				data["documents"] = append(data["documents"], doc)
				t.added[val(doc, "id")] = doc
				byHash[hash] = doc
			}
			r[spec.col+"_document_id"] = doc["id"]
		}
	}
	return nil
}
