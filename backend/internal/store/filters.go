package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

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

	// ValueIDs and ValueTexts are persisted as JSON arrays.
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

const filterColumns = `id, name, scope, query_text, "position", is_deleted, created_at, updated_at`

const filterItemColumns = `id, filter_id, field, operator, group_index, "position",
	negated, value_ids, value_texts, text, amount_min, amount_max, date_from, date_to,
	date_preset, state, created_at, updated_at`

// CreateFilter writes the filter and its items atomically: an empty filter
// matches everything, so callers must never see a half-written one.
func (s *Store) CreateFilter(ctx context.Context, spaceID SpaceID, f *Filter) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if f.ID == uuid.Nil {
			f.ID = uuid.New()
		}
		f.SpaceID = spaceID
		now := db.Now()
		_, err = db.Write().ExecContext(ctx, `
			INSERT INTO filters (id, name, scope, query_text, "position", is_deleted, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			f.ID, NullText(f.Name), f.Scope, NullText(f.QueryText),
			f.Position, f.IsDeleted, now, now)
		if err != nil {
			return wrap("store: create filter", err)
		}
		f.CreatedAt, f.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
		return tx.insertFilterItems(ctx, spaceID, f)
	})
}

// ReplaceFilterItems swaps a filter's clauses wholesale.
func (s *Store) ReplaceFilterItems(ctx context.Context, spaceID SpaceID, f *Filter) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		_, err = db.Write().ExecContext(ctx, `DELETE FROM filter_items WHERE filter_id = ?`, f.ID)
		if err != nil {
			return wrap("store: replace filter items", err)
		}
		return tx.insertFilterItems(ctx, spaceID, f)
	})
}

func (s *Store) insertFilterItems(ctx context.Context, spaceID SpaceID, f *Filter) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	primary, err := s.primaryCurrency()
	if err != nil {
		return err
	}
	for i := range f.Items {
		item := &f.Items[i]
		if item.ID == uuid.Nil {
			item.ID = uuid.New()
		}
		item.SpaceID, item.FilterID = spaceID, f.ID
		if item.ValueIDs == nil {
			item.ValueIDs = []uuid.UUID{}
		}
		if item.ValueTexts == nil {
			item.ValueTexts = []string{}
		}
		var min, max any
		if item.HasAmountMin {
			min, err = toMinor(item.AmountMin.Decimal(), primary)
			if err != nil {
				return wrap("store: filter amount min", err)
			}
		}
		if item.HasAmountMax {
			max, err = toMinor(item.AmountMax.Decimal(), primary)
			if err != nil {
				return wrap("store: filter amount max", err)
			}
		}
		now := db.Now()
		_, err = db.Write().ExecContext(ctx, `
			INSERT INTO filter_items (id, filter_id, field, operator, group_index,
				"position", negated, value_ids, value_texts, text, amount_min, amount_max,
				date_from, date_to, date_preset, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, f.ID, item.Field, item.Operator, item.GroupIndex,
			item.Position, item.Negated, JSONArray[uuid.UUID](item.ValueIDs), JSONArray[string](item.ValueTexts),
			NullText(item.Text), min, max, NullDate(item.DateFrom), NullDate(item.DateTo),
			NullText(item.DatePreset), item.State, now, now)
		if err != nil {
			return wrap("store: create filter item", err)
		}
		item.CreatedAt, item.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return nil
}

func (s *Store) GetFilter(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Filter, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Filter{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Filter{}, err
	}
	f, err := scanFilter(db.Read().QueryRowContext(ctx, `SELECT `+filterColumns+` FROM filters WHERE id = ?`, id), spaceID)
	if err != nil {
		return Filter{}, wrap("store: get filter", err)
	}
	items, err := h.listFilterItems(ctx, spaceID, []uuid.UUID{id})
	if err != nil {
		return Filter{}, err
	}
	f.Items = items[id]
	return f, nil
}

func (s *Store) ListFilters(ctx context.Context, spaceID SpaceID, includeDeleted bool) ([]Filter, error) {
	return s.listFilters(ctx, spaceID, includeDeleted, "")
}

func (s *Store) ListFiltersInScope(ctx context.Context, spaceID SpaceID, scope string) ([]Filter, error) {
	return s.listFilters(ctx, spaceID, false, scope)
}

func (s *Store) listFilters(ctx context.Context, spaceID SpaceID, includeDeleted bool, scope string) ([]Filter, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + filterColumns + ` FROM filters WHERE 1=1`
	var args []any
	if !includeDeleted {
		query += ` AND NOT is_deleted`
	}
	if scope != "" {
		query += ` AND scope = ?`
		args = append(args, scope)
	}
	query += ` ORDER BY "position", created_at`
	filters, err := sqliteQueryAll(ctx, db.Read(), "store: list filters",
		func(row scanner) (Filter, error) { return scanFilter(row, spaceID) }, query, args...)
	if err != nil || len(filters) == 0 {
		return filters, err
	}
	ids := make([]uuid.UUID, len(filters))
	for i := range filters {
		ids[i] = filters[i].ID
	}
	items, err := h.listFilterItems(ctx, spaceID, ids)
	if err != nil {
		return nil, err
	}
	for i := range filters {
		filters[i].Items = items[filters[i].ID]
	}
	return filters, nil
}

func (s *Store) listFilterItems(ctx context.Context, spaceID SpaceID, filterIDs []uuid.UUID) (map[uuid.UUID][]FilterItem, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	primary, err := s.primaryCurrency()
	if err != nil {
		return nil, err
	}
	in, args := InArgs(filterIDs)
	items, err := sqliteQueryAll(ctx, db.Read(), "store: list filter items",
		func(row scanner) (FilterItem, error) { return scanFilterItem(row, spaceID, primary) },
		`SELECT `+filterItemColumns+` FROM filter_items WHERE filter_id IN `+in+` ORDER BY group_index, "position"`, args...)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	now := db.Now()
	err = sqliteExecOne(ctx, db.Write(), "store: update filter", `
		UPDATE filters SET name = ?, scope = ?, query_text = ?, "position" = ?, is_deleted = ?, updated_at = ?
		WHERE id = ?`, NullText(f.Name), f.Scope, NullText(f.QueryText),
		f.Position, f.IsDeleted, now, f.ID)
	if err == nil {
		f.UpdatedAt = fromUnixMillis(now)
	}
	return err
}

// PruneAdHocFilters prunes unreferenced old searches in every live household.
func (s *Store) PruneAdHocFilters(ctx context.Context, olderThan time.Time) (int64, error) {
	spaces, err := s.ListSpaces(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, space := range spaces {
		h, err := s.scoped(ctx, space.ID)
		if err != nil {
			return total, err
		}
		db, err := h.household(ctx, space.ID.UUID())
		if err != nil {
			return total, err
		}
		result, err := db.Write().ExecContext(ctx, `
			DELETE FROM filters AS f WHERE f.scope = 'ad_hoc' AND f.updated_at < ?
			  AND NOT EXISTS (SELECT 1 FROM rules o WHERE o.filter_id = f.id)
			  AND NOT EXISTS (SELECT 1 FROM watchlists o WHERE o.filter_id = f.id)
			  AND NOT EXISTS (SELECT 1 FROM envelopes o WHERE o.filter_id = f.id)
			  AND NOT EXISTS (SELECT 1 FROM assistant_guidance o WHERE o.filter_id = f.id)
			  AND NOT EXISTS (SELECT 1 FROM assistant_automations o WHERE o.filter_id = f.id)`, unixMillis(olderThan))
		if err != nil {
			return total, wrap("store: prune ad hoc filters", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, wrap("store: prune ad hoc filters", err)
		}
		total += n
	}
	return total, nil
}

func (s *Store) DeleteFilter(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete filter",
		`UPDATE filters SET is_deleted = true, updated_at = ? WHERE id = ?`, db.Now(), id)
}

func scanFilter(row scanner, spaceID SpaceID) (Filter, error) {
	f := Filter{SpaceID: spaceID}
	var name, queryText sql.Null[string]
	var created, updated int64
	if err := row.Scan(&f.ID, &name, &f.Scope, &queryText, &f.Position, &f.IsDeleted, &created, &updated); err != nil {
		return Filter{}, err
	}
	f.Name, f.QueryText = name.V, queryText.V
	f.CreatedAt, f.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	return f, nil
}

func scanFilterItem(row scanner, spaceID SpaceID, primary string) (FilterItem, error) {
	item := FilterItem{SpaceID: spaceID}
	var text, preset, dateFrom, dateTo sql.Null[string]
	var min, max sql.Null[int64]
	var state sql.Null[bool]
	var ids JSONArray[uuid.UUID]
	var texts JSONArray[string]
	var created, updated int64
	err := row.Scan(&item.ID, &item.FilterID, &item.Field, &item.Operator,
		&item.GroupIndex, &item.Position, &item.Negated, &ids, &texts,
		&text, &min, &max, &dateFrom, &dateTo, &preset, &state, &created, &updated)
	if err != nil {
		return FilterItem{}, err
	}
	item.ValueIDs, item.ValueTexts = ids, texts
	item.Text, item.DatePreset, item.State = text.V, preset.V, nullValue(state)
	item.CreatedAt, item.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
	if dateFrom.Valid {
		item.DateFrom, err = domain.ParseDate(dateFrom.V)
		if err != nil {
			return FilterItem{}, err
		}
	}
	if dateTo.Valid {
		item.DateTo, err = domain.ParseDate(dateTo.V)
		if err != nil {
			return FilterItem{}, err
		}
	}
	item.HasAmountMin, item.HasAmountMax = min.Valid, max.Valid
	if min.Valid {
		value, err := fromMinor(min.V, primary)
		if err != nil {
			return FilterItem{}, err
		}
		item.AmountMin = domain.FromDecimal(value)
	}
	if max.Valid {
		value, err := fromMinor(max.V, primary)
		if err != nil {
			return FilterItem{}, err
		}
		item.AmountMax = domain.FromDecimal(value)
	}
	return item, nil
}
