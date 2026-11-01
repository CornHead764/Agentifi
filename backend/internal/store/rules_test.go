package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func newRuleFilter(t *testing.T, space SpaceID) uuid.UUID {
	t.Helper()
	filter := &Filter{Scope: "rule"}
	require.NoError(t, db(t).CreateFilter(t.Context(), space, filter))
	return filter.ID
}

// An unset category and an unset action flag are NULL on disk, never the zero
// uuid or false: NULL is "leave the row alone".
func TestRuleUnsetActionsAreNull(t *testing.T) {
	space := newSpace(t)
	st, ctx := db(t), t.Context()
	trip := &Tag{Name: "Trip"}
	require.NoError(t, st.CreateTag(ctx, space, trip))
	fuel := &Category{Name: "Fuel", Kind: domain.CategoryExpense, IsEditable: true}
	require.NoError(t, st.CreateCategory(ctx, space, fuel))

	no := false
	rule := &Rule{Name: "Fuel", FilterID: newRuleFilter(t, space), IsActive: true, SetIsReviewed: &no, AddTagIDs: []uuid.UUID{trip.ID}}
	require.NoError(t, st.CreateRule(ctx, space, rule))
	stored := func() (category, payee, notes, reports, plan, reviewed, active any) {
		t.Helper()
		require.NoError(t, householdDB(t, space).Read().QueryRowContext(ctx, `SELECT set_category_id, set_payee, set_notes,
			set_excluded_from_reports, set_excluded_from_spending_plan, set_is_reviewed, is_active FROM rules WHERE id = ?`, rule.ID).
			Scan(&category, &payee, &notes, &reports, &plan, &reviewed, &active))
		return
	}
	category, payee, notes, reports, plan, reviewed, active := stored()
	require.Nil(t, category)
	require.Nil(t, payee)
	require.Nil(t, notes)
	require.Nil(t, reports)
	require.Nil(t, plan)
	require.Equal(t, int64(0), reviewed)
	require.Equal(t, int64(1), active)
	read, err := st.GetRule(ctx, space, rule.ID)
	require.NoError(t, err)
	require.Equal(t, *rule, read)

	yes := true
	rule.SetCategoryID, rule.SetPayee, rule.SetExcludedFromReports, rule.SetIsReviewed = fuel.ID, "Fuel Stop", &yes, nil
	require.NoError(t, st.UpdateRule(ctx, space, rule))
	category, payee, _, reports, _, reviewed, _ = stored()
	require.Equal(t, fuel.ID.String(), category)
	require.Equal(t, "Fuel Stop", payee)
	require.Equal(t, int64(1), reports)
	require.Nil(t, reviewed)
	read, err = st.GetRule(ctx, space, rule.ID)
	require.NoError(t, err)
	require.Equal(t, *rule, read)

	rule.SetCategoryID, rule.SetPayee, rule.SetExcludedFromReports = uuid.Nil, "", nil
	require.NoError(t, st.UpdateRule(ctx, space, rule))
	category, payee, _, reports, _, _, _ = stored()
	require.Nil(t, category)
	require.Nil(t, payee)
	require.Nil(t, reports)
	read, err = st.GetRule(ctx, space, rule.ID)
	require.NoError(t, err)
	require.Equal(t, *rule, read)
}

func TestRuleTagOrder(t *testing.T) {
	space := newSpace(t)
	st, ctx := db(t), t.Context()
	var tags []uuid.UUID
	for _, name := range []string{"Charlie", "Alpha", "Bravo"} {
		tag := &Tag{Name: name}
		require.NoError(t, st.CreateTag(ctx, space, tag))
		tags = append(tags, tag.ID)
	}
	rule := &Rule{Name: "Tagged", FilterID: newRuleFilter(t, space), IsActive: true,
		AddTagIDs: []uuid.UUID{tags[0], tags[1], tags[0], tags[2]}}
	require.NoError(t, st.CreateRule(ctx, space, rule))
	read, err := st.GetRule(ctx, space, rule.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{tags[0], tags[1], tags[2]}, read.AddTagIDs, "a repeated id keeps its first place")

	rule.AddTagIDs = []uuid.UUID{tags[2], tags[0]}
	require.NoError(t, st.UpdateRule(ctx, space, rule))
	read, err = st.GetRule(ctx, space, rule.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{tags[2], tags[0]}, read.AddTagIDs)

	rule.AddTagIDs = nil
	require.NoError(t, st.UpdateRule(ctx, space, rule))
	require.Equal(t, []uuid.UUID{}, rule.AddTagIDs)
	listed, err := st.ListRules(ctx, space, RuleQuery{})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NotNil(t, listed[0].AddTagIDs)
	require.Empty(t, listed[0].AddTagIDs)
}

func TestRuleRunOrder(t *testing.T) {
	space := newSpace(t)
	st, ctx := db(t), t.Context()
	none, err := st.ListRules(ctx, space, RuleQuery{})
	require.NoError(t, err)
	require.NotNil(t, none)
	require.Empty(t, none)
	next, err := st.NextRulePriority(ctx, space)
	require.NoError(t, err)
	require.Equal(t, 0, next)

	filter := newRuleFilter(t, space)
	tag := &Tag{Name: "Trip"}
	require.NoError(t, st.CreateTag(ctx, space, tag))
	low, high := uuid.MustParse("00000000-0000-4000-8000-000000000001"), uuid.MustParse("00000000-0000-4000-8000-000000000002")
	rules := map[string]*Rule{
		"tie-high": {ID: high, Name: "tie-high", FilterID: filter, Priority: 3, IsActive: true, AddTagIDs: []uuid.UUID{tag.ID}},
		"tie-low":  {ID: low, Name: "tie-low", FilterID: filter, Priority: 3, IsActive: true},
		"first":    {Name: "first", FilterID: filter, Priority: 1, IsActive: true},
		"off":      {Name: "off", FilterID: filter, Priority: 2},
		"gone":     {Name: "gone", FilterID: filter, Priority: 9, IsActive: true},
	}
	for _, rule := range rules {
		require.NoError(t, st.CreateRule(ctx, space, rule))
	}
	require.NoError(t, st.DeleteRule(ctx, space, rules["gone"].ID))
	names := func(q RuleQuery) []string {
		t.Helper()
		listed, err := st.ListRules(ctx, space, q)
		require.NoError(t, err)
		var out []string
		for _, rule := range listed {
			require.Equal(t, space, rule.SpaceID)
			out = append(out, rule.Name)
		}
		return out
	}
	require.Equal(t, []string{"first", "off", "tie-low", "tie-high"}, names(RuleQuery{}))
	require.Equal(t, []string{"first", "tie-low", "tie-high"}, names(RuleQuery{ActiveOnly: true}))
	require.Equal(t, []string{"first", "off", "tie-low", "tie-high", "gone"}, names(RuleQuery{IncludeDeleted: true}))
	listed, err := st.ListRules(ctx, space, RuleQuery{ActiveOnly: true})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{tag.ID}, listed[2].AddTagIDs)
	gone, err := st.GetRule(ctx, space, rules["gone"].ID)
	require.NoError(t, err)
	require.True(t, gone.IsDeleted)
	next, err = st.NextRulePriority(ctx, space)
	require.NoError(t, err)
	require.Equal(t, 4, next, "a deleted rule's priority is not counted")

	require.NoError(t, st.ReorderRules(ctx, space, []uuid.UUID{high, low, rules["off"].ID, rules["first"].ID}))
	require.Equal(t, []string{"tie-high", "tie-low", "off", "first"}, names(RuleQuery{}))
	require.ErrorIs(t, st.ReorderRules(ctx, space, []uuid.UUID{low, rules["gone"].ID}), ErrNotFound)
	require.Equal(t, []string{"tie-high", "tie-low", "off", "first"}, names(RuleQuery{}), "a refused reorder changes nothing")

	missing := uuid.New()
	_, err = st.GetRule(ctx, space, missing)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, st.UpdateRule(ctx, space, &Rule{ID: missing, Name: "Nobody", FilterID: filter}), ErrNotFound)
	require.ErrorIs(t, st.DeleteRule(ctx, space, missing), ErrNotFound)
}
