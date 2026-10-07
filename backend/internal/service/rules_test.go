package service

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Rule conditions, the diff they produce, and the preview that withholds it.

var (
	ruleAccountID  = uuid.UUID{1}
	otherAccountID = uuid.UUID{2}
	ruleTxnID      = uuid.UUID{100}
)

func ruleRow(mods ...func(*domain.Transaction)) RuleCandidate {
	txn := domain.Transaction{
		ID:            domain.ID(ruleTxnID.String()),
		AccountID:     domain.ID(ruleAccountID.String()),
		Date:          on(2026, time.March, 2),
		Amount:        domain.MustFromString("-15.00"),
		StatementName: "STREAMSVC.COM 800-555-0100",
		Payee:         "Example Streaming",
		Currency:      "USD",
	}
	for _, mod := range mods {
		mod(&txn)
	}
	return RuleCandidate{
		ID: ruleTxnID,
		Posting: domain.Posting{
			Txn:     txn,
			Account: domain.Account{ID: txn.AccountID, Currency: "USD"},
		},
	}
}

func containsItem(field domain.FilterField, group int, keywords ...string) domain.FilterItem {
	return domain.FilterItem{
		Field:      field,
		Operator:   domain.OpContains,
		Values:     keywords,
		GroupIndex: group,
	}
}

func statementContains(keywords ...string) domain.FilterItem {
	return containsItem(domain.FieldStatementName, 0, keywords...)
}

func ruleWith(items ...domain.FilterItem) PreparedRule {
	return PreparedRule{ID: uuid.UUID{7}, Filter: domain.Filter{Items: items}}
}

func TestNoConditionsMatchesNothing(t *testing.T) {
	// The opposite of an empty register filter. An empty rule that matched
	// everything would recategorize the whole ledger on its first run.
	require.False(t, MatchesRule(ruleWith(), ruleRow()))
}

func TestABlankKeywordMatchesNothing(t *testing.T) {
	require.False(t, MatchesRule(ruleWith(statementContains("")), ruleRow()))
	require.False(t, MatchesRule(ruleWith(statementContains("   ")), ruleRow()))
}

func TestEveryKeywordInAGroupMustBePresent(t *testing.T) {
	require.True(t, MatchesRule(ruleWith(statementContains("streamsvc", "com")), ruleRow()))
	require.False(t, MatchesRule(ruleWith(statementContains("streamsvc", "hulu")), ruleRow()))
}

func TestASecondGroupWidensRatherThanNarrows(t *testing.T) {
	// The builder's + button adds an alternative keyword set. AND-ing the
	// groups would make every multi-group rule match nothing, silently.
	rule := ruleWith(
		containsItem(domain.FieldStatementName, 0, "hulu"),
		containsItem(domain.FieldStatementName, 1, "streamsvc"),
	)
	require.True(t, MatchesRule(rule, ruleRow()))
}

func TestIsExactlyIsTheWholeName(t *testing.T) {
	rule := ruleWith(domain.FilterItem{
		Field:    domain.FieldStatementName,
		Operator: domain.OpIsExactly,
		Values:   []string{"STREAMSVC.COM 800-555-0100"},
	})
	require.True(t, MatchesRule(rule, ruleRow()))
	require.False(t, MatchesRule(rule, ruleRow(func(txn *domain.Transaction) {
		txn.StatementName = "STREAMSVC.COM 999"
	})))
}

func TestMatchingIsCaseAndAccentInsensitive(t *testing.T) {
	row := ruleRow(func(txn *domain.Transaction) { txn.StatementName = "CAFÉ CENTRAL" })
	require.True(t, MatchesRule(ruleWith(statementContains("cafe")), row))
}

func TestTheTwoNamesAreMatchedSeparately(t *testing.T) {
	// A rule on the statement name goes on firing after its own rename action
	// lands; a rule on the payee does not.
	renamed := ruleRow(func(txn *domain.Transaction) {
		txn.StatementName, txn.Payee = "SQ *BLUE BOTTLE", "Blue Bottle Coffee"
	})
	require.True(t, MatchesRule(ruleWith(statementContains("sq")), renamed))
	require.False(t, MatchesRule(ruleWith(containsItem(domain.FieldPayee, 0, "sq")), renamed))
	require.True(t, MatchesRule(ruleWith(containsItem(domain.FieldPayee, 0, "blue bottle")), renamed))
}

func TestThePayeeFieldFallsBackToTheBankName(t *testing.T) {
	unnamed := ruleRow(func(txn *domain.Transaction) {
		txn.StatementName, txn.Payee = "ACME CORP", ""
	})
	require.True(t, MatchesRule(ruleWith(containsItem(domain.FieldPayee, 0, "acme")), unnamed))
}

func TestAmountComparisonsRunOnTheMagnitudeWithADirection(t *testing.T) {
	overTen := ruleWith(domain.FilterItem{
		Field:      domain.FieldAmount,
		Operator:   domain.OpGreaterThan,
		Minimum:    domain.MustFromString("10"),
		HasMinimum: true,
		HasState:   true,
		State:      false,
	})
	amount := func(value string) RuleCandidate {
		return ruleRow(func(txn *domain.Transaction) { txn.Amount = domain.MustFromString(value) })
	}
	require.True(t, MatchesRule(overTen, amount("-15.00")))
	require.False(t, MatchesRule(overTen, amount("-4.99")))
	// Same size, wrong direction: an expense rule must not fire on a refund.
	require.False(t, MatchesRule(overTen, amount("15.00")))
}

func TestBetweenIsInclusiveAtBothEnds(t *testing.T) {
	band := ruleWith(domain.FilterItem{
		Field:      domain.FieldAmount,
		Operator:   domain.OpBetween,
		Minimum:    domain.MustFromString("10"),
		HasMinimum: true,
		Maximum:    domain.MustFromString("20"),
		HasMaximum: true,
	})
	amount := func(value string) RuleCandidate {
		return ruleRow(func(txn *domain.Transaction) { txn.Amount = domain.MustFromString(value) })
	}
	require.True(t, MatchesRule(band, amount("-10.00")))
	require.True(t, MatchesRule(band, amount("-20.00")))
	require.False(t, MatchesRule(band, amount("-20.01")))
}

func TestNegationWrapsTheWholeItem(t *testing.T) {
	notAccount := ruleWith(domain.FilterItem{
		Field:   domain.FieldAccount,
		Values:  []string{ruleAccountID.String()},
		Negated: true,
	})
	require.False(t, MatchesRule(notAccount, ruleRow()))
	elsewhere := ruleRow(func(txn *domain.Transaction) {
		txn.AccountID = domain.ID(otherAccountID.String())
	})
	require.True(t, MatchesRule(notAccount, elsewhere))
}

func TestUncategorizedIsAStateNotAnAbsence(t *testing.T) {
	rule := ruleWith(domain.FilterItem{
		Field: domain.FieldIsUncategorized, HasState: true, State: true,
	})
	require.True(t, MatchesRule(rule, ruleRow()))
	categorized := ruleRow(func(txn *domain.Transaction) {
		txn.CategoryID = domain.ID(uuid.New().String())
	})
	require.False(t, MatchesRule(rule, categorized))
}

func actionRule(number byte, priority int, actions RuleActions) PreparedRule {
	return PreparedRule{
		ID:       uuid.UUID{0, number},
		Name:     "streamsvc",
		Filter:   domain.Filter{Items: []domain.FilterItem{statementContains("streamsvc")}},
		Actions:  actions,
		Priority: priority,
	}
}

func TestTheFirstRuleToSetAFieldWins(t *testing.T) {
	// Two rules renaming the same payee resolve the same way on every run.
	changes := PlanChanges(
		[]PreparedRule{
			actionRule(2, 10, RuleActions{SetPayee: "Second"}),
			actionRule(1, 1, RuleActions{SetPayee: "First"}),
		},
		[]RuleCandidate{ruleRow(func(txn *domain.Transaction) { txn.Payee = "" })},
	)
	require.Len(t, changes, 1)
	require.Equal(t, "First", changes[0].Payee)
}

func TestTagsAndNotesAccumulateAcrossRules(t *testing.T) {
	tagA, tagB := uuid.UUID{7}, uuid.UUID{8}
	changes := PlanChanges(
		[]PreparedRule{
			actionRule(1, 1, RuleActions{AddTagIDs: []uuid.UUID{tagA}, SetNotes: "streaming"}),
			actionRule(2, 2, RuleActions{AddTagIDs: []uuid.UUID{tagB}, SetNotes: "monthly"}),
		},
		[]RuleCandidate{ruleRow()},
	)
	require.Len(t, changes, 1)
	require.Equal(t, []uuid.UUID{tagA, tagB}, changes[0].AddTagIDs)
	require.Equal(t, "streaming monthly", changes[0].Notes)
}

func TestARuleSetsNoCategoryOnASplitRow(t *testing.T) {
	// The parent of a split row carries no category; its splits do. The
	// rule's other actions still apply.
	split := ruleRow(func(txn *domain.Transaction) {
		txn.Splits = []domain.Split{
			{ID: "s-1", Amount: domain.MustFromString("-9.99"), CategoryID: "cat-streaming"},
			{ID: "s-2", Amount: domain.MustFromString("-5.00"), CategoryID: "cat-gifts"},
		}
	})
	changes := PlanChanges(
		[]PreparedRule{actionRule(1, 1, RuleActions{SetCategoryID: uuid.UUID{9}, SetNotes: "family plan"})},
		[]RuleCandidate{split},
	)
	require.Len(t, changes, 1)
	require.False(t, changes[0].HasCategoryID)
	require.Equal(t, "family plan", changes[0].Notes)
}

func TestARuleThatChangesNothingProducesNoDiff(t *testing.T) {
	changes := PlanChanges(
		[]PreparedRule{actionRule(1, 1, RuleActions{SetPayee: "Example Streaming"})},
		[]RuleCandidate{ruleRow(func(txn *domain.Transaction) { txn.Payee = "Example Streaming" })},
	)
	require.Empty(t, changes)
}

func TestExclusionActionsAreThreeState(t *testing.T) {
	excluded := ruleRow(func(txn *domain.Transaction) { txn.ExcludedFromReports = true })
	changes := PlanChanges(
		[]PreparedRule{actionRule(1, 1, RuleActions{HasExcludedFromReports: true})},
		[]RuleCandidate{excluded},
	)
	require.Len(t, changes, 1)
	require.True(t, changes[0].HasExcludedFromReports)
	require.False(t, changes[0].ExcludedFromReports)

	// Unset is "leave alone", which is not the same as clearing it.
	require.Empty(t, PlanChanges(
		[]PreparedRule{actionRule(1, 1, RuleActions{})},
		[]RuleCandidate{excluded},
	))
}

func TestMergeNotesNeverDropsOrDoubles(t *testing.T) {
	require.Equal(t, "kept added", MergeNotes("kept", "added"))
	require.Equal(t, "kept added", MergeNotes("kept added", "added"))
	require.Equal(t, "added", MergeNotes("", "added"))
	require.Equal(t, "kept", MergeNotes("kept", ""))
}

// storedRule writes a filter and a rule the way the builder would.
func storedRule(
	t *testing.T, spaceID store.SpaceID, keyword string, apply func(*ruleFixture),
) uuid.UUID {
	t.Helper()
	filter := &store.Filter{
		Name:  "Example Streaming",
		Scope: "rule",
		Items: []store.FilterItem{{
			Field:      string(domain.FieldStatementName),
			Operator:   string(domain.OpContains),
			ValueTexts: []string{keyword},
		}},
	}
	require.NoError(t, db(t).CreateFilter(t.Context(), spaceID, filter))

	fixture := ruleFixture{ID: uuid.New(), IsActive: true, AddTagIDs: []uuid.UUID{}}
	if apply != nil {
		apply(&fixture)
	}
	_, err := db(t).Pool().Exec(t.Context(), `
		INSERT INTO rules (id, space_id, name, filter_id, priority, is_active, set_payee,
			set_category_id, add_tag_ids, set_notes, set_excluded_from_reports,
			set_excluded_from_spending_plan, set_is_reviewed)
		VALUES ($1, $2, 'Example Streaming', $3, 0, $4, $5, $6, $7, $8, $9, $10, $11)`,
		fixture.ID, spaceID.UUID(), filter.ID, fixture.IsActive, pgconv.NullText(fixture.SetPayee),
		pgconv.NullUUID(fixture.SetCategoryID), fixture.AddTagIDs, pgconv.NullText(fixture.SetNotes),
		fixture.SetExcludedFromReports, fixture.SetExcludedFromSpendingPlan, fixture.SetIsReviewed)
	require.NoError(t, err)
	return fixture.ID
}

type ruleFixture struct {
	ID                          uuid.UUID
	IsActive                    bool
	SetPayee                    string
	SetCategoryID               uuid.UUID
	AddTagIDs                   []uuid.UUID
	SetNotes                    string
	SetExcludedFromReports      *bool
	SetExcludedFromSpendingPlan *bool
	SetIsReviewed               *bool
}

func TestAPreviewWritesNothingAndThenTheApplyWritesIt(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	category := newCategory(t, spaceID, "Entertainment", uuid.Nil)
	tag := newTag(t, spaceID, "streaming")
	txn := newTransaction(t, spaceID, account, on(2026, time.March, 2), "-15.00",
		withStatementName("STREAMSVC.COM 800-555-0100"))
	reviewed := true
	ruleID := storedRule(t, spaceID, "streamsvc", func(f *ruleFixture) {
		f.SetPayee = "Example Streaming"
		f.SetCategoryID = category.ID
		f.AddTagIDs = []uuid.UUID{tag.ID}
		f.SetIsReviewed = &reviewed
	})

	rules := NewRules(db(t))
	preview, err := rules.PreviewRule(t.Context(), spaceID, ruleID, CandidateQuery{})
	require.NoError(t, err)
	require.Equal(t, 1, preview.Matched)
	require.Len(t, preview.Changes, 1)

	before := reload(t, spaceID, txn.ID)
	require.Equal(t, "", before.Payee)
	require.Equal(t, uuid.Nil, before.CategoryID)

	applied, err := rules.ApplyChanges(t.Context(), spaceID, preview.Changes)
	require.NoError(t, err)
	require.Equal(t, 1, applied)

	after := reload(t, spaceID, txn.ID)
	require.Equal(t, "Example Streaming", after.Payee)
	require.Equal(t, category.ID, after.CategoryID)
	require.True(t, after.IsReviewed)
	require.Equal(t, ruleID, after.RuleID)
	require.Equal(t, []uuid.UUID{tag.ID}, after.TagIDs)
}

func TestASecondApplyIsANoOp(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	tag := newTag(t, spaceID, "streaming")
	newTransaction(t, spaceID, account, on(2026, time.March, 2), "-15.00",
		withStatementName("STREAMSVC.COM"))
	ruleID := storedRule(t, spaceID, "streamsvc", func(f *ruleFixture) {
		f.SetPayee = "Example Streaming"
		f.AddTagIDs = []uuid.UUID{tag.ID}
	})

	rules := NewRules(db(t))
	first, err := rules.PreviewRule(t.Context(), spaceID, ruleID, CandidateQuery{})
	require.NoError(t, err)
	_, err = rules.ApplyChanges(t.Context(), spaceID, first.Changes)
	require.NoError(t, err)

	second, err := rules.PreviewRule(t.Context(), spaceID, ruleID, CandidateQuery{})
	require.NoError(t, err)
	require.Equal(t, 1, second.Matched)
	require.Empty(t, second.Changes)
	require.Equal(t, 1, second.Unchanged())
}

func TestAnInactiveRuleDoesNotRunOnNewRows(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	txn := newTransaction(t, spaceID, account, on(2026, time.March, 2), "-15.00",
		withStatementName("STREAMSVC.COM"))
	storedRule(t, spaceID, "streamsvc", func(f *ruleFixture) {
		f.SetPayee = "Example Streaming"
		f.IsActive = false
	})

	applied, err := NewRules(db(t)).RunRules(t.Context(), spaceID, []uuid.UUID{txn.ID})
	require.NoError(t, err)
	require.Equal(t, 0, applied)
	require.Equal(t, "", reload(t, spaceID, txn.ID).Payee)
}

func TestARuleCannotMatchOnTheSeriesMatchersVerdict(t *testing.T) {
	// The evaluator would fail such an item silently, which reads as a rule
	// that matches nothing for no visible reason.
	spaceID := newSpace(t)
	filter := &store.Filter{
		Name:  "Bills",
		Scope: "rule",
		Items: []store.FilterItem{{
			Field:    string(domain.FieldIsBillOrSubscription),
			Operator: string(domain.OpIsTrue),
			State:    new(bool),
		}},
	}
	require.NoError(t, db(t).CreateFilter(t.Context(), spaceID, filter))
	ruleID := uuid.New()
	_, err := db(t).Pool().Exec(t.Context(), `
		INSERT INTO rules (id, space_id, name, filter_id, priority) VALUES ($1, $2, 'Bills', $3, 0)`,
		ruleID, spaceID.UUID(), filter.ID)
	require.NoError(t, err)

	_, err = NewRules(db(t)).LoadRule(t.Context(), spaceID, ruleID)
	require.ErrorContains(t, err, "cannot match on is_bill_or_subscription")
}

func TestARuleBoundedByDatesReadsTheEffectiveDate(t *testing.T) {
	// Posted on the last day of February, paid with the card's March
	// statement: trap 4 files it in March.
	row := ruleRow(func(txn *domain.Transaction) {
		txn.Date = on(2026, time.February, 28)
		txn.EffectiveDate = on(2026, time.March, 20)
	})
	march := ruleWith(statementContains("streamsvc"),
		domain.FilterItem{Field: domain.FieldDate, Start: on(2026, time.March, 1), End: on(2026, time.March, 31)})
	february := ruleWith(statementContains("streamsvc"),
		domain.FilterItem{Field: domain.FieldDate, Start: on(2026, time.February, 1), End: on(2026, time.February, 28)})
	require.True(t, MatchesRule(march, row))
	require.False(t, MatchesRule(february, row))
}
