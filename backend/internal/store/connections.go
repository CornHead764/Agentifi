package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"
)

// The SimpleFIN connection, and the account-side lookups its sync needs.
//
// Connection carries no credential field: the Access URL is written by
// CreateConnection and read only by ConnectionAccessURL, so no listing or
// response can leak it. One Access URL spans every bank linked at the Bridge,
// so Status is the connection's and SyncErrors is per bank.

type ConnectionStatus string

const (
	ConnectionActive ConnectionStatus = "active"
	// ConnectionCredentialsExpired is the Access URL itself being refused;
	// only a fresh setup token fixes it, and only it prompts a reconnect.
	ConnectionCredentialsExpired ConnectionStatus = "credentials_expired"
	// ConnectionRateLimited is the Bridge throttling us; see RetryNotBefore.
	ConnectionRateLimited ConnectionStatus = "rate_limited"
	// ConnectionPendingLink is a claimed connection whose accounts have not
	// been matched to the household's existing ones. Nothing imports until
	// they are, or a Simplifi migration's history is silently duplicated
	// (docs/importing.md).
	ConnectionPendingLink ConnectionStatus = "pending_link"
)

// BankSyncError is one institution inside a connection that needs attention.
// Kept apart from the connection's status: the other banks still sync.
type BankSyncError struct {
	Institution string    `json:"institution"`
	Message     string    `json:"message"`
	At          time.Time `json:"at"`
}

// Connection is the connections row, minus the credential.
type Connection struct {
	ID      uuid.UUID
	SpaceID SpaceID
	Name    string

	Status       ConnectionStatus
	StatusDetail string
	SyncErrors   []BankSyncError

	LastSyncAt           *time.Time
	LastSuccessfulSyncAt *time.Time
	// RetryNotBefore parks a throttled connection; the caller refuses an
	// earlier sync.
	RetryNotBefore *time.Time

	IsDeleted bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NeedsSetupToken reports the one failure the user must fix with a new token.
func (c Connection) NeedsSetupToken() bool {
	return c.Status == ConnectionCredentialsExpired
}

const connectionColumns = `id, name, status, status_detail, sync_errors, last_sync_at,
	last_successful_sync_at, retry_not_before, is_deleted, created_at, updated_at`

// CreateConnection writes the row and seals the Access URL into it.
func (s *Store) CreateConnection(ctx context.Context, spaceID SpaceID, c *Connection, accessURL string) error {
	errorsJSON, err := syncErrorsArg(c.SyncErrors)
	if err != nil {
		return err
	}
	// The credential is sealed against its row id, so the id comes first.
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	sealed, err := s.sealString(connectionCredentialContext(spaceID, c.ID), accessURL)
	if err != nil {
		return err
	}
	if c.Status == "" {
		c.Status = ConnectionActive
	}
	c.SpaceID = spaceID

	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO connections (id, name, access_url_encrypted, status, status_detail,
			sync_errors, last_sync_at, last_successful_sync_at, retry_not_before, is_deleted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, pgconv.NullText(c.Name), sealed, string(c.Status),
		pgconv.NullText(c.StatusDetail), nullableJSON(errorsJSON), nullableMillis(c.LastSyncAt),
		nullableMillis(c.LastSuccessfulSyncAt), nullableMillis(c.RetryNotBefore), c.IsDeleted,
		now, now)
	if err == nil {
		c.CreatedAt, c.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create connection", err)
}

func (s *Store) GetConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Connection, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return Connection{}, err
	}
	row := db.Read().QueryRowContext(ctx,
		`SELECT `+connectionColumns+` FROM connections WHERE id = ?`, id)
	c, err := scanConnection(row, spaceID)
	return c, wrap("store: get connection", err)
}

func (s *Store) ListConnections(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Connection, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + connectionColumns + ` FROM connections`
	if !includeDeleted {
		query += ` WHERE NOT is_deleted`
	}
	query += ` ORDER BY created_at, id`
	return sqliteQueryAll(ctx, db.Read(), "store: list connections",
		func(row scanner) (Connection, error) { return scanConnection(row, spaceID) }, query)
}

// UpdateConnection rewrites everything except the credential.
func (s *Store) UpdateConnection(ctx context.Context, spaceID SpaceID, c *Connection) error {
	errorsJSON, err := syncErrorsArg(c.SyncErrors)
	if err != nil {
		return err
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update connection", `
		UPDATE connections SET name = ?, status = ?, status_detail = ?, sync_errors = ?, last_sync_at = ?,
			last_successful_sync_at = ?, retry_not_before = ?, is_deleted = ?, updated_at = ?
		WHERE id = ?`,
		pgconv.NullText(c.Name), string(c.Status), pgconv.NullText(c.StatusDetail),
		nullableJSON(errorsJSON), nullableMillis(c.LastSyncAt), nullableMillis(c.LastSuccessfulSyncAt),
		nullableMillis(c.RetryNotBefore), c.IsDeleted, now, c.ID)
	if err == nil {
		c.UpdatedAt = fromUnixMillis(now)
	}
	return err
}

// ConnectionAccessURL opens the stored credential — the only read path for it.
// A key that cannot open it returns ErrCredentialUnreadable, not "".
func (s *Store) ConnectionAccessURL(ctx context.Context, spaceID SpaceID, id uuid.UUID) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return "", err
	}
	var sealed string
	err = db.Read().QueryRowContext(ctx,
		`SELECT access_url_encrypted FROM connections WHERE id = ?`, id).Scan(&sealed)
	if err != nil {
		return "", wrap("store: read connection credential", err)
	}
	return cipher.Open(connectionCredentialContext(spaceID, id), sealed)
}

// SetConnectionAccessURL replaces the credential, for a re-claim.
func (s *Store) SetConnectionAccessURL(ctx context.Context, spaceID SpaceID, id uuid.UUID, accessURL string) error {
	sealed, err := s.sealString(connectionCredentialContext(spaceID, id), accessURL)
	if err != nil {
		return err
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set connection credential",
		`UPDATE connections SET access_url_encrypted = ?, updated_at = ? WHERE id = ?`,
		sealed, db.Now(), id)
}

// DeleteConnection hard-deletes the connection; accounts.connection_id is ON
// DELETE SET NULL, so its accounts and transactions remain as manual ones
// rather than connected-looking accounts that never update.
func (s *Store) DeleteConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete connection", `DELETE FROM connections WHERE id = ?`, id)
}

// --- The account side --------------------------------------------------------

// GetAccountByExternalID finds the local account one provider account maps to,
// on (connection_id, external_id).
func (s *Store) GetAccountByExternalID(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID, externalID string,
) (Account, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Account{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Account{}, err
	}
	a, err := scanAccount(db.Read().QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts
		 WHERE connection_id = ? AND external_id = ?`, connectionID, externalID), spaceID)
	return a, wrap("store: get account by external id", err)
}

// GetAccountAwaitingLink finds an unlinked account the relink step already
// matched to this provider account, so the next sync adopts it instead of
// creating a second one — see docs/importing.md.
func (s *Store) GetAccountAwaitingLink(
	ctx context.Context, spaceID SpaceID, simplefinAccountID string,
) (Account, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Account{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Account{}, err
	}
	a, err := scanAccount(db.Read().QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts
		 WHERE connection_id IS NULL AND simplefin_account_id = ? AND NOT is_deleted
		 ORDER BY created_at, id LIMIT 1`, simplefinAccountID), spaceID)
	return a, wrap("store: get account awaiting link", err)
}

// EnsureInstitution returns the space's row for one bank, creating it on first
// sight. SimpleFIN has no stable institution id, so the key is the lower-cased
// name: a renamed bank yields a second row.
func (s *Store) EnsureInstitution(
	ctx context.Context, spaceID SpaceID, name, logoURL string,
) (uuid.UUID, error) {
	if name == "" {
		return uuid.Nil, nil
	}
	key := strings.ToLower(strings.TrimSpace(name))
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return uuid.Nil, err
	}
	now := db.Now()
	var id uuid.UUID
	err = db.Write().QueryRowContext(ctx, `
		INSERT INTO institutions (id, name, external_id, logo_url, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (external_id)
		DO UPDATE SET logo_url = COALESCE(EXCLUDED.logo_url, institutions.logo_url),
			updated_at = excluded.updated_at
		RETURNING id`, uuid.New(), name, key, pgconv.NullText(logoURL), now, now).Scan(&id)
	return id, wrap("store: ensure institution", err)
}

// UnlinkAccount makes one account manual, keeping its transactions. The
// provider ids go too, so a re-added connection cannot silently re-adopt it.
func (s *Store) UnlinkAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: unlink account", `
		UPDATE accounts
		SET connection_id = NULL, external_id = NULL, simplefin_account_id = NULL,
			provider_balance = NULL, provider_balance_at = NULL, synced_through_on = NULL,
			updated_at = ? WHERE id = ?`, db.Now(), id)
}

func scanConnection(row scanner, spaceID SpaceID) (Connection, error) {
	var c Connection
	var name, detail sql.Null[string]
	var syncErrors sql.Null[string]
	var last, successful, retry sql.Null[int64]
	var created, updated int64
	err := row.Scan(&c.ID, &name, &c.Status, &detail, &syncErrors, &last,
		&successful, &retry, &c.IsDeleted, &created, &updated)
	if err != nil {
		return Connection{}, err
	}
	c.SpaceID, c.Name, c.StatusDetail = spaceID, name.V, detail.V
	c.LastSyncAt, c.LastSuccessfulSyncAt, c.RetryNotBefore = nullableTime(last), nullableTime(successful), nullableTime(retry)
	c.CreatedAt, c.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	if syncErrors.Valid {
		if err := json.Unmarshal([]byte(syncErrors.V), &c.SyncErrors); err != nil {
			return Connection{}, err
		}
	}
	return c, nil
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// syncErrorsArg writes NULL for no warnings, keeping "never reported" apart
// from "reported an empty list".
func syncErrorsArg(errs []BankSyncError) ([]byte, error) {
	if len(errs) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(errs)
	if err != nil {
		return nil, wrap("store: encode sync errors", err)
	}
	return encoded, nil
}

// DueConnection is one connection the scheduler has claimed, with its space:
// the scheduler has no request to resolve a tenant from.
type DueConnection struct {
	SpaceID SpaceID
	ID      uuid.UUID
	Name    string
}

// ClaimConnectionsDueForSync claims due connections atomically within each
// household file. The sweep is not atomic across households; a partial failure
// returns the claims already committed. There is no global queue ordering.
func (s *Store) ClaimConnectionsDueForSync(
	ctx context.Context, windowStart time.Time, now time.Time,
) ([]DueConnection, error) {
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := []DueConnection{}
	for _, spaceID := range ids {
		db, err := s.household(ctx, spaceID.UUID())
		if err != nil {
			return out, err
		}
		claimed, err := sqliteQueryAll(ctx, db.Write(), "store: claim connections due for sync",
			func(row scanner) (DueConnection, error) {
				one := DueConnection{SpaceID: spaceID}
				err := row.Scan(&one.ID, &one.Name)
				return one, err
			}, `UPDATE connections SET last_sync_at = ?, updated_at = ?
				WHERE NOT is_deleted AND status <> ? AND status <> ?
				AND (retry_not_before IS NULL OR retry_not_before <= ?)
				AND (last_sync_at IS NULL OR last_sync_at < ?)
				RETURNING id, coalesce(name, '')`,
			unixMillis(now), db.Now(), ConnectionCredentialsExpired,
			ConnectionPendingLink, unixMillis(now), unixMillis(windowStart))
		if err != nil {
			return out, err
		}
		out = append(out, claimed...)
	}
	return out, nil
}
