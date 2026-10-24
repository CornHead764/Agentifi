package store

import (
	"context"
	"database/sql"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/google/uuid"
)

// Watchlist is a saved slice of spending: a label and target over a shared
// Filter. HasTarget distinguishes an unset target from zero.
type Watchlist struct {
	ID           uuid.UUID
	FilterID     uuid.UUID
	Name         string
	Emoji        string
	Period       string
	TargetAmount domain.Money
	HasTarget    bool
}

const watchlistColumns = `id, filter_id, name, emoji, target_amount, period`

func (s *Store) ListWatchlists(ctx context.Context, spaceID SpaceID) ([]Watchlist, error) {
	db, primary, err := s.householdMoneyDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list watchlists", func(row scanner) (Watchlist, error) { return scanWatchlist(row, primary) },
		`SELECT `+watchlistColumns+` FROM watchlists WHERE is_deleted = false ORDER BY created_at, id`)
}

func (s *Store) GetWatchlist(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Watchlist, error) {
	db, primary, err := s.householdMoneyDB(ctx, spaceID)
	if err != nil {
		return Watchlist{}, err
	}
	w, err := scanWatchlist(db.Read().QueryRowContext(ctx, `SELECT `+watchlistColumns+` FROM watchlists WHERE id = ? AND is_deleted = false`, id), primary)
	return w, wrap("store: get watchlist", err)
}

func (s *Store) CreateWatchlist(ctx context.Context, spaceID SpaceID, w *Watchlist) error {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	amount, err := h.watchlistAmount(*w)
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `INSERT INTO watchlists (id, filter_id, name, emoji, target_amount, period, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.FilterID, w.Name, nullableGoalText(w.Emoji), amount, w.Period, now, now)
	return wrap("store: create watchlist", err)
}

// UpdateWatchlist leaves editing the filter items to the filter methods.
func (s *Store) UpdateWatchlist(ctx context.Context, spaceID SpaceID, w Watchlist) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	amount, err := h.watchlistAmount(w)
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: update watchlist", `UPDATE watchlists SET name = ?, emoji = ?, target_amount = ?, period = ?, filter_id = ?, updated_at = ? WHERE id = ? AND is_deleted = false`,
		w.Name, nullableGoalText(w.Emoji), amount, w.Period, w.FilterID, db.Now(), w.ID)
}

func (s *Store) watchlistAmount(w Watchlist) (any, error) {
	if !w.HasTarget {
		return nil, nil
	}
	primary, err := s.primaryCurrency()
	if err != nil {
		return nil, err
	}
	amount, err := toMinor(w.TargetAmount.Decimal(), primary)
	return amount, wrap("store: watchlist amount", err)
}

// DeleteWatchlist also deletes its watchlist-scoped filter unless another live
// surface still owns it. Both changes happen in the same household transaction.
func (s *Store) DeleteWatchlist(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		now := db.Now()
		_, err = db.Write().ExecContext(ctx, `UPDATE watchlists SET is_deleted = true, updated_at = ? WHERE id = ?`, now, id)
		if err != nil {
			return wrap("store: delete watchlist", err)
		}
		_, err = db.Write().ExecContext(ctx, `UPDATE filters AS f SET is_deleted = true, updated_at = ? WHERE f.scope = 'watchlist' AND f.id = (SELECT filter_id FROM watchlists WHERE id = ?) AND NOT `+savedFilter(), now, id)
		return wrap("store: delete watchlist filter", err)
	})
}

func scanWatchlist(row scanner, primary string) (Watchlist, error) {
	var out Watchlist
	var emoji sql.Null[string]
	var target sql.Null[int64]
	if err := row.Scan(&out.ID, &out.FilterID, &out.Name, &emoji, &target, &out.Period); err != nil {
		return Watchlist{}, err
	}
	out.Emoji, out.HasTarget = emoji.V, target.Valid
	if target.Valid {
		amount, err := fromMinor(target.V, primary)
		if err != nil {
			return Watchlist{}, err
		}
		out.TargetAmount = domain.FromDecimal(amount)
	}
	return out, nil
}
