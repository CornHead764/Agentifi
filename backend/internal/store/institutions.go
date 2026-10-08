package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
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
	return queryAll(ctx, s.db, "store: list institutions", scanInstitution,
		`SELECT `+institutionColumns+` FROM institutions
		  WHERE space_id = $1 AND NOT is_deleted
		  ORDER BY lower(name)`,
		spaceID.UUID())
}

// SetInstitutionHideBelow sets or, with present false, clears one
// institution's small-balance threshold.
func (s *Store) SetInstitutionHideBelow(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, threshold domain.Money, present bool,
) (Institution, error) {
	row := s.db.QueryRow(ctx,
		`UPDATE institutions SET hide_below_balance = $3, updated_at = now()
		  WHERE space_id = $1 AND id = $2 AND NOT is_deleted
		  RETURNING `+institutionColumns,
		spaceID.UUID(), id, dbconv.NullMoney(threshold, present))
	one, err := scanInstitution(row)
	return one, wrap("store: set institution threshold", err)
}

func scanInstitution(row scanner) (Institution, error) {
	var (
		one       Institution
		logoURL   *string
		threshold dbconv.Number
	)
	if err := row.Scan(&one.ID, &one.Name, &logoURL, &threshold); err != nil {
		return Institution{}, err
	}
	one.LogoURL = Deref(logoURL)
	var err error
	one.HideBelowBalance, one.HasHideBelowBalance, err = dbconv.ReadNullMoney(
		threshold, "institutions.hide_below_balance")
	return one, err
}
