package store

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newSQLiteTest(t *testing.T) *Store {
	t.Helper()
	st, err := OpenSQLite(t.Context(), t.TempDir(), time.Now)
	require.NoError(t, err)
	t.Cleanup(st.Close)
	return st
}

func TestSQLiteInArgs(t *testing.T) {
	query, args := InArgs([]string{"a", "b"})
	require.Equal(t, "(?,?)", query)
	require.Equal(t, []any{"a", "b"}, args)
	query, args = InArgs([]string{})
	require.Equal(t, "(NULL)", query)
	require.Empty(t, args)
	db, err := newSQLiteTest(t).server()
	require.NoError(t, err)
	var n int
	require.NoError(t, db.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM (SELECT 'a' AS id) WHERE id IN "+query, args...).Scan(&n))
	require.Zero(t, n)
}

func TestSQLiteMoney(t *testing.T) {
	for _, tt := range []struct {
		code, text string
		minor      int64
	}{{"USD", "12.34", 1234}, {"JPY", "1500", 1500}, {"KWD", "-1.234", -1234}} {
		n, err := toMinor(domain.MustFromString(tt.text), tt.code)
		require.NoError(t, err)
		require.Equal(t, tt.minor, n)
		amount, err := fromMinor(n, tt.code)
		require.NoError(t, err)
		require.Equal(t, tt.text, amount.String(), "an amount read back prints at its currency's scale")
	}
	_, err := toMinor(domain.MustFromString("1.001"), "USD")
	require.ErrorContains(t, err, "round")
	_, err = toMinor(domain.MustFromString("9223372036854775808"), "JPY")
	require.ErrorIs(t, err, domain.ErrAmountOutOfRange, "refused before int64 minor units could overflow")
	_, err = fromMinor(1, "not-a-currency")
	require.Error(t, err)
}

func TestSQLiteMoneyRefusesAnotherCurrencysScale(t *testing.T) {
	_, err := toMinor(domain.FromMinor(1500, 3), "USD")
	var mixed *domain.MixedScaleError
	require.ErrorAs(t, err, &mixed, "1.500 at KWD's scale is not written into a USD column")
	n, err := toMinor(domain.FromMinor(1500, 2), "EUR")
	require.NoError(t, err, "two currencies at one scale are the caller's to keep apart")
	require.Equal(t, int64(1500), n)
}

func TestSQLiteTimeAndNull(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.FixedZone("offset", 3600))
	require.True(t, fromUnixMillis(unixMillis(at)).Equal(at))
	require.Nil(t, nullValue(sql.Null[string]{}))
	require.Equal(t, "yes", *nullValue(sql.Null[string]{V: "yes", Valid: true}))
	require.Nil(t, nullableTime(sql.Null[int64]{}))
	require.True(t, nullableTime(sql.Null[int64]{V: unixMillis(at), Valid: true}).Equal(at))
	require.Nil(t, nullableMillis(nil))
	require.Equal(t, unixMillis(at), nullableMillis(&at))
	db, err := newSQLiteTest(t).server()
	require.NoError(t, err)
	var value sql.Null[int64]
	require.NoError(t, db.Read().QueryRowContext(t.Context(), "SELECT NULL").Scan(&value))
	require.Nil(t, nullableTime(value))
}

func TestSQLiteJSONArrays(t *testing.T) {
	var list JSONArray[string]
	value, err := list.Value()
	require.NoError(t, err)
	require.Equal(t, "[]", value)
	require.NoError(t, list.Scan(`["one","two"]`))
	require.Equal(t, JSONArray[string]{"one", "two"}, list)
	require.NoError(t, list.Scan([]byte(`[]`)))
	require.Empty(t, list)
	require.Error(t, list.Scan(`null`))
	require.Error(t, list.Scan(`{}`))
	require.NoError(t, list.Scan(nil))
	require.Nil(t, list)
	db, err := newSQLiteTest(t).server()
	require.NoError(t, err)
	require.NoError(t, db.Read().QueryRowContext(t.Context(), "SELECT ?", JSONArray[string]{"a"}).Scan(&list))
	require.Equal(t, JSONArray[string]{"a"}, list)
}

func TestSQLiteErrorMapping(t *testing.T) {
	st := newSQLiteTest(t)
	db, err := st.server()
	require.NoError(t, err)
	var value string
	err = db.Read().QueryRowContext(t.Context(), "SELECT jti FROM revoked_tokens WHERE jti = ?", "missing").Scan(&value)
	require.ErrorIs(t, wrap("read", err), ErrNotFound)
	_, err = db.Write().ExecContext(t.Context(), "INSERT INTO revoked_tokens VALUES ('one',1,1),('one',1,1)")
	var constraint *ConstraintError
	require.ErrorAs(t, wrap("write", err), &constraint)
	require.Equal(t, "23505", constraint.Code)
	_, err = db.Write().ExecContext(t.Context(), `INSERT INTO memberships (id,user_id,role,household_id,created_at,updated_at) VALUES ('m','u','owner','h',1,1)`)
	constraint = nil
	require.ErrorAs(t, wrap("write", err), &constraint)
	require.Equal(t, "23503", constraint.Code)
	require.ErrorIs(t, sqliteExecOne(t.Context(), db.Write(), "delete", "DELETE FROM revoked_tokens WHERE jti = ?", "missing"), ErrNotFound)
}

func TestSQLiteRoutingAndTransactions(t *testing.T) {
	st := newSQLiteTest(t)
	id := newSQLiteSpace(t, st)
	const workers = 8
	handles := make(chan *sqlitedb.DB, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() { db, err := st.household(t.Context(), id.UUID()); handles <- db; errs <- err })
	}
	wg.Wait()
	close(handles)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var first *sqlitedb.DB
	for db := range handles {
		if first == nil {
			first = db
		}
		require.Same(t, first, db)
	}
	scoped, err := st.ForHousehold(t.Context(), id)
	require.NoError(t, err)
	require.NoError(t, scoped.InTx(t.Context(), func(tx *Store) error {
		db, err := tx.household(t.Context(), id.UUID())
		require.NoError(t, err)
		_, err = tx.server()
		require.Error(t, err)
		_, err = tx.household(t.Context(), uuid.New())
		require.Error(t, err)
		return tx.InTx(t.Context(), func(nested *Store) error {
			inner, err := nested.household(t.Context(), id.UUID())
			require.NoError(t, err)
			require.NotSame(t, db, inner)
			return nil
		})
	}))
	stop := errors.New("rollback")
	require.ErrorIs(t, st.InTx(t.Context(), func(tx *Store) error {
		_, err := tx.household(t.Context(), id.UUID())
		require.Error(t, err)
		require.NoError(t, tx.RevokeToken(t.Context(), "rollback", time.Now()))
		require.ErrorIs(t, tx.InTx(t.Context(), func(nested *Store) error {
			require.NoError(t, nested.RevokeToken(t.Context(), "savepoint", time.Now()))
			return stop
		}), stop)
		found, err := tx.TokenRevoked(t.Context(), "savepoint")
		require.NoError(t, err)
		require.False(t, found)
		return stop
	}), stop)
	found, err := st.TokenRevoked(t.Context(), "rollback")
	require.NoError(t, err)
	require.False(t, found)
	st.Close()
	st.Close()
	_, err = st.household(t.Context(), id.UUID())
	require.Error(t, err)
	_, err = st.server()
	require.Error(t, err)
}

func TestTransactionHydrationBatchesPastSQLiteVariableLimit(t *testing.T) {
	st := newSQLiteTest(t)
	id := newSQLiteSpace(t, st)
	h, err := st.ForHousehold(t.Context(), id)
	require.NoError(t, err)
	// No allocations exist, but binding every ID in a single query still
	// exceeds modernc SQLite's compiled 32766-variable limit.
	txns := make([]Transaction, 32767)
	ids := make([]uuid.UUID, len(txns))
	for i := range txns {
		ids[i] = uuid.New()
		txns[i].ID = ids[i]
	}
	require.NoError(t, h.attachAllocations(t.Context(), id, txns))
	counts, err := h.CountDocumentsOnTransactions(t.Context(), id, ids)
	require.NoError(t, err)
	require.Empty(t, counts)
	matched, err := h.MerchantMatchedTransactions(t.Context(), id, ids)
	require.NoError(t, err)
	require.Empty(t, matched)
}
