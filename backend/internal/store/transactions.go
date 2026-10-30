package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// SkippedEstimate is the EstimateStatus of the tombstone row that records a
// skipped occurrence. Spelled once: internal/api writes it and internal/service
// reads it, and two spellings would leave a bill paid to one and due to the other.
const SkippedEstimate = "skipped"

// ProjectedEstimate is the EstimateStatus of a forecast row for an occurrence
// nobody has paid yet, dated on the due date. Simplifi calls these pending
// transactions; they are not — a pending charge is never dated in the future —
// and keeping them apart keeps next year's bills out of balances.
const ProjectedEstimate = "projected"

// MoneyMovedOn is the SQL for "this row is money that actually moved", for
// hand-written queries that cannot go through ListTransactions. Both clauses
// matter: forecasts are not deleted, so `NOT is_deleted` alone admits them.
// The SQL half of domain.CountsTowardBalance; alias qualifies the columns.
func MoneyMovedOn(alias string) string {
	qualifier := ""
	if alias != "" {
		qualifier = alias + "."
	}
	return "NOT " + qualifier + "is_deleted AND " + qualifier + "estimate_status IS NULL"
}

// MoneyMoved is MoneyMovedOn for a query whose table needs no qualifier.
var MoneyMoved = MoneyMovedOn("")

// Transaction is the transactions row, with its splits and tags loaded eagerly.
type Transaction struct {
	ID        uuid.UUID
	SpaceID   SpaceID
	AccountID uuid.UUID
	// ExternalID is the aggregator's id; a re-sync updates rather than duplicates on it.
	ExternalID string

	// EffectiveDate is when the row hits cash flow (for a card charge, its
	// statement); zero means Date. Readers ask the domain for ReportingDate.
	Date          domain.Date
	EffectiveDate domain.Date

	Amount   domain.Money
	Currency string
	// AmountPrimary is the amount in the space's primary currency, absent when
	// no conversion was needed; read PrimaryAmount(), which falls back.
	AmountPrimary    domain.Money
	HasAmountPrimary bool
	FxRateUsed       domain.Rate
	HasFxRateUsed    bool

	// StatementName is the bank's wording and is never edited; Payee is the
	// display name. Rules and series matching read the first, display the second.
	StatementName string
	Payee         string
	// Memo is the provider's second line, verbatim; Notes is the household's own.
	Memo        string
	Notes       string
	CheckNumber string

	CategoryID uuid.UUID
	Source     domain.Source

	IsPending  bool
	IsDeleted  bool
	IsReviewed bool
	// The two exclusion flags are independent.
	ExcludedFromReports      bool
	ExcludedFromSpendingPlan bool
	// ReceiptNotNeeded is a person saying the row needs no receipt
	// (domain.ReceiptStatusOf).
	ReceiptNotNeeded bool

	IsBill         bool
	IsSubscription bool

	// TransferPairID is set on both legs of a matched transfer and must be
	// released when either leg is deleted (trap 2).
	TransferPairID uuid.UUID

	// PaddedTxnID, on the income row a padding mail rule writes, is the
	// purchase it pads. Only LinkPadding writes it; deleting the purchase
	// deletes the pad.
	PaddedTxnID uuid.UUID

	UserFlag     string
	UserFlagNote string

	// TransactedOn is the purchase date where the feed gives one separately.
	TransactedOn domain.Date
	// ProviderExtra is the part of the feed's payload no column names, kept
	// whole because a re-sync only reaches back as far as the connector's window.
	ProviderExtra json.RawMessage

	// SeriesID and SeriesDueOn tie a row to one occurrence slot of a series,
	// so two payments in one month cannot claim the same due date.
	SeriesID    uuid.UUID
	SeriesDueOn domain.Date
	// EstimateStatus marks a projected row (ProjectedEstimate) or a skip
	// tombstone (SkippedEstimate).
	EstimateStatus string
	AcceptedOn     domain.Date
	ExpiresOn      domain.Date

	RuleID uuid.UUID
	// Balance is the materialized running balance.
	Balance    domain.Money
	HasBalance bool

	// NeedsSettle marks a row not yet through service.Ingest.AfterIngest; written
	// with the row so the backlog survives a crash. UpdateTransaction does not
	// write it, because the settle's own steps go through that function.
	NeedsSettle bool

	// CategoryCheckedAt/CategoryCheckNote record a category check that filed
	// and proposed nothing; CategoryCheckRunID is the assistant run that last
	// decided the category. The automations service owns all three and
	// UpdateTransaction does not write them: a hand edit is not a check, and a
	// read-modify-write carrying a stale pair would undo the settle.
	CategoryCheckedAt  *time.Time
	CategoryCheckNote  string
	CategoryCheckRunID *uuid.UUID

	Splits []Split
	TagIDs []uuid.UUID

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Split is one line of a split transaction. Its tags are its own; the union
// with the parent's is a filtering rule in the domain.
type Split struct {
	ID            uuid.UUID
	SpaceID       SpaceID
	TransactionID uuid.UUID
	Position      int
	Amount        domain.Money
	CategoryID    uuid.UUID
	Memo          string
	TagIDs        []uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const transactionColumns = `id, account_id, external_id, date, effective_date, amount,
	currency, amount_primary, fx_rate_used, statement_name, payee, memo, notes, check_number,
	category_id, source, is_pending, is_deleted, is_reviewed, excluded_from_reports,
	excluded_from_spending_plan, is_bill, is_subscription, transfer_pair_id, user_flag,
	user_flag_note, series_id, series_due_on, estimate_status, accepted_on, expires_on, rule_id,
	balance, needs_settle, transacted_on, provider_extra, category_checked_at,
	category_check_note, category_check_run_id, padded_txn_id, receipt_not_needed, created_at,
	updated_at`

// TransactionQuery narrows a listing. The zero value returns every live,
// non-estimate row in the space, newest first.
type TransactionQuery struct {
	IDs         []uuid.UUID
	AccountIDs  []uuid.UUID
	CategoryIDs []uuid.UUID
	// From and To bound the date inclusively; DateMode picks the effective
	// date, which is what reports use.
	From     domain.Date
	To       domain.Date
	DateMode domain.DateMode

	IncludeDeleted bool
	ExcludePending bool

	// IncludeEstimates keeps forecast rows. Off by default: nearly every
	// caller wants what happened. The Bills screen needs them so a slot with a
	// forecast is not projected twice.
	IncludeEstimates bool

	// Limit of zero means no limit; the report engine must not be truncated.
	Limit  int
	Offset int

	// SearchText matches payee or statement name case-insensitively, in SQL so
	// a limit bounds what was searched.
	SearchText string

	// HoldsASlot narrows to rows that occupy an occurrence slot. Pair it with
	// IncludeDeleted and IncludeEstimates: skip tombstones and forecasts decide
	// settlement too — see domain.SettledSlots.
	HoldsASlot bool
	// SeriesID narrows to rows linked to one series, whichever slot each fills.
	SeriesID uuid.UUID
}

// ListTransactions returns rows with their splits and tags attached, in four
// queries regardless of row count.
func (s *Store) ListTransactions(ctx context.Context, spaceID SpaceID, q TransactionQuery) ([]Transaction, error) {
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
	sql := &strings.Builder{}
	args := &sqliteArgList{}
	sql.WriteString(`SELECT ` + transactionColumns + ` FROM transactions WHERE 1=1`)

	if !q.IncludeDeleted {
		sql.WriteString(` AND NOT is_deleted`)
	}
	if q.ExcludePending {
		sql.WriteString(` AND NOT is_pending`)
	}
	if !q.IncludeEstimates {
		sql.WriteString(` AND estimate_status IS NULL`)
	}
	for _, filter := range []struct {
		column string
		ids    []uuid.UUID
	}{
		{"id", q.IDs}, {"account_id", q.AccountIDs}, {"category_id", q.CategoryIDs},
	} {
		if len(filter.ids) > 0 {
			marks, values := InArgs(filter.ids)
			sql.WriteString(` AND ` + filter.column + ` IN ` + marks)
			args.values = append(args.values, values...)
		}
	}
	if q.SeriesID != uuid.Nil {
		fmt.Fprintf(sql, ` AND series_id = %s`, args.add(q.SeriesID))
	}
	if q.HoldsASlot {
		sql.WriteString(` AND series_id IS NOT NULL AND series_due_on IS NOT NULL`)
	}
	if q.SearchText != "" {
		fmt.Fprintf(sql,
			` AND (payee LIKE %s ESCAPE '\' OR statement_name LIKE %s ESCAPE '\')`,
			args.add("%"+escapeLike(q.SearchText)+"%"),
			args.add("%"+escapeLike(q.SearchText)+"%"))
	}

	// An unset effective date means the posted date.
	dateExpr := `date`
	if q.DateMode == domain.DateEffective {
		dateExpr = `COALESCE(effective_date, date)`
	}
	if !q.From.IsZero() {
		fmt.Fprintf(sql, ` AND %s >= %s`, dateExpr, args.add(q.From.String()))
	}
	if !q.To.IsZero() {
		fmt.Fprintf(sql, ` AND %s <= %s`, dateExpr, args.add(q.To.String()))
	}

	sql.WriteString(` ORDER BY ` + dateExpr + ` DESC, created_at DESC, id`)
	if q.Limit > 0 {
		fmt.Fprintf(sql, ` LIMIT %s`, args.add(q.Limit))
	}
	if q.Offset > 0 {
		if q.Limit <= 0 {
			sql.WriteString(` LIMIT -1`)
		}
		fmt.Fprintf(sql, ` OFFSET %s`, args.add(q.Offset))
	}

	txns, err := sqliteQueryAll(ctx, db.Read(), "store: list transactions", func(row scanner) (Transaction, error) {
		return scanTransaction(row, spaceID, primary)
	}, sql.String(), args.values...)
	if err != nil {
		return nil, err
	}
	return txns, h.attachAllocations(ctx, spaceID, txns)
}

func (s *Store) GetTransaction(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Transaction, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Transaction{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Transaction{}, err
	}
	primary, err := h.primaryCurrency()
	if err != nil {
		return Transaction{}, err
	}
	txn, err := scanTransaction(db.Read().QueryRowContext(ctx,
		`SELECT `+transactionColumns+` FROM transactions WHERE id = ?`, id), spaceID, primary)
	if err != nil {
		return Transaction{}, wrap("store: get transaction", err)
	}
	list := []Transaction{txn}
	if err := h.attachAllocations(ctx, spaceID, list); err != nil {
		return Transaction{}, err
	}
	return list[0], nil
}

// TransactionByExternalID is the row an account already holds under a
// provider's key, or ErrNotFound.
func (s *Store) TransactionByExternalID(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, externalID string,
) (Transaction, error) {
	if externalID == "" {
		return Transaction{}, wrap("store: transaction by external id", ErrNotFound)
	}
	db, primary, err := s.householdMoneyDB(ctx, spaceID)
	if err != nil {
		return Transaction{}, err
	}
	txn, err := scanTransaction(db.Read().QueryRowContext(ctx,
		`SELECT `+transactionColumns+` FROM transactions
		  WHERE account_id = ? AND external_id = ?`, accountID, externalID), spaceID, primary)
	return txn, wrap("store: transaction by external id", err)
}

// attachAllocations fills in splits and tags for rows already read. Every path
// that returns transactions goes through here, so no screen misses split tags.
func (s *Store) attachAllocations(ctx context.Context, spaceID SpaceID, txns []Transaction) error {
	if len(txns) == 0 {
		return nil
	}
	if len(txns) > sqliteBatchSize {
		for batch := range slices.Chunk(txns, sqliteBatchSize) {
			if err := s.attachAllocations(ctx, spaceID, batch); err != nil {
				return err
			}
		}
		return nil
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, len(txns))
	index := make(map[uuid.UUID]int, len(txns))
	for i, txn := range txns {
		ids[i] = txn.ID
		index[txn.ID] = i
	}

	marks, values := InArgs(ids)
	splits, err := sqliteQueryAll(ctx, db.Read(), "store: load splits", func(row scanner) (Split, error) {
		return scanSplit(row, spaceID)
	}, `SELECT s.id, s.transaction_id, s."position", s.amount, s.category_id, s.memo,
		s.created_at, s.updated_at, t.currency FROM transaction_splits s
		JOIN transactions t ON t.id = s.transaction_id
		WHERE s.transaction_id IN `+marks+` ORDER BY s."position"`, values...)
	if err != nil {
		return err
	}

	splitIndex := make(map[uuid.UUID]*Split, len(splits))
	splitIDs := make([]uuid.UUID, 0, len(splits))
	for i := range splits {
		split := &splits[i]
		splitIDs = append(splitIDs, split.ID)
		splitIndex[split.ID] = split
	}

	for batch := range slices.Chunk(splitIDs, sqliteBatchSize) {
		marks, values := InArgs(batch)
		tagged, err := sqliteQueryAll(ctx, db.Read(), "store: load split tags", scanPair[uuid.UUID, uuid.UUID],
			`SELECT split_id, tag_id FROM split_tags WHERE split_id IN `+marks+` ORDER BY tag_id`, values...)
		if err != nil {
			return err
		}
		for _, one := range tagged {
			if split, ok := splitIndex[one.first]; ok {
				split.TagIDs = append(split.TagIDs, one.second)
			}
		}
	}

	for i := range splits {
		at := index[splits[i].TransactionID]
		txns[at].Splits = append(txns[at].Splits, splits[i])
	}

	tagged, err := sqliteQueryAll(ctx, db.Read(), "store: load transaction tags", scanPair[uuid.UUID, uuid.UUID],
		`SELECT transaction_id, tag_id FROM transaction_tags WHERE transaction_id IN `+marks+` ORDER BY tag_id`, values...)
	if err != nil {
		return err
	}
	for _, one := range tagged {
		at := index[one.first]
		txns[at].TagIDs = append(txns[at].TagIDs, one.second)
	}
	return nil
}

// CreateTransaction writes the row, its splits and its tags in one transaction.
func (s *Store) CreateTransaction(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if t.ID == uuid.Nil {
			t.ID = uuid.New()
		}
		t.SpaceID = spaceID
		primary, err := tx.primaryCurrency()
		if err != nil {
			return err
		}
		values, err := transactionWriteValues(t, primary)
		if err != nil {
			return err
		}
		now := db.Now()
		values = append([]any{t.ID}, values...)
		values = append(values, t.NeedsSettle, now, now)
		marks := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		_, err = db.Write().ExecContext(ctx, `INSERT INTO transactions (id, `+transactionWriteColumns+`, needs_settle, created_at, updated_at) VALUES (`+marks+`)`, values...)
		if err != nil {
			return wrap("store: create transaction", err)
		}
		t.CreatedAt, t.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
		if err := tx.replaceSplits(ctx, spaceID, t); err != nil {
			return err
		}
		if err := tx.setTransactionTags(ctx, spaceID, t.ID, t.TagIDs); err != nil {
			return err
		}
		return tx.refreshRowReceipts(ctx, spaceID, t)
	})
}

const transactionWriteColumns = `account_id, external_id, date, effective_date, amount,
	currency, amount_primary, fx_rate_used, statement_name, payee, memo, notes, check_number,
	category_id, source, is_pending, is_deleted, is_reviewed, excluded_from_reports,
	excluded_from_spending_plan, is_bill, is_subscription, transfer_pair_id, user_flag,
	user_flag_note, series_id, series_due_on, estimate_status, accepted_on, expires_on,
	rule_id, balance, transacted_on, provider_extra, receipt_not_needed`

func transactionWriteValues(t *Transaction, primary string) ([]any, error) {
	var conversionErr error
	money := func(value domain.Money, present bool, currency string) any {
		if !present {
			return nil
		}
		minor, err := toMinor(value.Decimal(), currency)
		if err != nil && conversionErr == nil {
			conversionErr = err
		}
		return minor
	}
	var rate any
	if t.HasFxRateUsed {
		rate = decimaltext.Format(t.FxRateUsed.Round(10))
	}
	values := []any{t.AccountID, NullText(t.ExternalID), t.Date.String(), NullDate(t.EffectiveDate),
		money(t.Amount, true, t.Currency), t.Currency, money(t.AmountPrimary, t.HasAmountPrimary, primary),
		rate, t.StatementName, t.Payee, t.Memo, NullText(t.Notes), NullText(t.CheckNumber),
		NullUUID(t.CategoryID), string(t.Source), t.IsPending, t.IsDeleted, t.IsReviewed,
		t.ExcludedFromReports, t.ExcludedFromSpendingPlan, t.IsBill, t.IsSubscription,
		NullUUID(t.TransferPairID), NullText(t.UserFlag), NullText(t.UserFlagNote),
		NullUUID(t.SeriesID), NullDate(t.SeriesDueOn), NullText(t.EstimateStatus),
		NullDate(t.AcceptedOn), NullDate(t.ExpiresOn), NullUUID(t.RuleID),
		money(t.Balance, t.HasBalance, t.Currency), NullDate(t.TransactedOn), jsonArg(t.ProviderExtra), t.ReceiptNotNeeded}
	return values, conversionErr
}

// UpdateTransaction rewrites the row and its allocations together.
func (s *Store) UpdateTransaction(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		primary, err := tx.primaryCurrency()
		if err != nil {
			return err
		}
		values, err := transactionWriteValues(t, primary)
		if err != nil {
			return err
		}
		columns := strings.Split(transactionWriteColumns, ",")
		for i := range columns {
			columns[i] = strings.TrimSpace(columns[i]) + " = ?"
		}
		now := db.Now()
		values = append(values, now, t.ID)
		if err := sqliteExecOne(ctx, db.Write(), "store: update transaction",
			`UPDATE transactions SET `+strings.Join(columns, ",")+`, updated_at = ? WHERE id = ?`, values...); err != nil {
			return err
		}
		t.UpdatedAt = fromUnixMillis(now)
		if err := tx.replaceSplits(ctx, spaceID, t); err != nil {
			return err
		}
		if err := tx.RefileLinkedRefunds(ctx, spaceID, t.ID, NullUUID(t.CategoryID)); err != nil {
			return err
		}
		if err := tx.setTransactionTags(ctx, spaceID, t.ID, t.TagIDs); err != nil {
			return err
		}
		return tx.refreshRowReceipts(ctx, spaceID, t)
	})
}

// SetTransactionBalances writes running balances on many rows in one
// statement, rather than through UpdateTransaction, which rewrites splits and
// tags too.
func (s *Store) SetTransactionBalances(ctx context.Context, spaceID SpaceID, balances map[uuid.UUID]domain.Money) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if len(balances) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(balances))
		for id := range balances {
			ids = append(ids, id)
		}
		encoded, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		// One lookup for every row's currency, not one per row.
		type rowCurrency struct {
			id       uuid.UUID
			currency string
		}
		rows, err := sqliteQueryAll(ctx, db.Read(), "store: balance currency", func(row scanner) (rowCurrency, error) {
			var r rowCurrency
			return r, row.Scan(&r.id, &r.currency)
		}, `SELECT id, currency FROM transactions WHERE id IN (SELECT value FROM json_each(?))`, string(encoded))
		if err != nil {
			return err
		}
		for _, r := range rows {
			id, currency, balance := r.id, r.currency, balances[r.id]
			minor, err := toMinor(balance.Decimal(), currency)
			if err != nil {
				return err
			}
			if _, err = db.Write().ExecContext(ctx, `UPDATE transactions SET balance = ?, updated_at = ? WHERE id = ?`, minor, db.Now(), id); err != nil {
				return wrap("store: set transaction balances", err)
			}
		}
		return nil
	})
}

// SetTransactionsReviewed flips the review flag on many rows in one statement.
func (s *Store) SetTransactionsReviewed(ctx context.Context, spaceID SpaceID, ids []uuid.UUID, reviewed bool) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	marks, args := InArgs(ids)
	args = append([]any{reviewed, db.Now()}, args...)
	_, err = db.Write().ExecContext(ctx, `UPDATE transactions SET is_reviewed = ?, updated_at = ? WHERE id IN `+marks+` AND NOT is_deleted`, args...)
	return wrap("store: set transactions reviewed", err)
}

// TagTransactions adds tags to, and removes tags from, many rows in one
// transaction. Every id must be a live row of the space, or nothing changes
// and the error is ErrNotFound. Tags land on the row, never on its splits.
func (s *Store) TagTransactions(ctx context.Context, spaceID SpaceID, ids, add, remove []uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		marks, args := InArgs(ids)
		rows, err := sqliteQueryAll(ctx, db.Read(), "store: transactions to tag", scanValue[uuid.UUID],
			`SELECT id FROM transactions WHERE id IN `+marks+` AND NOT is_deleted`, args...)
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return ErrNotFound
		}
		if len(remove) > 0 {
			tagMarks, tagArgs := InArgs(remove)
			values := append(append([]any{}, args...), tagArgs...)
			if _, err := db.Write().ExecContext(ctx, `DELETE FROM transaction_tags WHERE transaction_id IN `+marks+` AND tag_id IN `+tagMarks, values...); err != nil {
				return wrap("store: untag transactions", err)
			}
		}
		if len(add) > 0 {
			tagMarks, tagArgs := InArgs(add)
			values := append(append([]any{}, args...), tagArgs...)
			if _, err := db.Write().ExecContext(ctx, `INSERT INTO transaction_tags (transaction_id,tag_id)
				SELECT t.id,tg.id FROM transactions t CROSS JOIN tags tg WHERE t.id IN `+marks+` AND tg.id IN `+tagMarks+` ON CONFLICT DO NOTHING`, values...); err != nil {
				return wrap("store: tag transactions", err)
			}
		}
		_, err = db.Write().ExecContext(ctx, `UPDATE transactions SET updated_at = ? WHERE id IN `+marks, append([]any{db.Now()}, args...)...)
		return wrap("store: touch tagged transactions", err)
	})
}

// TransactionsNeedingSettle returns ids of rows never settled, oldest first.
// Deleted rows are included: a retired pending charge still owes its account
// a recomputed balance.
func (s *Store) TransactionsNeedingSettle(ctx context.Context, spaceID SpaceID) ([]uuid.UUID, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: transactions needing settle", scanValue[uuid.UUID],
		`SELECT id FROM transactions WHERE needs_settle ORDER BY created_at,id`)
}

// ClearNeedsSettle drops the mark from settled rows in one statement.
func (s *Store) ClearNeedsSettle(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	marks, args := InArgs(ids)
	args = append([]any{db.Now()}, args...)
	_, err = db.Write().ExecContext(ctx, `UPDATE transactions SET needs_settle = false, updated_at = ? WHERE id IN `+marks+` AND needs_settle`, args...)
	return wrap("store: clear needs settle", err)
}

// DeleteTransaction soft-deletes and releases every link the row is half of:
// its transfer pair (trap 2) and its refund links, which a foreign
// key would only cover on a hard delete. The income row padding a purchase is
// deleted with it: it only says the paycheck was net of that purchase.
func (s *Store) DeleteTransaction(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		pads, err := sqliteQueryAll(ctx, db.Read(), "store: pads of deleted row", scanValue[uuid.UUID],
			`SELECT id FROM transactions WHERE padded_txn_id = ? AND NOT is_deleted`, id)
		if err != nil {
			return err
		}
		for _, pad := range pads {
			if err := tx.retire(ctx, spaceID, pad, false); err != nil {
				return err
			}
		}
		return tx.retire(ctx, spaceID, id, false)
	})
}

// RetireDuplicate is DeleteTransaction for a charge the series matcher folded
// into the placeholder it paid; it also clears the external id, which is
// unique per account, so the survivor can take it and a re-import re-links.
// A pad is released, not deleted: the survivor is still the money that left.
func (s *Store) RetireDuplicate(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx, `UPDATE transactions SET padded_txn_id = NULL, updated_at = ? WHERE padded_txn_id = ?`, db.Now(), id); err != nil {
			return wrap("store: release pads", err)
		}
		return tx.retire(ctx, spaceID, id, true)
	})
}

func (s *Store) retire(ctx context.Context, spaceID SpaceID, id uuid.UUID, clearExternalID bool) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if err := tx.releaseTransferPairs(ctx, spaceID, "id", []uuid.UUID{id}); err != nil {
			return err
		}
		if err := tx.releaseRefundLinks(ctx, spaceID, id); err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx, `UPDATE bill_payments SET transaction_id = NULL, updated_at = ? WHERE transaction_id = ?`, db.Now(), id); err != nil {
			return wrap("store: release bill payment", err)
		}
		if err := sqliteExecOne(ctx, db.Write(), "store: delete transaction", `UPDATE transactions
			SET is_deleted = true, transfer_pair_id = NULL, padded_txn_id = NULL,
			external_id = CASE WHEN ? THEN NULL ELSE external_id END, updated_at = ? WHERE id = ?`,
			clearExternalID, db.Now(), id); err != nil {
			return err
		}
		_, _, err = tx.ReconcileReceipts(ctx, spaceID, []uuid.UUID{id})
		return err
	})
}

// LinkPadding records that the income row pads the purchase. Both rows are
// re-checked against the space and must be live; a pad already linked, or a
// purchase already padded, is left as it is. Idempotent.
func (s *Store) LinkPadding(ctx context.Context, spaceID SpaceID, padID, purchaseID uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `UPDATE transactions AS pad SET padded_txn_id = purchase.id, updated_at = ?
		FROM transactions purchase WHERE pad.id = ? AND `+MoneyMovedOn("pad")+`
		AND purchase.id = ? AND `+MoneyMovedOn("purchase")+`
		AND pad.id <> purchase.id AND pad.padded_txn_id IS NULL
		AND NOT EXISTS (SELECT 1 FROM transactions other WHERE other.padded_txn_id = purchase.id)`,
		db.Now(), padID, purchaseID)
	return wrap("store: link padding", err)
}

// PaddingOf maps each of these purchases to the live income row padding it.
func (s *Store) PaddingOf(ctx context.Context, spaceID SpaceID, purchaseIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	marks, args := InArgs(purchaseIDs)
	pairs, err := sqliteQueryAll(ctx, db.Read(), "store: padding of", scanPair[uuid.UUID, uuid.UUID],
		`SELECT padded_txn_id,id FROM transactions WHERE padded_txn_id IN `+marks+` AND NOT is_deleted`, args...)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]uuid.UUID{}
	for _, pair := range pairs {
		out[pair.first] = pair.second
	}
	return out, nil
}

// releaseTransferPairs releases both legs of every pair with a leg among ids.
// column is an internal trusted transaction column, never user input.
func (s *Store) releaseTransferPairs(ctx context.Context, spaceID SpaceID, column string, ids []uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	marks, values := InArgs(ids)
	args := append([]any{db.Now()}, values...)
	_, err = db.Write().ExecContext(ctx, `
		UPDATE transactions SET transfer_pair_id = NULL,
		category_id = CASE WHEN category_from_pair THEN NULL ELSE category_id END,
		category_from_pair = false, updated_at = ?
		WHERE transfer_pair_id IN (
			SELECT transfer_pair_id FROM transactions WHERE `+column+` IN `+marks+` AND transfer_pair_id IS NOT NULL)`, args...)
	return wrap("store: release transfer pairs", err)
}

func (s *Store) ReplaceSplits(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error { return tx.replaceSplits(ctx, spaceID, t) })
}

func (s *Store) replaceSplits(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	var currency string
	if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM transactions WHERE id = ?`, t.ID).Scan(&currency); err != nil {
		return wrap("store: split currency", err)
	}
	if _, err := db.Write().ExecContext(ctx, `DELETE FROM transaction_splits WHERE transaction_id = ?`, t.ID); err != nil {
		return wrap("store: clear splits", err)
	}
	for i := range t.Splits {
		split := &t.Splits[i]
		if split.ID == uuid.Nil {
			split.ID = uuid.New()
		}
		split.SpaceID, split.TransactionID, split.Position = spaceID, t.ID, i
		amount, err := toMinor(split.Amount.Decimal(), currency)
		if err != nil {
			return err
		}
		now := db.Now()
		_, err = db.Write().ExecContext(ctx, `INSERT INTO transaction_splits
			(id,transaction_id,position,amount,category_id,memo,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			split.ID, t.ID, i, amount, NullUUID(split.CategoryID), NullText(split.Memo), now, now)
		if err != nil {
			return wrap("store: create split", err)
		}
		split.CreatedAt, split.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
		for _, tagID := range split.TagIDs {
			if _, err := db.Write().ExecContext(ctx, `INSERT INTO split_tags (split_id,tag_id) SELECT ?,id FROM tags WHERE id = ? ON CONFLICT DO NOTHING`, split.ID, tagID); err != nil {
				return wrap("store: tag split", err)
			}
		}
	}
	return nil
}

func (s *Store) SetTransactionTags(ctx context.Context, spaceID SpaceID, txnID uuid.UUID, tagIDs []uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error { return tx.setTransactionTags(ctx, spaceID, txnID, tagIDs) })
}

func (s *Store) setTransactionTags(ctx context.Context, spaceID SpaceID, txnID uuid.UUID, tagIDs []uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if _, err := db.Write().ExecContext(ctx, `DELETE FROM transaction_tags WHERE transaction_id = ?`, txnID); err != nil {
		return wrap("store: clear transaction tags", err)
	}
	for _, tagID := range tagIDs {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO transaction_tags (transaction_id,tag_id)
			SELECT t.id,tg.id FROM transactions t CROSS JOIN tags tg WHERE t.id = ? AND tg.id = ?
			ON CONFLICT DO NOTHING`, txnID, tagID); err != nil {
			return wrap("store: tag transaction", err)
		}
	}
	return nil
}

func scanTransaction(row scanner, spaceID SpaceID, primary string) (Transaction, error) {
	var t Transaction
	var externalID, notes, checkNumber, userFlag, userFlagNote, estimateStatus sql.Null[string]
	var categoryID, transferPairID, seriesID, ruleID, paddedTxnID sql.Null[uuid.UUID]
	var amount int64
	var amountPrimary, balance sql.Null[int64]
	var fxRate sql.Null[string]
	var date string
	var effectiveDate, seriesDueOn, acceptedOn, expiresOn, transactedOn sql.Null[string]
	var providerExtra []byte
	var checkedAt sql.Null[int64]
	var createdAt, updatedAt int64
	var source string
	err := row.Scan(&t.ID, &t.AccountID, &externalID, &date, &effectiveDate, &amount,
		&t.Currency, &amountPrimary, &fxRate, &t.StatementName, &t.Payee, &t.Memo, &notes,
		&checkNumber, &categoryID, &source, &t.IsPending, &t.IsDeleted, &t.IsReviewed,
		&t.ExcludedFromReports, &t.ExcludedFromSpendingPlan, &t.IsBill, &t.IsSubscription,
		&transferPairID, &userFlag, &userFlagNote, &seriesID, &seriesDueOn, &estimateStatus,
		&acceptedOn, &expiresOn, &ruleID, &balance, &t.NeedsSettle, &transactedOn, &providerExtra,
		&checkedAt, &t.CategoryCheckNote, &t.CategoryCheckRunID, &paddedTxnID,
		&t.ReceiptNotNeeded, &createdAt, &updatedAt)
	if err != nil {
		return Transaction{}, err
	}
	t.SpaceID, t.Source = spaceID, domain.Source(source)
	for _, field := range []struct {
		raw string
		dst *domain.Date
	}{
		{date, &t.Date}, {effectiveDate.V, &t.EffectiveDate}, {seriesDueOn.V, &t.SeriesDueOn},
		{acceptedOn.V, &t.AcceptedOn}, {expiresOn.V, &t.ExpiresOn}, {transactedOn.V, &t.TransactedOn},
	} {
		if field.raw == "" {
			continue
		}
		day, err := time.Parse("2006-01-02", field.raw)
		if err != nil {
			return Transaction{}, err
		}
		*field.dst = domain.DateOf(day)
	}
	t.ExternalID, t.Notes, t.CheckNumber = externalID.V, notes.V, checkNumber.V
	t.UserFlag, t.UserFlagNote, t.EstimateStatus = userFlag.V, userFlagNote.V, estimateStatus.V
	t.CategoryID, t.TransferPairID, t.PaddedTxnID = categoryID.V, transferPairID.V, paddedTxnID.V
	t.SeriesID, t.RuleID = seriesID.V, ruleID.V
	t.ProviderExtra = json.RawMessage(providerExtra)
	t.CategoryCheckedAt = nullableTime(checkedAt)
	t.CreatedAt, t.UpdatedAt = fromUnixMillis(createdAt), fromUnixMillis(updatedAt)
	value, err := fromMinor(amount, t.Currency)
	if err != nil {
		return Transaction{}, err
	}
	t.Amount = domain.FromDecimal(value)
	if amountPrimary.Valid {
		value, err = fromMinor(amountPrimary.V, primary)
		if err != nil {
			return Transaction{}, err
		}
		t.AmountPrimary, t.HasAmountPrimary = domain.FromDecimal(value), true
	}
	if balance.Valid {
		value, err = fromMinor(balance.V, t.Currency)
		if err != nil {
			return Transaction{}, err
		}
		t.Balance, t.HasBalance = domain.FromDecimal(value), true
	}
	if fxRate.Valid {
		t.FxRateUsed, err = decimal.NewFromString(fxRate.V)
		if err != nil {
			return Transaction{}, err
		}
		t.HasFxRateUsed = true
	}
	return t, nil
}

func scanSplit(row scanner, spaceID SpaceID) (Split, error) {
	var split Split
	var categoryID sql.Null[uuid.UUID]
	var memo sql.Null[string]
	var amount, createdAt, updatedAt int64
	var currency string
	if err := row.Scan(&split.ID, &split.TransactionID, &split.Position, &amount,
		&categoryID, &memo, &createdAt, &updatedAt, &currency); err != nil {
		return Split{}, err
	}
	split.SpaceID, split.CategoryID, split.Memo = spaceID, categoryID.V, memo.V
	split.CreatedAt, split.UpdatedAt = fromUnixMillis(createdAt), fromUnixMillis(updatedAt)
	value, err := fromMinor(amount, currency)
	if err != nil {
		return Split{}, err
	}
	split.Amount = domain.FromDecimal(value)
	return split, nil
}

type sqliteArgList struct{ values []any }

func (a *sqliteArgList) add(value any) string {
	a.values = append(a.values, value)
	return "?"
}

// --- Transfer activity -------------------------------------------------------

// TransferLeg is one side of a movement between two of the household's own
// accounts — narrow, so the transfers screen skips the split and tag queries.
type TransferLeg struct {
	TransactionID uuid.UUID
	// PairID is the shared token, nil on an unpaired candidate.
	PairID      uuid.UUID
	AccountID   uuid.UUID
	AccountName string
	// Date is the posted day, not the reporting day: an effective date would
	// file a card payment under its statement month, apart from its withdrawal.
	Date         domain.Date
	Amount       domain.Money
	Currency     string
	Payee        string
	Source       domain.Source
	PairedByHand bool
}

// TransferPairing selects paired rows, unpaired rows, or both.
type TransferPairing int

const (
	AnyPairing TransferPairing = iota
	OnlyPaired
	OnlyUnpaired
)

// TransferLegQuery narrows a leg listing. The zero value returns every live
// row in the space.
type TransferLegQuery struct {
	// From and To bound the posted date inclusively — see TransferLeg.Date.
	From    domain.Date
	To      domain.Date
	Pairing TransferPairing
	PairIDs []uuid.UUID
	IDs     []uuid.UUID
	Limit   int
}

func (s *Store) ListTransferLegs(ctx context.Context, spaceID SpaceID, q TransferLegQuery) ([]TransferLeg, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT t.id,t.transfer_pair_id,t.account_id,a.name,t.date,t.amount,t.currency,
		t.payee,t.source,(m.transfer_pair_id IS NOT NULL) FROM transactions t
		JOIN accounts a ON a.id=t.account_id LEFT JOIN manual_transfer_pairs m
		ON m.transfer_pair_id=t.transfer_pair_id WHERE ` + MoneyMovedOn("t")
	var args []any
	switch q.Pairing {
	case OnlyPaired:
		query += ` AND t.transfer_pair_id IS NOT NULL`
	case OnlyUnpaired:
		query += ` AND t.transfer_pair_id IS NULL`
	}
	for _, filter := range []struct {
		column string
		ids    []uuid.UUID
	}{{"t.transfer_pair_id", q.PairIDs}, {"t.id", q.IDs}} {
		if len(filter.ids) > 0 {
			marks, values := InArgs(filter.ids)
			query += ` AND ` + filter.column + ` IN ` + marks
			args = append(args, values...)
		}
	}
	if !q.From.IsZero() {
		query += ` AND t.date >= ?`
		args = append(args, q.From.String())
	}
	if !q.To.IsZero() {
		query += ` AND t.date <= ?`
		args = append(args, q.To.String())
	}
	query += ` ORDER BY t.date DESC,t.id`
	if q.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, q.Limit)
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list transfer legs", scanTransferLeg, query, args...)
}

// ListTransferPairIDs returns pair tokens, newest movement first. Pairs are
// paged rather than legs so a LIMIT never cuts a pair in half.
func (s *Store) ListTransferPairIDs(ctx context.Context, spaceID SpaceID, from, to domain.Date, limit int) ([]uuid.UUID, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT transfer_pair_id FROM transactions WHERE ` + MoneyMoved + ` AND transfer_pair_id IS NOT NULL`
	var args []any
	if !from.IsZero() {
		query += ` AND date >= ?`
		args = append(args, from.String())
	}
	if !to.IsZero() {
		query += ` AND date <= ?`
		args = append(args, to.String())
	}
	query += ` GROUP BY transfer_pair_id ORDER BY max(date) DESC,transfer_pair_id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list transfer pairs", scanValue[uuid.UUID], query, args...)
}

// errHalfPair rolls back a pair write that reached fewer than both rows.
var errHalfPair = errors.New("store: transfer pair would be half-written")

// PairTransactions writes one shared token on two rows, only if both are live
// money that moved and neither is paired yet; otherwise it writes nothing and
// reports false. A plan made from a snapshot can go stale, and a leg paired
// since must keep its token. byHand records that a person joined them; callers
// validate what the two rows are.
func (s *Store) PairTransactions(ctx context.Context, spaceID SpaceID, payingID, receivingID uuid.UUID, byHand bool) (uuid.UUID, bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return uuid.Nil, false, err
	}
	pairID := uuid.New()
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		result, err := db.Write().ExecContext(ctx, `UPDATE transactions SET transfer_pair_id=?,updated_at=?
			WHERE id IN (?,?) AND `+MoneyMoved+` AND transfer_pair_id IS NULL`, pairID, db.Now(), payingID, receivingID)
		if err != nil {
			return wrap("store: pair transactions", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 2 {
			return errHalfPair
		}
		if err := tx.fileTransferLegs(ctx, spaceID, []uuid.UUID{pairID}); err != nil {
			return err
		}
		if !byHand {
			return nil
		}
		_, err = db.Write().ExecContext(ctx, `INSERT INTO manual_transfer_pairs (transfer_pair_id,created_at) VALUES (?,?)`, pairID, db.Now())
		return wrap("store: mark transfer pair manual", err)
	})
	if errors.Is(err, errHalfPair) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return pairID, true, nil
}

// ForgetTransferPair drops the manual mark for a pair that has been released.
func (s *Store) ForgetTransferPair(ctx context.Context, spaceID SpaceID, pairID uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `DELETE FROM manual_transfer_pairs WHERE transfer_pair_id=?`, pairID)
	return wrap("store: forget transfer pair", err)
}

func scanTransferLeg(row scanner) (TransferLeg, error) {
	var leg TransferLeg
	var pairID sql.Null[uuid.UUID]
	var date, source string
	var amount int64
	if err := row.Scan(&leg.TransactionID, &pairID, &leg.AccountID, &leg.AccountName, &date, &amount, &leg.Currency, &leg.Payee, &source, &leg.PairedByHand); err != nil {
		return TransferLeg{}, err
	}
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return TransferLeg{}, err
	}
	value, err := fromMinor(amount, leg.Currency)
	if err != nil {
		return TransferLeg{}, err
	}
	leg.PairID, leg.Date, leg.Source, leg.Amount = pairID.V, domain.DateOf(day), domain.Source(source), domain.FromDecimal(value)
	return leg, nil
}

// SlotHolders reduces rows to what domain.SettledSlots decides on, dropping
// rows that hold no slot. Load with HoldsASlot, IncludeDeleted and
// IncludeEstimates.
func SlotHolders(rows []Transaction) []domain.SlotHolder {
	out := make([]domain.SlotHolder, 0, len(rows))
	for _, row := range rows {
		if row.SeriesID == uuid.Nil || row.SeriesDueOn.IsZero() {
			continue
		}
		out = append(out, domain.SlotHolder{
			SeriesID:   domainID(row.SeriesID),
			DueOn:      row.SeriesDueOn,
			On:         row.Date,
			IsForecast: row.EstimateStatus != "" && row.EstimateStatus != SkippedEstimate,
			IsSkipped:  row.EstimateStatus == SkippedEstimate || row.IsDeleted,
		})
	}
	return out
}

// SeriesSlotSettled names the charge that has already answered this
// occurrence, if one has, so "mark as paid" retried twice is refused.
//
// A forecast settles nothing: Simplifi materializes each upcoming reminder as
// a row dated on its due date and carrying its series. It holds the slot for
// projection but does not pay it — otherwise a forecast bill could never be
// filed under its own series. A skip tombstone is deleted, so it is excluded too.
func (s *Store) SeriesSlotSettled(ctx context.Context, spaceID SpaceID, seriesID uuid.UUID, dueOn domain.Date) (Transaction, bool, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return Transaction{}, false, err
	}
	var out Transaction
	var day string
	var amount int64
	err = db.Read().QueryRowContext(ctx, `SELECT id,date,payee,statement_name,amount,currency
		FROM transactions WHERE series_id=? AND series_due_on=? AND `+MoneyMoved+`
		ORDER BY created_at, rowid LIMIT 1`, seriesID, dueOn.String()).
		Scan(&out.ID, &day, &out.Payee, &out.StatementName, &amount, &out.Currency)
	if errors.Is(err, sql.ErrNoRows) {
		return Transaction{}, false, nil
	}
	if err != nil {
		return Transaction{}, false, wrap("store: read series slot", err)
	}
	date, err := time.Parse("2006-01-02", day)
	if err != nil {
		return Transaction{}, false, err
	}
	value, err := fromMinor(amount, out.Currency)
	if err != nil {
		return Transaction{}, false, err
	}
	out.SpaceID, out.Date, out.Amount = spaceID, domain.DateOf(date), domain.FromDecimal(value)
	out.SeriesID, out.SeriesDueOn = seriesID, dueOn
	return out, true, nil
}

// escapeLike keeps a user-typed search from turning % or _ into a wildcard.
func escapeLike(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(text)
}

// ListPayees is every distinct display payee in the space (payee, else
// statement name), most-used first. Payees are a column, not an entity, so a
// picker has nowhere else to read the full set from.
func (s *Store) ListPayees(ctx context.Context, spaceID SpaceID, limit int) ([]string, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list payees", scanValue[string], `
		SELECT name FROM (SELECT COALESCE(NULLIF(payee,''),statement_name) AS name,count(*) AS uses
		FROM transactions WHERE NOT is_deleted GROUP BY 1) AS payees
		WHERE name IS NOT NULL AND name<>'' ORDER BY uses DESC,name LIMIT ?`, limit)
}
