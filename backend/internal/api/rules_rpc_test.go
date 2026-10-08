package api

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// RuleService over its own protocol. rules_test.go reaches the same methods
// through the REST bridge.

func cornerConditions() []*agentifiv1.FilterItemWrite {
	return []*agentifiv1.FilterItemWrite{{
		Field:      string(domain.FieldStatementName),
		Operator:   string(domain.OpContains),
		ValueTexts: []string{"CORNER"},
	}}
}

func createCornerRule(t *testing.T, l *ledger) *agentifiv1.Rule {
	t.Helper()
	res, err := call[agentifiv1.CreateRuleRequest, agentifiv1.CreateRuleResponse](
		l.alex, agentifiv1connect.RuleServiceCreateRuleProcedure, &agentifiv1.CreateRuleRequest{
			Name:       "Corner Store",
			Conditions: cornerConditions(),
			Actions: &agentifiv1.RuleActionsWrite{
				SetPayee:      proto.String("Corner Coffee"),
				SetIsReviewed: proto.Bool(false),
			},
		})
	require.Nil(t, err)
	return res.GetRule()
}

func TestARuleCreatedOverRPCOwnsItsConditions(t *testing.T) {
	l := buildLedger(t)
	rule := createCornerRule(t, l)
	require.True(t, rule.GetOwnsFilter())
	require.Equal(t, "rule", rule.GetFilter().GetScope())
	require.Len(t, rule.GetFilter().GetItems(), 1)
	require.Equal(t, []string{"CORNER"}, rule.GetFilter().GetItems()[0].GetValueTexts())
	require.Equal(t, "Corner Coffee", rule.GetActions().GetSetPayee())
	require.False(t, rule.GetActions().GetSetIsReviewed())
	require.NotNil(t, rule.GetActions().SetIsReviewed)
	require.Nil(t, rule.GetActions().SetCategoryId)
}

func TestAnActionsPatchClearsOnlyTheActionsItsMaskNames(t *testing.T) {
	l := buildLedger(t)
	rule := createCornerRule(t, l)
	update := func(actions *agentifiv1.RuleActionsWrite) *agentifiv1.RuleActions {
		t.Helper()
		res, err := call[agentifiv1.UpdateRuleRequest, agentifiv1.UpdateRuleResponse](
			l.alex, agentifiv1connect.RuleServiceUpdateRuleProcedure, &agentifiv1.UpdateRuleRequest{
				RuleId: rule.GetId(), Actions: actions,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"actions"}},
			})
		require.Nil(t, err)
		return res.GetRule().GetActions()
	}

	// Set: a note joins the actions already there.
	actions := update(&agentifiv1.RuleActionsWrite{SetNotes: proto.String("coffee")})
	require.Equal(t, "coffee", actions.GetSetNotes())
	require.Equal(t, "Corner Coffee", actions.GetSetPayee())

	// Cleared: named in the actions' own mask and unset.
	actions = update(&agentifiv1.RuleActionsWrite{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"set_is_reviewed"}},
	})
	require.Nil(t, actions.SetIsReviewed)
	require.Equal(t, "Corner Coffee", actions.GetSetPayee())
	require.Equal(t, "coffee", actions.GetSetNotes())

	// Absent: a rename of the rule leaves every action alone.
	res, err := call[agentifiv1.UpdateRuleRequest, agentifiv1.UpdateRuleResponse](
		l.alex, agentifiv1connect.RuleServiceUpdateRuleProcedure, &agentifiv1.UpdateRuleRequest{
			RuleId: rule.GetId(), Name: proto.String("Corner"),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		})
	require.Nil(t, err)
	require.Equal(t, "Corner Coffee", res.GetRule().GetActions().GetSetPayee())
}

func TestConditionsNamedInTheMaskAreReplacedAndCannotBeEmptied(t *testing.T) {
	l := buildLedger(t)
	rule := createCornerRule(t, l)

	replaced, err := call[agentifiv1.UpdateRuleRequest, agentifiv1.UpdateRuleResponse](
		l.alex, agentifiv1connect.RuleServiceUpdateRuleProcedure, &agentifiv1.UpdateRuleRequest{
			RuleId: rule.GetId(),
			Conditions: []*agentifiv1.FilterItemWrite{{
				Field: string(domain.FieldStatementName), Operator: string(domain.OpContains),
				ValueTexts: []string{"STORE"},
			}},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"conditions"}},
		})
	require.Nil(t, err)
	require.Equal(t, []string{"STORE"}, replaced.GetRule().GetFilter().GetItems()[0].GetValueTexts())

	_, err = call[agentifiv1.UpdateRuleRequest, agentifiv1.UpdateRuleResponse](
		l.alex, agentifiv1connect.RuleServiceUpdateRuleProcedure, &agentifiv1.UpdateRuleRequest{
			RuleId: rule.GetId(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"conditions"}},
		})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
}

func TestAnEmptyApprovedSetAppliesNothingAndAnUnsetOneAppliesEveryMatch(t *testing.T) {
	l := buildLedger(t)
	rule := createCornerRule(t, l)
	apply := func(approved *agentifiv1.IdSet) *agentifiv1.ApplyRuleResponse {
		t.Helper()
		res, err := call[agentifiv1.ApplyRuleRequest, agentifiv1.ApplyRuleResponse](
			l.alex, agentifiv1connect.RuleServiceApplyRuleProcedure,
			&agentifiv1.ApplyRuleRequest{RuleId: rule.GetId(), TransactionIds: approved})
		require.Nil(t, err)
		return res
	}

	require.Equal(t, int32(0), apply(&agentifiv1.IdSet{}).GetApplied())
	require.Equal(t, "Corner Store", reloadTxn(l, l.id("august_corner")).Payee)

	require.Equal(t, int32(1), apply(nil).GetApplied())
	require.Equal(t, "Corner Coffee", reloadTxn(l, l.id("august_corner")).Payee)
}

func TestAViewerMayPreviewARuleButNotApplyIt(t *testing.T) {
	l := buildLedger(t)
	rule := createCornerRule(t, l)
	vera := l.as("vera")

	preview, err := call[agentifiv1.PreviewRuleRequest, agentifiv1.PreviewRuleResponse](
		vera, agentifiv1connect.RuleServicePreviewRuleProcedure,
		&agentifiv1.PreviewRuleRequest{RuleId: rule.GetId()})
	require.Nil(t, err)
	require.Equal(t, int32(1), preview.GetChanged())
	require.Equal(t, "-25.00", preview.GetChanges()[0].GetAmount().GetAmount())

	_, err = call[agentifiv1.ApplyRuleRequest, agentifiv1.ApplyRuleResponse](
		vera, agentifiv1connect.RuleServiceApplyRuleProcedure,
		&agentifiv1.ApplyRuleRequest{RuleId: rule.GetId()})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}

func TestAnotherSpacesRuleIsNotFound(t *testing.T) {
	l := buildLedger(t)
	rule := createCornerRule(t, l)
	bob := l.as("bob").inSpace(store.SpaceIDOf(l.id("other_space")))
	for _, id := range []string{rule.GetId(), "not-an-id"} {
		_, err := call[agentifiv1.GetRuleRequest, agentifiv1.GetRuleResponse](
			bob, agentifiv1connect.RuleServiceGetRuleProcedure, &agentifiv1.GetRuleRequest{RuleId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Rule not found", err.Message())
	}
}
