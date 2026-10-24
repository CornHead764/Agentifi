package importer

import (
	"context"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/shopspring/decimal"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// written maps the fixture, writes it, and hands back the space it landed in.
// Each call gets its own space: the tests share a schema and must not share a
// tenant.
func written(t *testing.T) (*Mapped, uuid.UUID) {
	t.Helper()
	out := mapped(t, complete(t))
	require.True(t, out.Report.OK(), out.Report.Render())
	// The duplicate-import guard refuses a second space with the same name for
	// the same owner, and these tests share one database; every run names its
	// space uniquely so only the first import of a fixture ever lands.
	out.Space.Name = out.Space.Name + " " + uuid.NewString()[:8]
	require.NoError(t, Write(t.Context(), db(t), out))
	return out, uuid.UUID(out.SpaceID)
}

func scanOne[T any](t *testing.T, ctx context.Context, query string, space uuid.UUID, args ...any) T {
	t.Helper()
	return scanValue[T](t, queryRow(t, ctx, query, space, args...))
}

func TestARunWithErrorsIsNeverWritten(t *testing.T) {
	out := mapped(t, edited(t, "accountsStore", "a1", map[string]any{"subType": "CRYPTO_WALLET"}))
	require.False(t, out.Report.OK())
	err := Write(t.Context(), db(t), out)
	require.ErrorIs(t, err, ErrRefused)
}

func TestTheWholeImportCommits(t *testing.T) {
	out, space := written(t)
	ctx := t.Context()

	for table, expected := range map[string]int{
		"accounts":             len(out.Accounts),
		"categories":           len(out.Categories) + 1, // Credit Card Payment
		"tags":                 len(out.Tags),
		"filters":              len(out.Filters),
		"transactions":         len(out.Transactions),
		"series":               len(out.Series),
		"rules":                len(out.Rules),
		"goals":                len(out.Goals),
		"spending_plan_months": len(out.Months),
		"envelopes":            len(out.Envelopes),
		"watchlists":           len(out.Watchlists),
		"securities":           len(out.Securities),
		"holdings":             len(out.Holdings),
		"balance_snapshots":    len(out.BalanceSnapshots),
		"alert_rules":          len(out.AlertRules),
		// An attachment is a document with a transaction link; the export
		// carries the record and not the file.
		"documents":      len(out.Attachments),
		"document_links": len(out.Attachments),
		"institutions":   len(out.Institutions),
	} {
		count := scanOne[int](t, ctx, `SELECT count(*) FROM `+table+``, space)
		require.Equalf(t, expected, count, "%s", table)
	}
}

func TestTheAmountsComeBackAsDecimalsAtTwoPlaces(t *testing.T) {
	_, space := written(t)
	ctx := t.Context()

	require.Equal(t, int64(-2550), scanOne[int64](t, ctx, "SELECT amount FROM transactions WHERE statement_name=?", space, "SQ *COFFEE 1234"))
	require.Equal(t, int64(40001), scanOne[int64](t, ctx, "SELECT calculated_rollover_amount FROM spending_plan_months", space))
	require.Equal(t, "190.4761904762", scanOne[string](t, ctx, "SELECT average_cost FROM holdings", space))
}

func TestTheAuditTrailsSurviveAsArraysOfIds(t *testing.T) {
	out, space := written(t)
	ctx := t.Context()

	ids := scanOne[[]uuid.UUID](t, ctx,
		`SELECT spent_txn_ids FROM spending_plan_months`, space)
	require.Equal(t, out.Months[0].Spent.TxnIDs, ids)

	// A bucket with no trail is an empty array, not NULL: the column is NOT
	// NULL and a nil slice would have been encoded as NULL.
	goals := scanOne[[]uuid.UUID](t, ctx,
		`SELECT goals_txn_ids FROM spending_plan_months`, space)
	require.Empty(t, goals)

	envelope := scanOne[[]uuid.UUID](t, ctx,
		`SELECT txn_ids FROM envelopes`, space)
	require.Len(t, envelope, 2)
}

func TestTheRruleArrayAndTheLearnedWordingsRoundTrip(t *testing.T) {
	_, space := written(t)
	ctx := t.Context()

	days := scanOne[[]int32](t, ctx, `SELECT by_month_day FROM series`, space)
	require.Equal(t, []int32{3}, days)

	learned := scanOne[[]string](t, ctx,
		`SELECT learned_descriptions FROM series`, space)
	require.Equal(t, []string{"STREAMSVC.COM 800-555-0100"}, learned)

	byDay := scanOne[[]string](t, ctx, `SELECT by_day FROM series`, space)
	require.Empty(t, byDay)
}

func TestTheRestrictForeignKeysFromEnvelopesAndRulesResolve(t *testing.T) {
	_, space := written(t)
	ctx := t.Context()

	// ON DELETE RESTRICT against filters, so these only exist if step 6 ran
	// before steps 9 and 11.
	require.Equal(t, 1, scanOne[int](t, ctx,
		`SELECT count(*) FROM envelopes e JOIN filters f ON f.id = e.filter_id`, space))
	require.Equal(t, 1, scanOne[int](t, ctx,
		`SELECT count(*) FROM rules r JOIN filters f ON f.id = r.filter_id`, space))
	require.Equal(t, 1, scanOne[int](t, ctx,
		`SELECT count(*) FROM watchlists w JOIN filters f ON f.id = w.filter_id`, space))
}

func TestACategoryTreeSurvivesItsOwnSelfReferentialForeignKey(t *testing.T) {
	out, space := written(t)
	ctx := t.Context()
	child := categoryNamed(t, out, "Groceries")
	parent := scanOne[*uuid.UUID](t, ctx,
		`SELECT parent_id FROM categories WHERE id = ?1`, space, child.ID)
	require.NotNil(t, parent)
	require.Equal(t, categoryNamed(t, out, "Food & Dining").ID, *parent)
}

func TestBothLegsOfTheTransferCarryTheSameToken(t *testing.T) {
	_, space := written(t)
	ctx := t.Context()
	tokens := scanOne[int](t, ctx, `
		SELECT count(DISTINCT transfer_pair_id) FROM transactions
		WHERE transfer_pair_id IS NOT NULL`, space)
	require.Equal(t, 1, tokens)
	legs := scanOne[int](t, ctx, `
		SELECT count(*) FROM transactions
		WHERE transfer_pair_id IS NOT NULL`, space)
	require.Equal(t, 2, legs)
}

func TestEveryAccountLandsUnlinkedWithNoConnectionRow(t *testing.T) {
	_, space := written(t)
	ctx := t.Context()
	require.Equal(t, 0, scanOne[int](t, ctx,
		`SELECT count(*) FROM connections`, space))
	require.Equal(t, 0, scanOne[int](t, ctx, `
		SELECT count(*) FROM accounts
		WHERE (connection_id IS NOT NULL
			OR simplefin_account_id IS NOT NULL OR sync_floor_on IS NOT NULL)`, space))
	// Every row is stamped, which is how the relink step finds the history it
	// must not let SimpleFIN duplicate.
	require.Equal(t, 0, scanOne[int](t, ctx, `
		SELECT count(*) FROM transactions
		WHERE source <> 'simplifi_import'`, space))
}

func TestTheBalanceHistoryKeepsItsOneRowPerDay(t *testing.T) {
	_, space := written(t)
	ctx := t.Context()
	require.Equal(t, 2, scanOne[int](t, ctx,
		`SELECT count(*) FROM balance_snapshots`, space))
	require.Equal(t, 2, scanOne[int](t, ctx,
		`SELECT count(DISTINCT as_of) FROM balance_snapshots`, space))
	// Observations, not derivations: a history-start rebuild never deletes
	// them, and the earliest counts as evidence the account existed.
	require.Equal(t, 2, scanOne[int](t, ctx,
		`SELECT count(*) FROM balance_snapshots WHERE is_imported`, space))
}

func TestTheAlertRuleIsScopedToTheOwnerTheImportWrote(t *testing.T) {
	out, space := written(t)
	ctx := t.Context()
	// The owner is looked up by email rather than by the id mapping allocated:
	// a second import with the same --owner-email joins the account that
	// already exists, and takes its id.
	owner := scanOne[uuid.UUID](t, ctx, "SELECT user_id FROM alert_rules LIMIT 1", space)
	user, err := db(t).GetUser(ctx, owner)
	require.NoError(t, err)
	require.Equal(t, out.Users[0].Email, user.Email)
	require.Equal(t, owner, scanOne[uuid.UUID](t, ctx, "SELECT user_id FROM memberships WHERE household_id=?1", space))
}

func TestTheSplitsAndTheirTagsLandTogether(t *testing.T) {
	_, space := written(t)
	ctx := t.Context()
	require.Equal(t, 2, scanOne[int](t, ctx,
		`SELECT count(*) FROM transaction_splits`, space))
	require.Equal(t, 1, scanOne[int](t, ctx, `
		SELECT count(*) FROM split_tags st
		JOIN transaction_splits s ON s.id = st.split_id`, space))
	require.Equal(t, 1, scanOne[int](t, ctx, `
		SELECT count(*) FROM transaction_tags tt
		JOIN transactions x ON x.id = tt.transaction_id`, space))
}

func TestASecondImportOfTheSameDatasetIsRefused(t *testing.T) {
	out := mapped(t, complete(t))
	require.True(t, out.Report.OK(), out.Report.Render())
	require.NoError(t, Write(t.Context(), db(t), out))

	again := mapped(t, complete(t))
	require.True(t, again.Report.OK(), again.Report.Render())
	err := Write(t.Context(), db(t), again)
	require.ErrorContains(t, err, "already exists")
	require.ErrorContains(t, err, out.Space.Name)
}

func TestImportsAreIsolatedByHousehold(t *testing.T) {
	first := mapped(t, complete(t))
	require.NoError(t, Write(t.Context(), db(t), first))
	second := mapped(t, complete(t))
	second.Space.Name = "Other household"
	require.NoError(t, Write(t.Context(), db(t), second))
	require.Equal(t, len(first.Transactions), scanOne[int](t, t.Context(), "SELECT count(*) FROM transactions", uuid.UUID(first.SpaceID)))
	require.Equal(t, len(second.Transactions), scanOne[int](t, t.Context(), "SELECT count(*) FROM transactions", uuid.UUID(second.SpaceID)))
	_, err := householdSQL(t, uuid.UUID(second.SpaceID)).ExecContext(t.Context(), "DELETE FROM transactions")
	require.NoError(t, err)
	require.Equal(t, len(first.Transactions), scanOne[int](t, t.Context(), "SELECT count(*) FROM transactions", uuid.UUID(first.SpaceID)))
}

func TestImportMinorUnitsHonorCurrencyScale(t *testing.T) {
	for _, test := range []struct {
		currency string
		decimal  string
		amount   int64
	}{{"JPY", "12", 12}, {"BHD", "12.345", 12345}, {"USD", "-12.34", -1234}} {
		t.Run(test.currency, func(t *testing.T) {
			value, err := importValue(moneyArg(domain.FromDecimal(decimal.RequireFromString(test.decimal))), test.currency)
			require.NoError(t, err)
			require.Equal(t, test.amount, value)
		})
	}
	_, err := importValue(moneyArg(domain.FromDecimal(decimal.RequireFromString("1.001"))), "USD")
	require.Error(t, err)
}
