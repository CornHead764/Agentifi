package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Watchlist is a saved slice of spending: a label and target over a shared
// Filter, which decides the rows. An unset target (HasTarget false) is not a
// target of zero.
type Watchlist struct {
	ID       uuid.UUID
	FilterID uuid.UUID
	Name     string
	Emoji    string
	Period   string

	TargetAmount domain.Money
	HasTarget    bool
}

const watchlistColumns = `id, filter_id, name, emoji, target_amount, period`

func (s *Store) ListWatchlists(ctx context.Context, spaceID SpaceID) ([]Watchlist, error) {
	return queryAll(ctx, s.db, "store: list watchlists", scanWatchlist,
		`SELECT `+watchlistColumns+` FROM watchlists
		 WHERE space_id = $1 AND is_deleted = false
		 ORDER BY created_at, id`, spaceID.UUID())
}

func (s *Store) GetWatchlist(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Watchlist, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+watchlistColumns+` FROM watchlists
		 WHERE space_id = $1 AND id = $2 AND is_deleted = false`,
		spaceID.UUID(), id)
	watchlist, err := scanWatchlist(row)
	return watchlist, wrap("store: get watchlist", err)
}

func (s *Store) CreateWatchlist(ctx context.Context, spaceID SpaceID, w *Watchlist) error {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO watchlists (id, space_id, filter_id, name, emoji, target_amount, period)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		w.ID, spaceID.UUID(), w.FilterID, w.Name, dbconv.NullText(w.Emoji),
		dbconv.NullMoney(w.TargetAmount, w.HasTarget), w.Period)
	return wrap("store: create watchlist", err)
}

// UpdateWatchlist writes the label, target, period and filter id; the filter's
// items are edited through the filter.
func (s *Store) UpdateWatchlist(ctx context.Context, spaceID SpaceID, w Watchlist) error {
	return s.execOne(ctx, "store: update watchlist",
		`UPDATE watchlists SET name = $3, emoji = $4, target_amount = $5, period = $6,
		        filter_id = $7, updated_at = now()
		 WHERE space_id = $1 AND id = $2 AND is_deleted = false`,
		spaceID.UUID(), w.ID, w.Name, dbconv.NullText(w.Emoji),
		dbconv.NullMoney(w.TargetAmount, w.HasTarget), w.Period, w.FilterID)
}

// DeleteWatchlist soft-deletes the watchlist and the filter it owns: one of
// watchlist scope that nothing else live points at. A filter another surface
// still reads is left alone, because deleting it would make that surface
// match everything.
func (s *Store) DeleteWatchlist(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.InTx(ctx, func(tx *Store) error {
		_, err := tx.db.Exec(ctx,
			`UPDATE watchlists SET is_deleted = true, updated_at = now()
			 WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
		if err != nil {
			return wrap("store: delete watchlist", err)
		}
		_, err = tx.db.Exec(ctx, `
			UPDATE filters f SET is_deleted = true, updated_at = now()
			WHERE f.space_id = $1 AND f.scope = 'watchlist'
			  AND f.id = (SELECT filter_id FROM watchlists WHERE space_id = $1 AND id = $2)
			  AND NOT `+savedFilter(), spaceID.UUID(), id)
		return wrap("store: delete watchlist filter", err)
	})
}

func scanWatchlist(row scanner) (Watchlist, error) {
	var (
		out    Watchlist
		emoji  *string
		target dbconv.Number
		err    error
	)
	if err = row.Scan(&out.ID, &out.FilterID, &out.Name, &emoji, &target, &out.Period); err != nil {
		return Watchlist{}, err
	}
	out.Emoji = Deref(emoji)
	if out.TargetAmount, out.HasTarget, err = dbconv.ReadNullMoney(target, "watchlists.target_amount"); err != nil {
		return Watchlist{}, err
	}
	return out, nil
}
