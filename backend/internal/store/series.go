package store

import (
	"context"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

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
func SeriesListSQL(spaceID SpaceID, q SeriesQuery) (string, []any) {
	sql := `SELECT ` + seriesColumns + ` FROM series WHERE space_id = $1 AND NOT is_deleted`
	args := []any{spaceID.UUID()}
	if q.AccountID != uuid.Nil {
		args = append(args, q.AccountID)
		sql += fmt.Sprintf(" AND account_id = $%d", len(args))
	}
	if q.Kind != "" {
		args = append(args, q.Kind)
		sql += fmt.Sprintf(" AND kind = $%d", len(args))
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
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO series (id, space_id, account_id, category_id, kind, description, display_name,
			amount, currency, alias, frequency, "interval", by_month_day, by_day, start_on, end_on,
			next_due_on, auto_adjust_due_on, reminder_days, match_criteria,
			match_amount_min, match_amount_max, template_tag_ids, template_splits, is_active, by_month)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
			$19, $20, $21, $22, $23, $24, $25, $26)`,
		w.ID, spaceID.UUID(), w.AccountID, pgconv.NullUUID(w.CategoryID), w.Kind,
		w.Description, pgconv.NullText(w.DisplayName),
		pgconv.Money(w.Amount), w.Currency,
		string(w.Recurrence.Alias), frequencyArg(w.Recurrence.Frequency), w.Recurrence.Interval,
		monthDayArg(w.Recurrence.ByMonthDay), byDayArg(w.Recurrence.ByDay),
		w.StartOn.Time(), pgconv.NullDate(w.EndOn),
		w.StartOn.Time(),
		w.AutoAdjustDueOn, w.ReminderDays, w.MatchCriteria,
		pgconv.NullMoney(w.MatchAmountMin, w.HasMatchMin),
		pgconv.NullMoney(w.MatchAmountMax, w.HasMatchMax),
		w.TemplateTagIDs, w.TemplateSplits, w.IsActive, byMonthArg(w.Recurrence.ByMonth))
	return wrap("store: create series", err)
}

// UpdateSeries writes every column the editor can change. setReminderDays says
// whether the request named the reminder window; unnamed is not zero days.
func (s *Store) UpdateSeries(
	ctx context.Context, spaceID SpaceID, w SeriesWrite, setReminderDays bool,
) error {
	_, err := s.db.Exec(ctx, `
		UPDATE series SET
			account_id = $3, category_id = $4, kind = $5, description = $6, display_name = $7,
			amount = $8, currency = $9, alias = $10, frequency = $11, "interval" = $12,
			by_month_day = $13, by_day = $14, start_on = $15, end_on = $16,
			override_next_due_on = $17, override_next_amount = $18,
			auto_adjust_due_on = $19, match_criteria = $20, match_amount_min = $21,
			match_amount_max = $22, is_active = $23,
			template_tag_ids = $26, template_splits = $27,
			reminder_days = CASE WHEN $24::boolean THEN $25::integer ELSE reminder_days END,
			next_due_on = CASE WHEN $28::boolean THEN $29::date ELSE next_due_on END,
			by_month = $30, updated_at = now()
		WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), w.ID, w.AccountID, pgconv.NullUUID(w.CategoryID), w.Kind,
		w.Description, pgconv.NullText(w.DisplayName), pgconv.Money(w.Amount), w.Currency,
		string(w.Recurrence.Alias), frequencyArg(w.Recurrence.Frequency), w.Recurrence.Interval,
		monthDayArg(w.Recurrence.ByMonthDay), byDayArg(w.Recurrence.ByDay),
		w.StartOn.Time(), pgconv.NullDate(w.EndOn),
		pgconv.NullDate(w.OverrideNextDueOn),
		pgconv.NullMoney(w.OverrideNextAmount, w.HasOverrideNextAmount),
		w.AutoAdjustDueOn, w.MatchCriteria,
		pgconv.NullMoney(w.MatchAmountMin, w.HasMatchMin),
		pgconv.NullMoney(w.MatchAmountMax, w.HasMatchMax),
		w.IsActive, setReminderDays, w.ReminderDays,
		w.TemplateTagIDs, w.TemplateSplits,
		w.SetNextDueOn, pgconv.NullDate(w.NextDueOn), byMonthArg(w.Recurrence.ByMonth))
	return wrap("store: update series", err)
}

// DeleteSeries soft-deletes. The charges it already matched keep their
// series_id, so a deleted series that is restored still owns its history.
func (s *Store) DeleteSeries(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE series SET is_deleted = true, is_active = false, updated_at = now()
		 WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
	return wrap("store: delete series", err)
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
