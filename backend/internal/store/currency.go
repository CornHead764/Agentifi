package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The FX stamping pass's narrow read and write. The pass selects only its rows
// and writes only its three columns, so it cannot revert a concurrent payee or
// notes edit from a stale copy.

// ListUnstampedForeign returns the space's rows in some other currency that
// carry no conversion yet — the only rows a stamping pass can act on.
func (s *Store) ListUnstampedForeign(ctx context.Context, spaceID SpaceID, primary string) ([]Transaction, error) {
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
	return sqliteQueryAll(ctx, db.Read(), "store: list unstamped foreign rows", func(row scanner) (Transaction, error) {
		return scanTransaction(row, spaceID, currency)
	}, `SELECT `+transactionColumns+` FROM transactions
		WHERE NOT is_deleted
		  AND currency <> '' AND currency <> ? AND amount_primary IS NULL`, primary)
}

// SetTransactionConversion writes only the stamping pass's own columns.
func (s *Store) SetTransactionConversion(
	ctx context.Context, spaceID SpaceID, id uuid.UUID,
	amountPrimary domain.Money, rate domain.Rate,
) error {
	scoped, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	currency, err := scoped.primaryCurrency()
	if err != nil {
		return err
	}
	db, err := scoped.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	minor, err := toMinor(amountPrimary.Decimal(), currency)
	if err != nil {
		return wrap("store: set transaction conversion", err)
	}
	return sqliteExecOne(ctx, db.Write(), "store: set transaction conversion", `
		UPDATE transactions
		SET amount_primary = ?, fx_rate_used = ?, updated_at = ?
		WHERE id = ?`, minor, decimaltext.Format(rate), db.Now(), id)
}

// ClearConversions drops every stored conversion in the space. The pass only
// fills nulls, so after a primary-currency change it must clear first or old
// conversions stay in the old currency.
func (s *Store) ClearConversions(ctx context.Context, spaceID SpaceID) (int64, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, err
	}
	tag, err := db.Write().ExecContext(ctx, `
		UPDATE transactions SET amount_primary = NULL, fx_rate_used = NULL, updated_at = ?
		 WHERE amount_primary IS NOT NULL`, db.Now())
	if err != nil {
		return 0, wrap("store: clear conversions", err)
	}
	n, err := tag.RowsAffected()
	return n, wrap("store: clear conversions", err)
}
