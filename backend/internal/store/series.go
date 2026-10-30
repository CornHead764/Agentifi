package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Bills & income: the series table's writes and its listing statement. Reads
// live in internal/service, which owns the row shape and imports this package,
// so the listing hands back a statement rather than rows to avoid a cycle.

// SeriesWrite is every series column the editor can change, and none the
// matcher moves. Description is matching input and never rendered;
// DisplayName is rendered and never matched.
type SeriesWrite struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	CategoryID uuid.UUID
	Kind       string

	Description string
	DisplayName string
	Amount      domain.Money
	Currency    string

	// Recurrence stores both the dropdown's alias and the RRULE fields: "twice
	// a month" does not survive being re-derived from its month days.
	Recurrence domain.Recurrence

	StartOn domain.Date
	EndOn   domain.Date

	// NextDueOn is written only when SetNextDueOn says so: the settle owns the
	// pointer, and an edit leaving the schedule alone must not move it.
	NextDueOn    domain.Date
	SetNextDueOn bool

	OverrideNextDueOn     domain.Date
	OverrideNextAmount    domain.Money
	HasOverrideNextAmount bool

	AutoAdjustDueOn bool
	ReminderDays    int

	MatchCriteria  string
	MatchAmountMin domain.Money
	HasMatchMin    bool
	MatchAmountMax domain.Money
	HasMatchMax    bool

	// TemplateSplits stays JSON: it has no fixed arity. Nothing here reads it.
	TemplateTagIDs []uuid.UUID
	TemplateSplits []byte

	IsActive bool
}

// SeriesQuery narrows a series listing. The zero value is every live series in
// the space, oldest due date first.
type SeriesQuery struct {
	AccountID  uuid.UUID
	Kind       string
	ActiveOnly bool
}

// seriesColumns must agree with internal/service's unexported copy.
const seriesColumns = `id, account_id, category_id, kind, description, display_name, amount,
	currency, alias, frequency, "interval", by_month_day, by_day, start_on, end_on, next_due_on,
	override_next_due_on, override_next_amount, auto_adjust_due_on,
	match_criteria, match_amount_min, match_amount_max, learned_descriptions,
	template_tag_ids, template_splits, is_active, is_deleted, reminder_days, by_month`

// SeriesListSQL is the listing statement and its arguments, run by
// internal/service.
func SeriesListSQL(q SeriesQuery) (string, []any) {
	sql := `SELECT ` + seriesColumns + ` FROM series WHERE NOT is_deleted`
	var args []any
	if q.AccountID != uuid.Nil {
		args = append(args, q.AccountID)
		sql += " AND account_id = ?"
	}
	if q.Kind != "" {
		args = append(args, q.Kind)
		sql += " AND kind = ?"
	}
	if q.ActiveOnly {
		sql += " AND is_active"
	}
	sql += " ORDER BY COALESCE(next_due_on, start_on), id"
	return sql, args
}

// CreateSeries writes a new schedule with the pointer on its start: a series
// that has never fired is due the day it begins.
func (s *Store) CreateSeries(ctx context.Context, spaceID SpaceID, w *SeriesWrite) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	initial := *w
	initial.OverrideNextDueOn = domain.Date{}
	initial.HasOverrideNextAmount = false
	values, err := seriesWriteValues(initial)
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO series (account_id, category_id, kind, description, display_name,
			amount, currency, alias, frequency, "interval", by_month_day, by_day, start_on, end_on,
			override_next_due_on, override_next_amount, auto_adjust_due_on, match_criteria,
			match_amount_min, match_amount_max, is_active, template_tag_ids, template_splits, by_month,
			id, next_due_on, reminder_days, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		append(values, w.ID, w.StartOn.String(), w.ReminderDays, now, now)...)
	return wrap("store: create series", err)
}

// UpdateSeries writes every column the editor can change. setReminderDays says
// whether the request named the reminder window; unnamed is not zero days.
func (s *Store) UpdateSeries(
	ctx context.Context, spaceID SpaceID, w SeriesWrite, setReminderDays bool,
) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	values, err := seriesWriteValues(w)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `
		UPDATE series SET account_id = ?, category_id = ?, kind = ?, description = ?, display_name = ?,
			amount = ?, currency = ?, alias = ?, frequency = ?, "interval" = ?,
			by_month_day = ?, by_day = ?, start_on = ?, end_on = ?,
			override_next_due_on = ?, override_next_amount = ?, auto_adjust_due_on = ?,
			match_criteria = ?, match_amount_min = ?, match_amount_max = ?, is_active = ?,
			template_tag_ids = ?, template_splits = ?, by_month = ?,
			reminder_days = CASE WHEN ? THEN ? ELSE reminder_days END,
			next_due_on = CASE WHEN ? THEN ? ELSE next_due_on END, updated_at = ?
		WHERE id = ?`,
		append(values, setReminderDays, w.ReminderDays, w.SetNextDueOn, NullDate(w.NextDueOn), db.Now(), w.ID)...)
	return wrap("store: update series", err)
}

// DeleteSeries soft-deletes. The charges it already matched keep their
// series_id, so a deleted series that is restored still owns its history.
func (s *Store) DeleteSeries(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE series SET is_deleted = true, is_active = false, updated_at = ? WHERE id = ?`, db.Now(), id)
	return wrap("store: delete series", err)
}

func seriesWriteValues(w SeriesWrite) ([]any, error) {
	amount, err := toMinor(w.Amount, w.Currency)
	if err != nil {
		return nil, wrap("store: series amount", err)
	}
	var override, min, max any
	for _, field := range []struct {
		present bool
		amount  domain.Money
		dst     *any
	}{
		{w.HasOverrideNextAmount, w.OverrideNextAmount, &override},
		{w.HasMatchMin, w.MatchAmountMin, &min},
		{w.HasMatchMax, w.MatchAmountMax, &max},
	} {
		if !field.present {
			continue
		}
		value, err := toMinor(field.amount, w.Currency)
		if err != nil {
			return nil, wrap("store: series optional amount", err)
		}
		*field.dst = value
	}
	var splits any
	if w.TemplateSplits != nil {
		splits = string(w.TemplateSplits)
	}
	return []any{
		w.AccountID, NullUUID(w.CategoryID), w.Kind, w.Description, NullText(w.DisplayName),
		amount, w.Currency, string(w.Recurrence.Alias), frequencyArg(w.Recurrence.Frequency), w.Recurrence.Interval,
		JSONArray[int32](monthDayArg(w.Recurrence.ByMonthDay)), JSONArray[string](byDayArg(w.Recurrence.ByDay)),
		w.StartOn.String(), NullDate(w.EndOn), NullDate(w.OverrideNextDueOn), override,
		w.AutoAdjustDueOn, w.MatchCriteria, min, max, w.IsActive,
		JSONArray[uuid.UUID](w.TemplateTagIDs), splits, JSONArray[int32](byMonthArg(w.Recurrence.ByMonth)),
	}, nil
}

// frequencyArg writes NULL for one-time, meaning no recurrence.
func frequencyArg(f domain.Frequency) *string {
	if f == domain.FreqNone {
		return nil
	}
	value := string(f)
	return &value
}

func monthDayArg(days []int) []int32 {
	out := make([]int32, 0, len(days))
	for _, day := range days {
		out = append(out, int32(day))
	}
	return out
}

func byMonthArg(months []time.Month) []int32 {
	out := make([]int32, 0, len(months))
	for _, month := range months {
		out = append(out, int32(month))
	}
	return out
}

func byDayArg(days []domain.Weekday) []string {
	out := make([]string, 0, len(days))
	for _, day := range days {
		out = append(out, string(day))
	}
	return out
}
