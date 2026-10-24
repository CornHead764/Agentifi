package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Linking a posted charge to the series it pays: the database half. The
// algorithm, including the schedule-pointer move, is the pure domain.Decide
// (internal/domain/seriesmatching.go); this loads contexts, calls it, and
// writes the decision.
//
// Upgrading a placeholder keeps the placeholder's id: it is the row the user
// has already categorized and possibly split, so it absorbs the bank's facts
// and the posted duplicate is soft-deleted. The bank's wording is learned onto
// the series on every link.

const (
	// learnedDescriptionLimit is how many linked charges lend the series their
	// wording: enough to survive a payer renaming itself once.
	learnedDescriptionLimit = 5
	observedAmountLimit     = 12
	// placeholderWindowDays is how far either side of a charge a materialized
	// occurrence may stand and still be the one it fulfills.
	placeholderWindowDays = 45
)

// realSourceList is domain.CashFlowSources spelled for SQL. A placeholder is
// identified by its estimate status instead, and matching one against another
// would link a series to itself.
func realSourceList() []string {
	sources := domain.CashFlowSources()
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		out = append(out, string(source))
	}
	return out
}

// matchableCharge excludes rows already carrying a series id (one payment
// would fill two slots), forecasts (the same bill twice), and non-cash-flow
// sources (an asset revaluation is not the mortgage).
func matchableCharge(row store.Transaction) bool {
	return row.SeriesID == uuid.Nil && row.EstimateStatus == "" && row.Source.IsCashFlow()
}

type SeriesMatcher struct{ base }

func NewSeriesMatcher(st *store.Store) *SeriesMatcher { return &SeriesMatcher{newBase(st)} }

// SeriesRow is the series row in the shape this package reads it.
type SeriesRow struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	CategoryID uuid.UUID
	Kind       string

	Description string
	DisplayName string
	Amount      domain.Money
	Currency    string

	Alias      string
	Frequency  string
	Interval   int
	ByMonthDay []int
	ByDay      []string
	ByMonth    []int

	StartOn   domain.Date
	EndOn     domain.Date
	NextDueOn domain.Date

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

	LearnedDescriptions []string

	// The transaction template: tags and a standing split, so an occurrence
	// is a real transaction from birth. TemplateSplits stays JSON; the API
	// owns its shape.
	TemplateTagIDs []uuid.UUID
	TemplateSplits []byte

	IsActive  bool
	IsDeleted bool
}

const seriesColumns = `id, account_id, category_id, kind, description, display_name, amount,
	currency, alias, frequency, "interval", by_month_day, by_day, start_on, end_on, next_due_on,
	override_next_due_on, override_next_amount, auto_adjust_due_on,
	match_criteria, match_amount_min, match_amount_max, learned_descriptions,
	template_tag_ids, template_splits, is_active, is_deleted, reminder_days, by_month`

func ToRecurrence(row SeriesRow) domain.Recurrence {
	byDay := make([]domain.Weekday, 0, len(row.ByDay))
	for _, code := range row.ByDay {
		byDay = append(byDay, domain.Weekday(code))
	}
	var byMonth []time.Month
	for _, month := range row.ByMonth {
		byMonth = append(byMonth, time.Month(month))
	}
	return domain.Recurrence{
		Alias:      domain.RecurrenceAlias(row.Alias),
		Frequency:  domain.Frequency(row.Frequency),
		Interval:   row.Interval,
		ByMonthDay: row.ByMonthDay,
		ByDay:      byDay,
		ByMonth:    byMonth,
	}
}

// ToDomainSeries stringifies ids at this boundary; the Simplifi export's ids
// are not UUIDs.
func ToDomainSeries(row SeriesRow) domain.Series {
	series := domain.Series{
		ID:                    domain.ID(row.ID.String()),
		AccountID:             domain.ID(row.AccountID.String()),
		Kind:                  domain.SeriesKind(row.Kind),
		Description:           row.Description,
		Amount:                row.Amount,
		Recurrence:            ToRecurrence(row),
		StartOn:               row.StartOn,
		NextDueOn:             row.NextDueOn,
		DisplayName:           row.DisplayName,
		Currency:              row.Currency,
		EndOn:                 row.EndOn,
		OverrideNextDueOn:     row.OverrideNextDueOn,
		OverrideNextAmount:    row.OverrideNextAmount,
		HasOverrideNextAmount: row.HasOverrideNextAmount,
		AutoAdjustDueOn:       row.AutoAdjustDueOn,
		IsActive:              row.IsActive,
		IsDeleted:             row.IsDeleted,
	}
	if row.CategoryID != uuid.Nil {
		series.CategoryID = domain.ID(row.CategoryID.String())
	}
	return series
}

// ToTolerance is the series' Match Criteria as a band. A range missing a bound
// falls back to auto, which is never narrower than exact.
func ToTolerance(row SeriesRow) domain.AmountTolerance {
	criteria := domain.MatchCriteria(row.MatchCriteria)
	if criteria != domain.CriteriaRange {
		return domain.AmountTolerance{Criteria: criteria}
	}
	if !row.HasMatchMin || !row.HasMatchMax {
		return domain.AutoAmount()
	}
	tolerance, err := domain.BetweenAmounts(row.MatchAmountMin, row.MatchAmountMax)
	if err != nil {
		return domain.AutoAmount()
	}
	return tolerance
}

// LoadContexts returns every live series a charge in this account could belong
// to; a nil account id sweeps the space. Inactive and deleted series are left
// out here and again by domain.Decide, so a cancelled bill cannot reappear.
func (m *SeriesMatcher) LoadContexts(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID,
) ([]domain.MatchContext, error) {
	sql := `SELECT ` + seriesColumns + ` FROM series WHERE is_active AND NOT is_deleted`
	var args []any
	if accountID != uuid.Nil {
		sql += ` AND account_id = ?`
		args = append(args, accountID)
	}
	sql += ` ORDER BY id`

	rows, err := m.LoadSeriesRows(ctx, spaceID, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	learned, observed, err := m.learned(ctx, spaceID, ids)
	if err != nil {
		return nil, err
	}
	seriesIDs := make([]domain.ID, len(ids))
	for i, id := range ids {
		seriesIDs[i] = domain.ID(id.String())
	}
	bills, err := NewBills(m.store).BillConnectFor(ctx, spaceID, seriesIDs)
	if err != nil {
		return nil, err
	}

	contexts := make([]domain.MatchContext, 0, len(rows))
	for _, row := range rows {
		wordings := append(append([]string{}, row.LearnedDescriptions...), learned[row.ID]...)
		contexts = append(contexts, domain.MatchContext{
			Series:              ToDomainSeries(row),
			Tolerance:           ToTolerance(row),
			LearnedDescriptions: wordings,
			ObservedAmounts:     observed[row.ID],
			Bills:               bills[domain.ID(row.ID.String())],
		})
	}
	return contexts, nil
}

func (m *SeriesMatcher) LoadSeriesRows(
	ctx context.Context, spaceID store.SpaceID, query string, args ...any,
) ([]SeriesRow, error) {
	db, err := m.store.HouseholdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	rows, err := db.Read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("service: load series: %w", err)
	}
	defer rows.Close()

	var out []SeriesRow
	for rows.Next() {
		row, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (m *SeriesMatcher) GetSeries(
	ctx context.Context, spaceID store.SpaceID, seriesID uuid.UUID,
) (SeriesRow, error) {
	db, err := m.store.HouseholdDB(ctx, spaceID)
	if err != nil {
		return SeriesRow{}, err
	}
	return scanSeries(db.Read().QueryRowContext(ctx,
		`SELECT `+seriesColumns+` FROM series WHERE id = ?`, seriesID))
}

func scanSeries(row interface{ Scan(...any) error }) (SeriesRow, error) {
	var out SeriesRow
	var category sql.Null[uuid.UUID]
	var display, frequency, end, next, overrideDate, splits sql.Null[string]
	var start, monthDays, months, days, learned, tags string
	var amount int64
	var override, min, max sql.Null[int64]
	err := row.Scan(&out.ID, &out.AccountID, &category, &out.Kind, &out.Description, &display,
		&amount, &out.Currency, &out.Alias, &frequency, &out.Interval, &monthDays, &days, &start,
		&end, &next, &overrideDate, &override, &out.AutoAdjustDueOn,
		&out.MatchCriteria, &min, &max, &learned,
		&tags, &splits, &out.IsActive, &out.IsDeleted, &out.ReminderDays, &months)
	if err != nil {
		return SeriesRow{}, fmt.Errorf("service: load series: %w", err)
	}
	out.CategoryID, out.DisplayName, out.Frequency = category.V, display.V, frequency.V
	out.TemplateSplits = []byte(splits.V)
	for _, field := range []struct {
		raw string
		dst any
	}{
		{monthDays, &out.ByMonthDay}, {months, &out.ByMonth}, {days, &out.ByDay},
		{learned, &out.LearnedDescriptions}, {tags, &out.TemplateTagIDs},
	} {
		if err := json.Unmarshal([]byte(field.raw), field.dst); err != nil {
			return SeriesRow{}, err
		}
	}
	out.StartOn, err = domain.ParseDate(start)
	if err != nil {
		return SeriesRow{}, err
	}
	for _, field := range []struct {
		raw sql.Null[string]
		dst *domain.Date
	}{
		{end, &out.EndOn}, {next, &out.NextDueOn}, {overrideDate, &out.OverrideNextDueOn},
	} {
		if !field.raw.Valid {
			continue
		}
		*field.dst, err = domain.ParseDate(field.raw.V)
		if err != nil {
			return SeriesRow{}, err
		}
	}
	value, err := minorunits.FromMinor(amount, out.Currency)
	if err != nil {
		return SeriesRow{}, err
	}
	out.Amount = domain.FromDecimal(value)
	out.HasOverrideNextAmount, out.HasMatchMin, out.HasMatchMax = override.Valid, min.Valid, max.Valid
	for _, field := range []struct {
		raw sql.Null[int64]
		dst *domain.Money
	}{
		{override, &out.OverrideNextAmount}, {min, &out.MatchAmountMin}, {max, &out.MatchAmountMax},
	} {
		if !field.raw.Valid {
			continue
		}
		value, err := minorunits.FromMinor(field.raw.V, out.Currency)
		if err != nil {
			return SeriesRow{}, err
		}
		*field.dst = domain.FromDecimal(value)
	}
	return out, nil
}

// learned returns the wordings and amounts of the charges already linked to
// each series, in one sweep.
func (m *SeriesMatcher) learned(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []uuid.UUID,
) (map[uuid.UUID][]string, map[uuid.UUID][]domain.Money, error) {
	if len(seriesIDs) == 0 {
		return nil, nil, nil
	}
	db, _, err := m.household(ctx, spaceID)
	if err != nil {
		return nil, nil, err
	}
	ids, args := sqlIn(seriesIDs)
	sources, sourceArgs := sqlIn(realSourceList())
	args = append(args, sourceArgs...)
	rows, err := db.Read().QueryContext(ctx, `
		SELECT series_id, statement_name, payee, amount, currency FROM transactions
		WHERE series_id IN `+ids+` AND `+store.MoneyMoved+`
		  AND source IN `+sources+` ORDER BY date DESC, id`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("service: load learned wordings: %w", err)
	}
	defer rows.Close()

	texts := map[uuid.UUID][]string{}
	amounts := map[uuid.UUID][]domain.Money{}
	for rows.Next() {
		var (
			seriesID             uuid.UUID
			statementName, payee string
			amount               int64
			currency             string
		)
		if err := rows.Scan(&seriesID, &statementName, &payee, &amount, &currency); err != nil {
			return nil, nil, fmt.Errorf("service: load learned wordings: %w", err)
		}
		if len(texts[seriesID]) < learnedDescriptionLimit*2 {
			for _, text := range []string{statementName, payee} {
				if text != "" {
					texts[seriesID] = append(texts[seriesID], text)
				}
			}
		}
		if len(amounts[seriesID]) < observedAmountLimit {
			value, err := minorunits.FromMinor(amount, currency)
			if err != nil {
				return nil, nil, err
			}
			amounts[seriesID] = append(amounts[seriesID], domain.FromDecimal(value))
		}
	}
	return texts, amounts, rows.Err()
}

// loadPlaceholders returns the materialized occurrences due between from and
// to. Only a row with both a series and an occurrence date identifies a slot.
func (m *SeriesMatcher) loadPlaceholders(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []uuid.UUID, from, to domain.Date,
) ([]domain.MatchPlaceholder, error) {
	if len(seriesIDs) == 0 {
		return nil, nil
	}
	db, _, err := m.household(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	ids, args := sqlIn(seriesIDs)
	args = append(args, from.String(), to.String())
	rows, err := db.Read().QueryContext(ctx, `
		SELECT id, series_id, series_due_on, amount, currency FROM transactions
		WHERE series_id IN `+ids+` AND estimate_status IS NOT NULL
		  AND NOT is_deleted AND series_due_on IS NOT NULL
		  AND series_due_on >= ? AND series_due_on <= ? ORDER BY series_due_on, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("service: load placeholders: %w", err)
	}
	defer rows.Close()

	var out []domain.MatchPlaceholder
	for rows.Next() {
		var (
			id, seriesID uuid.UUID
			dueOn        string
			amount       int64
			currency     string
		)
		if err := rows.Scan(&id, &seriesID, &dueOn, &amount, &currency); err != nil {
			return nil, fmt.Errorf("service: load placeholders: %w", err)
		}
		value, err := minorunits.FromMinor(amount, currency)
		if err != nil {
			return nil, err
		}
		date, err := domain.ParseDate(dueOn)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.MatchPlaceholder{
			ID:       domain.ID(id.String()),
			SeriesID: domain.ID(seriesID.String()),
			DueOn:    date,
			Amount:   domain.FromDecimal(value),
		})
	}
	return out, rows.Err()
}

// loadSettled returns the slots a charge already records between from and to,
// per series. A forecast settles nothing (store.SeriesSlotSettled).
func (m *SeriesMatcher) loadSettled(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []uuid.UUID, from, to domain.Date,
) (map[domain.ID][]domain.Date, error) {
	db, _, err := m.household(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	ids, args := sqlIn(seriesIDs)
	args = append(args, from.String(), to.String())
	rows, err := db.Read().QueryContext(ctx, `
		SELECT series_id, series_due_on FROM transactions
		WHERE series_id IN `+ids+` AND series_due_on IS NOT NULL
		  AND series_due_on >= ? AND series_due_on <= ? AND `+store.MoneyMoved, args...)
	if err != nil {
		return nil, fmt.Errorf("service: load settled slots: %w", err)
	}
	defer rows.Close()

	out := map[domain.ID][]domain.Date{}
	for rows.Next() {
		var (
			seriesID uuid.UUID
			dueOn    string
		)
		if err := rows.Scan(&seriesID, &dueOn); err != nil {
			return nil, fmt.Errorf("service: load settled slots: %w", err)
		}
		date, err := domain.ParseDate(dueOn)
		if err != nil {
			return nil, err
		}
		id := domain.ID(seriesID.String())
		out[id] = append(out[id], date)
	}
	return out, rows.Err()
}

// accountMatch is everything domain.Decide reads for one account, loaded once.
type accountMatch struct {
	contexts     []domain.MatchContext
	placeholders []domain.MatchPlaceholder
}

// loadAccount reads the account's series, or only those in only when it is
// non-nil.
func (m *SeriesMatcher) loadAccount(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, earliest, latest domain.Date,
	only map[domain.ID]bool,
) (accountMatch, error) {
	contexts, err := m.LoadContexts(ctx, spaceID, accountID)
	if err != nil {
		return accountMatch{}, err
	}
	if only != nil {
		contexts = slices.DeleteFunc(contexts, func(context domain.MatchContext) bool {
			return !only[context.Series.ID]
		})
	}
	if len(contexts) == 0 {
		return accountMatch{}, nil
	}
	seriesIDs := make([]uuid.UUID, 0, len(contexts))
	for _, context := range contexts {
		id, err := store.ParseID(context.Series.ID)
		if err != nil {
			return accountMatch{}, err
		}
		seriesIDs = append(seriesIDs, id)
	}
	from, to := earliest.AddDays(-placeholderWindowDays), latest.AddDays(placeholderWindowDays)
	placeholders, err := m.loadPlaceholders(ctx, spaceID, seriesIDs, from, to)
	if err != nil {
		return accountMatch{}, err
	}
	settled, err := m.loadSettled(ctx, spaceID, seriesIDs, from, to)
	if err != nil {
		return accountMatch{}, err
	}
	for i := range contexts {
		contexts[i].SettledOn = settled[contexts[i].Series.ID]
	}
	return accountMatch{contexts: contexts, placeholders: placeholders}, nil
}

// decide is offered only the placeholders within placeholderWindowDays.
func (a accountMatch) decide(charge store.Transaction) (domain.MatchDecision, bool) {
	if len(a.contexts) == 0 {
		return domain.MatchDecision{}, false
	}
	var near []domain.MatchPlaceholder
	for _, placeholder := range a.placeholders {
		if absInt(domain.DaysBetween(charge.Date, placeholder.DueOn)) <= placeholderWindowDays {
			near = append(near, placeholder)
		}
	}
	return domain.Decide(store.DomainTransaction(charge), a.contexts, near)
}

// AbsorbedFields is the bank's facts for a placeholder being upgraded. What
// the user may have set (payee, category, notes, tags, exclusion flags) is
// structurally absent: that is why the placeholder keeps its id.
type AbsorbedFields struct {
	ExternalID       string
	StatementName    string
	Date             domain.Date
	EffectiveDate    domain.Date
	Currency         string
	AmountPrimary    domain.Money
	HasAmountPrimary bool
	FxRateUsed       domain.Rate
	HasFxRateUsed    bool
	Source           domain.Source
	IsPending        bool
	SeriesDueOn      domain.Date

	// Amount is set only when what posted differs from the estimate.
	Amount    domain.Money
	HasAmount bool
}

func AbsorbFrom(charge store.Transaction, decision domain.MatchDecision) AbsorbedFields {
	return AbsorbedFields{
		ExternalID:       charge.ExternalID,
		StatementName:    charge.StatementName,
		Date:             charge.Date,
		EffectiveDate:    charge.EffectiveDate,
		Currency:         charge.Currency,
		AmountPrimary:    charge.AmountPrimary,
		HasAmountPrimary: charge.HasAmountPrimary,
		FxRateUsed:       charge.FxRateUsed,
		HasFxRateUsed:    charge.HasFxRateUsed,
		Source:           charge.Source,
		IsPending:        charge.IsPending,
		SeriesDueOn:      decision.OccurrenceOn,
		Amount:           decision.AdoptAmount,
		HasAmount:        decision.HasAdoptAmount,
	}
}

// PointerUpdate is where the schedule pointer lands once this occurrence is
// fulfilled.
type PointerUpdate struct {
	// Applies is false on a back-fill: moving the pointer then would silently
	// skip a payment still to come.
	Applies   bool
	NextDueOn domain.Date
	// Deactivate parks a series with no occurrences left. The pointer stays
	// where it stood rather than being wound back to the start.
	Deactivate bool
	// ClearOverride drops a one-off override the paid occurrence used, or it
	// would drag the next occurrence onto the overridden date too.
	ClearOverride bool
}

func PointerFields(decision domain.MatchDecision, series SeriesRow) PointerUpdate {
	if decision.AdvancePointerTo.IsZero() && decision.Outcome == domain.OutcomeBackfill {
		return PointerUpdate{}
	}
	return PointerUpdate{
		Applies:       true,
		NextDueOn:     decision.AdvancePointerTo,
		Deactivate:    decision.AdvancePointerTo.IsZero(),
		ClearOverride: !series.OverrideNextDueOn.IsZero(),
	}
}

// LearnedDescriptionsAfter adds this charge's statement name to the series'
// wording list, reporting false when nothing changed. This is how one hand
// link teaches "Paycheck" to match "ACME CORP DES:PAYROLL".
func LearnedDescriptionsAfter(series SeriesRow, charge store.Transaction) ([]string, bool) {
	wording := strings.TrimSpace(charge.StatementName)
	if wording == "" || wording == series.Description {
		return nil, false
	}
	for _, existing := range series.LearnedDescriptions {
		if existing == wording {
			return nil, false
		}
	}
	return append(append([]string{}, series.LearnedDescriptions...), wording), true
}

// MatchOutcome: SurvivingID is the row the register keeps; RetiredID is the
// soft-deleted duplicate, which the balance recomputation needs.
type MatchOutcome struct {
	Outcome           domain.MatchOutcome
	SeriesID          uuid.UUID
	SurvivingID       uuid.UUID
	RetiredID         uuid.UUID
	PointerMovedTo    domain.Date
	SeriesDeactivated bool
}

func (m *SeriesMatcher) ApplyDecision(
	ctx context.Context,
	spaceID store.SpaceID,
	decision domain.MatchDecision,
	charge store.Transaction,
) (MatchOutcome, error) {
	seriesID, err := store.ParseID(decision.SeriesID)
	if err != nil {
		return MatchOutcome{}, err
	}
	series, err := m.GetSeries(ctx, spaceID, seriesID)
	if err != nil {
		return MatchOutcome{}, err
	}

	wordings, learned := LearnedDescriptionsAfter(series, charge)
	pointer := PointerFields(decision, series)
	outcome := MatchOutcome{
		Outcome:        decision.Outcome,
		SeriesID:       seriesID,
		SurvivingID:    charge.ID,
		PointerMovedTo: decision.AdvancePointerTo,
	}

	var placeholderID uuid.UUID
	if decision.Outcome == domain.OutcomeUpgradePlaceholder && decision.PlaceholderID != "" {
		if placeholderID, err = store.ParseID(decision.PlaceholderID); err != nil {
			return MatchOutcome{}, err
		}
	}
	upgrade := placeholderID != uuid.Nil && placeholderID != charge.ID

	err = m.inTx(ctx, spaceID, func(tx *store.Store) error {
		db, err := tx.HouseholdDB(ctx, spaceID)
		if err != nil {
			return err
		}
		currency, err := tx.HouseholdCurrency(ctx, spaceID)
		if err != nil {
			return err
		}
		if upgrade {
			// First: the survivor cannot take the bank's external id while the
			// duplicate still holds it.
			if err := tx.RetireDuplicate(ctx, spaceID, charge.ID); err != nil {
				return err
			}
			if err := absorb(ctx, db, currency, placeholderID, AbsorbFrom(charge, decision)); err != nil {
				return err
			}
			outcome.SurvivingID, outcome.RetiredID = placeholderID, charge.ID
		} else {
			_, err := db.Write().ExecContext(ctx, `
				UPDATE transactions SET series_id = ?, series_due_on = ?, updated_at = ? WHERE id = ?`,
				seriesID, decision.OccurrenceOn.String(), db.Now(), charge.ID)
			if err != nil {
				return fmt.Errorf("service: stamp series: %w", err)
			}
		}
		if _, _, err := tx.ReconcileReceipts(ctx, spaceID,
			[]uuid.UUID{outcome.SurvivingID, charge.ID}); err != nil {
			return err
		}
		return updateSeries(ctx, db, seriesID, pointer, wordings, learned)
	})
	if err != nil {
		return MatchOutcome{}, err
	}
	outcome.SeriesDeactivated = pointer.Applies && pointer.Deactivate
	return outcome, nil
}

// LinkByHand files the charge under the occurrence the user chose, through
// the same decision and writes as the matcher. No candidate gate runs: the
// user is overriding it. A placeholder in the slot is upgraded only from the
// series' own account, since the upgrade keeps the placeholder's account.
func (m *SeriesMatcher) LinkByHand(
	ctx context.Context, spaceID store.SpaceID, charge store.Transaction, series SeriesRow, occurrenceOn domain.Date,
) (MatchOutcome, error) {
	var placeholders []domain.MatchPlaceholder
	if charge.AccountID == series.AccountID {
		var err error
		placeholders, err = m.loadPlaceholders(ctx, spaceID, []uuid.UUID{series.ID}, occurrenceOn, occurrenceOn)
		if err != nil {
			return MatchOutcome{}, err
		}
	}
	decision := domain.DecideOccurrence(domain.MatchContext{Series: ToDomainSeries(series)},
		store.DomainTransaction(charge), occurrenceOn, placeholders)
	return m.ApplyDecision(ctx, spaceID, decision, charge)
}

// AdvancePast moves the schedule pointer as filing a charge under this
// occurrence would, for an occurrence answered without a bank charge: one
// accepted or skipped from the reminder.
func (m *SeriesMatcher) AdvancePast(
	ctx context.Context, spaceID store.SpaceID, series SeriesRow, occurrenceOn domain.Date,
) error {
	decision := domain.DecideOccurrence(domain.MatchContext{Series: ToDomainSeries(series)},
		domain.Transaction{}, occurrenceOn, nil)
	db, _, err := m.household(ctx, spaceID)
	if err != nil {
		return err
	}
	return updateSeries(ctx, db, series.ID, PointerFields(decision, series), nil, false)
}

func absorb(
	ctx context.Context, tx *sqlitedb.DB, primaryCurrency string, placeholderID uuid.UUID, fields AbsorbedFields,
) error {
	sets := []string{
		"external_id = ?", "statement_name = ?", "date = ?", "effective_date = ?",
		"currency = ?", "amount_primary = ?", "fx_rate_used = ?", "source = ?",
		"is_pending = ?", "series_due_on = ?", "estimate_status = NULL",
		// Recomputed for the whole account from the earliest touched date.
		"balance = NULL", "updated_at = ?",
	}
	args := []any{
		pgconv.NullText(fields.ExternalID), fields.StatementName,
		fields.Date.String(), optionalDate(fields.EffectiveDate), fields.Currency,
		sqlNullMoney(fields.AmountPrimary, fields.HasAmountPrimary, primaryCurrency),
		optionalDecimal(decimaltext.Format(fields.FxRateUsed), fields.HasFxRateUsed), string(fields.Source),
		fields.IsPending, optionalDate(fields.SeriesDueOn), tx.Now(),
	}
	if fields.HasAmount {
		args = append(args, sqlMoney(fields.Amount, fields.Currency))
		sets = append(sets, "amount = ?")
	}

	args = append(args, placeholderID)
	_, err := tx.Write().ExecContext(ctx,
		`UPDATE transactions SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("service: absorb charge into placeholder: %w", err)
	}
	return nil
}

func updateSeries(
	ctx context.Context,
	tx *sqlitedb.DB,
	seriesID uuid.UUID,
	pointer PointerUpdate,
	wordings []string,
	learned bool,
) error {
	var sets []string
	var args []any
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, column+" = ?")
	}
	if pointer.Applies {
		if pointer.Deactivate {
			add("is_active", false)
		} else {
			add("next_due_on", optionalDate(pointer.NextDueOn))
		}
		if pointer.ClearOverride {
			add("override_next_due_on", nil)
			add("override_next_amount", nil)
		}
	}
	if learned {
		raw, err := json.Marshal(wordings)
		if err != nil {
			return err
		}
		add("learned_descriptions", string(raw))
	}
	if len(sets) == 0 {
		return nil
	}
	add("updated_at", tx.Now())
	args = append(args, seriesID)
	_, err := tx.Write().ExecContext(ctx,
		`UPDATE series SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("service: update series: %w", err)
	}
	return nil
}

// MatchAll decides and writes for a batch of charges in order, returning what
// each match did. Each account is read once per batch and again only after a
// match, since a match changes the pointer, wording and placeholders the next
// row sees.
func (m *SeriesMatcher) MatchAll(
	ctx context.Context, spaceID store.SpaceID, charges []store.Transaction,
) ([]MatchOutcome, error) {
	return m.matchCharges(ctx, spaceID, charges, nil)
}

// MatchHistory offers the given series every unlinked row of their accounts
// dated from to to, oldest first, through the same decisions as MatchAll. A
// bill on file can make a past charge fit the slot it missed, so a bill pull
// and a new bill link call this. Only these series are offered the rows: a
// series nobody touched does not start claiming history.
func (m *SeriesMatcher) MatchHistory(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []uuid.UUID, from, to domain.Date,
) ([]MatchOutcome, error) {
	if len(seriesIDs) == 0 || to.Before(from) {
		return nil, nil
	}
	db, _, err := m.household(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	ids, args := sqlIn(seriesIDs)
	rows, err := db.Read().QueryContext(ctx, `
		SELECT id, account_id FROM series WHERE id IN `+ids+` AND is_active AND NOT is_deleted
		ORDER BY account_id, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("service: load history series: %w", err)
	}
	only := map[domain.ID]bool{}
	var accounts []uuid.UUID
	for rows.Next() {
		var seriesID, accountID uuid.UUID
		if err := rows.Scan(&seriesID, &accountID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("service: load history series: %w", err)
		}
		only[domain.ID(seriesID.String())] = true
		if !slices.Contains(accounts, accountID) {
			accounts = append(accounts, accountID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("service: load history series: %w", err)
	}

	var outcomes []MatchOutcome
	for _, accountID := range accounts {
		listed, err := m.store.ListTransactions(ctx, spaceID, store.TransactionQuery{
			AccountIDs: []uuid.UUID{accountID}, From: from, To: to,
		})
		if err != nil {
			return nil, err
		}
		slices.Reverse(listed)
		charges := slices.DeleteFunc(listed, func(row store.Transaction) bool {
			return !matchableCharge(row)
		})
		matched, err := m.matchCharges(ctx, spaceID, charges, only)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(matched, func(outcome MatchOutcome) bool {
			return outcome.RetiredID != uuid.Nil
		}) {
			if err := RecomputeRunningBalances(ctx, m.store, spaceID, accountID); err != nil {
				return nil, err
			}
		}
		outcomes = append(outcomes, matched...)
	}
	return outcomes, nil
}

// matchCharges is MatchAll offering the charges only to the series in only,
// or to every series of their accounts when it is nil.
func (m *SeriesMatcher) matchCharges(
	ctx context.Context, spaceID store.SpaceID, charges []store.Transaction, only map[domain.ID]bool,
) ([]MatchOutcome, error) {
	earliest, latest := map[uuid.UUID]domain.Date{}, map[uuid.UUID]domain.Date{}
	for _, charge := range charges {
		if !matchableCharge(charge) {
			continue
		}
		if first, seen := earliest[charge.AccountID]; !seen || charge.Date.Before(first) {
			earliest[charge.AccountID] = charge.Date
		}
		if last, seen := latest[charge.AccountID]; !seen || last.Before(charge.Date) {
			latest[charge.AccountID] = charge.Date
		}
	}

	loaded := map[uuid.UUID]accountMatch{}
	var outcomes []MatchOutcome
	for _, charge := range charges {
		if !matchableCharge(charge) {
			continue
		}
		account, seen := loaded[charge.AccountID]
		if !seen {
			var err error
			account, err = m.loadAccount(ctx, spaceID, charge.AccountID,
				earliest[charge.AccountID], latest[charge.AccountID], only)
			if err != nil {
				return nil, err
			}
			loaded[charge.AccountID] = account
		}
		decision, ok := account.decide(charge)
		if !ok {
			continue
		}
		outcome, err := m.ApplyDecision(ctx, spaceID, decision, charge)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, outcome)
		delete(loaded, charge.AccountID)
	}
	return outcomes, nil
}
