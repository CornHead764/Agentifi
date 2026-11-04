package store

import (
	"context"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/google/uuid"
)

// PaidMark is a person's word that one occurrence of a series was paid or
// received, with no transaction written for it. DueOn is the scheduled slot.
type PaidMark struct {
	SeriesID uuid.UUID
	DueOn    domain.Date
	MarkedOn domain.Date
}

// MarkSlotPaid records the mark. False when the slot was already marked, which
// keeps the first.
func (s *Store) MarkSlotPaid(ctx context.Context, spaceID SpaceID, seriesID uuid.UUID, dueOn, markedOn domain.Date) (bool, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return false, err
	}
	now := db.Now()
	result, err := db.Write().ExecContext(ctx, `INSERT INTO series_paid_marks (series_id, due_on, marked_on, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (series_id, due_on) DO NOTHING`,
		seriesID, dueOn.String(), markedOn.String(), now, now)
	if err != nil {
		return false, wrap("store: mark slot paid", err)
	}
	n, err := result.RowsAffected()
	return n > 0, wrap("store: mark slot paid", err)
}

// UnmarkSlotPaid removes the mark. False when there was none.
func (s *Store) UnmarkSlotPaid(ctx context.Context, spaceID SpaceID, seriesID uuid.UUID, dueOn domain.Date) (bool, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return false, err
	}
	result, err := db.Write().ExecContext(ctx,
		`DELETE FROM series_paid_marks WHERE series_id = ? AND due_on = ?`, seriesID, dueOn.String())
	if err != nil {
		return false, wrap("store: unmark slot paid", err)
	}
	n, err := result.RowsAffected()
	return n > 0, wrap("store: unmark slot paid", err)
}

// ListPaidMarks is every mark in the space, or one series' when seriesID is
// set.
func (s *Store) ListPaidMarks(ctx context.Context, spaceID SpaceID, seriesID uuid.UUID) ([]PaidMark, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT series_id, due_on, marked_on FROM series_paid_marks`
	var args []any
	if seriesID != uuid.Nil {
		query += ` WHERE series_id = ?`
		args = append(args, seriesID)
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list paid marks", func(row scanner) (PaidMark, error) {
		var one PaidMark
		var due, marked string
		if err := row.Scan(&one.SeriesID, &due, &marked); err != nil {
			return PaidMark{}, err
		}
		var err error
		if one.DueOn, err = domain.ParseDate(due); err != nil {
			return PaidMark{}, err
		}
		one.MarkedOn, err = domain.ParseDate(marked)
		return one, err
	}, query+` ORDER BY series_id, due_on`, args...)
}

// PaidMarkHolders is the marks as the slot holders domain.SettledSlots and
// domain.MarkedPaidSlots decide on, beside SlotHolders' rows.
func PaidMarkHolders(marks []PaidMark) []domain.SlotHolder {
	out := make([]domain.SlotHolder, 0, len(marks))
	for _, mark := range marks {
		out = append(out, domain.SlotHolder{
			SeriesID: domainID(mark.SeriesID), DueOn: mark.DueOn, On: mark.MarkedOn, IsMarkedPaid: true,
		})
	}
	return out
}
