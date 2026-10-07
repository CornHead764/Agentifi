package store

import (
	"context"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The FX stamping pass's narrow read and write. The pass selects only its rows
// and writes only its three columns, so it cannot revert a concurrent payee or
// notes edit from a stale copy.

// ListUnstampedForeign returns the space's rows in some other currency that
// carry no conversion yet — the only rows a stamping pass can act on.
func (s *Store) ListUnstampedForeign(ctx context.Context, spaceID SpaceID, primary string) ([]Transaction, error) {
	return queryAll(ctx, s.db, "store: list unstamped foreign rows", scanTransaction, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE space_id = $1 AND NOT is_deleted
		  AND currency <> '' AND currency <> $2 AND amount_primary IS NULL`,
		spaceID.UUID(), primary)
}

// SetTransactionConversion writes only the stamping pass's own columns.
func (s *Store) SetTransactionConversion(
	ctx context.Context, spaceID SpaceID, id uuid.UUID,
	amountPrimary domain.Money, rate domain.Rate,
) error {
	return s.execOne(ctx, "store: set transaction conversion", `
		UPDATE transactions
		SET amount_primary = $3, fx_rate_used = $4, updated_at = now()
		WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, pgconv.Money(amountPrimary), pgconv.NullNumeric(rate, true))
}

// ClearConversions drops every stored conversion in the space. The pass only
// fills nulls, so after a primary-currency change it must clear first or old
// conversions stay in the old currency.
func (s *Store) ClearConversions(ctx context.Context, spaceID SpaceID) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE transactions SET amount_primary = NULL, fx_rate_used = NULL, updated_at = now()
		 WHERE space_id = $1 AND amount_primary IS NOT NULL`, spaceID.UUID())
	if err != nil {
		return 0, wrap("store: clear conversions", err)
	}
	return tag.RowsAffected(), nil
}
