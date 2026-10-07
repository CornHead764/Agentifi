package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An alert marked Evaluated must be answerable from the AlertInputs the sweep
// in internal/service/alerts.go populates, or the settings page advertises a
// switch that can never ring. The alerts marked Evaluated: false have no
// evaluator or source; enabling one means flipping its flag here and in the
// catalog together.

// sweepFeedableAlerts is the alert types the current gather can decide.
var sweepFeedableAlerts = map[AlertType]bool{
	AlertLargeTransaction:       true,
	AlertLargeDeposit:           true,
	AlertBankFee:                true,
	AlertUncategorized:          true,
	AlertLowBankBalance:         true,
	AlertHighCardBalance:        true,
	AlertUpcomingBills:          true,
	AlertWatchlistNearingTarget: true,
	AlertProjectedLowBalance:    true,
	AlertBillsToIncomePct:       true,
	AlertExtraPaycheckMonth:     true,
	AlertMonthlySummary:         true,
	AlertSpendingUpdate:         true,
	AlertBillPaid:               true,
	AlertIncomeReceived:         true,
	AlertRefundStatus:           true,
	AlertGoalContribution:       true,
	AlertPlannedExpenseNearing:  true,
	AlertAvailableToSpendLow:    true,
	AlertStalePending:           true,
}

// connectorRaisedAlerts fire end to end without the sweep deciding anything:
// the connector delivers them itself.
var connectorRaisedAlerts = map[AlertType]bool{
	AlertAmazonSignIn:          true,
	AlertCostcoSignIn:          true,
	AlertBillChallenge:         true,
	AlertBillPullFailed:        true,
	AlertMerchantPullFailed:    true,
	AlertEmailConnectionFailed: true,
	AlertBackupFailed:          true,
}

func TestTheEvaluatedSetMatchesWhatTheSweepFeeds(t *testing.T) {
	evaluated := map[AlertType]bool{}
	for _, def := range AlertCatalog {
		if def.Evaluated {
			evaluated[def.Type] = true
		}
	}
	wanted := map[AlertType]bool{}
	for one := range sweepFeedableAlerts {
		wanted[one] = true
	}
	for one := range connectorRaisedAlerts {
		require.False(t, sweepFeedableAlerts[one], "%s is both swept and connector-raised", one)
		wanted[one] = true
	}
	require.Equal(t, wanted, evaluated,
		"the catalog's Evaluated flags and the inputs service.alerts.gather feeds disagree")
}

func TestEveryEvaluatedAlertHasAnEvaluatorAndViceVersa(t *testing.T) {
	for _, def := range AlertCatalog {
		if def.Evaluated {
			require.NotPanics(t, func() { evaluateOne(def.Type, AlertRuleView{}, AlertInputs{}) })
		}
	}

	// The switch in evaluateOne must name nothing the catalog does not carry.
	cataloged := map[AlertType]bool{}
	for _, def := range AlertCatalog {
		cataloged[def.Type] = true
	}
	for _, atype := range []AlertType{
		AlertLargeTransaction, AlertLargeDeposit, AlertBankFee, AlertUncategorized,
		AlertLowBankBalance, AlertHighCardBalance, AlertUpcomingBills,
		AlertWatchlistNearingTarget, AlertProjectedLowBalance, AlertBillsToIncomePct,
		AlertExtraPaycheckMonth, AlertMonthlySummary, AlertSpendingUpdate,
		AlertBillPaid, AlertIncomeReceived, AlertRefundStatus, AlertPlannedExpenseNearing,
		AlertGoalContribution, AlertAvailableToSpendLow,
	} {
		require.True(t, cataloged[atype], "%s has an evaluator but no catalog entry", atype)
	}
}
