package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
)

// connectorState names the household-local columns shared by signed-in connectors.
type connectorState struct {
	table, what                                                     string
	session, credential                                             func(SpaceID, uuid.UUID) string
	enabled, lastRun, runStatus, runError, runShot, runTrail, wayIn string
}

const (
	runOK           = "ok"
	runNeedsSignIn  = "needs_sign_in"
	runSignInFailed = "sign_in_failed"
)

var billConnectorState = connectorState{
	table: "bill_connections", what: "bill connection", session: billConnectionSessionContext, credential: billConnectionCredentialContext,
	enabled: "pull_enabled", lastRun: "last_pulled_at", runStatus: "last_pull_status", runError: "last_pull_error",
	runShot: "last_pull_screenshot_document_id", runTrail: "last_pull_trail_document_id",
	wayIn: `(session_state IS NOT NULL OR credential_sealed IS NOT NULL OR credential_source NOT IN ('session', 'stored'))`,
}
var merchantConnectorState = connectorState{
	table: "merchant_accounts", what: "merchant account", session: merchantSessionContext, credential: merchantCredentialContext,
	enabled: "sync_enabled", lastRun: "last_synced_at", runStatus: "last_sync_status", runError: "last_sync_error",
	runShot: "last_sync_screenshot_document_id", wayIn: `session_state IS NOT NULL`,
}

func (s *Store) saveSession(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, state string, fresh bool, also string, args []any) error {
	cipher, err := s.requireCipher()
	if err != nil {
		return err
	}
	sealed, err := cipher.Seal(c.session(spaceID, id), state)
	if err != nil {
		return err
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	sets := `session_state = ?, `
	values := []any{sealed}
	if fresh {
		sets += `signed_in_at = ?, needs_sign_in = false, sign_in_paused_at = NULL, sign_in_paused_for = '', ` + c.lastRun + ` = NULL, ` + c.runStatus + ` = '', ` + c.runError + ` = '', ` + c.runShot + ` = NULL, `
		values = append(values, db.Now())
		if c.runTrail != "" {
			sets += c.runTrail + ` = NULL, `
		}
	}
	values = append(values, args...)
	values = append(values, db.Now(), id)
	return sqliteExecOne(ctx, db.Write(), "store: save "+c.what+" session", `UPDATE `+c.table+` SET `+sets+also+` updated_at = ? WHERE id = ?`, values...)
}
func (s *Store) clearSession(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: clear "+c.what+" session", `UPDATE `+c.table+` SET session_state = NULL, signed_in_at = NULL, needs_sign_in = true, updated_at = ? WHERE id = ?`, db.Now(), id)
}
func (s *Store) saveCredential(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, credential BillCredential, also string, args []any) error {
	sealed, err := s.sealJSON("store: save "+c.what+" credential", c.credential(spaceID, id), credential)
	if err != nil {
		return err
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	values := append([]any{sealed, credential.TOTPSecret != ""}, args...)
	values = append(values, db.Now(), id)
	return sqliteExecOne(ctx, db.Write(), "store: save "+c.what+" credential", `UPDATE `+c.table+` SET credential_sealed = ?, credential_has_totp = ?, sign_in_paused_at = NULL, sign_in_paused_for = '', `+also+` updated_at = ? WHERE id = ?`, values...)
}
func (s *Store) credentialOf(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) (BillCredential, error) {
	return openJSON[BillCredential](ctx, s, "store: "+c.what+" credential", c.table, "credential_sealed", c.credential(spaceID, id), spaceID, id)
}
func (s *Store) clearCredential(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, also string, args []any) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	values := append([]any{}, args...)
	values = append(values, db.Now(), id)
	return sqliteExecOne(ctx, db.Write(), "store: clear "+c.what+" credential", `UPDATE `+c.table+` SET credential_sealed = NULL, credential_has_totp = false, sign_in_paused_at = NULL, sign_in_paused_for = '', `+also+` updated_at = ? WHERE id = ?`, values...)
}

const MaxFailureScreenshotBytes = 1 << 20
const maxTrailBytes = 1 << 20

func keptTrail(trail []byte) []byte {
	if len(trail) == 0 || len(trail) > maxTrailBytes {
		return nil
	}
	return trail
}

// Connector evidence uses the same content-addressed attachment layout as pgimport.
// Publish bytes before metadata: rollback can leave an unreferenced file, never a
// committed document whose bytes have not been published. Shared blobs are not
// deleted when a connector clears its reference; document lifecycle owns cleanup.
func (s *Store) connectorDocument(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, column string, data []byte, contentType string) (any, error) {
	if len(data) == 0 {
		return nil, nil
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	key := filepath.ToSlash(filepath.Join("attachments", spaceID.String(), hash))
	path := filepath.Join(s.sqlite.dataDir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".connector-*")
	if err != nil {
		return nil, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(temp, path); err != nil {
		return nil, err
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	documentID := uuid.New()
	now := db.Now()
	source := DocumentSourceBillPull
	if c.table == "merchant_accounts" {
		source = DocumentSourceMerchantPull
	}
	_, err = db.Write().ExecContext(ctx, `INSERT INTO documents (id, content_sha256, content_type, size_bytes, filename, storage_key, source, source_ref, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (content_sha256) DO NOTHING`, documentID, hash, contentType, len(data), column, key, source, c.table+"/"+id.String()+"/"+column, now, now)
	if err != nil {
		return nil, wrap("store: save connector evidence", err)
	}
	err = db.Read().QueryRowContext(ctx, `SELECT id FROM documents WHERE content_sha256 = ?`, hash).Scan(&documentID)
	return documentID, wrap("store: find connector evidence", err)
}
func (s *Store) writeRunEvidence(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, status, detail string, shot, trail []byte, signIn bool) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		// Preserve markRun's no-op on a deleted row without creating orphan metadata.
		var exists bool
		err = db.Read().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+c.table+` WHERE id = ?)`, id).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			if signIn {
				return ErrNotFound
			}
			return nil
		}
		if len(shot) > MaxFailureScreenshotBytes {
			shot = nil
		}
		shotID, err := tx.connectorDocument(ctx, c, spaceID, id, c.runShot, shot, http.DetectContentType(shot))
		if err != nil {
			return err
		}
		sets := c.runStatus + ` = ?, ` + c.runError + ` = ?, ` + c.runShot + ` = ?, `
		args := []any{status, detail, shotID}
		if c.runTrail != "" {
			trailID, err := tx.connectorDocument(ctx, c, spaceID, id, c.runTrail, keptTrail(trail), "application/json")
			if err != nil {
				return err
			}
			sets += c.runTrail + ` = ?, `
			args = append(args, trailID)
		}
		if !signIn {
			sets += c.lastRun + ` = ?, needs_sign_in = ?, sign_in_paused_at = CASE WHEN ? THEN NULL ELSE sign_in_paused_at END, sign_in_paused_for = CASE WHEN ? THEN '' ELSE sign_in_paused_for END, `
			args = append(args, db.Now(), status == runNeedsSignIn, status == runOK, status == runOK)
		}
		args = append(args, db.Now(), id)
		return sqliteExecOne(ctx, db.Write(), "store: mark "+c.what+" run", `UPDATE `+c.table+` SET `+sets+`updated_at = ? WHERE id = ?`, args...)
	})
}
func (s *Store) markRun(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, status, detail string, shot, trail []byte) error {
	return s.writeRunEvidence(ctx, c, spaceID, id, status, detail, shot, trail, false)
}
func (s *Store) markSignInEnded(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, detail string, shot, trail []byte) error {
	return s.writeRunEvidence(ctx, c, spaceID, id, runSignInFailed, detail, shot, trail, true)
}
func (s *Store) pauseSignIn(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, why string) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `UPDATE `+c.table+` SET sign_in_paused_at = ?, sign_in_paused_for = ?, updated_at = ? WHERE id = ?`, db.Now(), why, db.Now(), id)
	return wrap("store: pause "+c.what+" sign-in", err)
}
func (s *Store) noteRunSkipped(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, detail string) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	trail := ""
	if c.runTrail != "" {
		trail = c.runTrail + ` = NULL, `
	}
	_, err = db.Write().ExecContext(ctx, `UPDATE `+c.table+` SET `+c.runError+` = ?, `+c.runShot+` = NULL, `+trail+`updated_at = ? WHERE id = ? AND `+c.runError+` <> ?`, detail, db.Now(), id, detail)
	return wrap("store: note a skipped "+c.what+" run", err)
}
func (s *Store) connectorEvidence(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, column string) ([]byte, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	var key sql.Null[string]
	err = db.Read().QueryRowContext(ctx, `SELECT d.storage_key FROM `+c.table+` c LEFT JOIN documents d ON d.id = c.`+column+` WHERE c.id = ?`, id).Scan(&key)
	if err != nil {
		return nil, wrap("store: connector evidence", err)
	}
	if !key.Valid {
		return nil, ErrNotFound
	}
	path := filepath.Join(s.sqlite.dataDir, filepath.FromSlash(key.V))
	rel, err := filepath.Rel(s.sqlite.dataDir, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("store: invalid connector evidence storage key")
	}
	data, err := os.ReadFile(path)
	return data, wrap("store: read connector evidence", err)
}
func (s *Store) runTrailOf(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) ([]byte, error) {
	return s.connectorEvidence(ctx, c, spaceID, id, c.runTrail)
}
func (s *Store) runScreenshot(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) ([]byte, error) {
	return s.connectorEvidence(ctx, c, spaceID, id, c.runShot)
}

// listDue merges each file's listing by creation time. This is not a cross-file
// snapshot or a claim; callers must still coordinate scheduled execution.
func listDue(ctx context.Context, s *Store, c connectorState, since time.Time) ([]MerchantAccount, error) {
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := []MerchantAccount{}
	for _, id := range ids {
		h, err := s.scoped(ctx, id)
		if err != nil {
			return nil, err
		}
		db, err := h.household(ctx, id.UUID())
		if err != nil {
			return nil, err
		}
		currency, err := h.primaryCurrency()
		if err != nil {
			return nil, err
		}
		found, err := sqliteQueryAll(ctx, db.Read(), "store: "+c.what+"s due", func(row scanner) (MerchantAccount, error) {
			return scanMerchantAccount(row, id, currency)
		}, `SELECT `+merchantAccountColumns+` FROM `+c.table+` WHERE `+c.enabled+` AND sign_in_paused_at IS NULL AND (NOT needs_sign_in OR credential_sealed IS NOT NULL) AND `+c.wayIn+` AND (`+c.lastRun+` IS NULL OR `+c.lastRun+` < ?) ORDER BY created_at`, unixMillis(since))
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
