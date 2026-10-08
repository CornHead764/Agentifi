package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
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

const billConnectionColumns = `id, space_id, biller, label, username, site, credential_source,
	(session_state IS NOT NULL), (credential_sealed IS NOT NULL),
	(credential_sealed IS NOT NULL AND credential_has_totp), second_factor,
	profile_id, signed_in_at, needs_sign_in, sign_in_paused_at, sign_in_paused_for,
	autopay_rule, autopay_days, autopay_day, autopay_account_id, pull_enabled, pull_at,
	last_pulled_at, last_pull_status, last_pull_error, (last_pull_screenshot IS NOT NULL),
	(last_pull_trail IS NOT NULL), last_keepalive_at,
	created_at, updated_at`

func scanBillConnection(row scanner) (BillConnection, error) {
	var (
		one     BillConnection
		spaceID uuid.UUID
		site    *string
		profile *string
		days    *int16
		day     *int16
		account *uuid.UUID
		pullAt  *string
	)
	err := row.Scan(&one.ID, &spaceID, &one.Biller, &one.Label, &one.Username, &site,
		&one.CredentialSource, &one.HasSession, &one.HasCredential, &one.HasTOTP, &one.SecondFactor,
		&profile, &one.SignedInAt,
		&one.NeedsSignIn, &one.SignInPausedAt, &one.SignInPausedFor, &one.AutopayRule, &days, &day, &account, &one.PullEnabled, &pullAt,
		&one.LastPulledAt, &one.LastPullStatus, &one.LastPullError, &one.HasFailureScreenshot, &one.HasTrail, &one.LastKeepaliveAt,
		&one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.Site = Deref(site)
	one.ProfileID = Deref(profile)
	if days != nil {
		one.AutopayDays = int(*days)
	}
	if day != nil {
		one.AutopayDayOfMonth = int(*day)
	}
	one.AutopayAccountID = Deref(account)
	one.PullAt = Deref(pullAt)
	return one, nil
}

func (s *Store) CreateBillConnection(ctx context.Context, spaceID SpaceID, one *BillConnection) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	err := s.db.QueryRow(ctx,
		`INSERT INTO bill_connections
		     (id, space_id, biller, label, username, site, credential_source, autopay_rule,
		      autopay_days, autopay_day, autopay_account_id, pull_enabled, pull_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING created_at, updated_at`,
		one.ID, spaceID.UUID(), string(one.Biller), one.Label, one.Username,
		dbconv.NullText(one.Site),
		one.CredentialSource, string(one.AutopayRule), nullSmallIntArg(one.AutopayDays),
		nullSmallIntArg(one.AutopayDayOfMonth), dbconv.NullUUID(one.AutopayAccountID),
		one.PullEnabled, dbconv.NullText(one.PullAt)).
		Scan(&one.CreatedAt, &one.UpdatedAt)
	return wrap("store: create bill connection", err)
}

// UpdateBillConnection writes the fields a person owns. Session, profile and
// pull stamps have their own queries, so a settings save round-tripping a stale
// row cannot clear a session.
func (s *Store) UpdateBillConnection(ctx context.Context, spaceID SpaceID, one *BillConnection) error {
	return s.execOne(ctx, "store: update bill connection",
		`UPDATE bill_connections
		    SET label = $3, username = $4, site = $5, credential_source = $6,
		        autopay_rule = $7, autopay_days = $8, autopay_day = $9, autopay_account_id = $10,
		        pull_enabled = $11, pull_at = $12, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), one.ID, one.Label, one.Username, dbconv.NullText(one.Site),
		one.CredentialSource, string(one.AutopayRule), nullSmallIntArg(one.AutopayDays),
		nullSmallIntArg(one.AutopayDayOfMonth), dbconv.NullUUID(one.AutopayAccountID),
		one.PullEnabled, dbconv.NullText(one.PullAt))
}

func (s *Store) GetBillConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillConnection, error) {
	one, err := scanBillConnection(s.db.QueryRow(ctx,
		`SELECT `+billConnectionColumns+` FROM bill_connections WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	return one, wrap("store: get bill connection", err)
}

func (s *Store) ListBillConnections(ctx context.Context, spaceID SpaceID) ([]BillConnection, error) {
	return queryAll(ctx, s.db, "store: list bill connections", scanBillConnection,
		`SELECT `+billConnectionColumns+` FROM bill_connections
		  WHERE space_id = $1 ORDER BY biller, label`, spaceID.UUID())
}

// DeleteBillConnection cascades to subaccounts and bills, and releases the
// bills' statements first: document links have no foreign key, so a stale
// link would keep a statement out of the purge.
func (s *Store) DeleteBillConnection(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		receipted, err := queryAll(ctx, tx.db, "store: list receipts of bill statements", scanValue[uuid.UUID], `
			SELECT DISTINCT r.target_id FROM document_links r
			JOIN document_links l ON l.document_id = r.document_id AND l.space_id = r.space_id
			JOIN bills b ON b.id = l.target_id AND b.space_id = l.space_id
			JOIN bill_subaccounts sa ON sa.id = b.subaccount_id AND sa.space_id = b.space_id
			WHERE r.space_id = $1 AND r.kind = $2 AND l.kind = $3 AND sa.connection_id = $4`,
			spaceID.UUID(), string(DocumentLinkReceipt), string(DocumentLinkBill), id)
		if err != nil {
			return err
		}
		if _, err := tx.db.Exec(ctx, `
			DELETE FROM document_links
			WHERE space_id = $1 AND kind = $2 AND target_id IN (
			    SELECT b.id FROM bills b
			    JOIN bill_subaccounts sa ON sa.id = b.subaccount_id
			    WHERE sa.connection_id = $3 AND sa.space_id = $1)`,
			spaceID.UUID(), string(DocumentLinkBill), id); err != nil {
			return wrap("store: release bill statements", err)
		}
		if err := tx.execOne(ctx, "store: delete bill connection",
			`DELETE FROM bill_connections WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id); err != nil {
			return err
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, receipted)
		return err
	})
}

// ListBillConnectionsDue is every connection due a scheduled pull; see listDue.
func (s *Store) ListBillConnectionsDue(ctx context.Context, since time.Time) ([]BillConnection, error) {
	return listDue(ctx, s, billConnectorState, billConnectionColumns, scanBillConnection, since)
}

// ListBillConnectionsWithSession is every connection with a way in, for the
// keepalive pass. Not filtered on the pull switch: hand-pulled connections are
// the ones whose sessions go stale unnoticed.
func (s *Store) ListBillConnectionsWithSession(ctx context.Context) ([]BillConnection, error) {
	return queryAll(ctx, s.db, "store: bill connections with a session", scanBillConnection,
		`SELECT `+billConnectionColumns+` FROM bill_connections
		  WHERE session_state IS NOT NULL AND NOT needs_sign_in
		  ORDER BY created_at`)
}

// SaveBillConnectionSession seals a session onto the connection, and the
// browser profile it lives in when one is named; see saveSession.
func (s *Store) SaveBillConnectionSession(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, state, profileID string, fresh bool,
) error {
	return s.saveSession(ctx, billConnectorState, spaceID, id, state, fresh,
		`profile_id = CASE WHEN @profile <> '' THEN @profile ELSE profile_id END,`,
		sqlitedb.NamedArgs{"profile": profileID})
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
		`credential_source = @source, username = @username,`,
		sqlitedb.NamedArgs{"source": BillCredentialStored, "username": credential.Username})
}

// SetBillConnectionSecondFactor has its own query so a stale settings save
// cannot undo it.
func (s *Store) SetBillConnectionSecondFactor(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, factor domain.SecondFactor,
) error {
	return s.execOne(ctx, "store: set bill connection second factor",
		`UPDATE bill_connections SET second_factor = $3, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, string(factor))
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
		`credential_source = CASE WHEN credential_source = @stored THEN @session ELSE credential_source END,`,
		sqlitedb.NamedArgs{"stored": BillCredentialStored, "session": BillCredentialSession})
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
	_, err := s.db.Exec(ctx,
		`UPDATE bill_connections SET last_keepalive_at = $3, updated_at = now()
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, at)
	return wrap("store: mark bill keepalive", err)
}

// --- Subaccounts -------------------------------------------------------------

const billSubaccountColumns = `id, space_id, connection_id, external_id, label, masked_number,
	is_selected, account_id, created_at, updated_at`

func scanBillSubaccount(row scanner) (BillSubaccount, error) {
	var (
		one     BillSubaccount
		spaceID uuid.UUID
		masked  *string
		account *uuid.UUID
	)
	err := row.Scan(&one.ID, &spaceID, &one.ConnectionID, &one.ExternalID, &one.Label,
		&masked, &one.IsSelected, &account, &one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.AccountID = Deref(account)
	one.MaskedNumber = Deref(masked)
	return one, nil
}

// UpsertBillSubaccount writes what a pull found, keyed on the provider's id,
// so a relabelled premise keeps its series link.
func (s *Store) UpsertBillSubaccount(ctx context.Context, spaceID SpaceID, one *BillSubaccount) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	err := s.db.QueryRow(ctx,
		`INSERT INTO bill_subaccounts
		     (id, space_id, connection_id, external_id, label, masked_number, is_selected)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (connection_id, external_id) DO UPDATE
		    SET label = EXCLUDED.label, masked_number = EXCLUDED.masked_number,
		        updated_at = now()
		 RETURNING id, created_at, updated_at`,
		one.ID, spaceID.UUID(), one.ConnectionID, one.ExternalID, one.Label,
		dbconv.NullText(one.MaskedNumber), one.IsSelected).
		Scan(&one.ID, &one.CreatedAt, &one.UpdatedAt)
	return wrap("store: upsert bill subaccount", err)
}

func (s *Store) GetBillSubaccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) (BillSubaccount, error) {
	one, err := scanBillSubaccount(s.db.QueryRow(ctx,
		`SELECT `+billSubaccountColumns+` FROM bill_subaccounts WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	return one, wrap("store: get bill subaccount", err)
}

func (s *Store) ListBillSubaccounts(
	ctx context.Context, spaceID SpaceID, connectionID uuid.UUID,
) ([]BillSubaccount, error) {
	return queryAll(ctx, s.db, "store: list bill subaccounts", scanBillSubaccount,
		`SELECT `+billSubaccountColumns+` FROM bill_subaccounts
		  WHERE space_id = $1 AND ($2::uuid IS NULL OR connection_id = $2)
		  ORDER BY label`, spaceID.UUID(), dbconv.NullUUID(connectionID))
}

func (s *Store) SetBillSubaccountSelected(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, selected bool,
) error {
	return s.execOne(ctx, "store: set bill subaccount selected",
		`UPDATE bill_subaccounts SET is_selected = $3, updated_at = now()
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, selected)
}

// SetBillSubaccountAccount links a billed account to the household account it
// is, or unlinks it when accountID is nil. Both ends are unique: a card linked
// from another billed account is released from it first.
func (s *Store) SetBillSubaccountAccount(
	ctx context.Context, spaceID SpaceID, id, accountID uuid.UUID,
) error {
	if accountID != uuid.Nil {
		if _, err := s.db.Exec(ctx,
			`UPDATE bill_subaccounts SET account_id = NULL, updated_at = now()
			  WHERE space_id = $1 AND account_id = $2 AND id <> $3`,
			spaceID.UUID(), accountID, id); err != nil {
			return wrap("store: set bill subaccount account", err)
		}
	}
	return s.execOne(ctx, "store: set bill subaccount account",
		`UPDATE bill_subaccounts SET account_id = $3, updated_at = now()
		  WHERE space_id = $1 AND id = $2
		    AND ($3::uuid IS NULL OR EXISTS (
		        SELECT 1 FROM accounts WHERE space_id = $1 AND id = $3 AND NOT is_deleted))`,
		spaceID.UUID(), id, dbconv.NullUUID(accountID))
}

// --- Bills -------------------------------------------------------------------

// billColumns is read from bills aliased b, followed by billStatement.
const billColumns = `b.id, b.space_id, b.subaccount_id, b.due_on, b.invoice, b.amount_due, b.minimum_due,
	b.currency, b.issued_on, b.period_start, b.period_end, b.autopay_on, b.status, b.source,
	b.external_id, b.statement_url, statement.document_id, b.marked_paid_at, b.fetched_at,
	b.amended_at, b.created_at, b.updated_at`

// billStatement joins a bill's statement: its newest document link of role
// statement, of which a replaced statement leaves only one.
const billStatement = ` LEFT JOIN LATERAL (
	SELECT l.document_id FROM document_links l
	 WHERE l.space_id = b.space_id AND l.kind = '` + string(DocumentLinkBill) + `'
	   AND l.target_id = b.id AND l.role = '` + DocumentRoleStatement + `'
	 ORDER BY l.created_at DESC LIMIT 1) statement ON true`

func scanBill(row scanner) (Bill, error) {
	var (
		one      Bill
		spaceID  uuid.UUID
		amount   dbconv.Number
		minimum  dbconv.Number
		dueOn    time.Time
		issued   *time.Time
		start    *time.Time
		end      *time.Time
		autopay  *time.Time
		document *uuid.UUID
	)
	err := row.Scan(&one.ID, &spaceID, &one.SubaccountID, &dueOn, &one.Invoice, &amount, &minimum, &one.Currency,
		&issued, &start, &end, &autopay, &one.Status, &one.Source, &one.ExternalID,
		&one.StatementURL, &document, &one.MarkedPaidAt, &one.FetchedAt, &one.AmendedAt,
		&one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.DueOn = dateOf(dueOn)
	one.IssuedOn = dbconv.ReadNullDate(issued)
	one.PeriodStart = dbconv.ReadNullDate(start)
	one.PeriodEnd = dbconv.ReadNullDate(end)
	one.AutopayOn = dbconv.ReadNullDate(autopay)
	one.DocumentID = Deref(document)
	if one.MinimumDue, one.HasMinimumDue, err = dbconv.ReadNullMoney(minimum, "bills.minimum_due"); err != nil {
		return one, err
	}
	one.AmountDue, err = dbconv.ReadMoney(amount, "bills.amount_due")
	return one, err
}

// UpsertBill writes one statement, keyed on subaccount, due date and invoice,
// so a re-pull of a cycle updates the row. amended (the amount changed) is the
// caller's call, since it compares against the existing row.
func (s *Store) UpsertBill(ctx context.Context, spaceID SpaceID, one *Bill, amended bool) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	row := s.db.QueryRow(ctx,
		`WITH written AS (
		 INSERT INTO bills
		     (id, space_id, subaccount_id, due_on, amount_due, currency, issued_on,
		      period_start, period_end, autopay_on, status, source, external_id,
		      statement_url, fetched_at, minimum_due, invoice)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $17, $18)
		 ON CONFLICT (subaccount_id, due_on, invoice) DO UPDATE
		    SET amount_due = EXCLUDED.amount_due, minimum_due = EXCLUDED.minimum_due,
		        currency = EXCLUDED.currency,
		        issued_on = EXCLUDED.issued_on, period_start = EXCLUDED.period_start,
		        period_end = EXCLUDED.period_end, autopay_on = EXCLUDED.autopay_on,
		        status = EXCLUDED.status, source = EXCLUDED.source,
		        external_id = EXCLUDED.external_id, statement_url = EXCLUDED.statement_url,
		        fetched_at = EXCLUDED.fetched_at,
		        amended_at = CASE WHEN $16 THEN EXCLUDED.fetched_at ELSE bills.amended_at END,
		        updated_at = now()
		 RETURNING *)
		 SELECT `+billColumns+` FROM written b`+billStatement,
		one.ID, spaceID.UUID(), one.SubaccountID, one.DueOn.Time(), dbconv.Money(one.AmountDue),
		one.Currency, dbconv.NullDate(one.IssuedOn), dbconv.NullDate(one.PeriodStart),
		dbconv.NullDate(one.PeriodEnd), dbconv.NullDate(one.AutopayOn), string(one.Status), one.Source,
		one.ExternalID, one.StatementURL, one.FetchedAt, amended,
		dbconv.NullMoney(one.MinimumDue, one.HasMinimumDue), one.Invoice)
	stored, err := scanBill(row)
	if err != nil {
		return wrap("store: upsert bill", err)
	}
	*one = stored
	return nil
}

func (s *Store) GetBill(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Bill, error) {
	one, err := scanBill(s.db.QueryRow(ctx,
		`SELECT `+billColumns+` FROM bills b`+billStatement+`
		  WHERE b.space_id = $1 AND b.id = $2`, spaceID.UUID(), id))
	return one, wrap("store: get bill", err)
}

func (s *Store) ListBills(ctx context.Context, spaceID SpaceID, subaccountID uuid.UUID) ([]Bill, error) {
	return queryAll(ctx, s.db, "store: list bills", scanBill,
		`SELECT `+billColumns+` FROM bills b`+billStatement+`
		  WHERE b.space_id = $1 AND b.subaccount_id = $2 ORDER BY b.due_on DESC, b.invoice`,
		spaceID.UUID(), subaccountID)
}

// ListOpenBills is every open statement on the subaccounts, oldest first.
func (s *Store) ListOpenBills(
	ctx context.Context, spaceID SpaceID, subaccountIDs []uuid.UUID,
) ([]Bill, error) {
	if len(subaccountIDs) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.db, "store: list open bills", scanBill,
		`SELECT `+billColumns+` FROM bills b`+billStatement+`
		  WHERE b.space_id = $1 AND b.subaccount_id = ANY($2) AND b.status = $3
		  ORDER BY b.due_on, b.invoice`,
		spaceID.UUID(), subaccountIDs, string(domain.BillOpen))
}

// ListStandingBills is every open or paid statement on the subaccounts,
// oldest first: each cycle's own figure, without the statements a later one
// superseded.
func (s *Store) ListStandingBills(
	ctx context.Context, spaceID SpaceID, subaccountIDs []uuid.UUID,
) ([]Bill, error) {
	if len(subaccountIDs) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.db, "store: list standing bills", scanBill,
		`SELECT `+billColumns+` FROM bills b`+billStatement+`
		  WHERE b.space_id = $1 AND b.subaccount_id = ANY($2) AND b.status = ANY($3)
		  ORDER BY b.due_on, b.invoice`,
		spaceID.UUID(), subaccountIDs, []string{string(domain.BillOpen), string(domain.BillPaid)})
}

// BillConnectionsOf maps each bill to the connection it is filed under.
func (s *Store) BillConnectionsOf(
	ctx context.Context, spaceID SpaceID, billIDs []uuid.UUID,
) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if len(billIDs) == 0 {
		return out, nil
	}
	pairs, err := queryAll(ctx, s.db, "store: bill connections of bills", scanPair[uuid.UUID, uuid.UUID],
		`SELECT b.id, sa.connection_id FROM bills b
		   JOIN bill_subaccounts sa ON sa.id = b.subaccount_id
		  WHERE b.space_id = $1 AND b.id = ANY($2)`, spaceID.UUID(), billIDs)
	if err != nil {
		return nil, err
	}
	for _, one := range pairs {
		out[one.first] = one.second
	}
	return out, nil
}

// SetBillStatus is how a bill leaves `open`: superseded or paid.
func (s *Store) SetBillStatus(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, status domain.BillStatus,
) error {
	return s.execOne(ctx, "store: set bill status",
		`UPDATE bills SET status = $3, updated_at = now() WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, string(status))
}

// MarkBillPaid records a person's word that the bill was paid where the
// ledger cannot see it. Marking it twice keeps the first time.
func (s *Store) MarkBillPaid(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: mark bill paid",
		`UPDATE bills SET marked_paid_at = COALESCE(marked_paid_at, now()), updated_at = now()
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}

// --- Links -------------------------------------------------------------------

const seriesBillLinkColumns = `series_id, space_id, subaccount_id, created_at`

func scanSeriesBillLink(row scanner) (SeriesBillLink, error) {
	var (
		one     SeriesBillLink
		spaceID uuid.UUID
	)
	err := row.Scan(&one.SeriesID, &spaceID, &one.SubaccountID, &one.CreatedAt)
	one.SpaceID = SpaceIDOf(spaceID)
	return one, err
}

// LinkSeriesBill attaches a reminder to a subaccount. Both directions are
// unique, so an existing link on either side is released first rather than
// failing on the constraint.
func (s *Store) LinkSeriesBill(
	ctx context.Context, spaceID SpaceID, seriesID, subaccountID uuid.UUID,
) (SeriesBillLink, error) {
	var one SeriesBillLink
	err := s.InTx(ctx, func(tx *Store) error {
		released, err := queryAll(ctx, tx.db, "store: link series bill", scanValue[uuid.UUID],
			`DELETE FROM series_bill_links
			  WHERE space_id = $1 AND (series_id = $2 OR subaccount_id = $3)
			  RETURNING series_id`,
			spaceID.UUID(), seriesID, subaccountID)
		if err != nil {
			return err
		}
		one, err = scanSeriesBillLink(tx.db.QueryRow(ctx,
			`INSERT INTO series_bill_links (series_id, space_id, subaccount_id)
			 VALUES ($1, $2, $3) RETURNING `+seriesBillLinkColumns,
			seriesID, spaceID.UUID(), subaccountID))
		if err != nil {
			return wrap("store: link series bill", err)
		}
		return tx.ReconcileSeriesReceipts(ctx, spaceID, append(released, seriesID))
	})
	return one, err
}

func (s *Store) UnlinkSeriesBill(ctx context.Context, spaceID SpaceID, seriesID uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.execOne(ctx, "store: unlink series bill",
			`DELETE FROM series_bill_links WHERE space_id = $1 AND series_id = $2`,
			spaceID.UUID(), seriesID); err != nil {
			return err
		}
		return tx.ReconcileSeriesReceipts(ctx, spaceID, []uuid.UUID{seriesID})
	})
}

func (s *Store) ListSeriesBillLinks(
	ctx context.Context, spaceID SpaceID, seriesIDs []uuid.UUID,
) ([]SeriesBillLink, error) {
	return queryAll(ctx, s.db, "store: list series bill links", scanSeriesBillLink,
		`SELECT `+seriesBillLinkColumns+` FROM series_bill_links
		  WHERE space_id = $1 AND (COALESCE(cardinality($2::uuid[]), 0) = 0 OR series_id = ANY($2))
		  ORDER BY created_at`, spaceID.UUID(), seriesIDs)
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
