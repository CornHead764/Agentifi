package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Filter is the one filter entity (ground rule 3): watchlists, envelopes,
// reports and the transaction list point at a row here, as in Simplifi.
type Filter struct {
	ID      uuid.UUID
	SpaceID SpaceID
	Name    string
	// Scope records what mounted this filter; it never changes evaluation.
	Scope string
	// QueryText is the free-text box, kept so the user sees it again.
	QueryText string
	// Position orders a scope's filters where a person arranges them, as the
	// register's quick filters; ties fall back to creation order.
	Position  int
	IsDeleted bool
	Items     []FilterItem
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FilterItem is one clause. Items with the same GroupIndex are ANDed and the
// groups ORed; domain.filterSatisfies is the rule.
type FilterItem struct {
	ID       uuid.UUID
	SpaceID  SpaceID
	FilterID uuid.UUID
	Field    string
	Operator string

	GroupIndex int
	Position   int
	Negated    bool

	// ValueIDs holds row selections (categories, accounts, tags) as uuid[] so
	// they can be joined in SQL; ValueTexts holds the rest.
	ValueIDs   []uuid.UUID
	ValueTexts []string
	Text       string

	AmountMin    domain.Money
	HasAmountMin bool
	AmountMax    domain.Money
	HasAmountMax bool

	DateFrom domain.Date
	DateTo   domain.Date
	// DatePreset is resolved when the filter runs, so a saved "last 3 months"
	// still means that next quarter.
	DatePreset string

	// State is tri-state: true, false, or don't care.
	State *bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

const filterColumns = `id, space_id, name, scope, query_text, "position", is_deleted, created_at, updated_at`

const filterItemColumns = `id, space_id, filter_id, field, operator, group_index, "position",
	negated, value_ids, value_texts, text, amount_min, amount_max, date_from, date_to,
	date_preset, state, created_at, updated_at`

// CreateFilter writes the filter and its items in one transaction: a filter
// with no items matches everything, so a half-written one is a report over the
// whole ledger.
func (s *Store) CreateFilter(ctx context.Context, spaceID SpaceID, f *Filter) error {
	return s.InTx(ctx, func(tx *Store) error {
		if f.ID == uuid.Nil {
			f.ID = uuid.New()
		}
		f.SpaceID = spaceID
		err := tx.db.QueryRow(ctx, `
			INSERT INTO filters (id, space_id, name, scope, query_text, "position", is_deleted)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING created_at, updated_at`,
			f.ID, spaceID.UUID(), dbconv.NullText(f.Name), f.Scope, dbconv.NullText(f.QueryText),
			f.Position, f.IsDeleted,
		).Scan(&f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			return wrap("store: create filter", err)
		}
		return tx.insertFilterItems(ctx, spaceID, f)
	})
}

// ReplaceFilterItems swaps a filter's clauses wholesale.
func (s *Store) ReplaceFilterItems(ctx context.Context, spaceID SpaceID, f *Filter) error {
	return s.InTx(ctx, func(tx *Store) error {
		_, err := tx.db.Exec(ctx, `DELETE FROM filter_items WHERE space_id = $1 AND filter_id = $2`,
			spaceID.UUID(), f.ID)
		if err != nil {
			return wrap("store: replace filter items", err)
		}
		return tx.insertFilterItems(ctx, spaceID, f)
	})
}

func (s *Store) insertFilterItems(ctx context.Context, spaceID SpaceID, f *Filter) error {
	for i := range f.Items {
		item := &f.Items[i]
		if item.ID == uuid.Nil {
			item.ID = uuid.New()
		}
		item.SpaceID = spaceID
		item.FilterID = f.ID
		if item.ValueIDs == nil {
			item.ValueIDs = []uuid.UUID{}
		}
		if item.ValueTexts == nil {
			item.ValueTexts = []string{}
		}
		err := s.db.QueryRow(ctx, `
			INSERT INTO filter_items (id, space_id, filter_id, field, operator, group_index,
				"position", negated, value_ids, value_texts, text, amount_min, amount_max,
				date_from, date_to, date_preset, state)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
			RETURNING created_at, updated_at`,
			item.ID, spaceID.UUID(), f.ID, item.Field, item.Operator, item.GroupIndex,
			item.Position, item.Negated, item.ValueIDs, item.ValueTexts, dbconv.NullText(item.Text),
			dbconv.NullMoney(item.AmountMin, item.HasAmountMin),
			dbconv.NullMoney(item.AmountMax, item.HasAmountMax),
			dbconv.NullDate(item.DateFrom), dbconv.NullDate(item.DateTo),
			dbconv.NullText(item.DatePreset), item.State,
		).Scan(&item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			return wrap("store: create filter item", err)
		}
	}
	return nil
}

func (s *Store) GetFilter(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Filter, error) {
	row := s.db.QueryRow(ctx, `SELECT `+filterColumns+` FROM filters WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	f, err := scanFilter(row)
	if err != nil {
		return Filter{}, wrap("store: get filter", err)
	}
	items, err := s.listFilterItems(ctx, spaceID, []uuid.UUID{id})
	if err != nil {
		return Filter{}, err
	}
	f.Items = items[id]
	return f, nil
}

// ListFilters loads every filter and attaches its items in a second query, not
// one per filter.
func (s *Store) ListFilters(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Filter, error) {
	return s.listFilters(ctx, spaceID, includeDeleted, "")
}

// ListFiltersInScope is ListFilters for one scope's live filters, without
// loading every register search beside them.
func (s *Store) ListFiltersInScope(ctx context.Context, spaceID SpaceID, scope string) ([]Filter, error) {
	return s.listFilters(ctx, spaceID, false, scope)
}

func (s *Store) listFilters(ctx context.Context, spaceID SpaceID, includeDeleted bool, scope string) ([]Filter, error) {
	sql := `SELECT ` + filterColumns + ` FROM filters WHERE space_id = $1`
	args := []any{spaceID.UUID()}
	if !includeDeleted {
		sql += ` AND NOT is_deleted`
	}
	if scope != "" {
		sql += ` AND scope = $2`
		args = append(args, scope)
	}
	sql += ` ORDER BY "position", created_at`

	filters, err := queryAll(ctx, s.db, "store: list filters", scanFilter, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(filters) == 0 {
		return filters, nil
	}

	ids := make([]uuid.UUID, len(filters))
	for i, f := range filters {
		ids[i] = f.ID
	}
	items, err := s.listFilterItems(ctx, spaceID, ids)
	if err != nil {
		return nil, err
	}
	for i := range filters {
		filters[i].Items = items[filters[i].ID]
	}
	return filters, nil
}

func (s *Store) listFilterItems(ctx context.Context, spaceID SpaceID, filterIDs []uuid.UUID) (map[uuid.UUID][]FilterItem, error) {
	items, err := queryAll(ctx, s.db, "store: list filter items", scanFilterItem, `
		SELECT `+filterItemColumns+`
		FROM filter_items
		WHERE space_id = $1 AND filter_id IN (SELECT value FROM json_each($2))
		ORDER BY group_index, "position"`, spaceID.UUID(), filterIDs)
	if err != nil {
		return nil, err
	}
	byFilter := make(map[uuid.UUID][]FilterItem, len(filterIDs))
	for _, item := range items {
		byFilter[item.FilterID] = append(byFilter[item.FilterID], item)
	}
	return byFilter, nil
}

func (s *Store) UpdateFilter(ctx context.Context, spaceID SpaceID, f *Filter) error {
	err := s.db.QueryRow(ctx, `
		UPDATE filters SET name = $3, scope = $4, query_text = $5, "position" = $6, is_deleted = $7,
			updated_at = now()
		WHERE space_id = $1 AND id = $2
		RETURNING updated_at`,
		spaceID.UUID(), f.ID, dbconv.NullText(f.Name), f.Scope, dbconv.NullText(f.QueryText),
		f.Position, f.IsDeleted,
	).Scan(&f.UpdatedAt)
	return wrap("store: update filter", err)
}

// PruneAdHocFilters deletes, in every space, the ad_hoc filters untouched
// since olderThan that no rule, watchlist, envelope, guidance note or
// automation points at, deleted ones included: each of those keys restricts.
// A client still citing a pruned id is refused as for any unknown filter.
func (s *Store) PruneAdHocFilters(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM filters AS f
		WHERE f.scope = 'ad_hoc' AND f.updated_at < $1
		  AND NOT EXISTS (SELECT 1 FROM rules o WHERE o.filter_id = f.id)
		  AND NOT EXISTS (SELECT 1 FROM watchlists o WHERE o.filter_id = f.id)
		  AND NOT EXISTS (SELECT 1 FROM envelopes o WHERE o.filter_id = f.id)
		  AND NOT EXISTS (SELECT 1 FROM assistant_guidance o WHERE o.filter_id = f.id)
		  AND NOT EXISTS (SELECT 1 FROM assistant_automations o WHERE o.filter_id = f.id)`,
		olderThan)
	if err != nil {
		return 0, wrap("store: prune ad hoc filters", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) DeleteFilter(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete filter",
		`UPDATE filters SET is_deleted = true, updated_at = now() WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
}

func scanFilter(row scanner) (Filter, error) {
	var f Filter
	var spaceID uuid.UUID
	var name, queryText *string
	if err := row.Scan(&f.ID, &spaceID, &name, &f.Scope, &queryText, &f.Position, &f.IsDeleted,
		&f.CreatedAt, &f.UpdatedAt); err != nil {
		return Filter{}, err
	}
	f.SpaceID = SpaceID(spaceID)
	f.Name = Deref(name)
	f.QueryText = Deref(queryText)
	return f, nil
}

func scanFilterItem(row scanner) (FilterItem, error) {
	var (
		item                 FilterItem
		spaceID              uuid.UUID
		text, datePreset     *string
		amountMin, amountMax dbconv.Number
		dateFrom, dateTo     *time.Time
	)
	err := row.Scan(&item.ID, &spaceID, &item.FilterID, &item.Field, &item.Operator,
		&item.GroupIndex, &item.Position, &item.Negated, &item.ValueIDs, &item.ValueTexts,
		&text, &amountMin, &amountMax, &dateFrom, &dateTo, &datePreset, &item.State,
		&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return FilterItem{}, err
	}
	item.SpaceID = SpaceID(spaceID)
	item.Text = Deref(text)
	item.DatePreset = Deref(datePreset)
	item.DateFrom = dbconv.ReadNullDate(dateFrom)
	item.DateTo = dbconv.ReadNullDate(dateTo)
	if item.AmountMin, item.HasAmountMin, err = dbconv.ReadNullMoney(amountMin, "filter_items.amount_min"); err != nil {
		return FilterItem{}, err
	}
	if item.AmountMax, item.HasAmountMax, err = dbconv.ReadNullMoney(amountMax, "filter_items.amount_max"); err != nil {
		return FilterItem{}, err
	}
	return item, nil
}
