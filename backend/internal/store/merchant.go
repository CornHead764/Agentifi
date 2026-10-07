package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
)

// The merchant connector's tables: logins, orders and items, card charges,
// refunds, and bank-row matches. Every row carries its merchant; the facts
// about a merchant live in domain.Merchants.

// MerchantAccount is one of the household's logins at a merchant, by label.
type MerchantAccount struct {
	ID         uuid.UUID
	SpaceID    SpaceID
	Merchant   domain.MerchantID
	Label      string
	Email      string
	HasSession bool
	SignedInAt *time.Time
	// HasPassword and HasTOTP say secrets are sealed on the row; the
	// ciphertext is deliberately not a field, so no listing can carry it.
	HasPassword  bool
	HasTOTP      bool
	SecondFactor domain.SecondFactor
	// SignInPausedAt is when a pull stopped at something only a person can
	// fix (SignInPausedFor says which). The scheduler skips a paused account;
	// a person may still press Update now.
	SignInPausedAt  *time.Time
	SignInPausedFor string
	// SyncEnabled turns the daily pull on; SyncDays is how far back it reads.
	SyncEnabled bool
	SyncDays    int
	// The last pull: when, how it ended ("", ok, needs_sign_in, failed) and
	// why. NeedsSignIn stays up until a new session is stored.
	LastSyncedAt   *time.Time
	LastSyncStatus string
	LastSyncError  string
	// HasFailureScreenshot says the last pull's failure kept the page it
	// failed on; MerchantSyncScreenshot reads it.
	HasFailureScreenshot bool
	NeedsSignIn          bool
	// GiftCardAccountID is the household account holding this login's gift
	// card balance, once a pull has read one.
	GiftCardAccountID  uuid.UUID
	GiftCardBalance    domain.Money
	HasGiftCardBalance bool
	GiftCardBalanceAt  *time.Time
	// The last invoice backfill: when it ended, how many invoices it filed,
	// how many orders it left without one, and why it stopped early.
	InvoiceBackfillAt      *time.Time
	InvoiceBackfillFiled   int
	InvoiceBackfillLeft    int
	InvoiceBackfillStopped string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// Name is the label, else the email, else the merchant's own name.
func (a MerchantAccount) Name() string {
	if label := strings.TrimSpace(a.Label); label != "" {
		return label
	}
	if email := strings.TrimSpace(a.Email); email != "" {
		return email
	}
	if merchant, ok := domain.MerchantByID(a.Merchant); ok {
		return merchant.Name + " account"
	}
	return "Account"
}

// The last_sync_status values.
const (
	MerchantSyncOK          = runOK
	MerchantSyncNeedsSignIn = runNeedsSignIn
	MerchantSyncFailed      = "failed"
)

type MerchantOrder struct {
	ID                uuid.UUID
	SpaceID           SpaceID
	Merchant          domain.MerchantID
	MerchantAccountID uuid.UUID
	OrderNumber       string
	// Kind is one of domain.Purchase*; Location is where a warehouse or gas
	// purchase was made.
	Kind       string
	Location   string
	OrderedOn  domain.Date
	Total      domain.Money
	Currency   string
	Status     string
	DetailsURL string
	Source     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// From the invoice. HasGiftCard false means nobody has looked; true with
	// a zero amount means no gift card paid any of it.
	GiftCard    domain.Money
	HasGiftCard bool
	Tax         domain.Money
	HasTax      bool
	Shipping    domain.Money
	HasShipping bool
	// IgnoredAt is set when a person said no bank row will explain this order
	// (charged to an untracked card); it is offered to no row and not counted
	// as waiting.
	IgnoredAt *time.Time
	Items     []MerchantOrderItem
	// Refunds is filled only when the caller asks for it.
	Refunds []MerchantRefund
	// MatchedTransactionIDs and Matched are the matched bank rows, when the
	// list asked for them.
	MatchedTransactionIDs []uuid.UUID
	Matched               []MerchantMatchedTransaction
}

// CardTotal is the total less what a gift card paid; zero means no bank row
// will match. It delegates to the matcher so the two cannot disagree.
func (o MerchantOrder) CardTotal() domain.Money {
	return domain.MerchantOrderFacts{Total: o.Total, GiftCard: o.GiftCard}.CardTotal()
}

func (o MerchantOrder) Ignored() bool { return o.IgnoredAt != nil }

type MerchantMatchedTransaction struct {
	ID            uuid.UUID
	AccountID     uuid.UUID
	Date          domain.Date
	Amount        domain.Money
	Payee         string
	StatementName string
	AccountName   string
	Basis         string
	Confidence    float64
}

type MerchantOrderItem struct {
	ID       uuid.UUID
	OrderID  uuid.UUID
	Position int
	// SKU is the merchant's own item id: an ASIN, a Costco item number.
	SKU          string
	Title        string
	Quantity     int
	UnitPrice    domain.Money
	HasUnitPrice bool
	TotalOwed    domain.Money
	HasTotalOwed bool
	ShippedOn    domain.Date
	Condition    string
	URL          string
	// Catalog is the merchant listing's name for the item, when the catalog
	// pass found it.
	Catalog *CatalogItem
}

// MerchantCharge is one debit or credit on a card for an order, signed as the
// bank sees it.
type MerchantCharge struct {
	ID                uuid.UUID
	Merchant          domain.MerchantID
	MerchantAccountID uuid.UUID
	OrderNumber       string
	ChargedOn         domain.Date
	Amount            domain.Money
	Instrument        string
}

// MerchantRefund is one return the merchant's records show.
type MerchantRefund struct {
	ID                uuid.UUID
	Merchant          domain.MerchantID
	MerchantAccountID uuid.UUID
	OrderNumber       string
	// SKU and Title are empty for a refund of the whole order.
	SKU        string
	Title      string
	Quantity   int
	RefundedOn domain.Date
	// Amount is positive, signed as a bank row would be.
	Amount      domain.Money
	Instrument  string
	Destination string
	Status      string
	Source      string
	// TransactionID is the matched bank row, filled by the display queries.
	TransactionID uuid.UUID
}

// ToGiftCard says the money went to a balance the bank never sees, so no bank
// row will ever explain it.
func (r MerchantRefund) ToGiftCard() bool { return r.Destination == MerchantRefundToGiftCard }

const (
	MerchantRefundToCard     = "card"
	MerchantRefundToGiftCard = "gift_card"
)

type MerchantMatch struct {
	TransactionID uuid.UUID
	OrderID       uuid.UUID
	// RefundID is set only for a credit matched to a return.
	RefundID   *uuid.UUID
	Amount     domain.Money
	Basis      string
	Confidence float64
	CreatedAt  time.Time
}

// --- Accounts ----------------------------------------------------------------

const merchantAccountColumns = `id, space_id, merchant, label, email, (session_state IS NOT NULL), signed_in_at,
	(credential_sealed IS NOT NULL), (credential_sealed IS NOT NULL AND credential_has_totp), second_factor,
	sign_in_paused_at, sign_in_paused_for, sync_enabled, sync_days, last_synced_at, last_sync_status, last_sync_error,
	(last_sync_screenshot IS NOT NULL), needs_sign_in, gift_card_account_id, gift_card_balance, gift_card_balance_at,
	invoice_backfill_at, invoice_backfill_filed, invoice_backfill_left, invoice_backfill_stopped, created_at, updated_at`

func scanMerchantAccount(row scanner) (MerchantAccount, error) {
	var (
		one      MerchantAccount
		spaceID  uuid.UUID
		giftCard *uuid.UUID
		balance  pgtype.Numeric
	)
	err := row.Scan(&one.ID, &spaceID, &one.Merchant, &one.Label, &one.Email, &one.HasSession, &one.SignedInAt,
		&one.HasPassword, &one.HasTOTP, &one.SecondFactor, &one.SignInPausedAt, &one.SignInPausedFor, &one.SyncEnabled, &one.SyncDays, &one.LastSyncedAt, &one.LastSyncStatus, &one.LastSyncError,
		&one.HasFailureScreenshot, &one.NeedsSignIn, &giftCard, &balance, &one.GiftCardBalanceAt,
		&one.InvoiceBackfillAt, &one.InvoiceBackfillFiled, &one.InvoiceBackfillLeft, &one.InvoiceBackfillStopped,
		&one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.GiftCardAccountID = Deref(giftCard)
	one.GiftCardBalance, one.HasGiftCardBalance, err = pgconv.ReadNullMoney(balance, "merchant_accounts.gift_card_balance")
	return one, err
}

func (s *Store) SetMerchantGiftCard(ctx context.Context, spaceID SpaceID, id, accountID uuid.UUID, balance domain.Money, hasBalance bool, at time.Time) error {
	var figure *pgtype.Numeric
	if hasBalance {
		arg := pgconv.Money(balance)
		figure = &arg
	}
	return s.execOne(ctx, "store: set merchant gift card",
		`UPDATE merchant_accounts
		    SET gift_card_account_id = $3, gift_card_balance = $4, gift_card_balance_at = $5, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, accountID, figure, at)
}

// IsMerchantGiftCardAccount says whether an account holds a merchant login's
// gift card balance, which changes what its rows can be matched to.
func (s *Store) IsMerchantGiftCardAccount(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) (bool, error) {
	var found bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM merchant_accounts WHERE space_id = $1 AND gift_card_account_id = $2)`,
		spaceID.UUID(), accountID).Scan(&found)
	return found, wrap("store: is merchant gift card account", err)
}

// SaveMerchantSession seals a browser session onto the account; see
// saveSession. A fresh sign-in also turns the daily pull on.
func (s *Store) SaveMerchantSession(ctx context.Context, spaceID SpaceID, id uuid.UUID, email, state string, fresh bool) error {
	return s.saveSession(ctx, merchantConnectorState, spaceID, id, state, fresh,
		`email = CASE WHEN @email <> '' THEN @email ELSE email END,
		 sync_enabled = CASE WHEN @fresh THEN true ELSE sync_enabled END,`,
		pgx.NamedArgs{"email": email})
}

// MerchantSession opens the sealed session; ErrNotFound when there is none.
func (s *Store) MerchantSession(ctx context.Context, spaceID SpaceID, id uuid.UUID) (string, error) {
	return s.readSealed(ctx, "store: merchant session", "merchant_accounts", "session_state",
		merchantSessionContext(spaceID, id), spaceID, id)
}

// MerchantCredential is a merchant login, sealed in the same shape as a bill
// credential: Username is the email, TOTPSecret the authenticator key.
type MerchantCredential = BillCredential

// SaveMerchantCredential seals a password that just worked and lifts any pause.
func (s *Store) SaveMerchantCredential(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, credential MerchantCredential,
) error {
	return s.saveCredential(ctx, merchantConnectorState, spaceID, id, credential, "", nil)
}

// MerchantCredentialOf opens the sealed password; ErrNotFound when none is kept.
func (s *Store) MerchantCredentialOf(
	ctx context.Context, spaceID SpaceID, id uuid.UUID,
) (MerchantCredential, error) {
	return s.credentialOf(ctx, merchantConnectorState, spaceID, id)
}

// ClearMerchantCredential forgets the password, key and pause, not the session.
func (s *Store) ClearMerchantCredential(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.clearCredential(ctx, merchantConnectorState, spaceID, id, "", nil)
}

// PauseMerchantSignIn records that a pull stopped at something only a person
// can fix; see pauseSignIn.
func (s *Store) PauseMerchantSignIn(ctx context.Context, spaceID SpaceID, id uuid.UUID, reason string) error {
	return s.pauseSignIn(ctx, merchantConnectorState, spaceID, id, reason)
}

// ClearMerchantSession forgets the session; see clearSession.
func (s *Store) ClearMerchantSession(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.clearSession(ctx, merchantConnectorState, spaceID, id)
}

func (s *Store) UpdateMerchantSync(ctx context.Context, spaceID SpaceID, id uuid.UUID, enabled bool, days int) error {
	return s.execOne(ctx, "store: update merchant sync",
		`UPDATE merchant_accounts SET sync_enabled = $3, sync_days = $4, updated_at = now()
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, enabled, days)
}

// MarkMerchantSync records how a pull ended, with the page it failed on or
// nil; see markRun.
func (s *Store) MarkMerchantSync(ctx context.Context, spaceID SpaceID, id uuid.UUID, status, detail string, shot []byte) error {
	return s.markRun(ctx, merchantConnectorState, spaceID, id, status, detail, shot, nil)
}

// MerchantSyncScreenshot is the page the account's last pull failed on, as a
// JPEG; ErrNotFound when it kept none.
func (s *Store) MerchantSyncScreenshot(ctx context.Context, spaceID SpaceID, id uuid.UUID) ([]byte, error) {
	return s.runScreenshot(ctx, merchantConnectorState, spaceID, id)
}

// ListMerchantAccountsDue is every account due a scheduled pull; see listDue.
func (s *Store) ListMerchantAccountsDue(ctx context.Context, since time.Time) ([]MerchantAccount, error) {
	return listDue(ctx, s, merchantConnectorState, merchantAccountColumns, scanMerchantAccount, since)
}

func (s *Store) CreateMerchantAccount(ctx context.Context, spaceID SpaceID, one *MerchantAccount) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	err := s.db.QueryRow(ctx,
		`INSERT INTO merchant_accounts (id, space_id, merchant, label) VALUES ($1, $2, $3, $4)
		 RETURNING created_at, updated_at`,
		one.ID, spaceID.UUID(), string(one.Merchant), one.Label).Scan(&one.CreatedAt, &one.UpdatedAt)
	return wrap("store: create merchant account", err)
}

func (s *Store) GetMerchantAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MerchantAccount, error) {
	one, err := scanMerchantAccount(s.db.QueryRow(ctx,
		`SELECT `+merchantAccountColumns+` FROM merchant_accounts WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	return one, wrap("store: get merchant account", err)
}

func (s *Store) ListMerchantAccounts(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID) ([]MerchantAccount, error) {
	return queryAll(ctx, s.db, "store: list merchant accounts", scanMerchantAccount,
		`SELECT `+merchantAccountColumns+` FROM merchant_accounts
		  WHERE space_id = $1 AND merchant = $2 ORDER BY label`,
		spaceID.UUID(), string(merchant))
}

func (s *Store) SetMerchantSecondFactor(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, factor domain.SecondFactor,
) error {
	return s.execOne(ctx, "store: set merchant second factor",
		`UPDATE merchant_accounts SET second_factor = $3, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, string(factor))
}

func (s *Store) UpdateMerchantAccount(ctx context.Context, spaceID SpaceID, one *MerchantAccount) error {
	err := s.db.QueryRow(ctx,
		`UPDATE merchant_accounts SET label = $3, updated_at = now()
		 WHERE space_id = $1 AND id = $2 RETURNING updated_at`,
		spaceID.UUID(), one.ID, one.Label).Scan(&one.UpdatedAt)
	return wrap("store: update merchant account", err)
}

// DeleteMerchantAccount removes the account and, by cascade, everything
// imported from it. The orders' invoices are released first, and the receipts
// they were filed as with them: document links have no foreign key, so a stale
// link would keep an invoice out of the purge.
func (s *Store) DeleteMerchantAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		matched, err := queryAll(ctx, tx.db, "store: list rows matched to merchant account", scanValue[uuid.UUID], `
			SELECT DISTINCT m.transaction_id FROM merchant_matches m
			JOIN merchant_orders o ON o.id = m.order_id AND o.space_id = m.space_id
			WHERE m.space_id = $1 AND o.merchant_account_id = $2`, spaceID.UUID(), id)
		if err != nil {
			return err
		}
		if _, err := tx.db.Exec(ctx, `
			DELETE FROM document_links
			WHERE space_id = $1 AND kind = $2 AND target_id IN (
			    SELECT id FROM merchant_orders WHERE space_id = $1 AND merchant_account_id = $3)`,
			spaceID.UUID(), string(DocumentLinkMerchantOrder), id); err != nil {
			return wrap("store: release order invoices", err)
		}
		if err := tx.execOne(ctx, "store: delete merchant account",
			`DELETE FROM merchant_accounts WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id); err != nil {
			return err
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, matched)
		return err
	})
}

// --- Orders --------------------------------------------------------------------

const merchantOrderColumns = `id, space_id, merchant, merchant_account_id, order_number, ordered_on, total,
	currency, status, details_url, source, created_at, updated_at, gift_card_amount, tax, shipping,
	ignored_at, kind, location`

func scanMerchantOrder(row scanner) (MerchantOrder, error) {
	var (
		one                 MerchantOrder
		spaceID             uuid.UUID
		on                  time.Time
		total               pgtype.Numeric
		gift, tax, shipping pgtype.Numeric
	)
	err := row.Scan(&one.ID, &spaceID, &one.Merchant, &one.MerchantAccountID, &one.OrderNumber, &on, &total,
		&one.Currency, &one.Status, &one.DetailsURL, &one.Source, &one.CreatedAt, &one.UpdatedAt,
		&gift, &tax, &shipping, &one.IgnoredAt, &one.Kind, &one.Location)
	if err != nil {
		return MerchantOrder{}, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.OrderedOn = dateOf(on)
	if one.Total, err = pgconv.ReadMoney(total, "merchant_orders.total"); err != nil {
		return MerchantOrder{}, err
	}
	if one.GiftCard, one.HasGiftCard, err = pgconv.ReadNullMoney(gift, "merchant_orders.gift_card_amount"); err != nil {
		return MerchantOrder{}, err
	}
	if one.Tax, one.HasTax, err = pgconv.ReadNullMoney(tax, "merchant_orders.tax"); err != nil {
		return MerchantOrder{}, err
	}
	if one.Shipping, one.HasShipping, err = pgconv.ReadNullMoney(shipping, "merchant_orders.shipping"); err != nil {
		return MerchantOrder{}, err
	}
	return one, nil
}

// UpsertMerchantOrder writes an order by number within its account and
// reports whether it was new. Items are replaced only when the input carries
// any, so a charges-only file cannot strip an order.
func (s *Store) UpsertMerchantOrder(ctx context.Context, spaceID SpaceID, one *MerchantOrder) (bool, error) {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	if one.Kind == "" {
		one.Kind = domain.PurchaseOnline
	}
	var inserted bool
	err := s.InTx(ctx, func(tx *Store) error {
		err := tx.db.QueryRow(ctx,
			`INSERT INTO merchant_orders
			     (id, space_id, merchant_account_id, order_number, ordered_on, total, currency, status,
			      details_url, source, gift_card_amount, tax, shipping, merchant, kind, location)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			 ON CONFLICT (space_id, merchant_account_id, order_number) DO UPDATE SET
			     ordered_on = EXCLUDED.ordered_on,
			     total = CASE WHEN EXCLUDED.total <> 0 THEN EXCLUDED.total ELSE merchant_orders.total END,
			     currency = EXCLUDED.currency,
			     status = CASE WHEN EXCLUDED.status <> '' THEN EXCLUDED.status ELSE merchant_orders.status END,
			     details_url = CASE WHEN EXCLUDED.details_url <> '' THEN EXCLUDED.details_url
			                        ELSE merchant_orders.details_url END,
			     source = EXCLUDED.source,
			     gift_card_amount = COALESCE(EXCLUDED.gift_card_amount, merchant_orders.gift_card_amount),
			     tax = COALESCE(EXCLUDED.tax, merchant_orders.tax),
			     shipping = COALESCE(EXCLUDED.shipping, merchant_orders.shipping),
			     kind = EXCLUDED.kind,
			     location = CASE WHEN EXCLUDED.location <> '' THEN EXCLUDED.location
			                     ELSE merchant_orders.location END,
			     updated_at = now()
			 RETURNING id, created_at, updated_at, (xmax = 0)`,
			one.ID, spaceID.UUID(), one.MerchantAccountID, one.OrderNumber, one.OrderedOn.Time(),
			pgconv.Money(one.Total), one.Currency, one.Status, one.DetailsURL, one.Source,
			pgconv.NullMoney(one.GiftCard, one.HasGiftCard), pgconv.NullMoney(one.Tax, one.HasTax),
			pgconv.NullMoney(one.Shipping, one.HasShipping), string(one.Merchant), one.Kind, one.Location).
			Scan(&one.ID, &one.CreatedAt, &one.UpdatedAt, &inserted)
		if err != nil {
			return wrap("store: upsert merchant order", err)
		}
		if len(one.Items) == 0 {
			return nil
		}
		// Priced items beat unpriced ones: a later pull's order cards, which
		// carry no prices, must not undo an invoice read.
		priced := false
		for _, item := range one.Items {
			if item.HasUnitPrice || item.HasTotalOwed {
				priced = true
			}
		}
		if !priced {
			var kept int
			if err := tx.db.QueryRow(ctx, `SELECT count(*) FROM merchant_order_items
				WHERE order_id = $1 AND (unit_price IS NOT NULL OR total_owed IS NOT NULL)`, one.ID).
				Scan(&kept); err != nil {
				return wrap("store: upsert merchant order items", err)
			}
			if kept > 0 {
				return nil
			}
		}
		if _, err := tx.db.Exec(ctx, `DELETE FROM merchant_order_items WHERE order_id = $1`, one.ID); err != nil {
			return wrap("store: upsert merchant order items", err)
		}
		for i := range one.Items {
			item := &one.Items[i]
			if item.ID == uuid.Nil {
				item.ID = uuid.New()
			}
			item.OrderID, item.Position = one.ID, i
			_, err := tx.db.Exec(ctx,
				`INSERT INTO merchant_order_items
				     (id, space_id, order_id, position, sku, title, quantity, unit_price, total_owed,
				      shipped_on, condition, url)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
				item.ID, spaceID.UUID(), one.ID, i, item.SKU, item.Title, item.Quantity,
				pgconv.NullMoney(item.UnitPrice, item.HasUnitPrice),
				pgconv.NullMoney(item.TotalOwed, item.HasTotalOwed),
				pgconv.NullDate(item.ShippedOn), item.Condition, item.URL)
			if err != nil {
				return wrap("store: upsert merchant order items", err)
			}
		}
		return nil
	})
	return inserted, err
}

func (s *Store) ExistingMerchantOrderNumbers(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, numbers []string,
) (map[string]bool, error) {
	found, err := queryAll(ctx, s.db, "store: existing merchant orders", scanValue[string],
		`SELECT order_number FROM merchant_orders
		 WHERE space_id = $1 AND merchant_account_id = $2 AND order_number = ANY($3)`,
		spaceID.UUID(), accountID, numbers)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(found))
	for _, number := range found {
		out[number] = true
	}
	return out, nil
}

// MerchantOrderQuery narrows a listing. Merchant is required.
type MerchantOrderQuery struct {
	Merchant  domain.MerchantID
	AccountID uuid.UUID
	From, To  domain.Date
	Unmatched bool
	Limit     int
	Offset    int
}

// ListMerchantOrders lists orders newest first, with items and matched rows,
// and the total count.
func (s *Store) ListMerchantOrders(ctx context.Context, spaceID SpaceID, q MerchantOrderQuery) ([]MerchantOrder, int, error) {
	args := &argList{}
	where := ` WHERE space_id = ` + args.add(spaceID.UUID()) + ` AND merchant = ` + args.add(string(q.Merchant))
	if q.AccountID != uuid.Nil {
		where += ` AND merchant_account_id = ` + args.add(q.AccountID)
	}
	if !q.From.IsZero() {
		where += ` AND ordered_on >= ` + args.add(q.From.Time())
	}
	if !q.To.IsZero() {
		where += ` AND ordered_on <= ` + args.add(q.To.Time())
	}
	if q.Unmatched {
		// An order a gift card paid in full, one that was cancelled, or one a
		// person has ignored has no bank row to wait for.
		where += ` AND NOT EXISTS (SELECT 1 FROM merchant_matches m WHERE m.order_id = merchant_orders.id)
		           AND total - COALESCE(gift_card_amount, 0) > 0 AND status NOT ILIKE 'cancel%'
		           AND ignored_at IS NULL`
	}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM merchant_orders`+where, args.values...).
		Scan(&total); err != nil {
		return nil, 0, wrap("store: count merchant orders", err)
	}
	sql := `SELECT ` + merchantOrderColumns + ` FROM merchant_orders` + where +
		` ORDER BY ordered_on DESC, order_number DESC`
	if q.Limit > 0 {
		sql += ` LIMIT ` + args.add(q.Limit)
	}
	if q.Offset > 0 {
		sql += ` OFFSET ` + args.add(q.Offset)
	}
	orders, err := queryAll(ctx, s.db, "store: list merchant orders", scanMerchantOrder, sql, args.values...)
	if err != nil {
		return nil, 0, err
	}
	if err := s.attachMerchantItems(ctx, orders); err != nil {
		return nil, 0, err
	}
	if err := s.AttachMerchantMatches(ctx, orders); err != nil {
		return nil, 0, err
	}
	if err := s.AttachMerchantRefunds(ctx, orders); err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (s *Store) GetMerchantOrder(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MerchantOrder, error) {
	one, err := scanMerchantOrder(s.db.QueryRow(ctx,
		`SELECT `+merchantOrderColumns+` FROM merchant_orders WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	if err != nil {
		return MerchantOrder{}, wrap("store: get merchant order", err)
	}
	orders := []MerchantOrder{one}
	if err := s.attachMerchantItems(ctx, orders); err != nil {
		return MerchantOrder{}, err
	}
	if err := s.AttachMerchantRefunds(ctx, orders); err != nil {
		return MerchantOrder{}, err
	}
	return orders[0], nil
}

// DisplayTitle is the catalog's name when found, else the merchant's record.
func (i MerchantOrderItem) DisplayTitle() string {
	if i.Catalog != nil && i.Catalog.Title != "" {
		return i.Catalog.Title
	}
	return i.Title
}

func (s *Store) attachMerchantItems(ctx context.Context, orders []MerchantOrder) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(orders))
	index := make(map[uuid.UUID]int, len(orders))
	for i, one := range orders {
		ids = append(ids, one.ID)
		index[one.ID] = i
	}
	items, err := queryAll(ctx, s.db, "store: merchant order items", func(row scanner) (MerchantOrderItem, error) {
		var (
			item       MerchantOrderItem
			unit, owed pgtype.Numeric
			shipped    *time.Time
			catalog    [6]*string
			err        error
		)
		if err = row.Scan(&item.ID, &item.OrderID, &item.Position, &item.SKU, &item.Title,
			&item.Quantity, &unit, &owed, &shipped, &item.Condition, &item.URL,
			&catalog[0], &catalog[1], &catalog[2], &catalog[3], &catalog[4], &catalog[5]); err != nil {
			return item, err
		}
		if catalog[0] != nil {
			text := func(p *string) string {
				if p == nil {
					return ""
				}
				return *p
			}
			item.Catalog = &CatalogItem{
				SKU: item.SKU, Status: CatalogFound, Title: *catalog[0], Brand: text(catalog[1]),
				Size: text(catalog[2]), Category: text(catalog[3]), ImageURL: text(catalog[4]), URL: text(catalog[5]),
			}
		}
		if item.UnitPrice, item.HasUnitPrice, err = pgconv.ReadNullMoney(unit, "unit_price"); err != nil {
			return item, err
		}
		if item.TotalOwed, item.HasTotalOwed, err = pgconv.ReadNullMoney(owed, "total_owed"); err != nil {
			return item, err
		}
		item.ShippedOn = pgconv.ReadNullDate(shipped)
		return item, nil
	},
		`SELECT i.id, i.order_id, i.position, i.sku, i.title, i.quantity, i.unit_price, i.total_owed,
		        i.shipped_on, i.condition, i.url,
		        c.title, c.brand, c.size, c.category, c.image_url, c.url
		   FROM merchant_order_items i
		   JOIN merchant_orders o ON o.id = i.order_id
		   LEFT JOIN merchant_catalog c ON c.merchant = o.merchant AND c.sku = i.sku AND c.status = 'found'
		  WHERE i.order_id = ANY($1) ORDER BY i.order_id, i.position`, ids)
	if err != nil {
		return err
	}
	for _, item := range items {
		i := index[item.OrderID]
		orders[i].Items = append(orders[i].Items, item)
	}
	return nil
}

func (s *Store) AttachMerchantMatches(ctx context.Context, orders []MerchantOrder) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(orders))
	index := make(map[uuid.UUID]int, len(orders))
	for i, one := range orders {
		ids = append(ids, one.ID)
		index[one.ID] = i
	}
	type pair struct {
		order uuid.UUID
		txn   MerchantMatchedTransaction
	}
	pairs, err := queryAll(ctx, s.db, "store: merchant matches", func(row scanner) (pair, error) {
		var (
			p      pair
			on     time.Time
			amount pgtype.Numeric
		)
		err := row.Scan(&p.order, &p.txn.ID, &p.txn.AccountID, &on, &amount, &p.txn.Payee,
			&p.txn.StatementName, &p.txn.AccountName, &p.txn.Basis, &p.txn.Confidence)
		if err != nil {
			return p, err
		}
		p.txn.Date = dateOf(on)
		p.txn.Amount, err = pgconv.ReadMoney(amount, "transactions.amount")
		return p, err
	},
		`SELECT m.order_id, m.transaction_id, t.account_id, t.date, t.amount, t.payee,
		        t.statement_name, a.name, m.basis, m.confidence
		   FROM merchant_matches m
		   JOIN transactions t ON t.id = m.transaction_id
		   JOIN accounts a ON a.id = t.account_id
		  WHERE m.order_id = ANY($1)
		  ORDER BY m.created_at`, ids)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		i := index[p.order]
		orders[i].MatchedTransactionIDs = append(orders[i].MatchedTransactionIDs, p.txn.ID)
		orders[i].Matched = append(orders[i].Matched, p.txn)
	}
	return nil
}

// GetMerchantOrderByNumber finds an account's order by the merchant's number.
func (s *Store) GetMerchantOrderByNumber(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, number string) (MerchantOrder, error) {
	one, err := scanMerchantOrder(s.db.QueryRow(ctx,
		`SELECT `+merchantOrderColumns+` FROM merchant_orders
		  WHERE space_id = $1 AND merchant_account_id = $2 AND order_number = $3`,
		spaceID.UUID(), accountID, number))
	if err != nil {
		return MerchantOrder{}, wrap("store: get merchant order by number", err)
	}
	orders := []MerchantOrder{one}
	if err := s.attachMerchantItems(ctx, orders); err != nil {
		return MerchantOrder{}, err
	}
	if err := s.AttachMerchantRefunds(ctx, orders); err != nil {
		return MerchantOrder{}, err
	}
	return orders[0], nil
}

// MerchantMatchesForOrders is every match already made against the orders, so
// a charge one bank row took is not offered to another.
func (s *Store) MerchantMatchesForOrders(ctx context.Context, spaceID SpaceID, orderIDs []uuid.UUID) ([]MerchantMatch, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.db, "store: merchant matches for orders", scanMerchantMatch,
		`SELECT transaction_id, order_id, amount, basis, confidence, created_at, refund_id
		   FROM merchant_matches WHERE space_id = $1 AND order_id = ANY($2)`, spaceID.UUID(), orderIDs)
}

// MerchantOrdersWithInvoice lists the order numbers since a day whose invoice
// a pull has read in full (payment figures and every item priced), which the
// next pull may skip.
func (s *Store) MerchantOrdersWithInvoice(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, since domain.Date) ([]string, error) {
	return queryAll(ctx, s.db, "store: merchant orders with invoice", scanValue[string],
		`SELECT order_number FROM merchant_orders o
		  WHERE space_id = $1 AND merchant_account_id = $2 AND ordered_on >= $3
		    AND gift_card_amount IS NOT NULL
		    AND NOT EXISTS (SELECT 1 FROM merchant_order_items i WHERE i.order_id = o.id
		                       AND i.unit_price IS NULL AND i.total_owed IS NULL)`,
		spaceID.UUID(), accountID, since.Time())
}

// MerchantOrdersWithInvoiceDocument lists the order numbers since a day that
// already have an invoice document on file, which the next pull need not
// fetch again.
func (s *Store) MerchantOrdersWithInvoiceDocument(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, since domain.Date,
) ([]string, error) {
	return queryAll(ctx, s.db, "store: merchant orders with invoice document", scanValue[string],
		`SELECT o.order_number FROM merchant_orders o
		  WHERE o.space_id = $1 AND o.merchant_account_id = $2 AND o.ordered_on >= $3
		    AND EXISTS (SELECT 1 FROM document_links l
		                 WHERE l.space_id = o.space_id AND l.kind = $4
		                   AND l.target_id = o.id AND l.role = $5)`,
		spaceID.UUID(), accountID, since.Time(), string(DocumentLinkMerchantOrder), DocumentRoleInvoice)
}

// merchantOrderWantsInvoice is an order with no invoice document that a
// backfill could still reopen for one: it names a page, is not cancelled, and
// has not come back empty three times.
const merchantOrderWantsInvoice = `o.details_url <> '' AND o.status NOT ILIKE 'cancel%' AND o.invoice_misses < 3
	AND NOT EXISTS (SELECT 1 FROM document_links l
	                 WHERE l.space_id = o.space_id AND l.kind = 'merchant_order'
	                   AND l.target_id = o.id AND l.role = 'invoice')`

// MerchantOrdersWantingInvoice lists, newest first, every order of the
// account a backfill could reopen for its invoice.
func (s *Store) MerchantOrdersWantingInvoice(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) ([]string, error) {
	return queryAll(ctx, s.db, "store: merchant orders wanting an invoice", scanValue[string],
		`SELECT o.order_number FROM merchant_orders o
		  WHERE o.space_id = $1 AND o.merchant_account_id = $2 AND `+merchantOrderWantsInvoice+`
		  ORDER BY o.ordered_on DESC, o.order_number DESC`,
		spaceID.UUID(), accountID)
}

// CountMerchantOrdersWantingInvoice counts the account's orders a backfill
// could still reopen for their invoice.
func (s *Store) CountMerchantOrdersWantingInvoice(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM merchant_orders o
		  WHERE o.space_id = $1 AND o.merchant_account_id = $2 AND `+merchantOrderWantsInvoice,
		spaceID.UUID(), accountID).Scan(&n)
	return n, wrap("store: count merchant orders wanting an invoice", err)
}

// MarkMerchantInvoicesMissed records that a backfill reopened these orders
// and found no invoice to print.
func (s *Store) MarkMerchantInvoicesMissed(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, numbers []string,
) error {
	if len(numbers) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE merchant_orders SET invoice_misses = invoice_misses + 1
		  WHERE space_id = $1 AND merchant_account_id = $2 AND order_number = ANY($3)`,
		spaceID.UUID(), accountID, numbers)
	return wrap("store: mark merchant invoices missed", err)
}

// merchantReceiptWantsDocument is a warehouse or fuel purchase ($3) whose
// every item is described and that has no invoice document: one a receipt can
// be laid out for.
const merchantReceiptWantsDocument = `o.kind = ANY($3)
	AND EXISTS (SELECT 1 FROM merchant_order_items i WHERE i.order_id = o.id)
	AND NOT EXISTS (SELECT 1 FROM merchant_order_items i WHERE i.order_id = o.id AND btrim(i.title) = '')
	AND NOT EXISTS (SELECT 1 FROM document_links l
	                 WHERE l.space_id = o.space_id AND l.kind = 'merchant_order'
	                   AND l.target_id = o.id AND l.role = 'invoice')`

var receiptKinds = []string{domain.PurchaseWarehouse, domain.PurchaseFuel}

// MerchantReceiptsWithoutDocument lists, newest first and with their items,
// every purchase of the account a receipt can be laid out for.
func (s *Store) MerchantReceiptsWithoutDocument(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) ([]MerchantOrder, error) {
	orders, err := queryAll(ctx, s.db, "store: merchant receipts without a document", scanMerchantOrder,
		`SELECT `+merchantOrderColumns+` FROM merchant_orders o
		  WHERE o.space_id = $1 AND o.merchant_account_id = $2 AND `+merchantReceiptWantsDocument+`
		  ORDER BY o.ordered_on DESC, o.order_number DESC`,
		spaceID.UUID(), accountID, receiptKinds)
	if err != nil {
		return nil, err
	}
	return orders, s.attachMerchantItems(ctx, orders)
}

// CountMerchantReceiptsWithoutDocument counts the purchases of the account a
// receipt can be laid out for.
func (s *Store) CountMerchantReceiptsWithoutDocument(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM merchant_orders o
		  WHERE o.space_id = $1 AND o.merchant_account_id = $2 AND `+merchantReceiptWantsDocument,
		spaceID.UUID(), accountID, receiptKinds).Scan(&n)
	return n, wrap("store: count merchant receipts without a document", err)
}

// RecordMerchantInvoiceBackfill keeps how an invoice backfill ended. It
// leaves the last pull's stamp alone, which the scheduler reads.
func (s *Store) RecordMerchantInvoiceBackfill(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, filed, left int, stopped string,
) error {
	return s.execOne(ctx, "store: record merchant invoice backfill",
		`UPDATE merchant_accounts
		    SET invoice_backfill_at = now(), invoice_backfill_filed = $3, invoice_backfill_left = $4,
		        invoice_backfill_stopped = $5, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, filed, left, stopped)
}

func (s *Store) MerchantChargesForOrders(ctx context.Context, spaceID SpaceID, numbers []string) ([]MerchantCharge, error) {
	if len(numbers) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.db, "store: merchant charges for orders", scanMerchantCharge,
		`SELECT id, merchant, merchant_account_id, order_number, charged_on, amount, instrument
		   FROM merchant_charges WHERE space_id = $1 AND order_number = ANY($2)`, spaceID.UUID(), numbers)
}

// MerchantOrdersBetween is a merchant's non-ignored orders in a window, with
// items, across all the household's logins — the bank row does not say whose
// login placed it.
func (s *Store) MerchantOrdersBetween(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, from, to domain.Date) ([]MerchantOrder, error) {
	orders, err := queryAll(ctx, s.db, "store: merchant orders between", scanMerchantOrder,
		`SELECT `+merchantOrderColumns+` FROM merchant_orders
		 WHERE space_id = $1 AND merchant = $4 AND ordered_on BETWEEN $2 AND $3 AND ignored_at IS NULL
		 ORDER BY ordered_on`,
		spaceID.UUID(), from.Time(), to.Time(), string(merchant))
	if err != nil {
		return nil, err
	}
	return orders, s.attachMerchantItems(ctx, orders)
}

func (s *Store) SetMerchantOrderIgnored(ctx context.Context, spaceID SpaceID, id uuid.UUID, ignored bool) error {
	return s.execOne(ctx, "store: set merchant order ignored",
		`UPDATE merchant_orders
		    SET ignored_at = CASE WHEN $3 THEN COALESCE(ignored_at, now()) END, updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, ignored)
}

// MerchantMatchCandidate is a bank row offered for a hand match, with the
// orders it already explains: MatchedOrderID is the first,
// MatchedOrderNumber names them all.
type MerchantMatchCandidate struct {
	Transaction        Transaction
	AccountName        string
	MatchedOrderID     uuid.UUID
	MatchedOrderNumber string
}

// MerchantMatchCandidates is the bank rows a person might match to an order by
// hand, unranked (service.Merchants.RowCandidates ranks them): live rows in the
// weeks after the order, excluding rows already matched to it, the limit
// nearest in date. A search lifts the date and wording bounds rather than
// filtering inside them — someone typing already knows the row is not in the
// suggestion.
func (s *Store) MerchantMatchCandidates(ctx context.Context, spaceID SpaceID, order MerchantOrder, search string, limit int) ([]MerchantMatchCandidate, error) {
	args := &argList{}
	// Liveness only, not the wording clause of merchantMatchable: a hand pick
	// exists for rows the bank worded as "AMZ*Mktp" or the seller's name.
	where := ` WHERE space_id = ` + args.add(spaceID.UUID()) + ` AND ` + MoneyMoved + `
		   AND ` + NotInIgnoredAccountOn("transactions") + `
		   AND NOT EXISTS (SELECT 1 FROM merchant_matches m
		                    WHERE m.transaction_id = transactions.id AND m.order_id = ` + args.add(order.ID) + `)`
	if search = strings.TrimSpace(search); search != "" {
		like := args.add("%" + escapeLike(search) + "%")
		where += ` AND (payee ILIKE ` + like + ` OR statement_name ILIKE ` + like + `)`
	} else {
		where += ` AND date BETWEEN ` + args.add(order.OrderedOn.AddDays(-3).Time()) +
			` AND ` + args.add(order.OrderedOn.AddDays(60).Time()) +
			` AND ` + merchantWording(order.Merchant)
	}
	on := args.add(order.OrderedOn.Time())
	sql := `SELECT ` + transactionColumns + `,
		       (SELECT a.name FROM accounts a WHERE a.id = transactions.account_id),
		       (SELECT (array_agg(o.id ORDER BY m.created_at))[1]
		          FROM merchant_matches m JOIN merchant_orders o ON o.id = m.order_id
		         WHERE m.transaction_id = transactions.id),
		       (SELECT string_agg(o.order_number, ', ' ORDER BY m.created_at)
		          FROM merchant_matches m JOIN merchant_orders o ON o.id = m.order_id
		         WHERE m.transaction_id = transactions.id)
		  FROM transactions` + where +
		` ORDER BY abs(date - ` + on + `::date), transactions.id`
	if limit > 0 {
		sql += ` LIMIT ` + args.add(limit)
	}
	return queryAll(ctx, s.db, "store: merchant match candidates", func(row scanner) (MerchantMatchCandidate, error) {
		var (
			one     MerchantMatchCandidate
			account *string
			orderID *uuid.UUID
			number  *string
		)
		txn, err := scanTransaction(trailing{row, []any{&account, &orderID, &number}})
		if err != nil {
			return one, err
		}
		one.Transaction = txn
		one.AccountName = Deref(account)
		one.MatchedOrderNumber = Deref(number)
		one.MatchedOrderID = Deref(orderID)
		return one, nil
	}, sql, args.values...)
}

// MerchantOrderCandidates is the orders a person might match a bank row to by
// hand, with their items and unranked (service.Merchants.OrderCandidates ranks
// them): two months before the row to a week after, excluding orders the row
// already pays for, the limit nearest in date. A search reaches past the
// window, as in MerchantMatchCandidates. Empty merchants means every merchant.
func (s *Store) MerchantOrderCandidates(ctx context.Context, spaceID SpaceID, merchants []domain.MerchantID, txn Transaction, search string, limit int) ([]MerchantOrder, error) {
	args := &argList{}
	where := ` WHERE space_id = ` + args.add(spaceID.UUID())
	if len(merchants) > 0 {
		ids := make([]string, 0, len(merchants))
		for _, m := range merchants {
			ids = append(ids, string(m))
		}
		where += ` AND merchant = ANY(` + args.add(ids) + `)`
	}
	where += ` AND NOT EXISTS (SELECT 1 FROM merchant_matches m
		                    WHERE m.order_id = merchant_orders.id AND m.transaction_id = ` + args.add(txn.ID) + `)`
	if search = strings.TrimSpace(search); search != "" {
		like := args.add("%" + escapeLike(search) + "%")
		where += ` AND (order_number ILIKE ` + like + ` OR EXISTS (SELECT 1 FROM merchant_order_items i
		             WHERE i.order_id = merchant_orders.id AND i.title ILIKE ` + like + `))`
	} else {
		where += ` AND ordered_on BETWEEN ` + args.add(txn.Date.AddDays(-60).Time()) +
			` AND ` + args.add(txn.Date.AddDays(7).Time())
	}
	on := args.add(txn.Date.Time())
	sql := `SELECT ` + merchantOrderColumns + ` FROM merchant_orders` + where +
		` ORDER BY abs(ordered_on - ` + on + `::date), ordered_on DESC, id`
	if limit > 0 {
		sql += ` LIMIT ` + args.add(limit)
	}
	orders, err := queryAll(ctx, s.db, "store: merchant order candidates", scanMerchantOrder, sql, args.values...)
	if err != nil {
		return nil, err
	}
	return orders, s.attachMerchantItems(ctx, orders)
}

// trailing scans a row whose leading columns a shared scanner knows and whose
// last few belong to the caller.
type trailing struct {
	scanner
	extra []any
}

func (t trailing) Scan(dest ...any) error { return t.scanner.Scan(append(dest, t.extra...)...) }

// --- Charges -------------------------------------------------------------------

func (s *Store) UpsertMerchantCharge(ctx context.Context, spaceID SpaceID, one *MerchantCharge) (bool, error) {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	tag, err := s.db.Exec(ctx,
		`INSERT INTO merchant_charges
		     (id, space_id, merchant_account_id, order_number, charged_on, amount, instrument, merchant)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT DO NOTHING`,
		one.ID, spaceID.UUID(), one.MerchantAccountID, one.OrderNumber, one.ChargedOn.Time(),
		pgconv.Money(one.Amount), one.Instrument, string(one.Merchant))
	if err != nil {
		return false, wrap("store: upsert merchant charge", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) MerchantChargesBetween(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, from, to domain.Date) ([]MerchantCharge, error) {
	return queryAll(ctx, s.db, "store: merchant charges between", scanMerchantCharge,
		`SELECT id, merchant, merchant_account_id, order_number, charged_on, amount, instrument
		   FROM merchant_charges WHERE space_id = $1 AND merchant = $4 AND charged_on BETWEEN $2 AND $3
		  ORDER BY charged_on`,
		spaceID.UUID(), from.Time(), to.Time(), string(merchant))
}

func scanMerchantCharge(row scanner) (MerchantCharge, error) {
	var (
		one    MerchantCharge
		on     time.Time
		amount pgtype.Numeric
	)
	err := row.Scan(&one.ID, &one.Merchant, &one.MerchantAccountID, &one.OrderNumber, &on, &amount, &one.Instrument)
	if err != nil {
		return one, err
	}
	one.ChargedOn = dateOf(on)
	one.Amount, err = pgconv.ReadMoney(amount, "merchant_charges.amount")
	return one, err
}

// --- Refunds -------------------------------------------------------------------

const merchantRefundColumns = `r.id, r.merchant, r.merchant_account_id, r.order_number, r.sku, r.title,
	r.quantity, r.refunded_on, r.amount, r.instrument, r.destination, r.status, r.source,
	(SELECT m.transaction_id FROM merchant_matches m WHERE m.refund_id = r.id LIMIT 1)`

func scanMerchantRefund(row scanner) (MerchantRefund, error) {
	var (
		one    MerchantRefund
		on     time.Time
		amount pgtype.Numeric
		txn    *uuid.UUID
	)
	err := row.Scan(&one.ID, &one.Merchant, &one.MerchantAccountID, &one.OrderNumber, &one.SKU,
		&one.Title, &one.Quantity, &on, &amount, &one.Instrument, &one.Destination, &one.Status,
		&one.Source, &txn)
	if err != nil {
		return one, err
	}
	one.RefundedOn = dateOf(on)
	one.TransactionID = Deref(txn)
	one.Amount, err = pgconv.ReadMoney(amount, "merchant_refunds.amount")
	return one, err
}

// UpsertMerchantRefund writes a refund and reports whether it was new. It is
// keyed on the order, line, day and amount, since the returns page is re-read
// daily and its status changes meanwhile.
func (s *Store) UpsertMerchantRefund(ctx context.Context, spaceID SpaceID, one *MerchantRefund) (bool, error) {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	if one.Quantity <= 0 {
		one.Quantity = 1
	}
	if one.Destination == "" {
		one.Destination = MerchantRefundToCard
	}
	var inserted bool
	err := s.db.QueryRow(ctx,
		`INSERT INTO merchant_refunds
		     (id, space_id, merchant, merchant_account_id, order_number, sku, title, quantity,
		      refunded_on, amount, instrument, destination, status, source)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (space_id, merchant_account_id, order_number, sku, refunded_on, amount)
		 DO UPDATE SET
		     title = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE merchant_refunds.title END,
		     quantity = EXCLUDED.quantity,
		     instrument = CASE WHEN EXCLUDED.instrument <> '' THEN EXCLUDED.instrument
		                       ELSE merchant_refunds.instrument END,
		     destination = EXCLUDED.destination,
		     status = CASE WHEN EXCLUDED.status <> '' THEN EXCLUDED.status
		                   ELSE merchant_refunds.status END,
		     updated_at = now()
		 RETURNING id, (xmax = 0)`,
		one.ID, spaceID.UUID(), string(one.Merchant), one.MerchantAccountID, one.OrderNumber,
		one.SKU, one.Title, one.Quantity, one.RefundedOn.Time(), pgconv.Money(one.Amount),
		one.Instrument, one.Destination, one.Status, one.Source).Scan(&one.ID, &inserted)
	if err != nil {
		return false, wrap("store: upsert merchant refund", err)
	}
	return inserted, nil
}

// MerchantRefundsBetween is a merchant's refunds in a window, gift card ones
// included, for the matcher.
func (s *Store) MerchantRefundsBetween(
	ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, from, to domain.Date,
) ([]MerchantRefund, error) {
	return queryAll(ctx, s.db, "store: merchant refunds between", scanMerchantRefund,
		`SELECT `+merchantRefundColumns+` FROM merchant_refunds r
		  WHERE r.space_id = $1 AND r.merchant = $4 AND r.refunded_on BETWEEN $2 AND $3
		  ORDER BY r.refunded_on, r.id`,
		spaceID.UUID(), from.Time(), to.Time(), string(merchant))
}

func (s *Store) GetMerchantRefund(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MerchantRefund, error) {
	one, err := scanMerchantRefund(s.db.QueryRow(ctx,
		`SELECT `+merchantRefundColumns+` FROM merchant_refunds r WHERE r.space_id = $1 AND r.id = $2`,
		spaceID.UUID(), id))
	if err != nil {
		return MerchantRefund{}, wrap("store: get merchant refund", err)
	}
	return one, nil
}

// AttachMerchantRefunds fills in what each order gave back, joined by order
// number: a refund can arrive after its order is outside the pull's window.
func (s *Store) AttachMerchantRefunds(ctx context.Context, orders []MerchantOrder) error {
	if len(orders) == 0 {
		return nil
	}
	numbers := make([]string, 0, len(orders))
	index := make(map[string]int, len(orders))
	for i, one := range orders {
		numbers = append(numbers, one.OrderNumber)
		index[one.MerchantAccountID.String()+"/"+one.OrderNumber] = i
	}
	refunds, err := queryAll(ctx, s.db, "store: merchant order refunds", scanMerchantRefund,
		`SELECT `+merchantRefundColumns+` FROM merchant_refunds r
		  WHERE r.space_id = $1 AND r.order_number = ANY($2)
		  ORDER BY r.refunded_on, r.id`, orders[0].SpaceID.UUID(), numbers)
	if err != nil {
		return err
	}
	for _, refund := range refunds {
		i, known := index[refund.MerchantAccountID.String()+"/"+refund.OrderNumber]
		if !known {
			continue
		}
		orders[i].Refunds = append(orders[i].Refunds, refund)
	}
	return nil
}

// --- Matches -------------------------------------------------------------------

// SetMerchantMatch records the order behind a bank row, replacing any earlier
// match for that row.
func (s *Store) SetMerchantMatch(ctx context.Context, spaceID SpaceID, one *MerchantMatch) error {
	return s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.db.Exec(ctx,
			`DELETE FROM merchant_matches WHERE space_id = $1 AND transaction_id = $2 AND order_id <> $3`,
			spaceID.UUID(), one.TransactionID, one.OrderID); err != nil {
			return wrap("store: set merchant match", err)
		}
		return tx.AddMerchantMatch(ctx, spaceID, one)
	})
}

// AddMerchantMatch adds an order beside what the row already pays for.
func (s *Store) AddMerchantMatch(ctx context.Context, spaceID SpaceID, one *MerchantMatch) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.addMerchantMatch(ctx, spaceID, one); err != nil {
			return err
		}
		_, _, err := tx.ReconcileReceipts(ctx, spaceID, []uuid.UUID{one.TransactionID})
		return err
	})
}

func (s *Store) addMerchantMatch(ctx context.Context, spaceID SpaceID, one *MerchantMatch) error {
	err := s.db.QueryRow(ctx,
		`INSERT INTO merchant_matches (transaction_id, space_id, order_id, amount, basis, confidence,
		      refund_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (transaction_id, order_id) DO UPDATE SET
		     amount = EXCLUDED.amount, basis = EXCLUDED.basis, confidence = EXCLUDED.confidence,
		     refund_id = EXCLUDED.refund_id
		 RETURNING created_at`,
		one.TransactionID, spaceID.UUID(), one.OrderID, pgconv.Money(one.Amount), one.Basis,
		one.Confidence, one.RefundID).Scan(&one.CreatedAt)
	return wrap("store: add merchant match", err)
}

func (s *Store) SetMerchantMatchAmount(ctx context.Context, spaceID SpaceID, transactionID, orderID uuid.UUID, amount domain.Money) error {
	_, err := s.db.Exec(ctx,
		`UPDATE merchant_matches SET amount = $4 WHERE space_id = $1 AND transaction_id = $2 AND order_id = $3`,
		spaceID.UUID(), transactionID, orderID, pgconv.Money(amount))
	return wrap("store: set merchant match amount", err)
}

func (s *Store) DeleteMerchantMatchForOrder(ctx context.Context, spaceID SpaceID, transactionID, orderID uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.execOne(ctx, "store: delete merchant match for order",
			`DELETE FROM merchant_matches WHERE space_id = $1 AND transaction_id = $2 AND order_id = $3`,
			spaceID.UUID(), transactionID, orderID); err != nil {
			return err
		}
		_, _, err := tx.ReconcileReceipts(ctx, spaceID, []uuid.UUID{transactionID})
		return err
	})
}

// ListMerchantMatches is every order behind one bank row, oldest match first.
func (s *Store) ListMerchantMatches(ctx context.Context, spaceID SpaceID, transactionID uuid.UUID) ([]MerchantMatch, error) {
	return queryAll(ctx, s.db, "store: list merchant matches", scanMerchantMatch,
		`SELECT transaction_id, order_id, amount, basis, confidence, created_at, refund_id
		   FROM merchant_matches WHERE space_id = $1 AND transaction_id = $2
		  ORDER BY created_at, order_id`, spaceID.UUID(), transactionID)
}

// GetMerchantMatch is the oldest match for a bank row, or ErrNotFound.
func (s *Store) GetMerchantMatch(ctx context.Context, spaceID SpaceID, transactionID uuid.UUID) (MerchantMatch, error) {
	matches, err := s.ListMerchantMatches(ctx, spaceID, transactionID)
	if err != nil {
		return MerchantMatch{}, err
	}
	if len(matches) == 0 {
		return MerchantMatch{}, wrap("store: get merchant match", ErrNotFound)
	}
	return matches[0], nil
}

func scanMerchantMatch(row scanner) (MerchantMatch, error) {
	var (
		m      MerchantMatch
		amount pgtype.Numeric
	)
	if err := row.Scan(&m.TransactionID, &m.OrderID, &amount, &m.Basis, &m.Confidence, &m.CreatedAt,
		&m.RefundID); err != nil {
		return m, err
	}
	var err error
	m.Amount, err = pgconv.ReadMoney(amount, "merchant_matches.amount")
	return m, err
}

// merchantWording is the SQL form of domain.IsAmazonWording and
// domain.IsCostcoWording; the two must agree.
func merchantWording(merchant domain.MerchantID) string {
	switch merchant {
	case domain.MerchantCostco:
		return `(statement_name ILIKE '%costco%' OR payee ILIKE '%costco%')`
	default:
		return `(statement_name ILIKE '%amazon%' OR statement_name ILIKE '%amzn%'
	OR payee ILIKE '%amazon%' OR payee ILIKE '%amzn%')`
	}
}

// merchantMatchable is which bank rows may be matched to one merchant's order,
// shared by the bulk-match list, its waiting count and the per-sync matcher.
// Pending charges count: a pending row settles in place, so the match survives.
func merchantMatchable(merchant domain.MerchantID) string {
	return MoneyMoved + ` AND ` + NotInIgnoredAccountOn("transactions") + ` AND ` + merchantWording(merchant)
}

// matchedToMerchant is the clause for a row one of the merchant's orders is
// matched to; $2 is the merchant.
const matchedToMerchant = `EXISTS (SELECT 1 FROM merchant_matches m JOIN merchant_orders o ON o.id = m.order_id
	WHERE m.transaction_id = transactions.id AND o.merchant = $2)`

func (s *Store) ListUnmatchedMerchantTransactions(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, limit int) ([]Transaction, error) {
	sql := `SELECT ` + transactionColumns + ` FROM transactions
		 WHERE space_id = $1 AND ` + merchantMatchable(merchant) + ` AND NOT ` + matchedToMerchant + `
		 ORDER BY date DESC, id`
	if limit > 0 {
		sql += ` LIMIT ` + strconv.Itoa(limit)
	}
	return queryAll(ctx, s.db, "store: unmatched merchant transactions", scanTransaction, sql, spaceID.UUID(), string(merchant))
}

func (s *Store) ListMatchedMerchantTransactionIDs(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID) ([]uuid.UUID, error) {
	return queryAll(ctx, s.db, "store: matched merchant transactions", scanValue[uuid.UUID],
		`SELECT t.id FROM transactions t
		  WHERE t.space_id = $1 AND `+MoneyMovedOn("t")+`
		    AND EXISTS (SELECT 1 FROM merchant_matches m JOIN merchant_orders o ON o.id = m.order_id
		                 WHERE m.transaction_id = t.id AND o.merchant = $2)
		  ORDER BY t.date DESC, t.id`, spaceID.UUID(), string(merchant))
}

type MerchantSummary struct {
	Accounts             int
	Orders               int
	Items                int
	Charges              int
	MerchantTransactions int
	MatchedTransactions  int
	NewestOrder          domain.Date
	OldestOrder          domain.Date
}

func (s *Store) MerchantSummary(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID) (MerchantSummary, error) {
	var (
		out            MerchantSummary
		newest, oldest *time.Time
	)
	err := s.db.QueryRow(ctx,
		`SELECT
		    (SELECT count(*) FROM merchant_accounts WHERE space_id = $1 AND merchant = $2),
		    (SELECT count(*) FROM merchant_orders WHERE space_id = $1 AND merchant = $2),
		    (SELECT count(*) FROM merchant_order_items i JOIN merchant_orders o ON o.id = i.order_id
		      WHERE i.space_id = $1 AND o.merchant = $2),
		    (SELECT count(*) FROM merchant_charges WHERE space_id = $1 AND merchant = $2),
		    (SELECT count(*) FROM transactions
		      WHERE space_id = $1 AND `+merchantMatchable(merchant)+`),
		    (SELECT count(DISTINCT m.transaction_id) FROM merchant_matches m
		      JOIN merchant_orders o ON o.id = m.order_id WHERE m.space_id = $1 AND o.merchant = $2),
		    (SELECT max(ordered_on) FROM merchant_orders WHERE space_id = $1 AND merchant = $2),
		    (SELECT min(ordered_on) FROM merchant_orders WHERE space_id = $1 AND merchant = $2)`,
		spaceID.UUID(), string(merchant)).
		Scan(&out.Accounts, &out.Orders, &out.Items, &out.Charges, &out.MerchantTransactions,
			&out.MatchedTransactions, &newest, &oldest)
	if err != nil {
		return MerchantSummary{}, wrap("store: merchant summary", err)
	}
	out.NewestOrder, out.OldestOrder = pgconv.ReadNullDate(newest), pgconv.ReadNullDate(oldest)
	return out, nil
}

func (s *Store) CountMerchantOrdersByAccount(ctx context.Context, spaceID SpaceID) (map[uuid.UUID]int, error) {
	counts, err := queryAll(ctx, s.db, "store: merchant orders by account", scanPair[uuid.UUID, int],
		`SELECT merchant_account_id, count(*) FROM merchant_orders WHERE space_id = $1 GROUP BY 1`,
		spaceID.UUID())
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int, len(counts))
	for _, one := range counts {
		out[one.first] = one.second
	}
	return out, nil
}
