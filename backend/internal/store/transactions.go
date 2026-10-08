package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
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

const transactionColumns = `id, space_id, account_id, external_id, date, effective_date, amount,
	currency, amount_primary, fx_rate_used, statement_name, payee, memo, notes, check_number,
	category_id, source, is_pending, is_deleted, is_reviewed, excluded_from_reports,
	excluded_from_spending_plan, is_bill, is_subscription, transfer_pair_id, user_flag,
	user_flag_note, series_id, series_due_on, estimate_status, accepted_on, expires_on, rule_id,
	balance, needs_settle, transacted_on, provider_extra, category_checked_at,
	category_check_note, category_check_run_id, padded_txn_id, receipt_not_needed, created_at,
	updated_at`

const splitColumns = `id, space_id, transaction_id, "position", amount, category_id, memo,
	created_at, updated_at`

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
	sql := &strings.Builder{}
	args := &argList{}
	fmt.Fprintf(sql, `SELECT `+transactionColumns+` FROM transactions WHERE space_id = %s`,
		args.add(spaceID.UUID()))

	if !q.IncludeDeleted {
		sql.WriteString(` AND NOT is_deleted`)
	}
	if q.ExcludePending {
		sql.WriteString(` AND NOT is_pending`)
	}
	if !q.IncludeEstimates {
		sql.WriteString(` AND estimate_status IS NULL`)
	}
	if len(q.IDs) > 0 {
		fmt.Fprintf(sql, ` AND id = ANY(%s)`, args.add(q.IDs))
	}
	if len(q.AccountIDs) > 0 {
		fmt.Fprintf(sql, ` AND account_id = ANY(%s)`, args.add(q.AccountIDs))
	}
	if len(q.CategoryIDs) > 0 {
		fmt.Fprintf(sql, ` AND category_id = ANY(%s)`, args.add(q.CategoryIDs))
	}
	if q.SeriesID != uuid.Nil {
		fmt.Fprintf(sql, ` AND series_id = %s`, args.add(q.SeriesID))
	}
	if q.HoldsASlot {
		sql.WriteString(` AND series_id IS NOT NULL AND series_due_on IS NOT NULL`)
	}
	if q.SearchText != "" {
		fmt.Fprintf(sql,
			` AND (payee ILIKE %s OR statement_name ILIKE %s)`,
			args.add("%"+escapeLike(q.SearchText)+"%"),
			args.add("%"+escapeLike(q.SearchText)+"%"))
	}

	// An unset effective date means the posted date.
	dateExpr := `date`
	if q.DateMode == domain.DateEffective {
		dateExpr = `COALESCE(effective_date, date)`
	}
	if !q.From.IsZero() {
		fmt.Fprintf(sql, ` AND %s >= %s`, dateExpr, args.add(q.From.Time()))
	}
	if !q.To.IsZero() {
		fmt.Fprintf(sql, ` AND %s <= %s`, dateExpr, args.add(q.To.Time()))
	}

	sql.WriteString(` ORDER BY ` + dateExpr + ` DESC, created_at DESC, id`)
	if q.Limit > 0 {
		fmt.Fprintf(sql, ` LIMIT %s`, args.add(q.Limit))
	}
	if q.Offset > 0 {
		fmt.Fprintf(sql, ` OFFSET %s`, args.add(q.Offset))
	}

	txns, err := queryAll(ctx, s.db, "store: list transactions", scanTransaction, sql.String(), args.values...)
	if err != nil {
		return nil, err
	}
	return txns, s.attachAllocations(ctx, spaceID, txns)
}

func (s *Store) GetTransaction(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Transaction, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+transactionColumns+` FROM transactions WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	txn, err := scanTransaction(row)
	if err != nil {
		return Transaction{}, wrap("store: get transaction", err)
	}
	list := []Transaction{txn}
	if err := s.attachAllocations(ctx, spaceID, list); err != nil {
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
	txn, err := scanTransaction(s.db.QueryRow(ctx,
		`SELECT `+transactionColumns+` FROM transactions
		  WHERE space_id = $1 AND account_id = $2 AND external_id = $3`,
		spaceID.UUID(), accountID, externalID))
	return txn, wrap("store: transaction by external id", err)
}

// attachAllocations fills in splits and tags for rows already read. Every path
// that returns transactions goes through here, so no screen misses split tags.
func (s *Store) attachAllocations(ctx context.Context, spaceID SpaceID, txns []Transaction) error {
	if len(txns) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(txns))
	index := make(map[uuid.UUID]int, len(txns))
	for i, txn := range txns {
		ids[i] = txn.ID
		index[txn.ID] = i
	}

	splits, err := queryAll(ctx, s.db, "store: load splits", scanSplit,
		`SELECT `+splitColumns+` FROM transaction_splits
		 WHERE space_id = $1 AND transaction_id = ANY($2) ORDER BY "position"`,
		spaceID.UUID(), ids)
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

	if len(splitIDs) > 0 {
		tagged, err := queryAll(ctx, s.db, "store: load split tags", scanPair[uuid.UUID, uuid.UUID], `
			SELECT st.split_id, st.tag_id
			FROM split_tags st
			JOIN transaction_splits s ON s.id = st.split_id
			WHERE s.space_id = $1 AND st.split_id = ANY($2)
			ORDER BY st.tag_id`, spaceID.UUID(), splitIDs)
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

	tagged, err := queryAll(ctx, s.db, "store: load transaction tags", scanPair[uuid.UUID, uuid.UUID], `
		SELECT tt.transaction_id, tt.tag_id
		FROM transaction_tags tt
		JOIN transactions t ON t.id = tt.transaction_id
		WHERE t.space_id = $1 AND tt.transaction_id = ANY($2)
		ORDER BY tt.tag_id`, spaceID.UUID(), ids)
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
	return s.InTx(ctx, func(tx *Store) error {
		if t.ID == uuid.Nil {
			t.ID = uuid.New()
		}
		t.SpaceID = spaceID
		err := tx.db.QueryRow(ctx, `
			INSERT INTO transactions (id, space_id, account_id, external_id, date, effective_date,
				amount, currency, amount_primary, fx_rate_used, statement_name, payee, memo, notes,
				check_number, category_id, source, is_pending, is_deleted, is_reviewed,
				excluded_from_reports, excluded_from_spending_plan, is_bill, is_subscription,
				transfer_pair_id, user_flag, user_flag_note, series_id, series_due_on,
				estimate_status, accepted_on, expires_on, rule_id, balance, needs_settle,
				transacted_on, provider_extra)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
				$18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33,
				$34, $35, $36, $37)
			RETURNING created_at, updated_at`,
			t.ID, spaceID.UUID(), t.AccountID, dbconv.NullText(t.ExternalID), t.Date.Time(),
			dbconv.NullDate(t.EffectiveDate), dbconv.Money(t.Amount), t.Currency,
			dbconv.NullMoney(t.AmountPrimary, t.HasAmountPrimary),
			dbconv.NullNumeric(t.FxRateUsed, t.HasFxRateUsed), t.StatementName, t.Payee, t.Memo,
			dbconv.NullText(t.Notes), dbconv.NullText(t.CheckNumber), dbconv.NullUUID(t.CategoryID), string(t.Source),
			t.IsPending, t.IsDeleted, t.IsReviewed, t.ExcludedFromReports,
			t.ExcludedFromSpendingPlan, t.IsBill, t.IsSubscription, dbconv.NullUUID(t.TransferPairID),
			dbconv.NullText(t.UserFlag), dbconv.NullText(t.UserFlagNote), dbconv.NullUUID(t.SeriesID),
			dbconv.NullDate(t.SeriesDueOn), dbconv.NullText(t.EstimateStatus), dbconv.NullDate(t.AcceptedOn),
			dbconv.NullDate(t.ExpiresOn), dbconv.NullUUID(t.RuleID), dbconv.NullMoney(t.Balance, t.HasBalance),
			t.NeedsSettle, dbconv.NullDate(t.TransactedOn), jsonArg(t.ProviderExtra),
		).Scan(&t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return wrap("store: create transaction", err)
		}
		if err := tx.replaceSplits(ctx, spaceID, t); err != nil {
			return err
		}
		if err := tx.setTransactionTags(ctx, spaceID, t.ID, t.TagIDs); err != nil {
			return err
		}
		return tx.refreshRowReceipts(ctx, spaceID, t)
	})
}

// UpdateTransaction rewrites the row and its allocations together.
func (s *Store) UpdateTransaction(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	return s.InTx(ctx, func(tx *Store) error {
		err := tx.db.QueryRow(ctx, `
			UPDATE transactions SET
				account_id = $3, external_id = $4, date = $5, effective_date = $6, amount = $7,
				currency = $8, amount_primary = $9, fx_rate_used = $10, statement_name = $11,
				payee = $12, memo = $13, notes = $14, check_number = $15, category_id = $16,
				source = $17, is_pending = $18, is_deleted = $19, is_reviewed = $20,
				excluded_from_reports = $21, excluded_from_spending_plan = $22, is_bill = $23,
				is_subscription = $24, transfer_pair_id = $25, user_flag = $26,
				user_flag_note = $27, series_id = $28, series_due_on = $29, estimate_status = $30,
				accepted_on = $31, expires_on = $32, rule_id = $33, balance = $34,
				transacted_on = $35, provider_extra = $36, receipt_not_needed = $37,
				updated_at = now()
			WHERE space_id = $1 AND id = $2
			RETURNING updated_at`,
			spaceID.UUID(), t.ID, t.AccountID, dbconv.NullText(t.ExternalID), t.Date.Time(),
			dbconv.NullDate(t.EffectiveDate), dbconv.Money(t.Amount), t.Currency,
			dbconv.NullMoney(t.AmountPrimary, t.HasAmountPrimary),
			dbconv.NullNumeric(t.FxRateUsed, t.HasFxRateUsed), t.StatementName, t.Payee, t.Memo,
			dbconv.NullText(t.Notes), dbconv.NullText(t.CheckNumber), dbconv.NullUUID(t.CategoryID), string(t.Source),
			t.IsPending, t.IsDeleted, t.IsReviewed, t.ExcludedFromReports,
			t.ExcludedFromSpendingPlan, t.IsBill, t.IsSubscription, dbconv.NullUUID(t.TransferPairID),
			dbconv.NullText(t.UserFlag), dbconv.NullText(t.UserFlagNote), dbconv.NullUUID(t.SeriesID),
			dbconv.NullDate(t.SeriesDueOn), dbconv.NullText(t.EstimateStatus), dbconv.NullDate(t.AcceptedOn),
			dbconv.NullDate(t.ExpiresOn), dbconv.NullUUID(t.RuleID), dbconv.NullMoney(t.Balance, t.HasBalance),
			dbconv.NullDate(t.TransactedOn), jsonArg(t.ProviderExtra), t.ReceiptNotNeeded,
		).Scan(&t.UpdatedAt)
		if err != nil {
			return wrap("store: update transaction", err)
		}
		if err := tx.replaceSplits(ctx, spaceID, t); err != nil {
			return err
		}
		// A refund and its charge share a category, so a category set on
		// either lands on both, inside the same transaction.
		if err := tx.RefileLinkedRefunds(ctx, spaceID, t.ID, dbconv.NullUUID(t.CategoryID)); err != nil {
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
func (s *Store) SetTransactionBalances(
	ctx context.Context, spaceID SpaceID, balances map[uuid.UUID]domain.Money,
) error {
	if len(balances) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(balances))
	amounts := make([]dbconv.Number, 0, len(balances))
	for id, balance := range balances {
		ids = append(ids, id)
		amounts = append(amounts, dbconv.Money(balance))
	}
	_, err := s.db.Exec(ctx, `
		UPDATE transactions AS t
		SET balance = v.balance, updated_at = now()
		FROM unnest($2::uuid[], $3::numeric[]) AS v(id, balance)
		WHERE t.space_id = $1 AND t.id = v.id`,
		spaceID.UUID(), ids, amounts)
	return wrap("store: set transaction balances", err)
}

// SetTransactionsReviewed flips the review flag on many rows in one statement.
func (s *Store) SetTransactionsReviewed(
	ctx context.Context, spaceID SpaceID, ids []uuid.UUID, reviewed bool,
) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
		UPDATE transactions SET is_reviewed = $3, updated_at = now()
		WHERE space_id = $1 AND id = ANY($2) AND is_deleted = false`,
		spaceID.UUID(), ids, reviewed)
	return wrap("store: set transactions reviewed", err)
}

// TransactionsNeedingSettle returns ids of rows never settled, oldest first.
// Deleted rows are included: a retired pending charge still owes its account
// a recomputed balance.
func (s *Store) TransactionsNeedingSettle(ctx context.Context, spaceID SpaceID) ([]uuid.UUID, error) {
	return queryAll(ctx, s.db, "store: transactions needing settle", scanValue[uuid.UUID], `
		SELECT id FROM transactions
		WHERE space_id = $1 AND needs_settle
		ORDER BY created_at, id`, spaceID.UUID())
}

// ClearNeedsSettle drops the mark from settled rows in one statement.
func (s *Store) ClearNeedsSettle(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
		UPDATE transactions SET needs_settle = false
		WHERE space_id = $1 AND id = ANY($2) AND needs_settle`,
		spaceID.UUID(), ids)
	return wrap("store: clear needs settle", err)
}

// DeleteTransaction soft-deletes and releases every link the row is half of:
// its transfer pair (trap 2) and its refund links, which a foreign
// key would only cover on a hard delete. The income row padding a purchase is
// deleted with it: it only says the paycheck was net of that purchase.
func (s *Store) DeleteTransaction(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		pads, err := queryAll(ctx, tx.db, "store: pads of deleted row", scanValue[uuid.UUID], `
			SELECT id FROM transactions
			WHERE space_id = $1 AND padded_txn_id = $2 AND NOT is_deleted`, spaceID.UUID(), id)
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
	return s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.db.Exec(ctx, `
			UPDATE transactions SET padded_txn_id = NULL, updated_at = now()
			WHERE space_id = $1 AND padded_txn_id = $2`, spaceID.UUID(), id); err != nil {
			return wrap("store: release pads", err)
		}
		return tx.retire(ctx, spaceID, id, true)
	})
}

func (s *Store) retire(ctx context.Context, spaceID SpaceID, id uuid.UUID, clearExternalID bool) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.releaseTransferPairs(ctx, spaceID, `id = ANY($2)`, []uuid.UUID{id}); err != nil {
			return err
		}
		if err := tx.releaseRefundLinks(ctx, spaceID, id); err != nil {
			return err
		}
		if _, err := tx.db.Exec(ctx, `
			UPDATE bill_payments SET transaction_id = NULL, updated_at = now()
			WHERE space_id = $1 AND transaction_id = $2`, spaceID.UUID(), id); err != nil {
			return wrap("store: release bill payment", err)
		}
		if err := tx.execOne(ctx, "store: delete transaction", `
			UPDATE transactions
			SET is_deleted = true, transfer_pair_id = NULL, padded_txn_id = NULL,
			    external_id = CASE WHEN $3 THEN NULL ELSE external_id END,
			    updated_at = now()
			WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, clearExternalID); err != nil {
			return err
		}
		_, _, err := tx.ReconcileReceipts(ctx, spaceID, []uuid.UUID{id})
		return err
	})
}

// LinkPadding records that the income row pads the purchase. Both rows are
// re-checked against the space and must be live; a pad already linked, or a
// purchase already padded, is left as it is. Idempotent.
func (s *Store) LinkPadding(ctx context.Context, spaceID SpaceID, padID, purchaseID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE transactions pad SET padded_txn_id = purchase.id, updated_at = now()
		FROM transactions purchase
		WHERE pad.space_id = $1 AND pad.id = $2 AND `+MoneyMovedOn("pad")+`
		  AND purchase.space_id = $1 AND purchase.id = $3 AND `+MoneyMovedOn("purchase")+`
		  AND pad.id <> purchase.id AND pad.padded_txn_id IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM transactions other
		      WHERE other.space_id = $1 AND other.padded_txn_id = purchase.id)`,
		spaceID.UUID(), padID, purchaseID)
	return wrap("store: link padding", err)
}

// PaddingOf maps each of these purchases to the live income row padding it.
func (s *Store) PaddingOf(ctx context.Context, spaceID SpaceID, purchaseIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if len(purchaseIDs) == 0 {
		return out, nil
	}
	pairs, err := queryAll(ctx, s.db, "store: padding of", scanPair[uuid.UUID, uuid.UUID], `
		SELECT padded_txn_id, id FROM transactions
		WHERE space_id = $1 AND padded_txn_id = ANY($2) AND NOT is_deleted`,
		spaceID.UUID(), purchaseIDs)
	if err != nil {
		return nil, err
	}
	for _, pair := range pairs {
		out[pair.first] = pair.second
	}
	return out, nil
}

// releaseTransferPairs releases both legs of every pair with a leg among the
// rows predicate selects ($2 is ids). transfer_pair_id is a token shared by
// both legs, not either leg's id, so partners are found by the token.
func (s *Store) releaseTransferPairs(ctx context.Context, spaceID SpaceID, predicate string, ids []uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE transactions SET `+releasePairSet+`
		WHERE space_id = $1 AND transfer_pair_id IN (
			SELECT transfer_pair_id FROM transactions
			WHERE space_id = $1 AND `+predicate+` AND transfer_pair_id IS NOT NULL)`,
		spaceID.UUID(), ids)
	return wrap("store: release transfer pairs", err)
}

func (s *Store) ReplaceSplits(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	return s.InTx(ctx, func(tx *Store) error { return tx.replaceSplits(ctx, spaceID, t) })
}

func (s *Store) replaceSplits(ctx context.Context, spaceID SpaceID, t *Transaction) error {
	// split_tags has no space_id; reach it through the split rows.
	if _, err := s.db.Exec(ctx, `
		DELETE FROM split_tags WHERE split_id IN (
			SELECT id FROM transaction_splits WHERE space_id = $1 AND transaction_id = $2)`,
		spaceID.UUID(), t.ID); err != nil {
		return wrap("store: clear split tags", err)
	}
	if _, err := s.db.Exec(ctx,
		`DELETE FROM transaction_splits WHERE space_id = $1 AND transaction_id = $2`,
		spaceID.UUID(), t.ID); err != nil {
		return wrap("store: clear splits", err)
	}

	for i := range t.Splits {
		split := &t.Splits[i]
		if split.ID == uuid.Nil {
			split.ID = uuid.New()
		}
		split.SpaceID = spaceID
		split.TransactionID = t.ID
		split.Position = i
		err := s.db.QueryRow(ctx, `
			INSERT INTO transaction_splits (id, space_id, transaction_id, "position", amount,
				category_id, memo)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING created_at, updated_at`,
			split.ID, spaceID.UUID(), t.ID, split.Position, dbconv.Money(split.Amount),
			dbconv.NullUUID(split.CategoryID), dbconv.NullText(split.Memo),
		).Scan(&split.CreatedAt, &split.UpdatedAt)
		if err != nil {
			return wrap("store: create split", err)
		}
		for _, tagID := range split.TagIDs {
			// A foreign-space tag id inserts nothing.
			if _, err := s.db.Exec(ctx,
				`INSERT INTO split_tags (split_id, tag_id)
				 SELECT $1, id FROM tags WHERE space_id = $2 AND id = $3
				 ON CONFLICT DO NOTHING`, split.ID, spaceID.UUID(), tagID); err != nil {
				return wrap("store: tag split", err)
			}
		}
	}
	return nil
}

func (s *Store) SetTransactionTags(ctx context.Context, spaceID SpaceID, txnID uuid.UUID, tagIDs []uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		return tx.setTransactionTags(ctx, spaceID, txnID, tagIDs)
	})
}

func (s *Store) setTransactionTags(ctx context.Context, spaceID SpaceID, txnID uuid.UUID, tagIDs []uuid.UUID) error {
	// transaction_tags carries no space_id, so both statements reach the
	// tenant through the rows that do.
	if _, err := s.db.Exec(ctx, `
		DELETE FROM transaction_tags
		WHERE transaction_id IN (SELECT id FROM transactions WHERE space_id = $1 AND id = $2)`,
		spaceID.UUID(), txnID); err != nil {
		return wrap("store: clear transaction tags", err)
	}
	for _, tagID := range tagIDs {
		// Both sides of the join prove their tenancy, not just the tag.
		if _, err := s.db.Exec(ctx, `
			INSERT INTO transaction_tags (transaction_id, tag_id)
			SELECT t.id, tg.id
			FROM transactions t
			JOIN tags tg ON tg.space_id = t.space_id
			WHERE t.space_id = $1 AND t.id = $2 AND tg.id = $3
			ON CONFLICT DO NOTHING`, spaceID.UUID(), txnID, tagID); err != nil {
			return wrap("store: tag transaction", err)
		}
	}
	return nil
}

func scanTransaction(row scanner) (Transaction, error) {
	var (
		t                                                 Transaction
		spaceID                                           uuid.UUID
		externalID, notes, checkNumber                    *string
		userFlag, userFlagNote, estimateStatus            *string
		categoryID, transferPairID, seriesID, ruleID      *uuid.UUID
		paddedTxnID                                       *uuid.UUID
		amount, amountPrimary, fxRate, balance            dbconv.Number
		effectiveDate, seriesDueOn, acceptedOn, expiresOn *time.Time
		transactedOn                                      *time.Time
		providerExtra                                     []byte
		date                                              time.Time
		source                                            string
	)
	err := row.Scan(&t.ID, &spaceID, &t.AccountID, &externalID, &date, &effectiveDate, &amount,
		&t.Currency, &amountPrimary, &fxRate, &t.StatementName, &t.Payee, &t.Memo, &notes,
		&checkNumber, &categoryID, &source, &t.IsPending, &t.IsDeleted, &t.IsReviewed,
		&t.ExcludedFromReports, &t.ExcludedFromSpendingPlan, &t.IsBill, &t.IsSubscription,
		&transferPairID, &userFlag, &userFlagNote, &seriesID, &seriesDueOn, &estimateStatus,
		&acceptedOn, &expiresOn, &ruleID, &balance, &t.NeedsSettle, &transactedOn, &providerExtra,
		&t.CategoryCheckedAt, &t.CategoryCheckNote, &t.CategoryCheckRunID, &paddedTxnID,
		&t.ReceiptNotNeeded, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Transaction{}, err
	}
	t.TransactedOn = dbconv.ReadNullDate(transactedOn)
	if len(providerExtra) > 0 {
		t.ProviderExtra = json.RawMessage(providerExtra)
	}

	t.SpaceID = SpaceID(spaceID)
	t.Source = domain.Source(source)
	t.Date = dateOf(date)
	t.EffectiveDate = dbconv.ReadNullDate(effectiveDate)
	t.SeriesDueOn = dbconv.ReadNullDate(seriesDueOn)
	t.AcceptedOn = dbconv.ReadNullDate(acceptedOn)
	t.ExpiresOn = dbconv.ReadNullDate(expiresOn)
	t.ExternalID = Deref(externalID)
	t.Notes = Deref(notes)
	t.CheckNumber = Deref(checkNumber)
	t.UserFlag = Deref(userFlag)
	t.UserFlagNote = Deref(userFlagNote)
	t.EstimateStatus = Deref(estimateStatus)
	t.CategoryID = Deref(categoryID)
	t.TransferPairID = Deref(transferPairID)
	t.PaddedTxnID = Deref(paddedTxnID)
	t.SeriesID = Deref(seriesID)
	t.RuleID = Deref(ruleID)

	if t.Amount, err = dbconv.ReadMoney(amount, "transactions.amount"); err != nil {
		return Transaction{}, err
	}
	if t.AmountPrimary, t.HasAmountPrimary, err = dbconv.ReadNullMoney(amountPrimary, "transactions.amount_primary"); err != nil {
		return Transaction{}, err
	}
	if t.FxRateUsed, t.HasFxRateUsed, err = dbconv.ReadNullDecimal(fxRate, "transactions.fx_rate_used"); err != nil {
		return Transaction{}, err
	}
	if t.Balance, t.HasBalance, err = dbconv.ReadNullMoney(balance, "transactions.balance"); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

func scanSplit(row scanner) (Split, error) {
	var (
		split      Split
		spaceID    uuid.UUID
		categoryID *uuid.UUID
		memo       *string
		amount     dbconv.Number
	)
	err := row.Scan(&split.ID, &spaceID, &split.TransactionID, &split.Position, &amount,
		&categoryID, &memo, &split.CreatedAt, &split.UpdatedAt)
	if err != nil {
		return Split{}, err
	}
	split.SpaceID = SpaceID(spaceID)
	split.Memo = Deref(memo)
	split.CategoryID = Deref(categoryID)
	if split.Amount, err = dbconv.ReadMoney(amount, "transaction_splits.amount"); err != nil {
		return Split{}, err
	}
	return split, nil
}

// argList numbers placeholders as they are added.
type argList struct {
	values []any
}

func (a *argList) add(value any) string {
	a.values = append(a.values, value)
	return fmt.Sprintf("$%d", len(a.values))
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

func (s *Store) ListTransferLegs(
	ctx context.Context, spaceID SpaceID, q TransferLegQuery,
) ([]TransferLeg, error) {
	sql := &strings.Builder{}
	args := &argList{}
	// The manual mark is joined on the space too, so one household's pair
	// cannot label another's row.
	fmt.Fprintf(sql, `
		SELECT t.id, t.transfer_pair_id, t.account_id, a.name, t.date, t.amount, t.currency,
			t.payee, t.source, (m.transfer_pair_id IS NOT NULL)
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id AND a.space_id = t.space_id
		LEFT JOIN manual_transfer_pairs m
			ON m.transfer_pair_id = t.transfer_pair_id AND m.space_id = t.space_id
		WHERE t.space_id = %s AND `+MoneyMovedOn("t"), args.add(spaceID.UUID()))

	switch q.Pairing {
	case OnlyPaired:
		sql.WriteString(` AND t.transfer_pair_id IS NOT NULL`)
	case OnlyUnpaired:
		sql.WriteString(` AND t.transfer_pair_id IS NULL`)
	}
	if len(q.PairIDs) > 0 {
		fmt.Fprintf(sql, ` AND t.transfer_pair_id = ANY(%s)`, args.add(q.PairIDs))
	}
	if len(q.IDs) > 0 {
		fmt.Fprintf(sql, ` AND t.id = ANY(%s)`, args.add(q.IDs))
	}
	if !q.From.IsZero() {
		fmt.Fprintf(sql, ` AND t.date >= %s`, args.add(q.From.Time()))
	}
	if !q.To.IsZero() {
		fmt.Fprintf(sql, ` AND t.date <= %s`, args.add(q.To.Time()))
	}
	sql.WriteString(` ORDER BY t.date DESC, t.id`)
	if q.Limit > 0 {
		fmt.Fprintf(sql, ` LIMIT %s`, args.add(q.Limit))
	}

	return queryAll(ctx, s.db, "store: list transfer legs", scanTransferLeg, sql.String(), args.values...)
}

// ListTransferPairIDs returns pair tokens, newest movement first. Pairs are
// paged rather than legs so a LIMIT never cuts a pair in half.
func (s *Store) ListTransferPairIDs(
	ctx context.Context, spaceID SpaceID, from, to domain.Date, limit int,
) ([]uuid.UUID, error) {
	sql := &strings.Builder{}
	args := &argList{}
	fmt.Fprintf(sql, `
		SELECT transfer_pair_id, max(date) AS moved_on
		FROM transactions
		WHERE space_id = %s AND `+MoneyMoved+` AND transfer_pair_id IS NOT NULL`,
		args.add(spaceID.UUID()))
	if !from.IsZero() {
		fmt.Fprintf(sql, ` AND date >= %s`, args.add(from.Time()))
	}
	if !to.IsZero() {
		fmt.Fprintf(sql, ` AND date <= %s`, args.add(to.Time()))
	}
	sql.WriteString(` GROUP BY transfer_pair_id ORDER BY moved_on DESC, transfer_pair_id`)
	if limit > 0 {
		fmt.Fprintf(sql, ` LIMIT %s`, args.add(limit))
	}

	return queryAll(ctx, s.db, "store: list transfer pairs", func(row scanner) (uuid.UUID, error) {
		var id uuid.UUID
		var movedOn time.Time
		return id, row.Scan(&id, &movedOn)
	}, sql.String(), args.values...)
}

// errHalfPair rolls back a pair write that reached fewer than both rows.
var errHalfPair = errors.New("store: transfer pair would be half-written")

// PairTransactions writes one shared token on two rows, only if both are live
// money that moved and neither is paired yet; otherwise it writes nothing and
// reports false. A plan made from a snapshot can go stale, and a leg paired
// since must keep its token. byHand records that a person joined them; callers
// validate what the two rows are.
func (s *Store) PairTransactions(
	ctx context.Context, spaceID SpaceID, payingID, receivingID uuid.UUID, byHand bool,
) (uuid.UUID, bool, error) {
	pairID := uuid.New()
	err := s.InTx(ctx, func(tx *Store) error {
		result, err := tx.db.Exec(ctx, `
			UPDATE transactions SET transfer_pair_id = $3, updated_at = now()
			WHERE space_id = $1 AND id = ANY($2)
			  AND `+MoneyMoved+` AND transfer_pair_id IS NULL`,
			spaceID.UUID(), []uuid.UUID{payingID, receivingID}, pairID)
		if err != nil {
			return wrap("store: pair transactions", err)
		}
		if result.RowsAffected() != 2 {
			return errHalfPair
		}
		if err := tx.fileTransferLegs(ctx, spaceID, []uuid.UUID{pairID}); err != nil {
			return err
		}
		if !byHand {
			return nil
		}
		_, err = tx.db.Exec(ctx,
			`INSERT INTO manual_transfer_pairs (transfer_pair_id, space_id) VALUES ($1, $2)`,
			pairID, spaceID.UUID())
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
	_, err := s.db.Exec(ctx,
		`DELETE FROM manual_transfer_pairs WHERE space_id = $1 AND transfer_pair_id = $2`,
		spaceID.UUID(), pairID)
	return wrap("store: forget transfer pair", err)
}

func scanTransferLeg(row scanner) (TransferLeg, error) {
	var (
		leg    TransferLeg
		pairID *uuid.UUID
		date   time.Time
		amount dbconv.Number
		source string
	)
	err := row.Scan(&leg.TransactionID, &pairID, &leg.AccountID, &leg.AccountName, &date,
		&amount, &leg.Currency, &leg.Payee, &source, &leg.PairedByHand)
	if err != nil {
		return TransferLeg{}, err
	}
	leg.PairID = Deref(pairID)
	leg.Date = dateOf(date)
	leg.Source = domain.Source(source)
	if leg.Amount, err = dbconv.ReadMoney(amount, "transactions.amount"); err != nil {
		return TransferLeg{}, err
	}
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
func (s *Store) SeriesSlotSettled(
	ctx context.Context, spaceID SpaceID, seriesID uuid.UUID, dueOn domain.Date,
) (Transaction, bool, error) {
	var (
		out    Transaction
		payee  *string
		date   time.Time
		amount dbconv.Number
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, date, payee, statement_name, amount
		FROM transactions
		WHERE space_id = $1 AND series_id = $2 AND series_due_on = $3
		  AND `+MoneyMoved+`
		ORDER BY created_at
		LIMIT 1`, spaceID.UUID(), seriesID, dueOn.Time()).
		Scan(&out.ID, &date, &payee, &out.StatementName, &amount)
	if errors.Is(err, sqlitedb.ErrNoRows) {
		return Transaction{}, false, nil
	}
	if err != nil {
		return Transaction{}, false, wrap("store: read series slot", err)
	}
	out.Date = dateOf(date)
	out.Payee = Deref(payee)
	out.SeriesID, out.SeriesDueOn = seriesID, dueOn
	if out.Amount, err = dbconv.ReadMoney(amount, "transactions.amount"); err != nil {
		return Transaction{}, false, err
	}
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
	if limit <= 0 {
		limit = 500
	}
	return queryAll(ctx, s.db, "store: list payees", scanValue[string], `
		SELECT name FROM (
			SELECT COALESCE(NULLIF(payee, ''), statement_name) AS name, count(*) AS uses
			  FROM transactions
			 WHERE space_id = $1 AND NOT is_deleted
			 GROUP BY 1
		) AS payees
		 WHERE name IS NOT NULL AND name <> ''
		 ORDER BY uses DESC, name
		 LIMIT $2`, spaceID.UUID(), limit)
}
