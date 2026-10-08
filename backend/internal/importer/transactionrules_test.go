package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// An invented rules file, in the shape Simplifi's GET transaction-rules
// answers with. tr9 and tr8 are the two rules withRuleEvidence's rows name;
// tr7 is a rule no row has met yet, and matches business accounts.
const rulesFileJSON = `{"resources": [
  {
    "id": "tr8", "createdAt": "2026-01-02T00:00:00Z", "modifiedAt": "2026-01-02T00:00:00Z",
    "hashKey": "h8", "prevTransactionRuleId": "tr9", "nextTransactionRuleId": "tr7",
    "payeeSourceCondition": {"payeeType": "INFERRED", "payeeMatchingCriteria": [
      {"payeeMatchingOperator": "EQUALS", "payeeName": "Payee A"}
    ]},
    "amountSourceCondition": {"amountMatchingOperator": "GREATER_THAN", "amount1": 1000.00},
    "memoTarget": "checked", "isReviewedTarget": true,
    "applyToExistingTransactions": false
  },
  {
    "id": "tr9", "createdAt": "2026-01-01T00:00:00Z", "modifiedAt": "2026-01-01T00:00:00Z",
    "hashKey": "h9", "nextTransactionRuleId": "tr8",
    "payeeSourceCondition": {"payeeType": "STATEMENT", "payeeMatchingCriteria": [
      {"payeeMatchingOperator": "CONTAINS", "payeeName": "CITY PARKING"},
      {"payeeMatchingOperator": "EQUALS", "payeeName": "LOT SEVEN"}
    ]},
    "accountSourceCondition": {"accountSourceType": "SPECIFIC", "accountSourceIds": ["a1"]},
    "amountSourceCondition": {"amountMatchingOperator": "BETWEEN", "amountType": "EXPENSE",
      "amount1": -5.00, "amount2": -40.50},
    "usageSourceType": "PERSONAL",
    "payeeTarget": "Parking", "coaTargetCondition": {"type": "CATEGORY", "id": "c1"},
    "tagsTargetIds": ["t1"], "isExcludedFromReportsTarget": true
  },
  {
    "id": "tr7", "prevTransactionRuleId": "tr8",
    "payeeSourceCondition": {"payeeType": "STATEMENT", "payeeMatchingCriteria": [
      {"payeeMatchingOperator": "CONTAINS", "payeeName": "OFFICE"}
    ]},
    "accountSourceCondition": {"accountSourceType": "BUSINESS"},
    "payeeTarget": "Office Supplies"
  }
]}`

func withRulesFile(t *testing.T, export *Export, rules string) *Export {
	t.Helper()
	require.NoError(t, export.AddTransactionRules(fixtureDataset, []byte(rules)))
	return export
}

func groupOf(filter *store.Filter, group int) []store.FilterItem {
	var out []store.FilterItem
	for _, item := range filter.Items {
		if item.GroupIndex == group {
			out = append(out, item)
		}
	}
	return out
}

func TestARuleFromTheRulesFileKeepsSimplifisConditionsAndActions(t *testing.T) {
	out := mapped(t, withRulesFile(t, withRuleEvidence(t), rulesFileJSON))
	require.Empty(t, out.Report.Errors, out.Report.Render())

	rule := ruleBySource(t, out, "simplifi:transaction-rules:tr9")
	require.True(t, rule.IsActive)
	require.Equal(t, "Parking", rule.Name)
	require.Equal(t, "Parking", rule.SetPayee)
	require.Equal(t, categoryNamed(t, out, "Groceries").ID, rule.SetCategoryID)
	require.Equal(t, []uuid.UUID{out.Tags[0].ID}, rule.AddTagIDs)
	require.NotNil(t, rule.SetExcludedFromReports)
	require.True(t, *rule.SetExcludedFromReports)
	// The rule says nothing about the spending plan, so it leaves it alone.
	require.Nil(t, rule.SetExcludedFromSpendingPlan)
	require.Nil(t, rule.SetIsReviewed)

	// Each payee alternative is a group, and the account and amount
	// conditions hold in both.
	filter := filterByID(t, out, rule.FilterID)
	checking := accountNamed(t, out, "Checking 1").ID
	for group, payee := range []store.FilterItem{
		{Field: string(domain.FieldStatementName), Operator: string(domain.OpContains), ValueTexts: []string{"CITY", "PARKING"}},
		{Field: string(domain.FieldStatementName), Operator: string(domain.OpIsExactly), ValueTexts: []string{"LOT SEVEN"}},
	} {
		items := groupOf(filter, group)
		require.Len(t, items, 3)
		require.Equal(t, payee.Field, items[0].Field)
		require.Equal(t, payee.Operator, items[0].Operator)
		require.Equal(t, payee.ValueTexts, items[0].ValueTexts)
		require.Equal(t, string(domain.FieldAccount), items[1].Field)
		require.Equal(t, []uuid.UUID{checking}, items[1].ValueIDs)
		amount := items[2]
		require.Equal(t, string(domain.FieldAmount), amount.Field)
		require.Equal(t, string(domain.OpBetween), amount.Operator)
		require.Equal(t, "5.00", amount.AmountMin.String())
		require.Equal(t, "40.50", amount.AmountMax.String())
		require.NotNil(t, amount.State)
		require.False(t, *amount.State)
	}

	// The rows that named tr9 name the real rule, and nothing was rebuilt.
	require.Equal(t, rule.ID, txnByStatement(t, out, "CITY PARKING 001").RuleID)
	for _, found := range out.Rules {
		require.NotContains(t, found.SourceRef, sourceRefTransactionRule)
	}
	for _, warning := range out.Report.Warnings {
		require.NotEqual(t, "transactionRuleId", warning.Field, warning.Message)
	}
}

func TestARulesFileRuleReadsThePayeeItNamesAndAPositiveAmountAsIncome(t *testing.T) {
	out := mapped(t, withRulesFile(t, withRuleEvidence(t), rulesFileJSON))
	rule := ruleBySource(t, out, "simplifi:transaction-rules:tr8")
	require.True(t, rule.IsActive)
	require.Equal(t, "Payee A", rule.Name)
	require.Equal(t, "checked", rule.SetNotes)
	require.NotNil(t, rule.SetIsReviewed)
	require.True(t, *rule.SetIsReviewed)

	filter := filterByID(t, out, rule.FilterID)
	require.Len(t, filter.Items, 2)
	require.Equal(t, string(domain.FieldPayee), filter.Items[0].Field)
	require.Equal(t, string(domain.OpIsExactly), filter.Items[0].Operator)
	require.Equal(t, string(domain.OpGreaterThan), filter.Items[1].Operator)
	require.Equal(t, "1000.00", filter.Items[1].AmountMin.String())
	require.True(t, *filter.Items[1].State)
}

func TestRulesFileRulesKeepSimplifisOrder(t *testing.T) {
	out := mapped(t, withRulesFile(t, withRuleEvidence(t), rulesFileJSON))
	require.Equal(t, 1, ruleBySource(t, out, "simplifi:transaction-rules:tr9").Priority)
	require.Equal(t, 2, ruleBySource(t, out, "simplifi:transaction-rules:tr8").Priority)
	require.Equal(t, 3, ruleBySource(t, out, "simplifi:transaction-rules:tr7").Priority)
}

func TestARuleOnBusinessAccountsArrivesSwitchedOffAndSaysWhy(t *testing.T) {
	out := mapped(t, withRulesFile(t, withRuleEvidence(t), rulesFileJSON))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	rule := ruleBySource(t, out, "simplifi:transaction-rules:tr7")
	require.False(t, rule.IsActive)
	requireProblem(t, out.Report.Warnings, transactionRulesStore, "", "business accounts")
}

func TestARuleTheRowsNameButTheFileLacksIsStillRebuilt(t *testing.T) {
	onlyTr9 := `[{"id": "tr9", "payeeSourceCondition": {"payeeMatchingCriteria": [
		{"payeeMatchingOperator": "CONTAINS", "payeeName": "PARKING"}]}, "payeeTarget": "Parking"}]`
	out := mapped(t, withRulesFile(t, withRuleEvidence(t), onlyTr9))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	ruleBySource(t, out, "simplifi:transaction-rules:tr9")
	ruleBySource(t, out, "simplifi:transactionRuleId:tr8")
	requireProblem(t, out.Report.Warnings, "transactionStore", "transactionRuleId", "does not hold this rule")
}

func TestARuleWithNoConditionIsNeverLeftOn(t *testing.T) {
	out := mapped(t, withRulesFile(t, complete(t), `[{"id": "tr1", "payeeTarget": "Everything"}]`))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	require.False(t, ruleBySource(t, out, "simplifi:transaction-rules:tr1").IsActive)
	requireProblem(t, out.Report.Warnings, transactionRulesStore, "", "matches every row")
}

func TestAnUnknownRulesFileFieldIsNotedAndTheRuleImports(t *testing.T) {
	out := mapped(t, withRulesFile(t, complete(t), `[{"id": "tr1", "payeeTarget": "X", "splitTarget": 1,
		"payeeSourceCondition": {"payeeMatchingCriteria": [{"payeeName": "X", "caseSensitive": true}]}}]`))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	requireProblem(t, out.Report.Notes, transactionRulesStore, "splitTarget", "not read")
	requireProblem(t, out.Report.Notes, transactionRulesStore,
		"payeeSourceCondition.payeeMatchingCriteria.0.caseSensitive", "not read")
	require.Equal(t, "X", ruleBySource(t, out, "simplifi:transaction-rules:tr1").SetPayee)
}

func TestRulesSimplifiStampedWithUserModifiedAtImport(t *testing.T) {
	var rules []string
	for i := 1; i <= 6; i++ {
		rules = append(rules, fmt.Sprintf(`{"id": "tr%d", "payeeTarget": "Payee %d",
			"userModifiedAt": "2001-02-03T04:05:06Z",
			"payeeSourceCondition": {"payeeType": "STATEMENT",
				"payeeMatchingCriteria": [{"payeeMatchingOperator": "CONTAINS", "payeeName": "SHOP%d"}]}}`, i, i, i))
	}
	out := mapped(t, withRulesFile(t, complete(t), `{"resources": [`+strings.Join(rules, ",")+`]}`))
	require.Empty(t, out.Report.Errors, out.Report.Render())
	for i := 1; i <= 6; i++ {
		rule := ruleBySource(t, out, fmt.Sprintf("simplifi:transaction-rules:tr%d", i))
		require.True(t, rule.IsActive)
	}
	notes := problems(out.Report.Notes, transactionRulesStore, "userModifiedAt")
	require.Len(t, notes, 1)
	require.Equal(t, 6, notes[0].Occurrences)
}

func TestTheRulesFileMustBeTheRulesResponse(t *testing.T) {
	require.ErrorContains(t, complete(t).AddTransactionRules(fixtureDataset, []byte(`{"rules": []}`)), "resources")
	require.ErrorContains(t, complete(t).AddTransactionRules(fixtureDataset, []byte(`[{"payeeTarget": "X"}]`)), "no id")
	require.ErrorContains(t, complete(t).AddTransactionRules(fixtureDataset, []byte(`[{"id": "a"}, {"id": "a"}]`)), "twice")
	require.NoError(t, complete(t).AddTransactionRules(fixtureDataset, []byte(`[]`)))
}

func TestTheCommandLineTakesTheRulesFile(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "transaction-rules.json")
	require.NoError(t, os.WriteFile(rules, []byte(rulesFileJSON), 0o600))
	code, out, errOut := run(t, writeExport(t, withRuleEvidence(t)), "--transaction-rules", rules, "--dry-run")
	require.Equal(t, 0, code, errOut)
	require.Contains(t, out, transactionRulesStore)
}

// -- the rules-only upgrade -------------------------------------------

func TestARulesFileUpgradesTheRebuiltRulesInPlace(t *testing.T) {
	first, space := writtenFrom(t, withRuleEvidence(t))
	ctx := t.Context()
	rebuiltID := scanOne[uuid.UUID](t, ctx,
		`SELECT id FROM rules WHERE space_id = $1 AND source_ref = 'simplifi:transactionRuleId:tr9'`, space)

	outcome, err := WriteRules(ctx, db(t), remapped(t, withRulesFile(t, withRuleEvidence(t), rulesFileJSON), first), space, false)
	require.NoError(t, err, outcome.Render())
	require.Len(t, outcome.Upgraded, 2)
	require.Equal(t, []string{"Office Supplies"}, outcome.Inserted)
	require.Len(t, outcome.Present, 1)
	require.ElementsMatch(t,
		[]string{"simplifi:renameRuleStore:r1", "simplifi:transaction-rules:tr7",
			"simplifi:transaction-rules:tr8", "simplifi:transaction-rules:tr9"},
		scanAll[string](t, `SELECT source_ref FROM rules WHERE space_id = $1`, space))

	// The rule keeps its id, so the rows that named it still do, and now
	// carries Simplifi's conditions: three in each of two groups, the account
	// found again as the space's own.
	require.Equal(t, rebuiltID, scanOne[uuid.UUID](t, ctx,
		`SELECT id FROM rules WHERE space_id = $1 AND source_ref = 'simplifi:transaction-rules:tr9'`, space))
	require.Equal(t, 3, scanOne[int](t, ctx,
		`SELECT count(*) FROM transactions WHERE space_id = $1 AND rule_id = $2`, space, rebuiltID))
	require.Equal(t, 6, scanOne[int](t, ctx, `
		SELECT count(*) FROM filter_items i JOIN rules r ON r.filter_id = i.filter_id WHERE r.id = $1`, rebuiltID))
	require.Equal(t, []uuid.UUID{accountNamed(t, first, "Checking 1").ID}, scanOne[[]uuid.UUID](t, ctx, `
		SELECT i.value_ids FROM filter_items i JOIN rules r ON r.filter_id = i.filter_id
		 WHERE r.id = $1 AND i.field = 'account' AND i.group_index = 1`, rebuiltID))
	require.Equal(t, "Parking", scanOne[string](t, ctx, `SELECT name FROM rules WHERE id = $1`, rebuiltID))

	again, err := WriteRules(ctx, db(t), remapped(t, withRulesFile(t, withRuleEvidence(t), rulesFileJSON), first), space, false)
	require.NoError(t, err)
	require.Len(t, again.Present, 4)
	require.Empty(t, again.Upgraded)
	require.Empty(t, again.Inserted)
	require.Equal(t, 4, scanOne[int](t, ctx, `SELECT count(*) FROM rules WHERE space_id = $1`, space))
}

func TestARebuiltRuleDeletedHereStaysDeletedWhenUpgraded(t *testing.T) {
	first, space := writtenFrom(t, withRuleEvidence(t))
	ctx := t.Context()
	_, err := db(t).Pool().Exec(ctx, `UPDATE rules SET is_deleted = true, updated_at = ts_add(now(), '+1 minute')
		WHERE space_id = $1 AND source_ref = 'simplifi:transactionRuleId:tr8'`, space)
	require.NoError(t, err)

	outcome, err := WriteRules(ctx, db(t), remapped(t, withRulesFile(t, withRuleEvidence(t), rulesFileJSON), first), space, false)
	require.NoError(t, err)
	require.Contains(t, outcome.Render(), "it had been edited here")
	require.True(t, scanOne[bool](t, ctx,
		`SELECT is_deleted FROM rules WHERE space_id = $1 AND source_ref = 'simplifi:transaction-rules:tr8'`, space))
}
