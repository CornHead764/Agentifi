package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

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

const connectionColumns = `id, space_id, name, status, status_detail, sync_errors, last_sync_at,
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

	err = s.db.QueryRow(ctx, `
		INSERT INTO connections (id, space_id, name, access_url_encrypted, status, status_detail,
			sync_errors, last_sync_at, last_successful_sync_at, retry_not_before, is_deleted)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at, updated_at`,
		c.ID, spaceID.UUID(), dbconv.NullText(c.Name), sealed, string(c.Status),
		dbconv.NullText(c.StatusDetail), errorsJSON, c.LastSyncAt, c.LastSuccessfulSyncAt,
		c.RetryNotBefore, c.IsDeleted,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
	return wrap("store: create connection", err)
}

func (s *Store) GetConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Connection, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+connectionColumns+` FROM connections WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	c, err := scanConnection(row)
	return c, wrap("store: get connection", err)
}

func (s *Store) ListConnections(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Connection, error) {
	sql := `SELECT ` + connectionColumns + ` FROM connections WHERE space_id = $1`
	if !includeDeleted {
		sql += ` AND NOT is_deleted`
	}
	sql += ` ORDER BY created_at, id`

	return queryAll(ctx, s.db, "store: list connections", scanConnection, sql, spaceID.UUID())
}

// UpdateConnection rewrites everything except the credential.
func (s *Store) UpdateConnection(ctx context.Context, spaceID SpaceID, c *Connection) error {
	errorsJSON, err := syncErrorsArg(c.SyncErrors)
	if err != nil {
		return err
	}
	err = s.db.QueryRow(ctx, `
		UPDATE connections SET
			name = $3, status = $4, status_detail = $5, sync_errors = $6, last_sync_at = $7,
			last_successful_sync_at = $8, retry_not_before = $9, is_deleted = $10, updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), c.ID, dbconv.NullText(c.Name), string(c.Status), dbconv.NullText(c.StatusDetail),
		errorsJSON, c.LastSyncAt, c.LastSuccessfulSyncAt, c.RetryNotBefore, c.IsDeleted,
	).Scan(&c.UpdatedAt)
	return wrap("store: update connection", err)
}

// ConnectionAccessURL opens the stored credential — the only read path for it.
// A key that cannot open it returns ErrCredentialUnreadable, not "".
func (s *Store) ConnectionAccessURL(ctx context.Context, spaceID SpaceID, id uuid.UUID) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	var sealed string
	err = s.db.QueryRow(ctx,
		`SELECT access_url_encrypted FROM connections WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id).Scan(&sealed)
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
	return s.execOne(ctx, "store: set connection credential",
		`UPDATE connections SET access_url_encrypted = $3, updated_at = now()
		 WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, sealed)
}

// DeleteConnection hard-deletes the connection; accounts.connection_id is ON
// DELETE SET NULL, so its accounts and transactions remain as manual ones
// rather than connected-looking accounts that never update.
func (s *Store) DeleteConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete connection", `DELETE FROM connections WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
}

// --- The account side --------------------------------------------------------

// GetAccountByExternalID finds the local account one provider account maps to,
// on (connection_id, external_id).
func (s *Store) GetAccountByExternalID(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID, externalID string,
) (Account, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM accounts
		 WHERE space_id = $1 AND connection_id = $2 AND external_id = $3`,
		spaceID.UUID(), connectionID, externalID)
	a, err := scanAccount(row)
	return a, wrap("store: get account by external id", err)
}

// GetAccountAwaitingLink finds an unlinked account the relink step already
// matched to this provider account, so the next sync adopts it instead of
// creating a second one — see docs/importing.md.
func (s *Store) GetAccountAwaitingLink(
	ctx context.Context, spaceID SpaceID, simplefinAccountID string,
) (Account, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM accounts
		 WHERE space_id = $1 AND connection_id IS NULL AND simplefin_account_id = $2 AND NOT is_deleted
		 ORDER BY created_at, id
		 LIMIT 1`,
		spaceID.UUID(), simplefinAccountID)
	a, err := scanAccount(row)
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
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
		INSERT INTO institutions (id, space_id, name, external_id, logo_url)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (space_id, external_id)
		DO UPDATE SET logo_url = COALESCE(EXCLUDED.logo_url, institutions.logo_url),
			updated_at = now()
		RETURNING id`,
		uuid.New(), spaceID.UUID(), name, key, dbconv.NullText(logoURL)).Scan(&id)
	return id, wrap("store: ensure institution", err)
}

// UnlinkAccount makes one account manual, keeping its transactions. The
// provider ids go too, so a re-added connection cannot silently re-adopt it.
func (s *Store) UnlinkAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: unlink account", `
		UPDATE accounts
		SET connection_id = NULL, external_id = NULL, simplefin_account_id = NULL,
			provider_balance = NULL, provider_balance_at = NULL, synced_through_on = NULL,
			updated_at = now()
		WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}

func scanConnection(row scanner) (Connection, error) {
	var (
		c            Connection
		spaceID      uuid.UUID
		name, detail *string
		status       string
		syncErrors   []byte
	)
	err := row.Scan(&c.ID, &spaceID, &name, &status, &detail, &syncErrors, &c.LastSyncAt,
		&c.LastSuccessfulSyncAt, &c.RetryNotBefore, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Connection{}, err
	}
	c.SpaceID = SpaceID(spaceID)
	c.Name = Deref(name)
	c.Status = ConnectionStatus(status)
	c.StatusDetail = Deref(detail)
	if len(syncErrors) > 0 {
		if err := json.Unmarshal(syncErrors, &c.SyncErrors); err != nil {
			return Connection{}, err
		}
	}
	return c, nil
}

// syncErrorsArg writes NULL for no warnings, keeping "never reported" apart
// from "reported an empty list".
func syncErrorsArg(errs []BankSyncError) (json.RawMessage, error) {
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

// ClaimConnectionsDueForSync stamps every connection due for its daily run and
// returns them. The one query in the package that crosses spaces: it returns
// only ids and names, and the sync takes the space id back as an argument.
//
// Stamp and read are one statement, so two claims never both take a
// connection. It stamps last_sync_at, not
// last_successful_sync_at, so a failing bank waits for tomorrow's window too.
func (s *Store) ClaimConnectionsDueForSync(
	ctx context.Context, windowStart time.Time, now time.Time,
) ([]DueConnection, error) {
	return queryAll(ctx, s.db, "store: claim connections due for sync", func(row scanner) (DueConnection, error) {
		var one DueConnection
		var spaceID uuid.UUID
		err := row.Scan(&spaceID, &one.ID, &one.Name)
		one.SpaceID = SpaceID(spaceID)
		return one, err
	}, `
		UPDATE connections SET last_sync_at = $2, updated_at = now()
		WHERE id IN (
			SELECT id FROM connections
			WHERE NOT is_deleted
			  -- A refused Access URL is not fixed by asking again; it needs a
			  -- fresh setup token pasted in. Retrying it daily would spend the
			  -- rate limit to re-learn what the connection already says.
			  AND status <> $3
			  -- Accounts not yet matched: syncing would create a second copy
			  -- of every account the household already keeps by hand.
			  AND status <> $4
			  AND (retry_not_before IS NULL OR retry_not_before <= $2)
			  AND (last_sync_at IS NULL OR last_sync_at < $1)
		)
		RETURNING space_id, id, coalesce(name, '')`,
		windowStart, now, ConnectionCredentialsExpired, ConnectionPendingLink)
}
