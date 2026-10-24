package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The bills bridge's tables: provider logins, what each bills, statements, and
// the reminder each is attached to. Provider facts live in domain.Billers.
// Nothing here writes a `series` column: what the household keeps stays theirs.

// BillConnection is one signed-in account at one provider.
type BillConnection struct {
	ID      uuid.UUID
	SpaceID SpaceID
	Biller  domain.BillerID
	// Label is which login this is, in the household's words; unique per provider.
	Label string
	// Username is the login; the password is only ever sealed.
	Username string
	// Site is the deployment of a per-customer product (catalogue NeedsSite):
	// one hostname label, also a path segment in the provider's API, or at a
	// SiteAddress provider the portal's address (domain.SiteAddressOf).
	Site string
	// CredentialSource is session, stored or typed.
	CredentialSource string
	// HasSession, HasCredential and HasTOTP say what is sealed on the row; the
	// ciphertext is deliberately not a field, so no listing can carry it.
	HasSession    bool
	HasCredential bool
	HasTOTP       bool
	// SecondFactor is a preference, not a secret: any key it names is in the
	// sealed credential.
	SecondFactor domain.SecondFactor
	ProfileID    string
	SignedInAt   *time.Time
	NeedsSignIn  bool
	// SignInPausedAt/SignInPausedFor: while set, the scheduler leaves the
	// connection alone. A successful pull, a person's sign-in, and a new or
	// forgotten password each clear it.
	SignInPausedAt  *time.Time
	SignInPausedFor string
	// The autopay rule, for a provider that does not state its own payment
	// date; domain.AutopayOn is the rule itself.
	AutopayRule       domain.AutopayRuleKind
	AutopayDays       int
	AutopayDayOfMonth int
	AutopayAccountID  uuid.UUID
	PullEnabled       bool
	// PullAt is the hour a pull may start, HH:MM in the space's zone, or empty
	// for the sync window.
	PullAt         string
	LastPulledAt   *time.Time
	LastPullStatus string
	LastPullError  string
	// HasFailureScreenshot says the last pull's failure kept the page it
	// failed on; BillPullScreenshot reads it. HasTrail says the last pull or
	// unfinished sign-in kept its trail; BillPullTrail reads it.
	HasFailureScreenshot bool
	HasTrail             bool
	// LastKeepaliveAt is when the engine last touched the session without pulling.
	LastKeepaliveAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const (
	BillPullOK          = runOK
	BillPullNeedsSignIn = runNeedsSignIn
	BillPullChallenge   = "challenge"
	BillPullFailed      = "failed"
	// BillPullSignInFailed is a sign-in somebody started that never landed,
	// kept in the last pull's place; MarkBillSignInEnded writes it.
	BillPullSignInFailed = runSignInFailed
)

const (
	BillCredentialSession = "session"
	BillCredentialStored  = "stored"
	BillCredentialTyped   = "typed"
)

// DisplayName is the connection's label, else the provider's name. Nothing
// that names a connection should read Label directly.
func (c BillConnection) DisplayName() string {
	if named := strings.TrimSpace(c.Label); named != "" {
		return named
	}
	return c.ProviderName()
}

// ProviderName is the provider's own name, whatever the connection is called.
func (c BillConnection) ProviderName() string {
	if biller, known := domain.BillerByID(c.Biller); known {
		return biller.Name
	}
	return string(c.Biller)
}

// Title is the provider with the connection's label beside it, or the label
// alone at a generic provider, whose name tells two connections nothing.
func (c BillConnection) Title() string {
	label := strings.TrimSpace(c.Label)
	if label == "" {
		return c.ProviderName()
	}
	if biller, known := domain.BillerByID(c.Biller); !known || biller.Generic {
		return label
	}
	return c.ProviderName() + " (" + label + ")"
}

func (c BillConnection) AutopayRuleValue() domain.AutopayRule {
	return domain.AutopayRule{
		Kind:       c.AutopayRule,
		Days:       c.AutopayDays,
		DayOfMonth: c.AutopayDayOfMonth,
	}
}

// BillSubaccount is one thing a login bills: a premise, a card, a line.
type BillSubaccount struct {
	ID           uuid.UUID
	SpaceID      SpaceID
	ConnectionID uuid.UUID
	// ExternalID is the provider's own key, which a pull matches on.
	ExternalID   string
	Label        string
	MaskedNumber string
	IsSelected   bool
	// AccountID is the household account this billed account is (the card or
	// loan), not the one that pays it (AutopayAccountID). Bills.Ingest writes
	// its statement figures.
	AccountID uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Bill is one statement. AmountDue and MinimumDue are magnitudes, not signed
// posting amounts.
type Bill struct {
	ID           uuid.UUID
	SpaceID      SpaceID
	SubaccountID uuid.UUID
	DueOn        domain.Date
	// Invoice tells apart bills of one billed account due the same day; with
	// SubaccountID and DueOn it is the bill's identity. Empty for a provider
	// whose cycle is its due date.
	Invoice       string
	AmountDue     domain.Money
	MinimumDue    domain.Money
	HasMinimumDue bool
	Currency      string
	IssuedOn      domain.Date
	PeriodStart   domain.Date
	PeriodEnd     domain.Date
	// AutopayOn is the provider's stated payment date; zero means the
	// connection's rule answers instead.
	AutopayOn domain.Date
	Status    domain.BillStatus
	// Source is provider, email, manual or assistant.
	Source       string
	ExternalID   string
	StatementURL string
	// DocumentID is the bill's statement, read from its document link of role
	// statement; UpsertBill does not write it. Nil when it has none.
	DocumentID uuid.UUID
	// MarkedPaidAt is when a person said it was paid where the ledger cannot
	// see; a pull never writes it. It ends the pay-manually reminder.
	MarkedPaidAt *time.Time
	FetchedAt    time.Time
	AmendedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const (
	BillSourceProvider  = "provider"
	BillSourceEmail     = "email"
	BillSourceManual    = "manual"
	BillSourceAssistant = "assistant"
)

// DomainBill is the bill as the supersede rule reads it.
func (b Bill) DomainBill() domain.Bill {
	return domain.Bill{
		ID:           domain.ID(b.ID.String()),
		SubaccountID: domain.ID(b.SubaccountID.String()),
		DueOn:        b.DueOn,
		AmountDue:    b.AmountDue,
		Status:       b.Status,
	}
}

// SeriesBillLink is the reminder attached to a subaccount. The adjust switch
// lives on the series (auto_adjust_due_on), not here.
type SeriesBillLink struct {
	SeriesID     uuid.UUID
	SpaceID      SpaceID
	SubaccountID uuid.UUID
	CreatedAt    time.Time
}

// --- Connections -------------------------------------------------------------

const billConnectionColumns = `id, biller, label, username, site, credential_source,
	(session_state IS NOT NULL), (credential_sealed IS NOT NULL),
	(credential_sealed IS NOT NULL AND credential_has_totp), second_factor,
	profile_id, signed_in_at, needs_sign_in, sign_in_paused_at, sign_in_paused_for,
	autopay_rule, autopay_days, autopay_day, autopay_account_id, pull_enabled, pull_at,
	last_pulled_at, last_pull_status, last_pull_error, (last_pull_screenshot_document_id IS NOT NULL),
	(last_pull_trail_document_id IS NOT NULL), last_keepalive_at, created_at, updated_at`

func scanBillConnection(row scanner) (BillConnection, error) {
	var one BillConnection
	var site, profile, account, pullAt sql.Null[string]
	var days, day sql.Null[int]
	var signed, paused, pulled, keepalive sql.Null[int64]
	var created, updated int64
	err := row.Scan(&one.ID, &one.Biller, &one.Label, &one.Username, &site,
		&one.CredentialSource, &one.HasSession, &one.HasCredential, &one.HasTOTP, &one.SecondFactor,
		&profile, &signed, &one.NeedsSignIn, &paused, &one.SignInPausedFor,
		&one.AutopayRule, &days, &day, &account, &one.PullEnabled, &pullAt,
		&pulled, &one.LastPullStatus, &one.LastPullError, &one.HasFailureScreenshot,
		&one.HasTrail, &keepalive, &created, &updated)
	if err != nil {
		return one, err
	}
	one.Site, one.ProfileID, one.PullAt = site.V, profile.V, pullAt.V
	one.AutopayDays, one.AutopayDayOfMonth = days.V, day.V
	if account.Valid {
		one.AutopayAccountID, err = uuid.Parse(account.V)
	}
	one.SignedInAt, one.SignInPausedAt = nullableTime(signed), nullableTime(paused)
	one.LastPulledAt, one.LastKeepaliveAt = nullableTime(pulled), nullableTime(keepalive)
	one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	return one, err
}

func (s *Store) CreateBillConnection(ctx context.Context, spaceID SpaceID, one *BillConnection) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `INSERT INTO bill_connections
		(id, biller, label, username, site, credential_source, autopay_rule,
		autopay_days, autopay_day, autopay_account_id, pull_enabled, pull_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		one.ID, string(one.Biller), one.Label, one.Username, pgconv.NullText(one.Site),
		one.CredentialSource, string(one.AutopayRule), nullSmallIntArg(one.AutopayDays),
		nullSmallIntArg(one.AutopayDayOfMonth), pgconv.NullUUID(one.AutopayAccountID),
		one.PullEnabled, pgconv.NullText(one.PullAt), now, now)
	if err == nil {
		one.SpaceID = spaceID
		one.CreatedAt, one.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create bill connection", err)
}

// UpdateBillConnection writes the fields a person owns. Session, profile and
// pull stamps have their own queries, so a settings save round-tripping a stale
// row cannot clear a session.
func (s *Store) UpdateBillConnection(ctx context.Context, spaceID SpaceID, one *BillConnection) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: update bill connection", `UPDATE bill_connections
		SET label = ?, username = ?, site = ?, credential_source = ?, autopay_rule = ?,
		autopay_days = ?, autopay_day = ?, autopay_account_id = ?, pull_enabled = ?, pull_at = ?, updated_at = ?
		WHERE id = ?`, one.Label, one.Username, pgconv.NullText(one.Site), one.CredentialSource,
		string(one.AutopayRule), nullSmallIntArg(one.AutopayDays), nullSmallIntArg(one.AutopayDayOfMonth),
		pgconv.NullUUID(one.AutopayAccountID), one.PullEnabled, pgconv.NullText(one.PullAt), db.Now(), one.ID)
}

func (s *Store) GetBillConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillConnection, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return BillConnection{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return BillConnection{}, err
	}
	one, err := scanBillConnection(db.Read().QueryRowContext(ctx, `SELECT `+billConnectionColumns+` FROM bill_connections WHERE id = ?`, id))
	one.SpaceID = spaceID
	return one, wrap("store: get bill connection", err)
}

func (s *Store) ListBillConnections(ctx context.Context, spaceID SpaceID) ([]BillConnection, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list bill connections", func(row scanner) (BillConnection, error) {
		one, err := scanBillConnection(row)
		one.SpaceID = spaceID
		return one, err
	}, `SELECT `+billConnectionColumns+` FROM bill_connections ORDER BY biller, label`)
}

// DeleteBillConnection cascades to subaccounts and bills, and releases the
// bills' statements first: document links have no foreign key, so a stale
// link would keep a statement out of the purge.
func (s *Store) DeleteBillConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		receipted, err := sqliteQueryAll(ctx, db.Read(), "store: list receipts of bill statements", scanValue[uuid.UUID], `
			SELECT DISTINCT r.target_id FROM document_links r
			JOIN document_links l ON l.document_id = r.document_id
			JOIN bills b ON b.id = l.target_id
			JOIN bill_subaccounts sa ON sa.id = b.subaccount_id
			WHERE r.kind = ? AND l.kind = ? AND sa.connection_id = ?`,
			string(DocumentLinkReceipt), string(DocumentLinkBill), id)
		if err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx, `DELETE FROM document_links WHERE kind = ? AND target_id IN (
			SELECT b.id FROM bills b JOIN bill_subaccounts sa ON sa.id = b.subaccount_id WHERE sa.connection_id = ?)`,
			string(DocumentLinkBill), id); err != nil {
			return wrap("store: release bill statements", err)
		}
		if err := sqliteExecOne(ctx, db.Write(), "store: delete bill connection", `DELETE FROM bill_connections WHERE id = ?`, id); err != nil {
			return err
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, receipted)
		return err
	})
}

// ListBillConnectionsDue merges every live household's due rows by creation time.
// Like the previous query, this is a listing, not an atomic scheduler claim.
func (s *Store) ListBillConnectionsDue(ctx context.Context, since time.Time) ([]BillConnection, error) {
	return s.listBillConnectionsAcrossHouseholds(ctx, `pull_enabled AND sign_in_paused_at IS NULL
		AND (NOT needs_sign_in OR credential_sealed IS NOT NULL)
		AND `+billConnectorState.wayIn+` AND (last_pulled_at IS NULL OR last_pulled_at < ?)`, unixMillis(since))
}

// ListBillConnectionsWithSession is every connection with a way in, for the
// keepalive pass. Not filtered on the pull switch: hand-pulled connections are
// the ones whose sessions go stale unnoticed.
func (s *Store) ListBillConnectionsWithSession(ctx context.Context) ([]BillConnection, error) {
	return s.listBillConnectionsAcrossHouseholds(ctx, `session_state IS NOT NULL AND NOT needs_sign_in`)
}

func (s *Store) listBillConnectionsAcrossHouseholds(ctx context.Context, predicate string, args ...any) ([]BillConnection, error) {
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return nil, err
	}
	var out []BillConnection
	for _, id := range ids {
		h, err := s.scoped(ctx, id)
		if err != nil {
			return nil, err
		}
		db, err := h.household(ctx, id.UUID())
		if err != nil {
			return nil, err
		}
		found, err := sqliteQueryAll(ctx, db.Read(), "store: list bill connections across households",
			func(row scanner) (BillConnection, error) {
				one, err := scanBillConnection(row)
				one.SpaceID = id
				return one, err
			}, `SELECT `+billConnectionColumns+` FROM bill_connections WHERE `+predicate+` ORDER BY created_at, id`, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// SaveBillConnectionSession seals a session onto the connection, and the
// browser profile it lives in when one is named; see saveSession.
func (s *Store) SaveBillConnectionSession(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, state, profileID string, fresh bool,
) error {
	return s.saveSession(ctx, billConnectorState, spaceID, id, state, fresh,
		`profile_id = CASE WHEN ? <> '' THEN ? ELSE profile_id END,`,
		[]any{profileID, profileID})
}

// BillConnectionSession opens the sealed session; ErrNotFound when there is none.
func (s *Store) BillConnectionSession(ctx context.Context, spaceID SpaceID, id uuid.UUID) (string, error) {
	return s.readSealed(ctx, "store: bill connection session", "bill_connections", "session_state",
		billConnectionSessionContext(spaceID, id), spaceID, id)
}

// BillCredential is a provider login, held only long enough to seal it or hand
// it to the engine. TOTPSecret, when given, is sealed in the same blob so one
// seal and one forget cover both.
type BillCredential struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret,omitempty"`
}

// SaveBillConnectionCredential seals a password and makes it the way in,
// writing the username beside it so the two cannot drift apart.
func (s *Store) SaveBillConnectionCredential(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, credential BillCredential,
) error {
	return s.saveCredential(ctx, billConnectorState, spaceID, id, credential,
		`credential_source = ?, username = ?,`,
		[]any{BillCredentialStored, credential.Username})
}

// SetBillConnectionSecondFactor has its own query so a stale settings save
// cannot undo it.
func (s *Store) SetBillConnectionSecondFactor(ctx context.Context, spaceID SpaceID, id uuid.UUID, factor domain.SecondFactor) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set bill connection second factor",
		`UPDATE bill_connections SET second_factor = ?, updated_at = ? WHERE id = ?`, string(factor), db.Now(), id)
}

// BillConnectionCredential opens the sealed password; ErrNotFound when none is kept.
func (s *Store) BillConnectionCredential(
	ctx context.Context, spaceID SpaceID, id uuid.UUID,
) (BillCredential, error) {
	return s.credentialOf(ctx, billConnectorState, spaceID, id)
}

// ClearBillConnectionCredential forgets the password; the kept session, if
// any, is the way in again.
func (s *Store) ClearBillConnectionCredential(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.clearCredential(ctx, billConnectorState, spaceID, id,
		`credential_source = CASE WHEN credential_source = ? THEN ? ELSE credential_source END,`,
		[]any{BillCredentialStored, BillCredentialSession})
}

// ClearBillConnectionSession forgets the session; see clearSession.
func (s *Store) ClearBillConnectionSession(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.clearSession(ctx, billConnectorState, spaceID, id)
}

// MarkBillPull records how a pull ended, with the page it failed on or nil and
// its trail (JSON) or nil; a pull that got in lifts any pause.
func (s *Store) MarkBillPull(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, status, detail string, shot, trail []byte,
) error {
	return s.markRun(ctx, billConnectorState, spaceID, id, status, detail, shot, trail)
}

// BillPullScreenshot is the page the connection's last pull failed on, as a
// JPEG; ErrNotFound when it kept none.
func (s *Store) BillPullScreenshot(ctx context.Context, spaceID SpaceID, id uuid.UUID) ([]byte, error) {
	return s.runScreenshot(ctx, billConnectorState, spaceID, id)
}

// MarkBillSignInEnded keeps a sign-in that never landed on the connection; see
// markSignInEnded. trail is JSON.
func (s *Store) MarkBillSignInEnded(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, detail string, shot, trail []byte,
) error {
	return s.markSignInEnded(ctx, billConnectorState, spaceID, id, detail, shot, trail)
}

// BillPullTrail is the trail the connection's last pull or unfinished sign-in
// kept, as JSON; ErrNotFound when it kept none.
func (s *Store) BillPullTrail(ctx context.Context, spaceID SpaceID, id uuid.UUID) ([]byte, error) {
	return s.runTrailOf(ctx, billConnectorState, spaceID, id)
}

// PauseBillSignIn keeps the scheduler off a connection until a person acts.
func (s *Store) PauseBillSignIn(ctx context.Context, spaceID SpaceID, id uuid.UUID, why string) error {
	return s.pauseSignIn(ctx, billConnectorState, spaceID, id, why)
}

// NoteBillPullSkipped records why a scheduled pass skipped this connection;
// see noteRunSkipped.
func (s *Store) NoteBillPullSkipped(ctx context.Context, spaceID SpaceID, id uuid.UUID, detail string) error {
	return s.noteRunSkipped(ctx, billConnectorState, spaceID, id, detail)
}

func (s *Store) MarkBillKeepalive(ctx context.Context, spaceID SpaceID, id uuid.UUID, at time.Time) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `UPDATE bill_connections SET last_keepalive_at = ?, updated_at = ? WHERE id = ?`, unixMillis(at), db.Now(), id)
	return wrap("store: mark bill keepalive", err)
}

// --- Subaccounts -------------------------------------------------------------

const billSubaccountColumns = `id, connection_id, external_id, label, masked_number,
	is_selected, account_id, created_at, updated_at`

func scanBillSubaccount(row scanner) (BillSubaccount, error) {
	var one BillSubaccount
	var masked, account sql.Null[string]
	var created, updated int64
	err := row.Scan(&one.ID, &one.ConnectionID, &one.ExternalID, &one.Label,
		&masked, &one.IsSelected, &account, &created, &updated)
	if err != nil {
		return one, err
	}
	if account.Valid {
		one.AccountID, err = uuid.Parse(account.V)
	}
	one.MaskedNumber = masked.V
	one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	return one, err
}

// UpsertBillSubaccount writes what a pull found, keyed on the provider's id,
// so a relabelled premise keeps its series link.
func (s *Store) UpsertBillSubaccount(ctx context.Context, spaceID SpaceID, one *BillSubaccount) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	now := db.Now()
	var created, updated int64
	err = db.Write().QueryRowContext(ctx, `INSERT INTO bill_subaccounts
		(id, connection_id, external_id, label, masked_number, is_selected, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (connection_id, external_id) DO UPDATE
		SET label = excluded.label, masked_number = excluded.masked_number, updated_at = excluded.updated_at
		RETURNING id, created_at, updated_at`,
		one.ID, one.ConnectionID, one.ExternalID, one.Label, pgconv.NullText(one.MaskedNumber),
		one.IsSelected, now, now).Scan(&one.ID, &created, &updated)
	if err == nil {
		one.SpaceID = spaceID
		one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	}
	return wrap("store: upsert bill subaccount", err)
}

func (s *Store) GetBillSubaccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillSubaccount, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return BillSubaccount{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return BillSubaccount{}, err
	}
	one, err := scanBillSubaccount(db.Read().QueryRowContext(ctx,
		`SELECT `+billSubaccountColumns+` FROM bill_subaccounts WHERE id = ?`, id))
	one.SpaceID = spaceID
	return one, wrap("store: get bill subaccount", err)
}

func (s *Store) ListBillSubaccounts(ctx context.Context, spaceID SpaceID, connectionID uuid.UUID) ([]BillSubaccount, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list bill subaccounts", func(row scanner) (BillSubaccount, error) {
		one, err := scanBillSubaccount(row)
		one.SpaceID = spaceID
		return one, err
	}, `SELECT `+billSubaccountColumns+` FROM bill_subaccounts WHERE (? IS NULL OR connection_id = ?) ORDER BY label`,
		pgconv.NullUUID(connectionID), pgconv.NullUUID(connectionID))
}

func (s *Store) SetBillSubaccountSelected(ctx context.Context, spaceID SpaceID, id uuid.UUID, selected bool) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set bill subaccount selected",
		`UPDATE bill_subaccounts SET is_selected = ?, updated_at = ? WHERE id = ?`, selected, db.Now(), id)
}

// SetBillSubaccountAccount links a billed account to the household account it
// is, or unlinks it when accountID is nil. Both ends are unique: a card linked
// from another billed account is released from it first.
func (s *Store) SetBillSubaccountAccount(ctx context.Context, spaceID SpaceID, id, accountID uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if accountID != uuid.Nil {
			if _, err := db.Write().ExecContext(ctx, `UPDATE bill_subaccounts SET account_id = NULL, updated_at = ?
				WHERE account_id = ? AND id <> ?`, db.Now(), accountID, id); err != nil {
				return wrap("store: set bill subaccount account", err)
			}
		}
		return sqliteExecOne(ctx, db.Write(), "store: set bill subaccount account",
			`UPDATE bill_subaccounts SET account_id = ?, updated_at = ? WHERE id = ?
			 AND (? IS NULL OR EXISTS (SELECT 1 FROM accounts WHERE id = ? AND NOT is_deleted))`,
			pgconv.NullUUID(accountID), db.Now(), id, pgconv.NullUUID(accountID), pgconv.NullUUID(accountID))
	})
}

// --- Bills -------------------------------------------------------------------

// billColumns is read from bills aliased b; the statement is a correlated
// scalar subquery, not a lateral join.
const billColumns = `b.id, b.subaccount_id, b.due_on, b.invoice, b.amount_due, b.minimum_due,
	b.currency, b.issued_on, b.period_start, b.period_end, b.autopay_on, b.status, b.source,
	b.external_id, b.statement_url, (SELECT l.document_id FROM document_links l WHERE l.kind = '` + string(DocumentLinkBill) + `'
	AND l.target_id = b.id AND l.role = '` + DocumentRoleStatement + `' ORDER BY l.created_at DESC, l.document_id DESC LIMIT 1),
	b.marked_paid_at, b.fetched_at, b.amended_at, b.created_at, b.updated_at`

func scanBill(row scanner) (Bill, error) {
	var one Bill
	var amount, fetched, created, updated int64
	var minimum, marked, amended sql.Null[int64]
	var due string
	var issued, start, end, autopay, document sql.Null[string]
	err := row.Scan(&one.ID, &one.SubaccountID, &due, &one.Invoice, &amount, &minimum, &one.Currency,
		&issued, &start, &end, &autopay, &one.Status, &one.Source, &one.ExternalID,
		&one.StatementURL, &document, &marked, &fetched, &amended, &created, &updated)
	if err != nil {
		return one, err
	}
	one.DueOn, err = domain.ParseDate(due)
	if err != nil {
		return one, err
	}
	for _, field := range []struct {
		value sql.Null[string]
		dest  *domain.Date
	}{
		{issued, &one.IssuedOn}, {start, &one.PeriodStart}, {end, &one.PeriodEnd}, {autopay, &one.AutopayOn},
	} {
		if field.value.Valid {
			*field.dest, err = domain.ParseDate(field.value.V)
			if err != nil {
				return one, err
			}
		}
	}
	if document.Valid {
		one.DocumentID, err = uuid.Parse(document.V)
		if err != nil {
			return one, err
		}
	}
	value, err := fromMinor(amount, one.Currency)
	if err != nil {
		return one, err
	}
	one.AmountDue = domain.FromDecimal(value)
	if minimum.Valid {
		value, err = fromMinor(minimum.V, one.Currency)
		if err != nil {
			return one, err
		}
		one.MinimumDue, one.HasMinimumDue = domain.FromDecimal(value), true
	}
	one.MarkedPaidAt, one.AmendedAt = nullableTime(marked), nullableTime(amended)
	one.FetchedAt, one.CreatedAt, one.UpdatedAt = fromUnixMillis(fetched), fromUnixMillis(created), fromUnixMillis(updated)
	return one, nil
}

// UpsertBill writes one statement, keyed on subaccount, due date and invoice,
// so a re-pull of a cycle updates the row. amended (the amount changed) is the
// caller's call, since it compares against the existing row.
func (s *Store) UpsertBill(ctx context.Context, spaceID SpaceID, one *Bill, amended bool) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	amount, err := toMinor(one.AmountDue.Decimal(), one.Currency)
	if err != nil {
		return wrap("store: bill amount", err)
	}
	var minimum any
	if one.HasMinimumDue {
		minimum, err = toMinor(one.MinimumDue.Decimal(), one.Currency)
		if err != nil {
			return wrap("store: bill minimum", err)
		}
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		now := db.Now()
		var id uuid.UUID
		err = db.Write().QueryRowContext(ctx, `INSERT INTO bills
			(id, subaccount_id, due_on, amount_due, currency, issued_on, period_start, period_end,
			autopay_on, status, source, external_id, statement_url, fetched_at, minimum_due, invoice, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (subaccount_id, due_on, invoice) DO UPDATE SET
			amount_due = excluded.amount_due, minimum_due = excluded.minimum_due, currency = excluded.currency,
			issued_on = excluded.issued_on, period_start = excluded.period_start, period_end = excluded.period_end,
			autopay_on = excluded.autopay_on, status = excluded.status, source = excluded.source,
			external_id = excluded.external_id, statement_url = excluded.statement_url, fetched_at = excluded.fetched_at,
			amended_at = CASE WHEN ? THEN excluded.fetched_at ELSE bills.amended_at END, updated_at = excluded.updated_at
			RETURNING id`, one.ID, one.SubaccountID, one.DueOn.String(), amount, one.Currency,
			billDateArg(one.IssuedOn), billDateArg(one.PeriodStart), billDateArg(one.PeriodEnd), billDateArg(one.AutopayOn),
			string(one.Status), one.Source, one.ExternalID, one.StatementURL, unixMillis(one.FetchedAt),
			minimum, one.Invoice, now, now, amended).Scan(&id)
		if err != nil {
			return wrap("store: upsert bill", err)
		}
		stored, err := tx.GetBill(ctx, spaceID, id)
		if err != nil {
			return err
		}
		*one = stored
		return nil
	})
}

func (s *Store) GetBill(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Bill, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Bill{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Bill{}, err
	}
	one, err := scanBill(db.Read().QueryRowContext(ctx, `SELECT `+billColumns+` FROM bills b WHERE b.id = ?`, id))
	one.SpaceID = spaceID
	return one, wrap("store: get bill", err)
}

func (s *Store) ListBills(ctx context.Context, spaceID SpaceID, subaccountID uuid.UUID) ([]Bill, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list bills", billScanner(spaceID),
		`SELECT `+billColumns+` FROM bills b WHERE b.subaccount_id = ? ORDER BY b.due_on DESC, b.invoice`, subaccountID)
}

// ListOpenBills is every open statement on the subaccounts, oldest first.
func (s *Store) ListOpenBills(ctx context.Context, spaceID SpaceID, subaccountIDs []uuid.UUID) ([]Bill, error) {
	return s.listStandingBills(ctx, spaceID, subaccountIDs, false)
}

// ListStandingBills is every open or paid statement on the subaccounts,
// oldest first: each cycle's own figure, without the statements a later one
// superseded.
func (s *Store) ListStandingBills(ctx context.Context, spaceID SpaceID, subaccountIDs []uuid.UUID) ([]Bill, error) {
	return s.listStandingBills(ctx, spaceID, subaccountIDs, true)
}
func (s *Store) listStandingBills(ctx context.Context, spaceID SpaceID, ids []uuid.UUID, includePaid bool) ([]Bill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	in, args := inArgs(ids)
	status := `b.status = 'open'`
	if includePaid {
		status = `b.status IN ('open', 'paid')`
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list standing bills", billScanner(spaceID),
		`SELECT `+billColumns+` FROM bills b WHERE b.subaccount_id IN `+in+` AND `+status+` ORDER BY b.due_on, b.invoice`, args...)
}

func billScanner(spaceID SpaceID) func(scanner) (Bill, error) {
	return func(row scanner) (Bill, error) { one, err := scanBill(row); one.SpaceID = spaceID; return one, err }
}

func billDateArg(on domain.Date) any {
	if on.IsZero() {
		return nil
	}
	return on.String()
}

// BillConnectionsOf maps each bill to the connection it is filed under.
func (s *Store) BillConnectionsOf(ctx context.Context, spaceID SpaceID, billIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if len(billIDs) == 0 {
		return out, nil
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	in, args := inArgs(billIDs)
	pairs, err := sqliteQueryAll(ctx, db.Read(), "store: bill connections of bills", scanPair[uuid.UUID, uuid.UUID],
		`SELECT b.id, sa.connection_id FROM bills b JOIN bill_subaccounts sa ON sa.id = b.subaccount_id WHERE b.id IN `+in, args...)
	if err != nil {
		return nil, err
	}
	for _, one := range pairs {
		out[one.first] = one.second
	}
	return out, nil
}

// SetBillStatus is how a bill leaves `open`: superseded or paid.
func (s *Store) SetBillStatus(ctx context.Context, spaceID SpaceID, id uuid.UUID, status domain.BillStatus) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set bill status", `UPDATE bills SET status = ?, updated_at = ? WHERE id = ?`, string(status), db.Now(), id)
}

// MarkBillPaid records a person's word that the bill was paid where the
// ledger cannot see it. Marking it twice keeps the first time.
func (s *Store) MarkBillPaid(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	return sqliteExecOne(ctx, db.Write(), "store: mark bill paid", `UPDATE bills SET marked_paid_at = COALESCE(marked_paid_at, ?), updated_at = ? WHERE id = ?`, now, now, id)
}

// --- Links -------------------------------------------------------------------

const seriesBillLinkColumns = `series_id, subaccount_id, created_at`

func scanSeriesBillLink(row scanner) (SeriesBillLink, error) {
	var one SeriesBillLink
	var created int64
	err := row.Scan(&one.SeriesID, &one.SubaccountID, &created)
	one.CreatedAt = fromUnixMillis(created)
	return one, err
}

// LinkSeriesBill attaches a reminder to a subaccount. Both directions are
// unique, so an existing link on either side is released first rather than
// failing on the constraint.
func (s *Store) LinkSeriesBill(ctx context.Context, spaceID SpaceID, seriesID, subaccountID uuid.UUID) (SeriesBillLink, error) {
	var one SeriesBillLink
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return one, err
	}
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		released, err := sqliteQueryAll(ctx, db.Write(), "store: link series bill", scanValue[uuid.UUID],
			`DELETE FROM series_bill_links WHERE series_id = ? OR subaccount_id = ? RETURNING series_id`, seriesID, subaccountID)
		if err != nil {
			return err
		}
		one, err = scanSeriesBillLink(db.Write().QueryRowContext(ctx,
			`INSERT INTO series_bill_links (series_id, subaccount_id, created_at) VALUES (?, ?, ?) RETURNING `+seriesBillLinkColumns,
			seriesID, subaccountID, db.Now()))
		if err != nil {
			return wrap("store: link series bill", err)
		}
		one.SpaceID = spaceID
		return tx.ReconcileSeriesReceipts(ctx, spaceID, append(released, seriesID))
	})
	return one, err
}

func (s *Store) UnlinkSeriesBill(ctx context.Context, spaceID SpaceID, seriesID uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if err := sqliteExecOne(ctx, db.Write(), "store: unlink series bill", `DELETE FROM series_bill_links WHERE series_id = ?`, seriesID); err != nil {
			return err
		}
		return tx.ReconcileSeriesReceipts(ctx, spaceID, []uuid.UUID{seriesID})
	})
}

func (s *Store) ListSeriesBillLinks(ctx context.Context, spaceID SpaceID, seriesIDs []uuid.UUID) ([]SeriesBillLink, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + seriesBillLinkColumns + ` FROM series_bill_links`
	var args []any
	if len(seriesIDs) > 0 {
		var in string
		in, args = inArgs(seriesIDs)
		query += ` WHERE series_id IN ` + in
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list series bill links", func(row scanner) (SeriesBillLink, error) {
		one, err := scanSeriesBillLink(row)
		one.SpaceID = spaceID
		return one, err
	}, query+` ORDER BY created_at`, args...)
}

// nullSmallIntArg encodes an optional smallint; zero is absent for both
// autopay figures (no day zero, and 0 days before due is on_due_date).
func nullSmallIntArg(value int) *int16 {
	if value == 0 {
		return nil
	}
	small := int16(value)
	return &small
}
