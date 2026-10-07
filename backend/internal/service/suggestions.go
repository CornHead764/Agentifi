package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Recurring suggestions: series the ledger implies but nobody created
// (Simplifi's Suggested tab). Nothing is created until the user accepts one,
// and a dismissal is remembered by the pattern's signature, not a row.
//
// A pattern is one counterparty (cleaned payee, else the statement name with
// digit-bearing tokens dropped), at a cadence the recurrence model can express,
// seen often and regularly enough, in a category a bill could live in (food is
// excluded), and still running.
//
// GetSuggestions sweeps the space; SuggestForTransaction answers for one row
// and still answers when the history is thin.

const (
	// HistoryDays: two years gives a confident monthly or quarterly read and a
	// plausible yearly one.
	HistoryDays = 730

	// MinOccurrences is how many sightings make a pattern. Yearly needs only
	// two, since three would span the whole history window.
	MinOccurrences       = 3
	MinOccurrencesYearly = 2

	// MaxSilenceDays is how long a pattern may go quiet and still be proposed;
	// a series predicts immediately, so a stopped one would invent charges. The
	// per-cadence floor keeps quarterly and yearly bills proposable.
	MaxSilenceDays = 92

	// MinConfidence combines occurrence count with evenness of spacing.
	MinConfidence = 0.5

	// keyTokens is enough tokens to tell payees apart, few enough that a trailing
	// store number or city does not split one payee into several groups.
	keyTokens = 4

	// coveredSimilarity is the bar domain.Decide accepts a link at.
	coveredSimilarity = domain.SimilarityThreshold

	defaultSuggestionLimit = 20
)

// oneOffCategoryTokens are category words for habitual rather than scheduled
// spending, matched against the category's name and its parent's. Kept to
// food: entertainment and shopping are where subscriptions get filed. Matched
// a token at a time after stripping accents, so "Barber" is not caught by
// "bar".
var oneOffCategoryTokens = map[string]bool{
	"food": true, "dining": true, "restaurant": true, "restaurants": true,
	"takeout": true, "takeaway": true, "coffee": true, "cafe": true, "cafes": true,
	"bar": true, "bars": true, "pub": true, "pubs": true, "snacks": true,
	"groceries": true, "grocery": true, "supermarket": true, "supermarkets": true,
	"comida": true, "comidas": true, "restaurante": true, "restaurantes": true,
	"cafeteria": true, "abarrotes": true, "mercado": true, "supermercado": true,
}

type Suggestions struct{ base }

func NewSuggestions(st *store.Store) *Suggestions { return &Suggestions{newBase(st)} }

type Row struct {
	ID            uuid.UUID
	AccountID     uuid.UUID
	CategoryID    uuid.UUID
	StatementName string
	Payee         string
	Amount        domain.Money
	Currency      string
	On            domain.Date
}

// GroupKey is what makes two transactions the same recurring event. A struct
// so a map key has no separator ambiguity; SignatureOf owns the stored
// encoding.
type GroupKey struct {
	AccountID uuid.UUID
	// Direction is "in" or "out": a refund in the same group would halve the
	// apparent cadence.
	Direction string
	Currency  string
	// Kind is "payee" (cleaned payee) or "text" (normalized statement name).
	Kind  string
	Value string
}

// RecurringSuggestion keeps Description (what a matcher compares) apart from
// DisplayName (what a person reads), as a series does.
type RecurringSuggestion struct {
	Signature   string
	AccountID   uuid.UUID
	CategoryID  uuid.UUID
	Description string
	DisplayName string
	Kind        domain.SeriesKind
	Currency    string
	Amount      domain.Money
	Tolerance   domain.AmountTolerance
	Recurrence  domain.Recurrence

	// StartOn is the next occurrence the pattern predicts, never one in the
	// past, which would re-materialize occurrences the ledger holds.
	StartOn domain.Date

	Occurrences int
	FirstSeen   domain.Date
	LastSeen    domain.Date
	Confidence  float64

	// GeneratePlaceholders is false on a connected account, where the real
	// charge always arrives and an unmatched placeholder is a stale row.
	GeneratePlaceholders bool
	TransactionIDs       []uuid.UUID
}

func DescriptionKey(text string) string {
	tokens := domain.IdentifyingWords(text)
	if len(tokens) > keyTokens {
		tokens = tokens[:keyTokens]
	}
	return strings.Join(tokens, " ")
}

// DisplayNameFor keeps the original casing and cuts at the first token
// carrying a digit: "ACME CORP DES:PAYROLL ID:9904" becomes
// "ACME CORP DES:PAYROLL".
func DisplayNameFor(row Row) string {
	if row.Payee != "" {
		return row.Payee
	}
	var kept []string
	for _, word := range strings.Fields(row.StatementName) {
		if strings.ContainsAny(word, "0123456789") {
			break
		}
		kept = append(kept, word)
	}
	if len(kept) == 0 {
		return row.StatementName
	}
	return strings.Join(kept, " ")
}

// GroupKeyOf reports the row's group, or false to skip it. A cleaned payee
// wins when present; otherwise a statement name normalizing to nothing cannot
// be grouped.
func GroupKeyOf(row Row) (GroupKey, bool) {
	key := GroupKey{AccountID: row.AccountID, Currency: row.Currency, Direction: "out"}
	if row.Amount.IsPositive() {
		key.Direction = "in"
	}
	if row.Payee != "" {
		key.Kind, key.Value = "payee", strings.ToLower(row.Payee)
		return key, true
	}
	text := DescriptionKey(row.StatementName)
	if text == "" {
		return GroupKey{}, false
	}
	key.Kind, key.Value = "text", text
	return key, true
}

// SignatureOf is a stable id for a group, so a dismissal outlives the sweep.
func SignatureOf(key GroupKey) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s",
		key.AccountID, key.Direction, key.Currency, key.Kind, key.Value)))
	return hex.EncodeToString(sum[:])
}

func Build(key GroupKey, rows []Row, today domain.Date, generate bool) (RecurringSuggestion, bool) {
	rows = append([]Row{}, rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].On.Before(rows[j].On) })
	if len(rows) < 2 {
		return RecurringSuggestion{}, false
	}

	intervals := make([]int, 0, len(rows)-1)
	for i := 1; i < len(rows); i++ {
		gap := domain.DaysBetween(rows[i-1].On, rows[i].On)
		// Two charges on one day are one event, not a zero-day cadence.
		if gap <= 0 {
			return RecurringSuggestion{}, false
		}
		intervals = append(intervals, gap)
	}

	name, regularity, ok := domain.ClassifyCadence(intervals)
	if !ok {
		return RecurringSuggestion{}, false
	}
	floor := MinOccurrences
	if name == "yearly" {
		floor = MinOccurrencesYearly
	}
	if len(rows) < floor {
		return RecurringSuggestion{}, false
	}

	// Diminishing returns: six occurrences is not twice as convincing as three.
	volume := math.Min(1.0, float64(len(rows))/6)
	confidence := math.Round(regularity*(0.6+0.4*volume)*1000) / 1000
	if confidence < MinConfidence {
		return RecurringSuggestion{}, false
	}

	amounts := make([]domain.Money, len(rows))
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		amounts[i], ids[i] = row.Amount, row.ID
	}
	last := rows[len(rows)-1]
	recurrence := domain.RecurrenceFor(name, last.On)

	kind := domain.SeriesBill
	if last.Amount.IsPositive() {
		kind = domain.SeriesIncome
	}
	description := last.StatementName
	if description == "" {
		description = DisplayNameFor(last)
	}

	return RecurringSuggestion{
		Signature: SignatureOf(key),
		AccountID: last.AccountID,
		// From the most recent occurrence that has one.
		CategoryID:           latestCategoryID(rows),
		Description:          description,
		DisplayName:          DisplayNameFor(last),
		Kind:                 kind,
		Currency:             last.Currency,
		Amount:               domain.MedianAmount(amounts),
		Tolerance:            domain.SuggestTolerance(amounts),
		Recurrence:           recurrence,
		StartOn:              domain.NextStart(recurrence, last.On, today),
		Occurrences:          len(rows),
		FirstSeen:            rows[0].On,
		LastSeen:             last.On,
		Confidence:           confidence,
		GeneratePlaceholders: generate,
		TransactionIDs:       ids,
	}, true
}

func latestCategoryID(rows []Row) uuid.UUID {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].CategoryID != uuid.Nil {
			return rows[i].CategoryID
		}
	}
	return uuid.Nil
}

// HasGoneQuiet: silence longer than the cadence means the pattern ended. Only
// the sweep applies it; a row the user points at may be an old bill revived.
func HasGoneQuiet(suggestion RecurringSuggestion, today domain.Date) bool {
	allowance := MaxSilenceDays
	if cadence := int(math.Round(domain.PeriodDays(suggestion.Recurrence) *
		(1 + domain.MaxIntervalJitter))); cadence > allowance {
		allowance = cadence
	}
	return domain.DaysBetween(suggestion.LastSeen, today) > allowance
}

// ExistingSeries is one live series as the coverage test reads it.
type ExistingSeries struct {
	AccountID   uuid.UUID
	Amount      domain.Money
	Description string
	Wordings    []string
	PeriodDays  float64
}

// CoveredByExisting is deliberately generous: on the same account and
// direction, either matching wording or the same amount at the same cadence
// counts, because a duplicate suggestion invites a double series. The amount
// arm catches "Rent" named for "PROPERTY MGMT CO"; its cost is that two
// same-sized subscriptions on one account hide each other.
func CoveredByExisting(suggestion RecurringSuggestion, existing []ExistingSeries) bool {
	for _, series := range existing {
		if series.AccountID != suggestion.AccountID {
			continue
		}
		if series.Amount.IsPositive() != suggestion.Amount.IsPositive() {
			continue
		}
		texts := append([]string{series.Description}, series.Wordings...)
		best := 0.0
		for _, text := range texts {
			if found := domain.TokenSimilarity(text, suggestion.Description); found > best {
				best = found
			}
		}
		if best >= coveredSimilarity {
			return true
		}
		period := domain.PeriodDays(suggestion.Recurrence)
		if math.Abs(series.PeriodDays-period) > series.PeriodDays*0.1 {
			continue
		}
		if series.Amount.Equal(suggestion.Amount) {
			return true
		}
	}
	return false
}

func ReadsAsOneOff(names ...string) bool {
	for _, name := range names {
		if name == "" {
			continue
		}
		for _, token := range domain.Words(name) {
			if oneOffCategoryTokens[token] {
				return true
			}
		}
	}
	return false
}

// Rank keeps one proposal per account and schedule (the better-evidenced of
// a payee-keyed and a text-keyed group for one counterparty), keeps the most
// confident up to the limit, then orders by recency.
func Rank(suggestions []RecurringSuggestion, limit int) []RecurringSuggestion {
	ordered := append([]RecurringSuggestion{}, suggestions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Occurrences != ordered[j].Occurrences {
			return ordered[i].Occurrences > ordered[j].Occurrences
		}
		return ordered[i].Confidence > ordered[j].Confidence
	})

	seen := map[string]bool{}
	var ranked []RecurringSuggestion
	for _, suggestion := range ordered {
		key := dedupeKey(suggestion)
		if seen[key] {
			continue
		}
		seen[key] = true
		ranked = append(ranked, suggestion)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Confidence != ranked[j].Confidence {
			return ranked[i].Confidence > ranked[j].Confidence
		}
		return ranked[i].Occurrences > ranked[j].Occurrences
	})
	if limit >= 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].LastSeen != ranked[j].LastSeen {
			return ranked[i].LastSeen.After(ranked[j].LastSeen)
		}
		return ranked[i].Confidence > ranked[j].Confidence
	})
	return ranked
}

func dedupeKey(s RecurringSuggestion) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s",
		s.AccountID, s.Currency, s.Amount, recurrenceKey(s.Recurrence), s.StartOn)
}

// recurrenceKey exists because domain.Recurrence holds slices and cannot key
// a map.
func recurrenceKey(r domain.Recurrence) string {
	return fmt.Sprintf("%s/%s/%d/%v/%v", r.Alias, r.Frequency, r.Interval, r.ByMonthDay, r.ByDay)
}

// candidateRows is the history worth reading for patterns: not already linked
// to a series, and passing domain.CountsAsIncomeOrExpense on loaded postings
// rather than a second copy of that predicate in SQL.
func (s *Suggestions) candidateRows(
	ctx context.Context, spaceID store.SpaceID, since domain.Date,
) ([]Row, error) {
	txns, err := s.store.ListTransactions(ctx, spaceID, store.TransactionQuery{From: since})
	if err != nil {
		return nil, err
	}
	if len(txns) == 0 {
		return nil, nil
	}
	accounts, err := s.store.ListAccounts(ctx, spaceID,
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	categories, err := s.store.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nil, err
	}
	postings, err := store.BuildPostings(txns, accounts, categories)
	if err != nil {
		return nil, err
	}

	var out []Row
	for i, txn := range txns {
		// visibleAccountsOnly: a closed account's bill cannot recur.
		if !domain.CountsAsIncomeOrExpense(postings[i], true) {
			continue
		}
		if txn.IsPending || txn.SeriesID != uuid.Nil || txn.EstimateStatus != "" {
			continue
		}
		out = append(out, rowOf(txn))
	}
	return out, nil
}

func rowOf(txn store.Transaction) Row {
	return Row{
		ID:            txn.ID,
		AccountID:     txn.AccountID,
		CategoryID:    txn.CategoryID,
		StatementName: txn.StatementName,
		Payee:         txn.Payee,
		Amount:        txn.Amount,
		Currency:      txn.Currency,
		On:            txn.Date,
	}
}

func (s *Suggestions) liveSeries(
	ctx context.Context, spaceID store.SpaceID,
) ([]ExistingSeries, error) {
	matcher := SeriesMatcher{s.base}
	rows, err := matcher.LoadSeriesRows(ctx,
		`SELECT `+seriesColumns+` FROM series
		 WHERE space_id = $1 AND is_active AND NOT is_deleted ORDER BY id`, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	out := make([]ExistingSeries, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExistingSeries{
			AccountID:   row.AccountID,
			Amount:      row.Amount,
			Description: row.Description,
			Wordings:    row.LearnedDescriptions,
			PeriodDays:  domain.PeriodDays(ToRecurrence(row)),
		})
	}
	return out, nil
}

// connectedAccountIDs are the accounts a provider writes to, where a series
// should not materialize placeholder rows.
func (s *Suggestions) connectedAccountIDs(
	ctx context.Context, spaceID store.SpaceID,
) (map[uuid.UUID]bool, error) {
	accounts, err := s.store.ListAccounts(ctx, spaceID,
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	connected := map[uuid.UUID]bool{}
	for _, account := range accounts {
		if account.ConnectionID != uuid.Nil {
			connected[account.ID] = true
		}
	}
	return connected, nil
}

// oneOffCategoryIDs checks the parent as well, so dining children under a
// Food parent are covered without listing each name.
func (s *Suggestions) oneOffCategoryIDs(
	ctx context.Context, spaceID store.SpaceID,
) (map[uuid.UUID]bool, error) {
	categories, err := s.store.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(categories))
	for _, category := range categories {
		names[category.ID] = category.Name
	}
	oneOff := map[uuid.UUID]bool{}
	for _, category := range categories {
		if ReadsAsOneOff(category.Name, names[category.ParentID]) {
			oneOff[category.ID] = true
		}
	}
	return oneOff, nil
}

type SuggestionQuery struct {
	// Today is the day the sweep is run for; zero reads the clock.
	Today domain.Date
	// Limit is how many proposals to return; zero means the tab's default.
	Limit int
	// Dismissed holds the signatures the user has dismissed, loaded by the
	// caller from suggestion_dismissals.
	Dismissed map[string]bool
}

func (q SuggestionQuery) today() domain.Date {
	if q.Today.IsZero() {
		return domain.DateOf(time.Now())
	}
	return q.Today
}

func (q SuggestionQuery) limit() int {
	if q.Limit == 0 {
		return defaultSuggestionLimit
	}
	return q.Limit
}

func (s *Suggestions) GetSuggestions(
	ctx context.Context, spaceID store.SpaceID, q SuggestionQuery,
) ([]RecurringSuggestion, error) {
	today := q.today()
	rows, err := s.candidateRows(ctx, spaceID, today.AddDays(-HistoryDays))
	if err != nil {
		return nil, err
	}

	groups, order := groupRows(rows)
	existing, err := s.liveSeries(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	connected, err := s.connectedAccountIDs(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	oneOff, err := s.oneOffCategoryIDs(ctx, spaceID)
	if err != nil {
		return nil, err
	}

	var suggestions []RecurringSuggestion
	for _, key := range order {
		members := groups[key]
		suggestion, ok := Build(key, members, today, !connected[members[0].AccountID])
		if !ok || q.Dismissed[suggestion.Signature] {
			continue
		}
		if HasGoneQuiet(suggestion, today) {
			continue
		}
		if suggestion.CategoryID != uuid.Nil && oneOff[suggestion.CategoryID] {
			continue
		}
		if CoveredByExisting(suggestion, existing) {
			continue
		}
		suggestions = append(suggestions, suggestion)
	}
	return Rank(suggestions, q.limit()), nil
}

// groupRows keeps first-seen order so a sweep is deterministic.
func groupRows(rows []Row) (map[GroupKey][]Row, []GroupKey) {
	groups := map[GroupKey][]Row{}
	var order []GroupKey
	for _, row := range rows {
		key, ok := GroupKeyOf(row)
		if !ok {
			continue
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}
	return groups, order
}

// SuggestForTransaction backs "make this recurring": it answers even with thin
// history, falling back to a monthly series on the row's own amount and date.
// It reports false only when the row is already linked or has no wording.
func (s *Suggestions) SuggestForTransaction(
	ctx context.Context, spaceID store.SpaceID, transactionID uuid.UUID, today domain.Date,
) (RecurringSuggestion, bool, error) {
	if today.IsZero() {
		today = domain.DateOf(time.Now())
	}
	txn, err := s.store.GetTransaction(ctx, spaceID, transactionID)
	if err != nil {
		return RecurringSuggestion{}, false, err
	}
	if txn.SeriesID != uuid.Nil {
		return RecurringSuggestion{}, false, nil
	}
	row := rowOf(txn)
	key, ok := GroupKeyOf(row)
	if !ok {
		return RecurringSuggestion{}, false, nil
	}

	history, err := s.candidateRows(ctx, spaceID, today.AddDays(-HistoryDays))
	if err != nil {
		return RecurringSuggestion{}, false, err
	}
	var members []Row
	found := false
	for _, candidate := range history {
		if candidateKey, ok := GroupKeyOf(candidate); !ok || candidateKey != key {
			continue
		}
		members = append(members, candidate)
		found = found || candidate.ID == row.ID
	}
	if !found {
		// Outside the sweep, but the user asked about this row.
		members = append(members, row)
	}

	connected, err := s.connectedAccountIDs(ctx, spaceID)
	if err != nil {
		return RecurringSuggestion{}, false, err
	}
	generate := !connected[row.AccountID]

	if suggestion, ok := Build(key, members, today, generate); ok {
		return suggestion, true, nil
	}

	// No usable pattern: fall back to the transaction as written.
	recurrence := domain.EveryMonth(row.On.Day)
	kind := domain.SeriesBill
	if row.Amount.IsPositive() {
		kind = domain.SeriesIncome
	}
	description := row.StatementName
	if description == "" {
		description = DisplayNameFor(row)
	}
	first, last := members[0].On, members[0].On
	ids := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		if member.On.Before(first) {
			first = member.On
		}
		if member.On.After(last) {
			last = member.On
		}
		ids = append(ids, member.ID)
	}
	return RecurringSuggestion{
		Signature:            SignatureOf(key),
		AccountID:            row.AccountID,
		CategoryID:           row.CategoryID,
		Description:          description,
		DisplayName:          DisplayNameFor(row),
		Kind:                 kind,
		Currency:             row.Currency,
		Amount:               row.Amount,
		Tolerance:            domain.ExactAmount(),
		Recurrence:           recurrence,
		StartOn:              domain.NextStart(recurrence, row.On, today),
		Occurrences:          len(members),
		FirstSeen:            first,
		LastSeen:             last,
		Confidence:           0.0,
		GeneratePlaceholders: generate,
		TransactionIDs:       ids,
	}, true, nil
}
