package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestTheCatalogIsTheThirtyAlertsTheProductShips(t *testing.T) {
	// Counted: an alert dropped in an edit can never be switched on again.
	// Twenty-two come from Simplifi; stale pending, six connector
	// alerts and the backup alert are ours.
	require.Len(t, domain.AlertCatalog, 30)

	seen := map[domain.AlertType]bool{}
	for _, one := range domain.AlertCatalog {
		require.False(t, seen[one.Type], "duplicate alert %q", one.Type)
		seen[one.Type] = true
		require.NotEmpty(t, one.Label, "%q has no label", one.Type)
		require.NotEmpty(t, one.Trigger, "%q has no trigger", one.Type)
		require.NotEmpty(t, one.Group, "%q has no group", one.Type)
	}
}

func TestAThresholdedAlertCarriesADefaultToThreshold(t *testing.T) {
	// A threshold kind with no default is a control the settings page draws
	// empty, and an empty threshold compares against zero — which for "large
	// transaction" means every transaction.
	for _, one := range domain.AlertCatalog {
		switch one.Threshold {
		case domain.ThresholdAmount:
			require.True(t, one.DefaultAmount.IsPositive(), "%q has no default amount", one.Type)
		case domain.ThresholdCount:
			require.Positive(t, one.DefaultCount, "%q has no default count", one.Type)
		case domain.ThresholdPercent:
			require.Positive(t, one.DefaultPercent, "%q has no default percent", one.Type)
		case domain.ThresholdNone:
			require.True(t, one.DefaultAmount.IsZero(), "%q thresholds on nothing", one.Type)
			require.Zero(t, one.DefaultCount, "%q thresholds on nothing", one.Type)
			require.Zero(t, one.DefaultPercent, "%q thresholds on nothing", one.Type)
		default:
			t.Fatalf("%q has an unknown threshold kind %q", one.Type, one.Threshold)
		}
	}
}

func TestAnAlertNeverDefaultsToAChannelItCannotUse(t *testing.T) {
	// A switched-on control that silently does nothing is worse than none.
	for _, one := range domain.AlertCatalog {
		require.NotEmpty(t, one.Channels, "%q can be reached by nothing", one.Type)
		for _, channel := range one.DefaultOn {
			require.True(t, one.Allows(channel),
				"%q defaults to %q, which it cannot use", one.Type, channel)
		}
	}
}

func TestTheCreditScoreAlertsAreEmailOnly(t *testing.T) {
	// They come from a bureau by email. Offering push or in-app would draw two
	// toggles that can never deliver anything.
	for _, one := range []domain.AlertType{
		domain.AlertCreditScoreNewAlert,
		domain.AlertCreditScoreNewReport,
	} {
		definition, ok := domain.AlertByType(one)
		require.True(t, ok)
		require.Equal(t, []domain.AlertChannel{domain.ChannelEmail}, definition.Channels)
		require.False(t, definition.Allows(domain.ChannelPush))
		require.False(t, definition.Allows(domain.ChannelInApp))
	}
}

// pushesByDefault is the closed list of alerts that arrive on a phone before
// anybody switches push on: each reports a stopped connector or a stopped
// backup, and a parked code expires twenty minutes later. Adding one is a
// deliberate edit here.
var pushesByDefault = map[domain.AlertType]bool{
	domain.AlertAmazonSignIn:          true,
	domain.AlertCostcoSignIn:          true,
	domain.AlertBillChallenge:         true,
	domain.AlertBillPullFailed:        true,
	domain.AlertMerchantPullFailed:    true,
	domain.AlertEmailConnectionFailed: true,
	domain.AlertBackupFailed:          true,
}

func TestEmailAndPushStayOffUntilSomebodyAsks(t *testing.T) {
	// Email and push reach somebody who did not ask for it.
	for _, one := range domain.AlertCatalog {
		require.False(t, one.DefaultsOn(domain.ChannelEmail), "%q emails by default", one.Type)
		if pushesByDefault[one.Type] {
			require.True(t, one.DefaultsOn(domain.ChannelPush),
				"%q is listed as pushing by default and does not", one.Type)
			continue
		}
		require.False(t, one.DefaultsOn(domain.ChannelPush), "%q pushes by default", one.Type)
	}
}

func TestAnUnknownAlertIsNotInTheCatalog(t *testing.T) {
	_, ok := domain.AlertByType("no_such_alert")
	require.False(t, ok)

	for _, one := range domain.AlertCatalog {
		found, ok := domain.AlertByType(one.Type)
		require.True(t, ok, "%q is unreachable by type", one.Type)
		require.Equal(t, one.Label, found.Label)
	}
}

func TestOnlyNewsThatAsksNothingIsOffByDefault(t *testing.T) {
	// These report what is already on screen and would bury the rest.
	// Everything else, above all whatever pushes by default, starts on.
	off := map[domain.AlertType]bool{
		domain.AlertLargeDeposit:   true,
		domain.AlertBillPaid:       true,
		domain.AlertIncomeReceived: true,
		domain.AlertSpendingUpdate: true,
	}
	for _, one := range domain.AlertCatalog {
		require.Equal(t, !off[one.Type], one.DefaultEnabled(), "%q", one.Type)
		if pushesByDefault[one.Type] {
			require.True(t, one.DefaultEnabled(), "%q pushes by default and is off", one.Type)
		}
	}
}
