package store_test

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteTags(t *testing.T) {
	now := time.Date(2026, time.January, 5, 12, 0, 0, 123000000, time.UTC)
	st, err := store.OpenSQLite(t.Context(), t.TempDir(), func() time.Time { return now })
	require.NoError(t, err)
	t.Cleanup(st.Close)
	space := storetest.NewHousehold(t, st)
	other := storetest.NewHousehold(t, st)
	tag := &store.Tag{Name: "Travel"}
	require.NoError(t, st.CreateTag(t.Context(), space, tag))
	require.False(t, tag.CreatedAt.IsZero())
	got, err := st.GetTag(t.Context(), space, tag.ID)
	require.NoError(t, err)
	require.Equal(t, *tag, got)
	_, err = st.GetTag(t.Context(), other, tag.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	tag.Name, tag.Color = "Trips", "blue"
	now = now.Add(time.Second)
	require.NoError(t, st.UpdateTag(t.Context(), space, tag))
	got, err = st.GetTag(t.Context(), space, tag.ID)
	require.NoError(t, err)
	require.Equal(t, *tag, got)
	require.Equal(t, now, got.UpdatedAt, "updates preserve the injected store clock")
	// Repeated writes within one clock tick still must not switch to wall time.
	tag.Color = "green"
	require.NoError(t, st.UpdateTag(t.Context(), space, tag))
	got, err = st.GetTag(t.Context(), space, tag.ID)
	require.NoError(t, err)
	require.Equal(t, *tag, got)
	require.NoError(t, st.CreateTag(t.Context(), other, &store.Tag{Name: "Trips"}))
	require.NoError(t, st.DeleteTag(t.Context(), space, tag.ID))
	tags, err := st.ListTags(t.Context(), space, false)
	require.NoError(t, err)
	require.Empty(t, tags)
	tags, err = st.ListTags(t.Context(), space, true)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	require.True(t, tags[0].IsDeleted)
	require.ErrorIs(t, st.DeleteTag(t.Context(), space, uuid.New()), store.ErrNotFound)
}

func TestSQLiteIgnoredAccounts(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	connection := uuid.New()
	_, err = db.Write().ExecContext(t.Context(), `INSERT INTO connections (id,access_url_encrypted,status,created_at,updated_at) VALUES (?,'','active',?,?)`, connection, db.Now(), db.Now())
	require.NoError(t, err)
	one := &store.IgnoredRemoteAccount{ConnectionID: connection, ExternalID: "remote", Name: "Old"}
	require.NoError(t, st.IgnoreRemoteAccount(t.Context(), space, one))
	original := *one
	one = &store.IgnoredRemoteAccount{ConnectionID: connection, ExternalID: "remote", Name: "New"}
	require.NoError(t, st.IgnoreRemoteAccount(t.Context(), space, one))
	require.Equal(t, original.ID, one.ID)
	require.Equal(t, original.IgnoredAt, one.IgnoredAt)
	list, err := st.ListIgnoredRemoteAccounts(t.Context(), space, connection)
	require.NoError(t, err)
	require.Equal(t, []store.IgnoredRemoteAccount{*one}, list)
	ids, err := st.IgnoredExternalIDs(t.Context(), space, connection)
	require.NoError(t, err)
	require.True(t, ids["remote"])
	restored, err := st.RestoreRemoteAccount(t.Context(), space, uuid.New(), one.ID)
	require.NoError(t, err)
	require.False(t, restored)
	restored, err = st.RestoreRemoteAccount(t.Context(), space, connection, one.ID)
	require.NoError(t, err)
	require.True(t, restored)
	restored, err = st.RestoreRemoteAccount(t.Context(), space, connection, one.ID)
	require.NoError(t, err)
	require.False(t, restored)
}

func TestSQLiteCategories(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	parent := &store.Category{Name: "Food", Kind: domain.CategoryExpense, IsEditable: true}
	require.NoError(t, st.CreateCategory(t.Context(), space, parent))
	child := &store.Category{Name: "Cafe", Kind: domain.CategoryExpense, ParentID: parent.ID, TxfIDs: []string{"1", "2"}, IsEditable: true}
	require.NoError(t, st.CreateCategory(t.Context(), space, child))
	got, err := st.GetCategory(t.Context(), space, child.ID)
	require.NoError(t, err)
	require.Equal(t, *child, got)
	child.Name, child.TxfIDs = "Coffee", nil
	require.NoError(t, st.UpdateCategory(t.Context(), space, child))
	got, err = st.GetCategory(t.Context(), space, child.ID)
	require.NoError(t, err)
	require.Equal(t, *child, got)
	require.NoError(t, st.DeleteCategory(t.Context(), space, child.ID))
	live, err := st.ListCategories(t.Context(), space, false)
	require.NoError(t, err)
	require.Len(t, live, 1)
	parent.IsEditable = false
	require.NoError(t, st.UpdateCategory(t.Context(), space, parent))
	require.ErrorIs(t, st.DeleteCategory(t.Context(), space, parent.ID), store.ErrCategoryProtected)
	require.ErrorIs(t, st.DeleteCategory(t.Context(), space, uuid.New()), store.ErrNotFound)
	other := storetest.NewHousehold(t, st)
	_, err = st.GetCategory(t.Context(), other, parent.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestSQLiteSeedCategories(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	result, err := st.SeedCategories(t.Context(), space, domain.DefaultCategories)
	require.NoError(t, err)
	require.Positive(t, result.Created)
	again, err := st.SeedCategories(t.Context(), space, domain.DefaultCategories)
	require.NoError(t, err)
	require.Zero(t, again.Created)
	require.Equal(t, result.Created, again.Existing)
	untouched, err := st.OnlyUntouchedDefaults(t.Context(), space, domain.DefaultCategories)
	require.NoError(t, err)
	require.True(t, untouched)
}

func sqliteLedgerRows(t *testing.T, st *store.Store, space store.SpaceID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	account, charge, refund := uuid.New(), uuid.New(), uuid.New()
	_, err = db.Write().ExecContext(t.Context(), `INSERT INTO accounts (id,name,kind,type,currency,sort_order,created_at,updated_at) VALUES (?,'Checking','checking','checking','USD',0,?,?)`, account, db.Now(), db.Now())
	require.NoError(t, err)
	for _, id := range []uuid.UUID{charge, refund} {
		_, err = db.Write().ExecContext(t.Context(), `INSERT INTO transactions (id,account_id,date,amount,currency,source,created_at,updated_at) VALUES (?,?,'2026-01-01',100,'USD','manual',?,?)`, id, account, db.Now(), db.Now())
		require.NoError(t, err)
	}
	return charge, refund
}

func TestSQLiteRefundLinks(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	charge, refund := sqliteLedgerRows(t, st, space)
	category := &store.Category{Name: "Shopping", Kind: domain.CategoryExpense, IsEditable: true}
	require.NoError(t, st.CreateCategory(t.Context(), space, category))
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(t.Context(), `UPDATE transactions SET category_id = ? WHERE id = ?`, category.ID, charge)
	require.NoError(t, err)
	require.NoError(t, st.LinkRefund(t.Context(), space, refund, charge))
	require.NoError(t, st.LinkRefund(t.Context(), space, refund, charge))
	links, err := st.ListRefundLinks(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, domain.ID(category.ID.String()), links[0].ChargeCategoryID)
	touching, err := st.ListRefundLinksFor(t.Context(), space, []uuid.UUID{charge})
	require.NoError(t, err)
	require.Equal(t, []store.RefundLink{{RefundTxnID: refund, ChargeTxnID: charge}}, touching)
	require.NoError(t, st.RefileLinkedRefunds(t.Context(), space, charge, nil))
	var value *string
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT category_id FROM transactions WHERE id = ?`, refund).Scan(&value))
	require.Nil(t, value)
	n, err := st.UnlinkRefund(t.Context(), space, refund, charge)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.ErrorIs(t, st.LinkRefund(t.Context(), space, uuid.New(), charge), store.ErrNotFound)
}

func TestSQLiteCategoryChecks(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	charge, _ := sqliteLedgerRows(t, st, space)
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	automation, run := uuid.New(), uuid.New()
	_, err = db.Write().ExecContext(t.Context(), `INSERT INTO assistant_automations (id,created_by,name,trigger,prompt,created_at,updated_at) VALUES (?,'user','Category','manual','',?,?)`, automation, db.Now(), db.Now())
	require.NoError(t, err)
	_, err = db.Write().ExecContext(t.Context(), `INSERT INTO assistant_automation_runs (id,automation_id,fired_by,transaction_id,queued_at) VALUES (?,?,'manual',?,?)`, run, automation, charge, db.Now())
	require.NoError(t, err)
	pending, err := st.PendingCategoryChecks(t.Context(), space, []uuid.UUID{charge})
	require.NoError(t, err)
	require.True(t, pending[charge])
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	require.NoError(t, st.MarkCategoryUndetermined(t.Context(), space, charge, at, "No match", run))
	var checked int64
	var note string
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT category_checked_at,category_check_note FROM transactions WHERE id = ?`, charge).Scan(&checked, &note))
	require.Equal(t, at.UnixMilli(), checked)
	require.Equal(t, "No match", note)
	require.NoError(t, st.ClearCategoryCheck(t.Context(), space, charge, run))
	var cleared *int64
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT category_checked_at,category_check_note FROM transactions WHERE id = ?`, charge).Scan(&cleared, &note))
	require.Nil(t, cleared)
	require.Empty(t, note)
}

func TestSQLiteCategoryUses(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	charge, _ := sqliteLedgerRows(t, st, space)
	category := &store.Category{Name: "Food", Kind: domain.CategoryExpense, IsEditable: true}
	require.NoError(t, st.CreateCategory(t.Context(), space, category))
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(t.Context(), `UPDATE transactions SET category_id = ? WHERE id = ?`, category.ID, charge)
	require.NoError(t, err)
	uses, err := st.CategoryUses(t.Context(), space, nil)
	require.NoError(t, err)
	require.Empty(t, uses)
	_, err = db.Write().ExecContext(t.Context(), `UPDATE transactions SET is_reviewed = 1 WHERE id = ?`, charge)
	require.NoError(t, err)
	uses, err = st.CategoryUses(t.Context(), space, []uuid.UUID{category.ID})
	require.NoError(t, err)
	require.Equal(t, []string{"reviewed transactions"}, uses[category.ID])
	uses, err = st.CategoryUses(t.Context(), space, []uuid.UUID{uuid.New()})
	require.NoError(t, err)
	require.Empty(t, uses)
	tags, err := st.TagUses(t.Context(), space, nil)
	require.NoError(t, err)
	require.Empty(t, tags)
}

func TestSQLiteDuplicates(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	first, second := sqliteLedgerRows(t, st, space)
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	var account uuid.UUID
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT account_id FROM transactions WHERE id = ?`, first).Scan(&account))
	key := domain.DuplicateKey(domain.ID(first.String()), domain.ID(second.String()))
	pairs := []domain.DuplicateCandidate{{First: key[0], Second: key[1], DaysApart: 0}}
	n, err := st.SaveDuplicateCandidates(t.Context(), space, account, pairs)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	n, err = st.SaveDuplicateCandidates(t.Context(), space, account, pairs)
	require.NoError(t, err)
	require.Zero(t, n)
	open, err := st.ListOpenDuplicates(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, open, 1)
	require.False(t, open[0].CreatedAt.IsZero())
	count, err := st.CountOpenDuplicates(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.NoError(t, st.DecideDuplicate(t.Context(), space, open[0].ID, store.VerdictDistinct, uuid.Nil, uuid.New()))
	one, err := st.GetDuplicate(t.Context(), space, open[0].ID)
	require.NoError(t, err)
	require.NotNil(t, one.DecidedAt)
	require.Equal(t, store.VerdictDistinct, one.Verdict)
	require.ErrorIs(t, st.DecideDuplicate(t.Context(), space, one.ID, store.VerdictDuplicate, first, uuid.Nil), store.ErrNotFound)
	ruledOut, err := st.RuledOutDuplicates(t.Context(), space, []uuid.UUID{account})
	require.NoError(t, err)
	require.True(t, ruledOut[key])
	ruledOut, err = st.RuledOutDuplicates(t.Context(), space, nil)
	require.NoError(t, err)
	require.Empty(t, ruledOut)
}

func TestSQLiteBalanceSnapshots(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	first, _ := sqliteLedgerRows(t, st, space)
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	var account uuid.UUID
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT account_id FROM transactions WHERE id = ?`, first).Scan(&account))
	_, err = db.Write().ExecContext(t.Context(), `UPDATE accounts SET currency = 'JPY' WHERE id = ?`, account)
	require.NoError(t, err)
	day := domain.DateOf(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	snapshot := store.BalanceSnapshot{AccountID: account, AsOf: day, Balance: domain.MustFromString("123"),
		Anchor: domain.BalanceAnchor{On: day, Balance: domain.MustFromString("150")}, HasAnchor: true}
	require.NoError(t, st.UpsertBalanceSnapshots(t.Context(), space, []store.BalanceSnapshot{snapshot}))
	var minor int64
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT balance FROM balance_snapshots WHERE account_id = ?`, account).Scan(&minor))
	require.Equal(t, int64(123), minor)
	history, err := st.ListAllBalanceHistory(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.True(t, history[0].HasAnchor)
	require.Equal(t, "123.00", history[0].Balance.String())
	require.Equal(t, "150.00", history[0].Anchor.Balance.String())
	established, ok, err := st.LastEstablishedBalance(t.Context(), space, account)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, history[0].Balance, established.Balance)
	history, err = st.ListBalanceHistory(t.Context(), space, day.AddDays(-1))
	require.NoError(t, err)
	require.Empty(t, history)
	snapshot.Balance = domain.MustFromString("1.5")
	require.Error(t, st.UpsertBalanceSnapshots(t.Context(), space, []store.BalanceSnapshot{snapshot}))
}

func TestSQLiteConversions(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	first, _ := sqliteLedgerRows(t, st, space)
	require.NoError(t, st.SetTransactionConversion(t.Context(), space, first, domain.MustFromString("12.34"), domain.MustFromString("1.25").Decimal()))
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	var amount int64
	var rate string
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT amount_primary,fx_rate_used FROM transactions WHERE id = ?`, first).Scan(&amount, &rate))
	require.Equal(t, int64(1234), amount)
	require.Equal(t, "1.25", rate)
	n, err := st.ClearConversions(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	n, err = st.ClearConversions(t.Context(), space)
	require.NoError(t, err)
	require.Zero(t, n)
	require.ErrorIs(t, st.SetTransactionConversion(t.Context(), space, uuid.New(), domain.Zero, domain.MustFromString("1").Decimal()), store.ErrNotFound)
}

func TestSQLiteAccountRoundTrip(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	a := &store.Account{Name: "Yen", Kind: domain.AccountKind("checking"), Type: "checking", Currency: "JPY",
		OpeningBalance: domain.MustFromString("123"), HasProviderBalance: true,
		ProviderBalance: domain.MustFromString("456"), IncludeInNetWorth: true}
	require.NoError(t, st.CreateAccount(t.Context(), space, a))
	got, err := st.GetAccount(t.Context(), space, a.ID)
	require.NoError(t, err)
	require.Equal(t, a.OpeningBalance, got.OpeningBalance)
	require.Equal(t, a.ProviderBalance, got.ProviderBalance)
	require.Equal(t, space, got.SpaceID)
	require.Equal(t, a.CreatedAt, got.CreatedAt)
	a.Name = "Updated"
	require.NoError(t, st.UpdateAccount(t.Context(), space, a))
	list, err := st.ListAccounts(t.Context(), space, store.AccountQuery{IDs: []uuid.UUID{a.ID}})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Updated", list[0].Name)
	empty, err := st.ListAccounts(t.Context(), space, store.AccountQuery{IDs: []uuid.UUID{}})
	require.NoError(t, err)
	require.Empty(t, empty)
	changed, err := st.SetAccountsIgnored(t.Context(), space, []uuid.UUID{a.ID}, true)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{a.ID}, changed)
	changed, err = st.SetAccountsIgnored(t.Context(), space, []uuid.UUID{a.ID}, true)
	require.NoError(t, err)
	require.Empty(t, changed)
	require.NoError(t, st.DeleteAccount(t.Context(), space, a.ID))
	a.ID = uuid.New()
	a.OpeningBalance = domain.MustFromString("1.5")
	require.Error(t, st.CreateAccount(t.Context(), space, a))
}

func TestSQLiteTransactionRead(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	first, _ := sqliteLedgerRows(t, st, space)
	require.NoError(t, st.SetTransactionConversion(t.Context(), space, first, domain.MustFromString("12.34"), domain.MustFromString("1.25").Decimal()))
	got, err := st.GetTransaction(t.Context(), space, first)
	require.NoError(t, err)
	require.Equal(t, space, got.SpaceID)
	require.True(t, got.HasAmountPrimary)
	require.Equal(t, "12.34", got.AmountPrimary.String())
	require.Equal(t, "1.25", got.FxRateUsed.String())
	list, err := st.ListTransactions(t.Context(), space, store.TransactionQuery{IDs: []uuid.UUID{first}})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, got, list[0])
}

func TestSQLiteFiltersAndRules(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	other := storetest.NewHousehold(t, st)
	state := false
	f := &store.Filter{Name: "Search", Scope: "ad_hoc", Items: []store.FilterItem{{
		Field: "amount", Operator: "between", AmountMin: domain.MustFromString("12.34"),
		HasAmountMin: true, DateFrom: domain.DateOf(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), State: &state,
		ValueIDs: []uuid.UUID{uuid.New()}, ValueTexts: []string{"hello"},
	}}}
	require.NoError(t, st.CreateFilter(t.Context(), space, f))
	got, err := st.GetFilter(t.Context(), space, f.ID)
	require.NoError(t, err)
	require.Equal(t, *f, got)
	_, err = st.GetFilter(t.Context(), other, f.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	r := &store.Rule{Name: "Rule", FilterID: f.ID, IsActive: true, SetIsReviewed: &state}
	require.NoError(t, st.CreateRule(t.Context(), space, r))
	rule, err := st.GetRule(t.Context(), space, r.ID)
	require.NoError(t, err)
	require.Equal(t, *r, rule)
	r.Name = "Updated"
	require.NoError(t, st.UpdateRule(t.Context(), space, r))
	require.ErrorIs(t, st.ReorderRules(t.Context(), space, []uuid.UUID{r.ID, uuid.New()}), store.ErrNotFound)
	rule, err = st.GetRule(t.Context(), space, r.ID)
	require.NoError(t, err)
	require.Equal(t, 0, rule.Priority)
	count, err := st.PruneAdHocFilters(t.Context(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, st.DeleteRule(t.Context(), space, r.ID))
	require.NoError(t, st.DeleteFilter(t.Context(), space, f.ID))
}

func TestSQLiteSeriesWrites(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	charge, _ := sqliteLedgerRows(t, st, space)
	transaction, err := st.GetTransaction(t.Context(), space, charge)
	require.NoError(t, err)
	w := &store.SeriesWrite{AccountID: transaction.AccountID, Kind: "bill",
		Description: "Water", Amount: domain.MustFromString("12.34"), Currency: "USD",
		StartOn: domain.DateOf(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), IsActive: true,
		MatchCriteria: "exact", Recurrence: domain.Recurrence{Interval: 1},
	}
	require.NoError(t, st.CreateSeries(t.Context(), space, w))
	db, err := st.HouseholdDB(t.Context(), space)
	require.NoError(t, err)
	var amount int64
	var next string
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT amount,next_due_on FROM series WHERE id=?`, w.ID).Scan(&amount, &next))
	require.EqualValues(t, 1234, amount)
	require.Equal(t, "2026-01-01", next)
	w.Amount = domain.MustFromString("23.45")
	require.NoError(t, st.UpdateSeries(t.Context(), space, *w, false))
	require.NoError(t, st.DeleteSeries(t.Context(), space, w.ID))
}

func TestSQLiteTransactionWrites(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	a := &store.Account{Name: "Cash", Kind: domain.AccountKind("checking"), Type: "checking", Currency: "USD"}
	require.NoError(t, st.CreateAccount(t.Context(), space, a))
	tag := &store.Tag{Name: "Trip"}
	require.NoError(t, st.CreateTag(t.Context(), space, tag))
	day := domain.DateOf(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	first := &store.Transaction{AccountID: a.ID, Date: day, Amount: domain.MustFromString("-12.34"),
		Currency: "USD", Source: domain.Source("manual"), NeedsSettle: true, TagIDs: []uuid.UUID{tag.ID},
		Splits: []store.Split{{Amount: domain.MustFromString("-12.34"), TagIDs: []uuid.UUID{tag.ID}}}}
	require.NoError(t, st.CreateTransaction(t.Context(), space, first))
	got, err := st.GetTransaction(t.Context(), space, first.ID)
	require.NoError(t, err)
	require.Equal(t, first.Amount, got.Amount)
	require.Equal(t, first.TagIDs, got.TagIDs)
	require.Len(t, got.Splits, 1)
	require.Equal(t, first.Splits[0].Amount, got.Splits[0].Amount)
	first.Splits, first.TagIDs = nil, nil
	first.Payee = "Updated"
	require.NoError(t, st.UpdateTransaction(t.Context(), space, first))
	require.NoError(t, st.SetTransactionBalances(t.Context(), space, map[uuid.UUID]domain.Money{first.ID: domain.MustFromString("100.25")}))
	require.NoError(t, st.TagTransactions(t.Context(), space, []uuid.UUID{first.ID}, []uuid.UUID{tag.ID}, nil))
	require.ErrorIs(t, st.TagTransactions(t.Context(), space, []uuid.UUID{first.ID, uuid.New()}, nil, []uuid.UUID{tag.ID}), store.ErrNotFound)
	got, err = st.GetTransaction(t.Context(), space, first.ID)
	require.NoError(t, err)
	require.Empty(t, got.Splits)
	require.Equal(t, "100.25", got.Balance.String())
	require.Equal(t, []uuid.UUID{tag.ID}, got.TagIDs)
	require.NoError(t, st.ClearNeedsSettle(t.Context(), space, []uuid.UUID{first.ID}))
	pending, err := st.TransactionsNeedingSettle(t.Context(), space)
	require.NoError(t, err)
	require.Empty(t, pending)
	second := &store.Transaction{AccountID: a.ID, Date: day, Amount: domain.MustFromString("12.34"), Currency: "USD", Source: domain.Source("manual")}
	require.NoError(t, st.CreateTransaction(t.Context(), space, second))
	pair, paired, err := st.PairTransactions(t.Context(), space, first.ID, second.ID, true)
	require.NoError(t, err)
	require.True(t, paired)
	legs, err := st.ListTransferLegs(t.Context(), space, store.TransferLegQuery{PairIDs: []uuid.UUID{pair}})
	require.NoError(t, err)
	require.Len(t, legs, 2)
	require.True(t, legs[0].PairedByHand)
	require.NoError(t, st.DeleteTransaction(t.Context(), space, first.ID))
	got, err = st.GetTransaction(t.Context(), space, second.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, got.TransferPairID)
	require.Equal(t, uuid.Nil, got.CategoryID)
}

func TestSQLiteHoldingsAndPurges(t *testing.T) {
	st := storetest.New(t)
	space := storetest.NewHousehold(t, st)
	a := &store.Account{Name: "Portfolio", Kind: domain.AccountKind("checking"), Type: "checking", Currency: "USD"}
	require.NoError(t, st.CreateAccount(t.Context(), space, a))
	sec := &store.Security{Symbol: "TEST", Name: "Test", Kind: "stock", Currency: "JPY"}
	require.NoError(t, st.CreateSecurity(t.Context(), space, sec))
	require.NoError(t, st.SetSecurityPrice(t.Context(), space, sec.ID, domain.MustFromString("12.34").Decimal(), time.Now()))
	holding := &store.Holding{AccountID: a.ID, SecurityID: sec.ID, Shares: domain.MustFromString("1.23456789").Decimal(),
		CostBasis: domain.MustFromString("123"), HasCostBasis: true, IsComplete: true}
	created, err := st.CreateHolding(t.Context(), space, holding)
	require.NoError(t, err)
	require.True(t, created)
	created, err = st.CreateHolding(t.Context(), space, holding)
	require.NoError(t, err)
	require.False(t, created)
	list, err := st.ListHoldings(t.Context(), space, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, holding.CostBasis, list[0].CostBasis)
	require.True(t, holding.Shares.Equal(list[0].Shares))
	symbols, err := st.HeldSymbols(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, []string{"TEST"}, symbols)
	tag := &store.Tag{Name: "Unused"}
	require.NoError(t, st.CreateTag(t.Context(), space, tag))
	f := &store.Filter{Name: "Search", Scope: "ad_hoc", Items: []store.FilterItem{{Field: "tag", Operator: "in", ValueIDs: []uuid.UUID{tag.ID}}}}
	require.NoError(t, st.CreateFilter(t.Context(), space, f))
	result, err := st.PruneTags(t.Context(), space, []uuid.UUID{tag.ID})
	require.NoError(t, err)
	require.Equal(t, 1, result.Deleted)
	require.Equal(t, 1, result.FiltersRetired)
	require.NoError(t, st.DeleteHolding(t.Context(), space, holding.ID))
}
