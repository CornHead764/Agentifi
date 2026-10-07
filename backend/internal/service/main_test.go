package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

func TestMain(m *testing.M) { storetest.Main(m, "service") }

var (
	db             = storetest.DB
	newSpace       = storetest.NewSpace
	newAccount     = storetest.NewAccount
	withKind       = storetest.WithKind
	withConnection = storetest.WithConnection
	newConnection  = storetest.NewConnection
	newCategory    = storetest.NewCategory
	newTag         = storetest.NewTag
)

type txnOption func(*store.Transaction)

func withStatementName(name string) txnOption {
	return func(t *store.Transaction) { t.StatementName = name }
}

func withSource(source domain.Source) txnOption {
	return func(t *store.Transaction) { t.Source = source }
}

func withCategory(id uuid.UUID) txnOption {
	return func(t *store.Transaction) { t.CategoryID = id }
}

func withTransferPair(id uuid.UUID) txnOption {
	return func(t *store.Transaction) { t.TransferPairID = id }
}

func withSeries(id uuid.UUID, dueOn domain.Date) txnOption {
	return func(t *store.Transaction) { t.SeriesID, t.SeriesDueOn = id, dueOn }
}

func withEstimate(status string) txnOption {
	return func(t *store.Transaction) { t.EstimateStatus = status }
}

func withPayee(payee string) txnOption {
	return func(t *store.Transaction) { t.Payee = payee }
}

func withExternalID(id string) txnOption {
	return func(t *store.Transaction) { t.ExternalID = id }
}

func withPending() txnOption {
	return func(t *store.Transaction) { t.IsPending = true }
}

func newTransaction(
	t *testing.T,
	spaceID store.SpaceID,
	account *store.Account,
	on domain.Date,
	amount string,
	opts ...txnOption,
) *store.Transaction {
	t.Helper()
	txn := &store.Transaction{
		AccountID: account.ID,
		Date:      on,
		Amount:    domain.MustFromString(amount),
		Currency:  account.Currency,
		Source:    domain.SourceSync,
	}
	for _, opt := range opts {
		opt(txn)
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))
	return txn
}

func reload(t *testing.T, spaceID store.SpaceID, id uuid.UUID) store.Transaction {
	t.Helper()
	txn, err := db(t).GetTransaction(t.Context(), spaceID, id)
	require.NoError(t, err)
	return txn
}

func on(year int, month time.Month, day int) domain.Date {
	return domain.NewDate(year, month, day)
}

type seriesOption func(*SeriesRow)

func seriesNextDueOn(due domain.Date) seriesOption {
	return func(s *SeriesRow) { s.NextDueOn = due }
}

func seriesCriteria(criteria domain.MatchCriteria) seriesOption {
	return func(s *SeriesRow) { s.MatchCriteria = string(criteria) }
}

func seriesOverrideDueOn(due domain.Date) seriesOption {
	return func(s *SeriesRow) { s.OverrideNextDueOn = due }
}

func seriesAmount(amount domain.Money) seriesOption {
	return func(s *SeriesRow) { s.Amount = amount }
}

// seriesAutoAdjustDueOn sets the Bill Connect switch: whether a linked
// biller's open statement moves its slot to the statement's due date.
func seriesAutoAdjustDueOn(dueOn bool) seriesOption {
	return func(s *SeriesRow) { s.AutoAdjustDueOn = dueOn }
}

func seriesDescription(description string) seriesOption {
	return func(s *SeriesRow) { s.Description = description }
}

// newSeries writes a monthly-on-the-15th series.
func newSeries(
	t *testing.T, spaceID store.SpaceID, account *store.Account, opts ...seriesOption,
) SeriesRow {
	t.Helper()
	row := SeriesRow{
		ID:                  uuid.New(),
		AccountID:           account.ID,
		Kind:                string(domain.SeriesBill),
		Description:         "STREAMSVC.COM",
		Amount:              domain.MustFromString("-15.00"),
		Currency:            account.Currency,
		Alias:               string(domain.AliasEveryMonth),
		Frequency:           string(domain.FreqMonthly),
		Interval:            1,
		ByMonthDay:          []int{15},
		ByDay:               []string{},
		StartOn:             on(2026, time.January, 15),
		NextDueOn:           on(2026, time.March, 15),
		MatchCriteria:       string(domain.CriteriaExact),
		LearnedDescriptions: []string{},
		IsActive:            true,
	}
	for _, opt := range opts {
		opt(&row)
	}
	_, err := db(t).Pool().Exec(t.Context(), `
		INSERT INTO series (id, space_id, account_id, kind, description, amount, currency, alias,
			frequency, "interval", by_month_day, by_day, start_on, next_due_on,
			override_next_due_on, reminder_days, match_criteria, learned_descriptions, is_active,
			auto_adjust_due_on)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 0, $16, $17, $18,
			$19)`,
		row.ID, spaceID.UUID(), row.AccountID, row.Kind, row.Description, pgconv.Money(row.Amount),
		row.Currency, row.Alias, pgconv.NullText(row.Frequency), row.Interval, row.ByMonthDay, row.ByDay,
		row.StartOn.Time(), pgconv.NullDate(row.NextDueOn), pgconv.NullDate(row.OverrideNextDueOn),
		row.MatchCriteria, row.LearnedDescriptions, row.IsActive,
		row.AutoAdjustDueOn)
	require.NoError(t, err)
	return row
}
