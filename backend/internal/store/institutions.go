package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Institution is the institutions row: one bank, as the sync first named it.
// Rows are made by EnsureInstitution and never by a client.
type Institution struct {
	ID      uuid.UUID
	Name    string
	LogoURL string
	// HideBelowBalance is the institution's small-balance threshold
	// (domain.HiddenForSmallBalance), absent when it has none.
	HideBelowBalance    domain.Money
	HasHideBelowBalance bool
}

const institutionColumns = `id, name, logo_url, hide_below_balance`

// ListInstitutions returns the space's live institutions, by name.
func (s *Store) ListInstitutions(ctx context.Context, spaceID SpaceID) ([]Institution, error) {
	db, currency, err := s.householdMoneyDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list institutions",
		func(row scanner) (Institution, error) { return scanInstitution(row, currency) },
		`SELECT `+institutionColumns+` FROM institutions WHERE NOT is_deleted ORDER BY lower(name)`)
}

// SetInstitutionHideBelow sets or, with present false, clears one
// institution's small-balance threshold.
func (s *Store) SetInstitutionHideBelow(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, threshold domain.Money, present bool,
) (Institution, error) {
	db, currency, err := s.householdMoneyDB(ctx, spaceID)
	if err != nil {
		return Institution{}, err
	}
	var value any
	if present {
		minor, err := toMinor(threshold, currency)
		if err != nil {
			return Institution{}, err
		}
		value = minor
	}
	row := db.Write().QueryRowContext(ctx,
		`UPDATE institutions SET hide_below_balance = ?, updated_at = ?
		  WHERE id = ? AND NOT is_deleted RETURNING `+institutionColumns,
		value, db.Now(), id)
	one, err := scanInstitution(row, currency)
	return one, wrap("store: set institution threshold", err)
}

func scanInstitution(row scanner, currency string) (Institution, error) {
	var one Institution
	var logoURL sql.Null[string]
	var threshold sql.Null[int64]
	if err := row.Scan(&one.ID, &one.Name, &logoURL, &threshold); err != nil {
		return Institution{}, err
	}
	one.LogoURL = logoURL.V
	if threshold.Valid {
		amount, err := fromMinor(threshold.V, currency)
		if err != nil {
			return Institution{}, err
		}
		one.HideBelowBalance, one.HasHideBelowBalance = amount, true
	}
	return one, nil
}
