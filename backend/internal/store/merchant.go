package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
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
	// RefundTotal is what the invoice says was refunded, as of the last read
	// for it; HasRefundTotal false means no invoice has been read for it.
	RefundTotal    domain.Money
	HasRefundTotal bool
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

const merchantAccountColumns = `id, merchant, label, email, (session_state IS NOT NULL), signed_in_at,
	(credential_sealed IS NOT NULL), (credential_sealed IS NOT NULL AND credential_has_totp), second_factor,
	sign_in_paused_at, sign_in_paused_for, sync_enabled, sync_days, last_synced_at, last_sync_status, last_sync_error,
	(last_sync_screenshot_document_id IS NOT NULL), needs_sign_in, gift_card_account_id, gift_card_balance, gift_card_balance_at,
	invoice_backfill_at, invoice_backfill_filed, invoice_backfill_left, invoice_backfill_stopped, created_at, updated_at`

func scanMerchantAccount(row scanner, spaceID SpaceID, currency string) (MerchantAccount, error) {
	var one MerchantAccount
	var giftCard sql.Null[uuid.UUID]
	var balance, signedIn, paused, synced, balanceAt, backfill sql.Null[int64]
	var created, updated int64
	err := row.Scan(&one.ID, &one.Merchant, &one.Label, &one.Email, &one.HasSession, &signedIn,
		&one.HasPassword, &one.HasTOTP, &one.SecondFactor, &paused, &one.SignInPausedFor, &one.SyncEnabled, &one.SyncDays, &synced, &one.LastSyncStatus, &one.LastSyncError,
		&one.HasFailureScreenshot, &one.NeedsSignIn, &giftCard, &balance, &balanceAt,
		&backfill, &one.InvoiceBackfillFiled, &one.InvoiceBackfillLeft, &one.InvoiceBackfillStopped,
		&created, &updated)
	if err != nil {
		return one, err
	}
	one.SpaceID = spaceID
	one.GiftCardAccountID = giftCard.V
	one.SignedInAt, one.SignInPausedAt, one.LastSyncedAt = nullableTime(signedIn), nullableTime(paused), nullableTime(synced)
	one.GiftCardBalanceAt, one.InvoiceBackfillAt = nullableTime(balanceAt), nullableTime(backfill)
	one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	one.HasGiftCardBalance = balance.Valid
	if balance.Valid {
		value, decodeErr := fromMinor(balance.V, currency)
		err = decodeErr
		one.GiftCardBalance = domain.FromDecimal(value)
	}
	return one, err
}

func (s *Store) SetMerchantGiftCard(ctx context.Context, spaceID SpaceID, id, accountID uuid.UUID, balance domain.Money, hasBalance bool, at time.Time) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	currency, err := h.primaryCurrency()
	if err != nil {
		return err
	}
	var figure any
	if hasBalance {
		figure, err = toMinor(balance.Decimal(), currency)
		if err != nil {
			return err
		}
	}
	return sqliteExecOne(ctx, db.Write(), "store: set merchant gift card",
		`UPDATE merchant_accounts SET gift_card_account_id = ?, gift_card_balance = ?, gift_card_balance_at = ?, updated_at = ? WHERE id = ?`,
		accountID, figure, unixMillis(at), db.Now(), id)
}

// IsMerchantGiftCardAccount says whether an account holds a merchant login's
// gift card balance, which changes what its rows can be matched to.
func (s *Store) IsMerchantGiftCardAccount(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) (bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return false, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return false, err
	}
	var found bool
	err = db.Read().QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM merchant_accounts WHERE gift_card_account_id = ?)`, accountID).Scan(&found)
	return found, wrap("store: is merchant gift card account", err)
}

// SaveMerchantSession seals a browser session onto the account; see
// saveSession. A fresh sign-in also turns the daily pull on.
func (s *Store) SaveMerchantSession(ctx context.Context, spaceID SpaceID, id uuid.UUID, email, state string, fresh bool) error {
	return s.saveSession(ctx, merchantConnectorState, spaceID, id, state, fresh,
		`email = CASE WHEN ? <> '' THEN ? ELSE email END,
		 sync_enabled = CASE WHEN ? THEN true ELSE sync_enabled END,`,
		[]any{email, email, fresh})
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: update merchant sync",
		`UPDATE merchant_accounts SET sync_enabled = ?, sync_days = ?, updated_at = ? WHERE id = ?`, enabled, days, db.Now(), id)
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
	return listDue(ctx, s, merchantConnectorState, since)
}

func (s *Store) CreateMerchantAccount(ctx context.Context, spaceID SpaceID, one *MerchantAccount) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO merchant_accounts (id, merchant, label, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		one.ID, string(one.Merchant), one.Label, now, now)
	if err == nil {
		one.CreatedAt, one.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create merchant account", err)
}

func (s *Store) GetMerchantAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MerchantAccount, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return MerchantAccount{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return MerchantAccount{}, err
	}
	currency, err := h.primaryCurrency()
	if err != nil {
		return MerchantAccount{}, err
	}
	one, err := scanMerchantAccount(db.Read().QueryRowContext(ctx,
		`SELECT `+merchantAccountColumns+` FROM merchant_accounts WHERE id = ?`, id), spaceID, currency)
	return one, wrap("store: get merchant account", err)
}

func (s *Store) ListMerchantAccounts(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID) ([]MerchantAccount, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	currency, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list merchant accounts",
		func(row scanner) (MerchantAccount, error) { return scanMerchantAccount(row, spaceID, currency) },
		`SELECT `+merchantAccountColumns+` FROM merchant_accounts WHERE merchant = ? ORDER BY label`, string(merchant))
}

func (s *Store) SetMerchantSecondFactor(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, factor domain.SecondFactor,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set merchant second factor",
		`UPDATE merchant_accounts SET second_factor = ?, updated_at = ? WHERE id = ?`, string(factor), db.Now(), id)
}

func (s *Store) UpdateMerchantAccount(ctx context.Context, spaceID SpaceID, one *MerchantAccount) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update merchant account",
		`UPDATE merchant_accounts SET label = ?, updated_at = ? WHERE id = ?`, one.Label, now, one.ID)
	if err == nil {
		one.UpdatedAt = fromUnixMillis(now)
	}
	return wrap("store: update merchant account", err)
}

// DeleteMerchantAccount removes the account and, by cascade, everything
// imported from it. The orders' invoices are released first, and the receipts
// they were filed as with them: document links have no foreign key, so a stale
// link would keep an invoice out of the purge.
func (s *Store) DeleteMerchantAccount(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		matched, err := sqliteQueryAll(ctx, db.Read(), "store: list rows matched to merchant account", scanValue[uuid.UUID], `
			SELECT DISTINCT m.transaction_id FROM merchant_matches m
			JOIN merchant_orders o ON o.id = m.order_id WHERE o.merchant_account_id = ?`, id)
		if err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx, `
			DELETE FROM document_links WHERE kind = ? AND target_id IN (
			    SELECT id FROM merchant_orders WHERE merchant_account_id = ?)`,
			string(DocumentLinkMerchantOrder), id); err != nil {
			return wrap("store: release order invoices", err)
		}
		if err := sqliteExecOne(ctx, db.Write(), "store: delete merchant account",
			`DELETE FROM merchant_accounts WHERE id = ?`, id); err != nil {
			return err
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, matched)
		return err
	})
}

// --- Orders --------------------------------------------------------------------

const merchantOrderColumns = `id, merchant, merchant_account_id, order_number, ordered_on, total,
	currency, status, details_url, source, created_at, updated_at, gift_card_amount, tax, shipping,
	ignored_at, kind, location, refund_total`

func scanMerchantOrder(row scanner, spaceID SpaceID) (MerchantOrder, error) {
	var one MerchantOrder
	var on string
	var total, created, updated int64
	var gift, tax, shipping, refunded, ignored sql.Null[int64]
	if err := row.Scan(&one.ID, &one.Merchant, &one.MerchantAccountID, &one.OrderNumber, &on, &total,
		&one.Currency, &one.Status, &one.DetailsURL, &one.Source, &created, &updated,
		&gift, &tax, &shipping, &ignored, &one.Kind, &one.Location, &refunded); err != nil {
		return one, err
	}
	one.SpaceID = spaceID
	var err error
	one.OrderedOn, err = domain.ParseDate(on)
	if err != nil {
		return one, err
	}
	one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	one.IgnoredAt = nullableTime(ignored)
	minorValue530, moneyErr := fromMinor(total, one.Currency)
	if moneyErr != nil {
		return one, moneyErr
	}
	one.Total = domain.FromDecimal(minorValue530)
	minorValue531, moneyErr := fromMinor(gift.V, one.Currency)
	if moneyErr != nil {
		return one, moneyErr
	}
	one.GiftCard, one.HasGiftCard = domain.FromDecimal(minorValue531), gift.Valid
	minorValue532, moneyErr := fromMinor(tax.V, one.Currency)
	if moneyErr != nil {
		return one, moneyErr
	}
	one.Tax, one.HasTax = domain.FromDecimal(minorValue532), tax.Valid
	minorValue533, moneyErr := fromMinor(shipping.V, one.Currency)
	if moneyErr != nil {
		return one, moneyErr
	}
	one.Shipping, one.HasShipping = domain.FromDecimal(minorValue533), shipping.Valid
	minorValue534, moneyErr := fromMinor(refunded.V, one.Currency)
	if moneyErr != nil {
		return one, moneyErr
	}
	one.RefundTotal, one.HasRefundTotal = domain.FromDecimal(minorValue534), refunded.Valid
	return one, nil
}

// SetMerchantOrderRefundTotal keeps what an order's invoice said was refunded
// on the day it was read. ErrNotFound for an order not on file.
func (s *Store) SetMerchantOrderRefundTotal(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, orderNumber string, refunded domain.Money, on domain.Date) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	var code string
	if err := db.Read().QueryRowContext(ctx, "SELECT currency FROM merchant_orders WHERE merchant_account_id = ? AND order_number = ?", accountID, orderNumber).Scan(&code); err != nil {
		return wrap("store: set merchant order refund total", err)
	}
	minor, err := toMinor(refunded.Decimal(), code)
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set merchant order refund total", "UPDATE merchant_orders SET refund_total = ?, refund_checked_on = ?, updated_at = ? WHERE merchant_account_id = ? AND order_number = ?", minor, on.String(), db.Now(), accountID, orderNumber)
}

// MerchantOrdersDueRefundCheck names the orders whose invoice a pull should
// read again for what was refunded: an order of the last year never read for
// it, and one of the last refundRecentDays not read for it in
// refundRecheckDays. A return lands weeks after the order, and the first read
// of an invoice is at the order.
func (s *Store) MerchantOrdersDueRefundCheck(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, today domain.Date) ([]string, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: MerchantOrdersDueRefundCheck", scanValue[string], `SELECT order_number FROM merchant_orders WHERE merchant_account_id = ? AND ignored_at IS NULL AND lower(status) NOT LIKE '%cancel%' AND ordered_on >= ? AND (refund_checked_on IS NULL OR (ordered_on >= ? AND refund_checked_on <= ?)) ORDER BY ordered_on DESC, order_number DESC`, accountID, today.AddDays(-refundCheckYearDays).String(), today.AddDays(-refundRecentDays).String(), today.AddDays(-refundRecheckDays).String())
	if err != nil {
		return nil, err
	}
	return found, nil
}

const (
	refundCheckYearDays = 365
	refundRecentDays    = 100
	refundRecheckDays   = 14
)

// MerchantOrdersRefundedBetween is the merchant's orders placed between from
// and to whose invoice says something was refunded.
func (s *Store) MerchantOrdersRefundedBetween(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, from, to domain.Date) ([]MerchantOrder, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: MerchantOrdersRefundedBetween", func(row scanner) (MerchantOrder, error) { return scanMerchantOrder(row, spaceID) }, `SELECT `+merchantOrderColumns+` FROM merchant_orders WHERE merchant = ? AND ordered_on BETWEEN ? AND ? AND refund_total > 0 AND ignored_at IS NULL ORDER BY ordered_on, order_number`, string(merchant), from.String(), to.String())
	if err != nil {
		return nil, err
	}
	return found, nil
}

// UpsertMerchantOrder writes an order by number within its account and
// reports whether it was new. Items are replaced only when the input carries
// any, so a charges-only file cannot strip an order.
func (s *Store) UpsertMerchantOrder(ctx context.Context, spaceID SpaceID, one *MerchantOrder) (bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return false, err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	if one.Kind == "" {
		one.Kind = domain.PurchaseOnline
	}
	total, err := toMinor(one.Total.Decimal(), one.Currency)
	if err != nil {
		return false, err
	}
	optional := func(m domain.Money, valid bool) (any, error) {
		if !valid {
			return nil, nil
		}
		return toMinor(m.Decimal(), one.Currency)
	}
	gift, err := optional(one.GiftCard, one.HasGiftCard)
	if err != nil {
		return false, err
	}
	tax, err := optional(one.Tax, one.HasTax)
	if err != nil {
		return false, err
	}
	shipping, err := optional(one.Shipping, one.HasShipping)
	if err != nil {
		return false, err
	}
	var inserted bool
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		var exists bool
		if err := db.Read().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM merchant_orders WHERE merchant_account_id = ? AND order_number = ?)", one.MerchantAccountID, one.OrderNumber).Scan(&exists); err != nil {
			return wrap("store: find merchant order", err)
		}
		inserted = !exists
		var created, updated int64
		err = db.Write().QueryRowContext(ctx,
			`INSERT INTO merchant_orders (id, merchant_account_id, order_number, ordered_on, total, currency, status, details_url, source, gift_card_amount, tax, shipping, merchant, kind, location, created_at, updated_at)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT (merchant_account_id, order_number) DO UPDATE SET
 ordered_on = excluded.ordered_on,
 total = CASE WHEN excluded.total <> 0 THEN excluded.total ELSE merchant_orders.total END,
 currency = excluded.currency,
 status = CASE WHEN excluded.status <> '' THEN excluded.status ELSE merchant_orders.status END,
 details_url = CASE WHEN excluded.details_url <> '' THEN excluded.details_url ELSE merchant_orders.details_url END,
 source = excluded.source,
 gift_card_amount = COALESCE(excluded.gift_card_amount, merchant_orders.gift_card_amount),
 tax = COALESCE(excluded.tax, merchant_orders.tax), shipping = COALESCE(excluded.shipping, merchant_orders.shipping),
 kind = excluded.kind, location = CASE WHEN excluded.location <> '' THEN excluded.location ELSE merchant_orders.location END,
 updated_at = excluded.updated_at RETURNING id, created_at, updated_at`,
			one.ID, one.MerchantAccountID, one.OrderNumber, one.OrderedOn.String(), total, one.Currency, one.Status, one.DetailsURL, one.Source, gift, tax, shipping, string(one.Merchant), one.Kind, one.Location, db.Now(), db.Now()).Scan(&one.ID, &created, &updated)
		if err != nil {
			return wrap("store: upsert merchant order", err)
		}
		one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
		if len(one.Items) == 0 {
			return nil
		}
		priced := false
		for _, item := range one.Items {
			if item.HasUnitPrice || item.HasTotalOwed {
				priced = true
			}
		}
		if !priced {
			var kept int
			if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM merchant_order_items WHERE order_id = ? AND (unit_price IS NOT NULL OR total_owed IS NOT NULL)", one.ID).Scan(&kept); err != nil {
				return wrap("store: upsert merchant order items", err)
			}
			if kept > 0 {
				return nil
			}
		}
		if _, err := db.Write().ExecContext(ctx, "DELETE FROM merchant_order_items WHERE order_id = ?", one.ID); err != nil {
			return wrap("store: upsert merchant order items", err)
		}
		for i := range one.Items {
			item := &one.Items[i]
			if item.ID == uuid.Nil {
				item.ID = uuid.New()
			}
			item.OrderID, item.Position = one.ID, i
			unit, err := optional(item.UnitPrice, item.HasUnitPrice)
			if err != nil {
				return err
			}
			owed, err := optional(item.TotalOwed, item.HasTotalOwed)
			if err != nil {
				return err
			}
			_, err = db.Write().ExecContext(ctx, `INSERT INTO merchant_order_items (id, order_id, position, sku, title, quantity, unit_price, total_owed, shipped_on, condition, url) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				item.ID, one.ID, i, item.SKU, item.Title, item.Quantity, unit, owed, billDateArg(item.ShippedOn), item.Condition, item.URL)
			if err != nil {
				return wrap("store: upsert merchant order items", err)
			}
		}
		return nil
	})
	return inserted, err
}

func (s *Store) ExistingMerchantOrderNumbers(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, numbers []string) (map[string]bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	in, args := inArgs(numbers)
	args = append([]any{accountID}, args...)
	found, err := sqliteQueryAll(ctx, db.Read(), "store: existing merchant orders", scanValue[string], "SELECT order_number FROM merchant_orders WHERE merchant_account_id = ? AND order_number IN "+in, args...)
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(found))
	for _, n := range found {
		result[n] = true
	}
	return result, nil
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, 0, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, 0, err
	}
	args := &sqliteArgList{}
	where := " WHERE merchant = " + args.add(string(q.Merchant))
	if q.AccountID != uuid.Nil {
		where += " AND merchant_account_id = " + args.add(q.AccountID)
	}
	if !q.From.IsZero() {
		where += " AND ordered_on >= " + args.add(q.From.String())
	}
	if !q.To.IsZero() {
		where += " AND ordered_on <= " + args.add(q.To.String())
	}
	if q.Unmatched {
		where += " AND NOT EXISTS (SELECT 1 FROM merchant_matches m WHERE m.order_id = merchant_orders.id) AND total - COALESCE(gift_card_amount, 0) > 0 AND lower(status) NOT LIKE 'cancel%' AND ignored_at IS NULL"
	}
	var total int
	if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM merchant_orders"+where, args.values...).Scan(&total); err != nil {
		return nil, 0, wrap("store: count merchant orders", err)
	}
	query := "SELECT " + merchantOrderColumns + " FROM merchant_orders" + where + " ORDER BY ordered_on DESC, order_number DESC"
	if q.Limit > 0 {
		query += " LIMIT " + args.add(q.Limit)
	} else if q.Offset > 0 {
		query += " LIMIT -1"
	}
	if q.Offset > 0 {
		query += " OFFSET " + args.add(q.Offset)
	}
	orders, err := sqliteQueryAll(ctx, db.Read(), "store: list merchant orders", func(row scanner) (MerchantOrder, error) { return scanMerchantOrder(row, spaceID) }, query, args.values...)
	if err != nil {
		return nil, 0, err
	}
	if err := s.attachMerchantItems(ctx, orders); err != nil {
		return nil, 0, err
	}
	if err := h.AttachMerchantMatches(ctx, orders); err != nil {
		return nil, 0, err
	}
	if err := h.AttachMerchantRefunds(ctx, orders); err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (s *Store) GetMerchantOrder(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MerchantOrder, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return MerchantOrder{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return MerchantOrder{}, err
	}
	one, err := scanMerchantOrder(db.Read().QueryRowContext(ctx, "SELECT "+merchantOrderColumns+" FROM merchant_orders WHERE id = ?", id), spaceID)
	if err != nil {
		return MerchantOrder{}, wrap("store: GetMerchantOrder", err)
	}
	orders := []MerchantOrder{one}
	if err := s.attachMerchantItems(ctx, orders); err != nil {
		return MerchantOrder{}, err
	}
	if err := h.AttachMerchantRefunds(ctx, orders); err != nil {
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
	spaceID := orders[0].SpaceID
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(orders))
	index := make(map[uuid.UUID]int, len(orders))
	for i, one := range orders {
		if one.SpaceID != spaceID {
			return errors.New("store: merchant orders span households")
		}
		ids = append(ids, one.ID)
		index[one.ID] = i
	}
	in, args := inArgs(ids)
	items, err := sqliteQueryAll(ctx, db.Read(), "store: merchant order items", func(row scanner) (MerchantOrderItem, error) {
		var item MerchantOrderItem
		var unit, owed sql.Null[int64]
		var shipped sql.Null[string]
		var code string
		err := row.Scan(&item.ID, &item.OrderID, &item.Position, &item.SKU, &item.Title, &item.Quantity, &unit, &owed, &shipped, &item.Condition, &item.URL, &code)
		if err != nil {
			return item, err
		}
		minorValue868, moneyErr := fromMinor(unit.V, code)
		if moneyErr != nil {
			return item, moneyErr
		}
		item.UnitPrice, item.HasUnitPrice = domain.FromDecimal(minorValue868), unit.Valid
		minorValue869, moneyErr := fromMinor(owed.V, code)
		if moneyErr != nil {
			return item, moneyErr
		}
		item.TotalOwed, item.HasTotalOwed = domain.FromDecimal(minorValue869), owed.Valid
		if shipped.Valid {
			item.ShippedOn, err = domain.ParseDate(shipped.V)
		}
		return item, err
	}, "SELECT i.id, i.order_id, i.position, i.sku, i.title, i.quantity, i.unit_price, i.total_owed, i.shipped_on, i.condition, i.url, o.currency FROM merchant_order_items i JOIN merchant_orders o ON o.id = i.order_id WHERE i.order_id IN "+in+" ORDER BY i.order_id, i.position", args...)
	if err != nil {
		return err
	}
	// Catalogue facts live in server.db, never in the household file.
	catalog, err := s.server()
	if err != nil {
		return err
	}
	type key struct {
		merchant domain.MerchantID
		sku      string
	}
	wanted := make(map[key]bool)
	for _, item := range items {
		if item.SKU != "" {
			wanted[key{orders[index[item.OrderID]].Merchant, item.SKU}] = true
		}
	}
	facts := make(map[key]*CatalogItem, len(wanted))
	for k := range wanted {
		var c CatalogItem
		c.Merchant, c.SKU, c.Status = k.merchant, k.sku, CatalogFound
		err := catalog.Read().QueryRowContext(ctx, "SELECT title, brand, size, category, image_url, url FROM merchant_catalog WHERE merchant = ? AND sku = ? AND status = 'found'", string(k.merchant), k.sku).Scan(&c.Title, &c.Brand, &c.Size, &c.Category, &c.ImageURL, &c.URL)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return wrap("store: merchant item catalogue", err)
		}
		facts[k] = &c
	}
	for _, item := range items {
		i := index[item.OrderID]
		item.Catalog = facts[key{orders[i].Merchant, item.SKU}]
		orders[i].Items = append(orders[i].Items, item)
	}
	return nil
}

func (s *Store) AttachMerchantMatches(ctx context.Context, orders []MerchantOrder) error {
	if len(orders) == 0 {
		return nil
	}
	spaceID := orders[0].SpaceID
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(orders))
	index := make(map[uuid.UUID]int, len(orders))
	for i, one := range orders {
		if one.SpaceID != spaceID {
			return errors.New("store: merchant orders span households")
		}
		ids = append(ids, one.ID)
		index[one.ID] = i
	}
	in, args := inArgs(ids)
	type pair struct {
		order uuid.UUID
		txn   MerchantMatchedTransaction
	}
	pairs, err := sqliteQueryAll(ctx, db.Read(), "store: merchant matches", func(row scanner) (pair, error) {
		var p pair
		var on, code string
		var amount int64
		err := row.Scan(&p.order, &p.txn.ID, &p.txn.AccountID, &on, &amount, &p.txn.Payee, &p.txn.StatementName, &p.txn.AccountName, &p.txn.Basis, &p.txn.Confidence, &code)
		if err != nil {
			return p, err
		}
		p.txn.Date, err = domain.ParseDate(on)
		minorValue950, moneyErr := fromMinor(amount, code)
		if moneyErr != nil {
			return p, moneyErr
		}
		p.txn.Amount = domain.FromDecimal(minorValue950)
		return p, err
	}, "SELECT m.order_id, m.transaction_id, t.account_id, t.date, t.amount, t.payee, t.statement_name, a.name, m.basis, m.confidence, a.currency FROM merchant_matches m JOIN transactions t ON t.id = m.transaction_id JOIN accounts a ON a.id = t.account_id WHERE m.order_id IN "+in+" ORDER BY m.created_at", args...)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return MerchantOrder{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return MerchantOrder{}, err
	}
	one, err := scanMerchantOrder(db.Read().QueryRowContext(ctx, "SELECT "+merchantOrderColumns+" FROM merchant_orders WHERE merchant_account_id = ? AND order_number = ?", accountID, number), spaceID)
	if err != nil {
		return MerchantOrder{}, wrap("store: GetMerchantOrderByNumber", err)
	}
	orders := []MerchantOrder{one}
	if err := s.attachMerchantItems(ctx, orders); err != nil {
		return MerchantOrder{}, err
	}
	if err := h.AttachMerchantRefunds(ctx, orders); err != nil {
		return MerchantOrder{}, err
	}
	return orders[0], nil
}

// MerchantMatchesForOrders is every match already made against the orders, so
// a charge one bank row took is not offered to another.
func (s *Store) MerchantMatchesForOrders(ctx context.Context, spaceID SpaceID, orderIDs []uuid.UUID) ([]MerchantMatch, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	in, args := inArgs(orderIDs)
	return sqliteQueryAll(ctx, db.Read(), "store: MerchantMatchesForOrders", func(row scanner) (MerchantMatch, error) { return scanMerchantMatch(row, primary) }, "SELECT transaction_id,order_id,amount,basis,confidence,created_at,refund_id FROM merchant_matches WHERE order_id IN "+in, args...)
}

// MerchantOrdersWithInvoice lists the order numbers since a day whose invoice
// a pull has read in full (payment figures and every item priced), which the
// next pull may skip.
func (s *Store) MerchantOrdersWithInvoice(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, since domain.Date) ([]string, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: MerchantOrdersWithInvoice", scanValue[string], `SELECT order_number FROM merchant_orders o WHERE merchant_account_id = ? AND ordered_on >= ? AND gift_card_amount IS NOT NULL AND NOT EXISTS (SELECT 1 FROM merchant_order_items i WHERE i.order_id = o.id AND i.unit_price IS NULL AND i.total_owed IS NULL)`, accountID, since.String())
	if err != nil {
		return nil, err
	}
	return found, nil
}

// MerchantOrdersWithInvoiceDocument lists the order numbers since a day that
// already have an invoice document on file, which the next pull need not
// fetch again.
func (s *Store) MerchantOrdersWithInvoiceDocument(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, since domain.Date) ([]string, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: MerchantOrdersWithInvoiceDocument", scanValue[string], `SELECT o.order_number FROM merchant_orders o WHERE o.merchant_account_id = ? AND o.ordered_on >= ? AND EXISTS (SELECT 1 FROM document_links l WHERE l.kind = ? AND l.target_id = o.id AND l.role = ?)`, accountID, since.String(), string(DocumentLinkMerchantOrder), DocumentRoleInvoice)
	if err != nil {
		return nil, err
	}
	return found, nil
}

// merchantOrderWantsInvoice is an order with no invoice document that a
// backfill could still reopen for one: it names a page, is not cancelled, and
// has not come back empty three times.
const merchantOrderWantsInvoice = `o.details_url <> '' AND lower(o.status) NOT LIKE 'cancel%' AND o.invoice_misses < 3
	AND NOT EXISTS (SELECT 1 FROM document_links l
	                 WHERE l.kind = 'merchant_order'
	                   AND l.target_id = o.id AND l.role = 'invoice')`

// MerchantOrdersWantingInvoice lists, newest first, every order of the
// account a backfill could reopen for its invoice.
func (s *Store) MerchantOrdersWantingInvoice(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) ([]string, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: MerchantOrdersWantingInvoice", scanValue[string], `SELECT o.order_number FROM merchant_orders o WHERE o.merchant_account_id = ? AND `+merchantOrderWantsInvoice+` ORDER BY o.ordered_on DESC, o.order_number DESC`, accountID)
	if err != nil {
		return nil, err
	}
	return found, nil
}

// CountMerchantOrdersWantingInvoice counts the account's orders a backfill
// could still reopen for their invoice.
func (s *Store) CountMerchantOrdersWantingInvoice(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) (int, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, err
	}
	var n int
	err = db.Read().QueryRowContext(ctx, "SELECT count(*) FROM merchant_orders o WHERE o.merchant_account_id = ? AND "+merchantOrderWantsInvoice, accountID).Scan(&n)
	return n, wrap("store: CountMerchantOrdersWantingInvoice", err)
}

// MarkMerchantInvoicesMissed records that a backfill reopened these orders
// and found no invoice to print.
func (s *Store) MarkMerchantInvoicesMissed(ctx context.Context, spaceID SpaceID, accountID uuid.UUID, numbers []string) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	in, args := inArgs(numbers)
	args = append([]any{accountID}, args...)
	_, err = db.Write().ExecContext(ctx, "UPDATE merchant_orders SET invoice_misses = invoice_misses + 1, updated_at = ? WHERE merchant_account_id = ? AND order_number IN "+in, append([]any{db.Now()}, args...)...)
	return wrap("store: mark merchant invoices missed", err)
}

// merchantReceiptWantsDocument is a warehouse or fuel purchase whose
// every item is described and that has no invoice document: one a receipt can
// be laid out for.
const merchantReceiptWantsDocument = `o.kind IN ('warehouse', 'fuel')
	AND EXISTS (SELECT 1 FROM merchant_order_items i WHERE i.order_id = o.id)
	AND NOT EXISTS (SELECT 1 FROM merchant_order_items i WHERE i.order_id = o.id AND trim(i.title) = '')
	AND NOT EXISTS (SELECT 1 FROM document_links l
	                 WHERE l.kind = 'merchant_order'
	                   AND l.target_id = o.id AND l.role = 'invoice')`

var receiptKinds = []string{domain.PurchaseWarehouse, domain.PurchaseFuel}

// MerchantReceiptsWithoutDocument lists, newest first and with their items,
// every purchase of the account a receipt can be laid out for.
func (s *Store) MerchantReceiptsWithoutDocument(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) ([]MerchantOrder, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: MerchantReceiptsWithoutDocument", func(row scanner) (MerchantOrder, error) { return scanMerchantOrder(row, spaceID) }, `SELECT `+merchantOrderColumns+` FROM merchant_orders o WHERE o.merchant_account_id = ? AND `+merchantReceiptWantsDocument+` ORDER BY o.ordered_on DESC, o.order_number DESC`, accountID)
	if err != nil {
		return nil, err
	}
	return found, s.attachMerchantItems(ctx, found)
}

// CountMerchantReceiptsWithoutDocument counts the purchases of the account a
// receipt can be laid out for.
func (s *Store) CountMerchantReceiptsWithoutDocument(ctx context.Context, spaceID SpaceID, accountID uuid.UUID) (int, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, err
	}
	var n int
	err = db.Read().QueryRowContext(ctx, "SELECT count(*) FROM merchant_orders o WHERE o.merchant_account_id = ? AND "+merchantReceiptWantsDocument, accountID).Scan(&n)
	return n, wrap("store: CountMerchantReceiptsWithoutDocument", err)
}

// RecordMerchantInvoiceBackfill keeps how an invoice backfill ended. It
// leaves the last pull's stamp alone, which the scheduler reads.
func (s *Store) RecordMerchantInvoiceBackfill(ctx context.Context, spaceID SpaceID, id uuid.UUID, filed, left int, stopped string) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: record merchant invoice backfill", "UPDATE merchant_accounts SET invoice_backfill_at = ?, invoice_backfill_filed = ?, invoice_backfill_left = ?, invoice_backfill_stopped = ?, updated_at = ? WHERE id = ?", db.Now(), filed, left, stopped, db.Now(), id)
}

func (s *Store) MerchantChargesForOrders(ctx context.Context, spaceID SpaceID, numbers []string) ([]MerchantCharge, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	in, args := inArgs(numbers)
	return sqliteQueryAll(ctx, db.Read(), "store: MerchantChargesForOrders", func(row scanner) (MerchantCharge, error) { return scanMerchantCharge(row, primary) }, "SELECT id,merchant,merchant_account_id,order_number,charged_on,amount,instrument FROM merchant_charges WHERE order_number IN "+in, args...)
}

// MerchantOrdersBetween is a merchant's non-ignored orders in a window, with
// items, across all the household's logins — the bank row does not say whose
// login placed it.
func (s *Store) MerchantOrdersBetween(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, from, to domain.Date) ([]MerchantOrder, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	found, err := sqliteQueryAll(ctx, db.Read(), "store: MerchantOrdersBetween", func(row scanner) (MerchantOrder, error) { return scanMerchantOrder(row, spaceID) }, `SELECT `+merchantOrderColumns+` FROM merchant_orders WHERE merchant = ? AND ordered_on BETWEEN ? AND ? AND ignored_at IS NULL ORDER BY ordered_on`, string(merchant), from.String(), to.String())
	if err != nil {
		return nil, err
	}
	return found, s.attachMerchantItems(ctx, found)
}

func (s *Store) SetMerchantOrderIgnored(ctx context.Context, spaceID SpaceID, id uuid.UUID, ignored bool) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set merchant order ignored", "UPDATE merchant_orders SET ignored_at = CASE WHEN ? THEN COALESCE(ignored_at, ?) END, updated_at = ? WHERE id = ?", ignored, db.Now(), db.Now(), id)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	args := &sqliteArgList{}
	where := " WHERE " + MoneyMoved + " AND " + NotInIgnoredAccountOn("transactions") + " AND NOT EXISTS (SELECT 1 FROM merchant_matches m WHERE m.transaction_id=transactions.id AND m.order_id=" + args.add(order.ID) + ")"
	if search = strings.TrimSpace(search); search != "" {
		like := "%" + escapeLike(search) + "%"
		where += " AND (payee LIKE " + args.add(like) + " ESCAPE '\\' OR statement_name LIKE " + args.add(like) + " ESCAPE '\\')"
	} else {
		where += " AND date BETWEEN " + args.add(order.OrderedOn.AddDays(-3).String()) + " AND " + args.add(order.OrderedOn.AddDays(60).String()) + " AND " + merchantWording(order.Merchant)
	}
	query := "SELECT " + transactionColumns + `,
 (SELECT a.name FROM accounts a WHERE a.id=transactions.account_id),
 (SELECT o.id FROM merchant_matches m JOIN merchant_orders o ON o.id=m.order_id WHERE m.transaction_id=transactions.id ORDER BY m.created_at LIMIT 1),
 (SELECT group_concat(order_number, ', ') FROM (SELECT o.order_number FROM merchant_matches m JOIN merchant_orders o ON o.id=m.order_id WHERE m.transaction_id=transactions.id ORDER BY m.created_at))
 FROM transactions` + where + " ORDER BY abs(julianday(date)-julianday(" + args.add(order.OrderedOn.String()) + ")),transactions.id"
	if limit > 0 {
		query += " LIMIT " + args.add(limit)
	}
	return sqliteQueryAll(ctx, db.Read(), "store: merchant match candidates", func(row scanner) (MerchantMatchCandidate, error) {
		var one MerchantMatchCandidate
		var account, number sql.Null[string]
		var orderID sql.Null[uuid.UUID]
		txn, err := scanTransaction(trailing{row, []any{&account, &orderID, &number}}, spaceID, primary)
		if err != nil {
			return one, err
		}
		one.Transaction = txn
		one.AccountName = account.V
		one.MatchedOrderNumber = number.V
		one.MatchedOrderID = orderID.V
		return one, nil
	}, query, args.values...)
}

// MerchantOrderCandidates is the orders a person might match a bank row to by
// hand, with their items and unranked (service.Merchants.OrderCandidates ranks
// them): two months before the row to a week after, excluding orders the row
// already pays for, the limit nearest in date. A search reaches past the
// window, as in MerchantMatchCandidates. Empty merchants means every merchant.
func (s *Store) MerchantOrderCandidates(ctx context.Context, spaceID SpaceID, merchants []domain.MerchantID, txn Transaction, search string, limit int) ([]MerchantOrder, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	args := &sqliteArgList{}
	where := " WHERE 1=1"
	if len(merchants) > 0 {
		in, values := inArgs(merchants)
		where += " AND merchant IN " + in
		args.values = append(args.values, values...)
	}
	where += " AND NOT EXISTS (SELECT 1 FROM merchant_matches m WHERE m.order_id=merchant_orders.id AND m.transaction_id=" + args.add(txn.ID) + ")"
	if search = strings.TrimSpace(search); search != "" {
		like := "%" + escapeLike(search) + "%"
		where += " AND (order_number LIKE " + args.add(like) + " ESCAPE '\\' OR EXISTS (SELECT 1 FROM merchant_order_items i WHERE i.order_id=merchant_orders.id AND i.title LIKE " + args.add(like) + " ESCAPE '\\'))"
	} else {
		where += " AND ordered_on BETWEEN " + args.add(txn.Date.AddDays(-60).String()) + " AND " + args.add(txn.Date.AddDays(7).String())
	}
	query := "SELECT " + merchantOrderColumns + " FROM merchant_orders" + where + " ORDER BY abs(julianday(ordered_on)-julianday(" + args.add(txn.Date.String()) + ")),ordered_on DESC,id"
	if limit > 0 {
		query += " LIMIT " + args.add(limit)
	}
	orders, err := sqliteQueryAll(ctx, db.Read(), "store: merchant order candidates", func(row scanner) (MerchantOrder, error) { return scanMerchantOrder(row, spaceID) }, query, args.values...)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return false, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return false, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return false, err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	amount, err := toMinor(one.Amount.Decimal(), primary)
	if err != nil {
		return false, err
	}
	result, err := db.Write().ExecContext(ctx, "INSERT INTO merchant_charges (id,merchant_account_id,order_number,charged_on,amount,instrument,merchant,created_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING", one.ID, one.MerchantAccountID, one.OrderNumber, one.ChargedOn.String(), amount, one.Instrument, string(one.Merchant), db.Now())
	if err != nil {
		return false, wrap("store: upsert merchant charge", err)
	}
	n, err := result.RowsAffected()
	return n > 0, wrap("store: upsert merchant charge", err)
}

func (s *Store) MerchantChargesBetween(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, from, to domain.Date) ([]MerchantCharge, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: MerchantChargesBetween", func(row scanner) (MerchantCharge, error) { return scanMerchantCharge(row, primary) }, "SELECT id,merchant,merchant_account_id,order_number,charged_on,amount,instrument FROM merchant_charges WHERE merchant = ? AND charged_on BETWEEN ? AND ? ORDER BY charged_on", []any{string(merchant), from.String(), to.String()}...)
}

func scanMerchantCharge(row scanner, currency string) (MerchantCharge, error) {
	var one MerchantCharge
	var on string
	var amount int64
	err := row.Scan(&one.ID, &one.Merchant, &one.MerchantAccountID, &one.OrderNumber, &on, &amount, &one.Instrument)
	if err != nil {
		return one, err
	}
	one.ChargedOn, err = domain.ParseDate(on)
	minorValue1376, moneyErr := fromMinor(amount, currency)
	if moneyErr != nil {
		return one, moneyErr
	}
	one.Amount = domain.FromDecimal(minorValue1376)
	return one, err
}

// --- Refunds -------------------------------------------------------------------

const merchantRefundColumns = `r.id, r.merchant, r.merchant_account_id, r.order_number, r.sku, r.title,
	r.quantity, r.refunded_on, r.amount, r.instrument, r.destination, r.status, r.source,
	(SELECT m.transaction_id FROM merchant_matches m WHERE m.refund_id = r.id LIMIT 1)`

func scanMerchantRefund(row scanner, currency string) (MerchantRefund, error) {
	var one MerchantRefund
	var on string
	var amount int64
	var txn sql.Null[uuid.UUID]
	err := row.Scan(&one.ID, &one.Merchant, &one.MerchantAccountID, &one.OrderNumber, &one.SKU, &one.Title, &one.Quantity, &on, &amount, &one.Instrument, &one.Destination, &one.Status, &one.Source, &txn)
	if err != nil {
		return one, err
	}
	one.RefundedOn, err = domain.ParseDate(on)
	one.TransactionID = Deref(nullValue(txn))
	minorValue1397, moneyErr := fromMinor(amount, currency)
	if moneyErr != nil {
		return one, moneyErr
	}
	one.Amount = domain.FromDecimal(minorValue1397)
	return one, err
}

// UpsertMerchantRefund writes a refund and reports whether it was new. It is
// keyed on the order, line, day and amount, since the returns page is re-read
// daily and its status changes meanwhile.
func (s *Store) UpsertMerchantRefund(ctx context.Context, spaceID SpaceID, one *MerchantRefund) (bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return false, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return false, err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	if one.Quantity <= 0 {
		one.Quantity = 1
	}
	if one.Destination == "" {
		one.Destination = MerchantRefundToCard
	}
	amount, err := toMinor(one.Amount.Decimal(), primary)
	if err != nil {
		return false, err
	}
	var inserted bool
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		var exists bool
		err = db.Read().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM merchant_refunds WHERE merchant_account_id=? AND order_number=? AND sku=? AND refunded_on=? AND amount=?)", one.MerchantAccountID, one.OrderNumber, one.SKU, one.RefundedOn.String(), amount).Scan(&exists)
		if err != nil {
			return wrap("store: find merchant refund", err)
		}
		inserted = !exists
		err = db.Write().QueryRowContext(ctx,
			`INSERT INTO merchant_refunds (id,merchant,merchant_account_id,order_number,sku,title,quantity,refunded_on,amount,instrument,destination,status,source,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT (merchant_account_id,order_number,sku,refunded_on,amount) DO UPDATE SET
 title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE merchant_refunds.title END, quantity=excluded.quantity,
 instrument=CASE WHEN excluded.instrument<>'' THEN excluded.instrument ELSE merchant_refunds.instrument END,
 destination=excluded.destination,status=CASE WHEN excluded.status<>'' THEN excluded.status ELSE merchant_refunds.status END,
 updated_at=excluded.updated_at RETURNING id`,
			one.ID, string(one.Merchant), one.MerchantAccountID, one.OrderNumber, one.SKU, one.Title, one.Quantity, one.RefundedOn.String(), amount, one.Instrument, one.Destination, one.Status, one.Source, db.Now(), db.Now()).Scan(&one.ID)
		return wrap("store: upsert merchant refund", err)
	})
	return inserted, err
}

// MerchantRefundsBetween is a merchant's refunds in a window, gift card ones
// included, for the matcher.
func (s *Store) MerchantRefundsBetween(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, from, to domain.Date) ([]MerchantRefund, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: merchant refunds between", func(row scanner) (MerchantRefund, error) { return scanMerchantRefund(row, primary) }, "SELECT "+merchantRefundColumns+" FROM merchant_refunds r WHERE r.merchant=? AND r.refunded_on BETWEEN ? AND ? ORDER BY r.refunded_on,r.id", string(merchant), from.String(), to.String())
}

func (s *Store) GetMerchantRefund(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MerchantRefund, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return MerchantRefund{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return MerchantRefund{}, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return MerchantRefund{}, err
	}
	one, err := scanMerchantRefund(db.Read().QueryRowContext(ctx, "SELECT "+merchantRefundColumns+" FROM merchant_refunds r WHERE r.id=?", id), primary)
	return one, wrap("store: get merchant refund", err)
}

// AttachMerchantRefunds fills in what each order gave back, joined by order
// number: a refund can arrive after its order is outside the pull's window.
func (s *Store) AttachMerchantRefunds(ctx context.Context, orders []MerchantOrder) error {
	if len(orders) == 0 {
		return nil
	}
	spaceID := orders[0].SpaceID
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return err
	}
	numbers := make([]string, 0, len(orders))
	index := make(map[string]int, len(orders))
	for i, one := range orders {
		if one.SpaceID != spaceID {
			return errors.New("store: merchant orders span households")
		}
		numbers = append(numbers, one.OrderNumber)
		index[one.MerchantAccountID.String()+"/"+one.OrderNumber] = i
	}
	in, args := inArgs(numbers)
	refunds, err := sqliteQueryAll(ctx, db.Read(), "store: merchant order refunds", func(row scanner) (MerchantRefund, error) { return scanMerchantRefund(row, primary) }, "SELECT "+merchantRefundColumns+" FROM merchant_refunds r WHERE r.order_number IN "+in+" ORDER BY r.refunded_on,r.id", args...)
	if err != nil {
		return err
	}
	for _, refund := range refunds {
		if i, ok := index[refund.MerchantAccountID.String()+"/"+refund.OrderNumber]; ok {
			orders[i].Refunds = append(orders[i].Refunds, refund)
		}
	}
	return nil
}

// --- Matches -------------------------------------------------------------------

// SetMerchantMatch records the order behind a bank row, replacing any earlier
// match for that row.
func (s *Store) SetMerchantMatch(ctx context.Context, spaceID SpaceID, one *MerchantMatch) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx, "DELETE FROM merchant_matches WHERE transaction_id=? AND order_id<>?", one.TransactionID, one.OrderID); err != nil {
			return wrap("store: set merchant match", err)
		}
		return tx.AddMerchantMatch(ctx, spaceID, one)
	})
}

// MerchantMatchedTransactions is which of a page of bank rows have an order
// matched to them, in one query; the register asks for every row it renders.
func (s *Store) MerchantMatchedTransactions(ctx context.Context, spaceID SpaceID, txnIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	in, args := inArgs(txnIDs)
	ids, err := sqliteQueryAll(ctx, db.Read(), "store: merchant matched transactions", scanValue[uuid.UUID], "SELECT DISTINCT transaction_id FROM merchant_matches WHERE transaction_id IN "+in, args...)
	if err != nil {
		return nil, err
	}
	matched := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		matched[id] = true
	}
	return matched, nil
}

// AddMerchantMatch adds an order beside what the row already pays for.
func (s *Store) AddMerchantMatch(ctx context.Context, spaceID SpaceID, one *MerchantMatch) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		if err := tx.addMerchantMatch(ctx, spaceID, one); err != nil {
			return err
		}
		_, _, err := tx.ReconcileReceipts(ctx, spaceID, []uuid.UUID{one.TransactionID})
		return err
	})
}

func (s *Store) addMerchantMatch(ctx context.Context, spaceID SpaceID, one *MerchantMatch) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return err
	}
	amount, err := toMinor(one.Amount.Decimal(), primary)
	if err != nil {
		return err
	}
	var created int64
	err = db.Write().QueryRowContext(ctx, "INSERT INTO merchant_matches (transaction_id,order_id,amount,basis,confidence,refund_id,created_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT (transaction_id,order_id) DO UPDATE SET amount=excluded.amount,basis=excluded.basis,confidence=excluded.confidence,refund_id=excluded.refund_id RETURNING created_at", one.TransactionID, one.OrderID, amount, one.Basis, one.Confidence, one.RefundID, db.Now()).Scan(&created)
	if err != nil {
		return wrap("store: add merchant match", err)
	}
	one.CreatedAt = fromUnixMillis(created)
	return nil
}

func (s *Store) SetMerchantMatchAmount(ctx context.Context, spaceID SpaceID, transactionID, orderID uuid.UUID, amount domain.Money) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return err
	}
	minor, err := toMinor(amount.Decimal(), primary)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, "UPDATE merchant_matches SET amount=? WHERE transaction_id=? AND order_id=?", minor, transactionID, orderID)
	return wrap("store: set merchant match amount", err)
}

func (s *Store) DeleteMerchantMatchForOrder(ctx context.Context, spaceID SpaceID, transactionID, orderID uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if err := sqliteExecOne(ctx, db.Write(), "store: delete merchant match for order", "DELETE FROM merchant_matches WHERE transaction_id=? AND order_id=?", transactionID, orderID); err != nil {
			return err
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, []uuid.UUID{transactionID})
		return err
	})
}

// ListMerchantMatches is every order behind one bank row, oldest match first.
func (s *Store) ListMerchantMatches(ctx context.Context, spaceID SpaceID, transactionID uuid.UUID) ([]MerchantMatch, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: ListMerchantMatches", func(row scanner) (MerchantMatch, error) { return scanMerchantMatch(row, primary) }, "SELECT transaction_id,order_id,amount,basis,confidence,created_at,refund_id FROM merchant_matches WHERE transaction_id=? ORDER BY created_at,order_id", transactionID)
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

func scanMerchantMatch(row scanner, currency string) (MerchantMatch, error) {
	var m MerchantMatch
	var amount, created int64
	var refund sql.Null[uuid.UUID]
	if err := row.Scan(&m.TransactionID, &m.OrderID, &amount, &m.Basis, &m.Confidence, &created, &refund); err != nil {
		return m, err
	}
	m.RefundID = nullValue(refund)
	m.CreatedAt = fromUnixMillis(created)
	minorValue1689, moneyErr := fromMinor(amount, currency)
	if moneyErr != nil {
		return m, moneyErr
	}
	m.Amount = domain.FromDecimal(minorValue1689)
	return m, nil
}

// merchantWording is the SQL form of each merchant's domain wording check
// (domain.IsAmazonWording, IsCostcoWording, IsEbayWording); each pair must
// agree. A merchant with no case here matches no row.
func merchantWording(merchant domain.MerchantID) string {
	switch merchant {
	case domain.MerchantAmazon:
		return `(statement_name LIKE '%amazon%' OR statement_name LIKE '%amzn%'
	OR payee LIKE '%amazon%' OR payee LIKE '%amzn%')`
	case domain.MerchantCostco:
		return `(statement_name LIKE '%costco%' OR payee LIKE '%costco%')`
	case domain.MerchantEbay:
		return `(statement_name LIKE '%ebay%' OR payee LIKE '%ebay%')`
	default:
		return `FALSE`
	}
}

// merchantMatchable is which bank rows may be matched to one merchant's order,
// shared by the bulk-match list, its waiting count and the per-sync matcher.
// Pending charges count: a pending row settles in place, so the match survives.
func merchantMatchable(merchant domain.MerchantID) string {
	return MoneyMoved + ` AND ` + NotInIgnoredAccountOn("transactions") + ` AND ` + merchantWording(merchant)
}

// matchedToMerchant is the clause for a row one of the merchant's orders is
// matched to; the placeholder is the merchant.
const matchedToMerchant = `EXISTS (SELECT 1 FROM merchant_matches m JOIN merchant_orders o ON o.id = m.order_id
	WHERE m.transaction_id = transactions.id AND o.merchant = ?)`

func (s *Store) ListUnmatchedMerchantTransactions(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID, limit int) ([]Transaction, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	query := "SELECT " + transactionColumns + " FROM transactions WHERE " + merchantMatchable(merchant) + " AND NOT " + matchedToMerchant + " ORDER BY date DESC,id"
	args := []any{string(merchant)}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	return sqliteQueryAll(ctx, db.Read(), "store: unmatched merchant transactions", func(row scanner) (Transaction, error) { return scanTransaction(row, spaceID, primary) }, query, args...)
}

func (s *Store) ListMatchedMerchantTransactionIDs(ctx context.Context, spaceID SpaceID, merchant domain.MerchantID) ([]uuid.UUID, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: matched merchant transactions", scanValue[uuid.UUID], "SELECT t.id FROM transactions t WHERE "+MoneyMovedOn("t")+" AND EXISTS (SELECT 1 FROM merchant_matches m JOIN merchant_orders o ON o.id=m.order_id WHERE m.transaction_id=t.id AND o.merchant=?) ORDER BY t.date DESC,t.id", string(merchant))
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return MerchantSummary{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return MerchantSummary{}, err
	}
	var out MerchantSummary
	var newest, oldest sql.Null[string]
	err = db.Read().QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM merchant_accounts WHERE merchant=?),(SELECT count(*) FROM merchant_orders WHERE merchant=?),
 (SELECT count(*) FROM merchant_order_items i JOIN merchant_orders o ON o.id=i.order_id WHERE o.merchant=?),
 (SELECT count(*) FROM merchant_charges WHERE merchant=?),
 (SELECT count(*) FROM transactions WHERE `+merchantMatchable(merchant)+`),
 (SELECT count(DISTINCT m.transaction_id) FROM merchant_matches m JOIN merchant_orders o ON o.id=m.order_id WHERE o.merchant=?),
 (SELECT max(ordered_on) FROM merchant_orders WHERE merchant=?),(SELECT min(ordered_on) FROM merchant_orders WHERE merchant=?)`,
		string(merchant), string(merchant), string(merchant), string(merchant), string(merchant), string(merchant), string(merchant)).Scan(&out.Accounts, &out.Orders, &out.Items, &out.Charges, &out.MerchantTransactions, &out.MatchedTransactions, &newest, &oldest)
	if err != nil {
		return out, wrap("store: merchant summary", err)
	}
	if newest.Valid {
		out.NewestOrder, err = domain.ParseDate(newest.V)
		if err != nil {
			return out, err
		}
	}
	if oldest.Valid {
		out.OldestOrder, err = domain.ParseDate(oldest.V)
	}
	return out, err
}

func (s *Store) CountMerchantOrdersByAccount(ctx context.Context, spaceID SpaceID) (map[uuid.UUID]int, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	counts, err := sqliteQueryAll(ctx, db.Read(), "store: merchant orders by account", scanPair[uuid.UUID, int], "SELECT merchant_account_id,count(*) FROM merchant_orders GROUP BY merchant_account_id")
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int, len(counts))
	for _, one := range counts {
		out[one.first] = one.second
	}
	return out, nil
}
