package importer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// writtenFrom writes one export into a space of its own and hands back the
// mapping and the space.
func writtenFrom(t *testing.T, export *Export) (*Mapped, uuid.UUID) {
	t.Helper()
	out := mapped(t, export)
	require.True(t, out.Report.OK(), out.Report.Render())
	out.Space.Name = out.Space.Name + " " + uuid.NewString()[:8]
	require.NoError(t, Write(t.Context(), db(t), out))
	return out, uuid.UUID(out.SpaceID)
}

// remapped maps the same export again, as a later rules-only run does: every
// id is new, and the space's own ids have to be found again by name.
func remapped(t *testing.T, export *Export, first *Mapped) *Mapped {
	t.Helper()
	again := mapped(t, export)
	require.True(t, again.Report.OK(), again.Report.Render())
	again.Space.Name = first.Space.Name
	return again
}

func renameShapedRule(t *testing.T) *Export {
	t.Helper()
	return edited(t, "renameRuleStore", "r1", map[string]any{
		"filterId": nil, "renamedPayee": nil, "coa": nil, "tags": nil, "isExcludedFromF2S": nil,
		"renamePayeeFrom": "STATE TOLL ROAD", "renamePayeeTo": "Toll Road",
		"matchCriteria": "If Payee Contains",
	})
}

func TestTheFullImportRecordsWhereEachRuleCameFrom(t *testing.T) {
	_, space := writtenFrom(t, withRuleEvidence(t))
	require.ElementsMatch(t,
		[]string{"simplifi:renameRuleStore:r1", "simplifi:transactionRuleId:tr8", "simplifi:transactionRuleId:tr9"},
		scanAll[string](t, `SELECT source_ref FROM rules WHERE space_id = $1`, space))
}

func TestRulesAlreadyImportedAreNotWrittenAgain(t *testing.T) {
	export := withRuleEvidence(t)
	first, space := writtenFrom(t, export)
	ctx := t.Context()

	for run := 0; run < 2; run++ {
		outcome, err := WriteRules(ctx, db(t), remapped(t, withRuleEvidence(t), first), space, false)
		require.NoError(t, err)
		require.Len(t, outcome.Present, 3)
		require.Empty(t, outcome.Inserted)
		require.Empty(t, outcome.Adopted)
	}
	require.Equal(t, 3, scanOne[int](t, ctx, `SELECT count(*) FROM rules WHERE space_id = $1`, space))
}

func TestARuleWithNoSourceRefIsAdoptedNotDuplicated(t *testing.T) {
	first, space := writtenFrom(t, renameShapedRule(t))
	ctx := t.Context()
	_, err := db(t).Pool().Exec(ctx, `UPDATE rules SET source_ref = '' WHERE space_id = $1`, space)
	require.NoError(t, err)

	outcome, err := WriteRules(ctx, db(t), remapped(t, renameShapedRule(t), first), space, false)
	require.NoError(t, err)
	require.Len(t, outcome.Adopted, 1)
	require.Empty(t, outcome.Inserted)
	require.Equal(t, "simplifi:renameRuleStore:r1", scanOne[string](t, ctx,
		`SELECT source_ref FROM rules WHERE space_id = $1`, space))

	outcome, err = WriteRules(ctx, db(t), remapped(t, renameShapedRule(t), first), space, false)
	require.NoError(t, err)
	require.Len(t, outcome.Present, 1)
	require.Equal(t, 1, scanOne[int](t, ctx, `SELECT count(*) FROM rules WHERE space_id = $1`, space))
}

func TestAMissingRuleIsWrittenAgainstTheSpacesOwnCategoryAndTag(t *testing.T) {
	first, space := writtenFrom(t, complete(t))
	ctx := t.Context()
	_, err := db(t).Pool().Exec(ctx, `UPDATE rules SET source_ref = 'made-here:' || id WHERE space_id = $1`, space)
	require.NoError(t, err)

	again := remapped(t, complete(t), first)
	outcome, err := WriteRules(ctx, db(t), again, space, false)
	require.NoError(t, err)
	require.Len(t, outcome.Inserted, 1)

	groceries := categoryNamed(t, first, "Groceries").ID
	require.NotEqual(t, groceries, categoryNamed(t, again, "Groceries").ID)
	require.Equal(t, groceries, scanOne[uuid.UUID](t, ctx,
		`SELECT set_category_id FROM rules WHERE space_id = $1 AND source_ref = 'simplifi:renameRuleStore:r1'`, space))
	require.Equal(t, []uuid.UUID{first.Tags[0].ID}, scanOne[[]uuid.UUID](t, ctx,
		`SELECT add_tag_ids FROM rules WHERE space_id = $1 AND source_ref = 'simplifi:renameRuleStore:r1'`, space))
	require.Equal(t, 2, scanOne[int](t, ctx, `
		SELECT count(*) FROM filter_items i JOIN rules r ON r.filter_id = i.filter_id
		 WHERE r.space_id = $1 AND r.source_ref = 'simplifi:renameRuleStore:r1'`, space))
}

func TestARuleWhoseCategoryTheSpaceNoLongerHasIsRefusedAndNothingIsWritten(t *testing.T) {
	first, space := writtenFrom(t, withRuleEvidence(t))
	ctx := t.Context()
	_, err := db(t).Pool().Exec(ctx, `UPDATE rules SET source_ref = 'made-here:' || id WHERE space_id = $1`, space)
	require.NoError(t, err)
	_, err = db(t).Pool().Exec(ctx,
		`UPDATE categories SET name = 'Renamed' WHERE space_id = $1 AND name = 'Groceries'`, space)
	require.NoError(t, err)

	outcome, err := WriteRules(ctx, db(t), remapped(t, withRuleEvidence(t), first), space, false)
	require.ErrorIs(t, err, ErrRefused)
	require.NotEmpty(t, outcome.Refused)
	require.Contains(t, outcome.Refused[0], "categories named")
	require.Equal(t, 3, scanOne[int](t, ctx, `SELECT count(*) FROM rules WHERE space_id = $1`, space))
}

func TestARulesOnlyDryRunWritesNothing(t *testing.T) {
	first, space := writtenFrom(t, complete(t))
	ctx := t.Context()
	_, err := db(t).Pool().Exec(ctx, `UPDATE rules SET source_ref = 'made-here:' || id WHERE space_id = $1`, space)
	require.NoError(t, err)

	outcome, err := WriteRules(ctx, db(t), remapped(t, complete(t), first), space, true)
	require.NoError(t, err)
	require.Len(t, outcome.Inserted, 1)
	require.Equal(t, 1, scanOne[int](t, ctx, `SELECT count(*) FROM rules WHERE space_id = $1`, space))
}

func TestTheRulesOnlyImportFindsTheSpaceTheFullImportWrote(t *testing.T) {
	first, space := writtenFrom(t, complete(t))
	found, err := FindImportedSpace(t.Context(), db(t), remapped(t, complete(t), first))
	require.NoError(t, err)
	require.Equal(t, space, found)

	nowhere := remapped(t, complete(t), first)
	nowhere.Space.Name = "No such space " + uuid.NewString()
	_, err = FindImportedSpace(t.Context(), db(t), nowhere)
	require.ErrorContains(t, err, "no space named")
}

func scanAll[T any](t *testing.T, sql string, args ...any) []T {
	t.Helper()
	rows, err := db(t).Pool().Query(t.Context(), sql, args...)
	require.NoError(t, err)
	defer rows.Close()
	var out []T
	for rows.Next() {
		var value T
		require.NoError(t, rows.Scan(&value))
		out = append(out, value)
	}
	require.NoError(t, rows.Err())
	return out
}
