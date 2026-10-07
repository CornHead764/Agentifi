package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func synced() domain.AutomationTransactionFacts {
	return domain.AutomationTransactionFacts{
		Source:  domain.SourceSync,
		Posting: domain.Posting{Txn: domain.Transaction{AccountID: "acct-1"}},
	}
}

func scope(items ...domain.FilterItem) *domain.Filter {
	return &domain.Filter{Items: items}
}

func inAccounts(ids ...string) domain.FilterItem {
	return domain.FilterItem{Field: domain.FieldAccount, Operator: domain.OpIn, Values: ids}
}

func state(field domain.FilterField, value bool) domain.FilterItem {
	return domain.FilterItem{Field: field, Operator: domain.OpIsTrue, State: value, HasState: true}
}

func TestOnlyBankAndFileRowsFireAnAutomationAsTheyArrive(t *testing.T) {
	config := domain.AutomationTriggerConfig{}
	for source, fires := range map[domain.Source]bool{
		domain.SourceSync:              true,
		domain.SourceFileImport:        true,
		domain.SourceManual:            false,
		domain.SourceSimplifiImport:    false,
		domain.SourceEmail:             false,
		domain.SourceOpeningBalance:    false,
		domain.SourceBalanceAdjustment: false,
	} {
		facts := synced()
		facts.Source = source
		require.Equal(t, fires,
			domain.AutomationTriggers(config, nil, facts, domain.AutomationFiredByTransaction),
			"source %s", source)
	}
}

func TestALaterLookDoesNotAskWhereTheRowCameFrom(t *testing.T) {
	// An imported ledger is every row of a source the arrival rule refuses.
	config := domain.AutomationTriggerConfig{}
	for _, source := range []domain.Source{
		domain.SourceSimplifiImport, domain.SourceManual, domain.SourceSync,
	} {
		facts := synced()
		facts.Source = source
		for _, firedBy := range []string{
			domain.AutomationFiredByManual, domain.AutomationFiredByMatch,
		} {
			require.True(t, domain.AutomationTriggers(config, nil, facts, firedBy),
				"source %s fired by %s", source, firedBy)
		}
	}
}

func TestALaterLookStillHonoursTheHouseholdsOwnNarrowing(t *testing.T) {
	facts := synced()
	facts.Source = domain.SourceSimplifiImport
	facts.IsTransfer = true
	require.False(t, domain.AutomationTriggers(
		domain.AutomationTriggerConfig{SkipTransfers: true}, nil, facts,
		domain.AutomationFiredByManual))

	elsewhere := synced()
	elsewhere.Source = domain.SourceSimplifiImport
	require.False(t, domain.AutomationTriggers(
		domain.AutomationTriggerConfig{}, scope(inAccounts("acct-2")), elsewhere,
		domain.AutomationFiredByMatch))

	deleted := synced()
	deleted.Source = domain.SourceSimplifiImport
	deleted.IsDeleted = true
	require.False(t, domain.AutomationTriggers(domain.AutomationTriggerConfig{}, nil, deleted,
		domain.AutomationFiredByManual), "a deleted row is nobody's to check")
}

func TestTheTriggerFilterNarrowsLikeAnyOtherFilter(t *testing.T) {
	none := domain.AutomationTriggerConfig{}
	arrived := domain.AutomationFiredByTransaction
	facts := synced()
	require.True(t, domain.AutomationTriggers(none, nil, facts, arrived), "no filter is every row")
	require.False(t, domain.AutomationTriggers(none, scope(), facts, arrived),
		"a scope that lost its items widened to every row")

	require.False(t, domain.AutomationTriggers(none, scope(inAccounts("acct-2")), facts, arrived),
		"another account's row fired an account-limited automation")
	require.True(t, domain.AutomationTriggers(none, scope(inAccounts("acct-2", "acct-1")), facts, arrived))

	negated := inAccounts("acct-1")
	negated.Negated = true
	require.False(t, domain.AutomationTriggers(none, scope(negated), facts, arrived),
		"an excluded account still fired")

	categorized := synced()
	categorized.Posting.Txn.CategoryID = "cat-1"
	uncategorizedOnly := scope(state(domain.FieldIsUncategorized, true))
	require.False(t, domain.AutomationTriggers(none, uncategorizedOnly, categorized, arrived))
	require.True(t, domain.AutomationTriggers(none, uncategorizedOnly, facts, arrived))

	pending := synced()
	pending.Posting.Txn.IsPending = true
	postedOnly := scope(state(domain.FieldIsPending, false))
	require.False(t, domain.AutomationTriggers(none, postedOnly, pending, arrived))
	require.True(t, domain.AutomationTriggers(none, postedOnly, facts, arrived))

	transfer := synced()
	transfer.IsTransfer = true
	require.False(t, domain.AutomationTriggers(
		domain.AutomationTriggerConfig{SkipTransfers: true}, nil, transfer, arrived))
	require.True(t, domain.AutomationTriggers(none, nil, transfer, arrived),
		"a transfer leg is only skipped when asked")

	deleted := synced()
	deleted.IsDeleted = true
	require.False(t, domain.AutomationTriggers(none, nil, deleted, arrived),
		"a deleted row fired an automation")
}

func TestAnObservingAutomationIsOfferedNoChangeTools(t *testing.T) {
	for _, tool := range domain.AutomationToolsFor(domain.AutomationModeObserve, nil) {
		require.False(t, tool.Writes, "%q offered to an observing automation", tool.Name)
	}
	both := domain.AutomationToolsFor(domain.AutomationModePropose, nil)
	attended := 0
	for _, tool := range domain.AssistantTools {
		if tool.Attended {
			attended++
		}
	}
	require.Len(t, both, len(domain.AssistantTools)+len(domain.AssistantWriteTools)-attended)
}

func TestAnAutomationIsNeverOfferedTheMailTools(t *testing.T) {
	// A mail is whatever its sender wanted a model to see, and in a run nobody
	// is there to read it alongside.
	for _, mode := range []string{
		domain.AutomationModeObserve, domain.AutomationModePropose, domain.AutomationModeApply,
	} {
		for _, tool := range domain.AutomationToolsFor(mode, []string{"list_mail", "read_mail"}) {
			require.Fail(t, "a mail tool was offered to an automation", "%s in %s", tool.Name, mode)
		}
	}
	for _, name := range []string{"list_mail", "read_mail"} {
		tool, ok := domain.AssistantToolByName(name)
		require.True(t, ok)
		require.True(t, tool.Attended, "%s is offered to automations", name)
		require.False(t, tool.Writes)
	}
	read, _ := domain.AssistantToolByName("read_mail")
	require.True(t, read.Unrecorded, "a mail's text would be kept in the conversation's record")
}

func TestChosenToolsNarrowTheCatalogueAndUnknownNamesDrop(t *testing.T) {
	tools := domain.AutomationToolsFor(domain.AutomationModePropose,
		[]string{"list_categories", "update_transaction", "renamed_away"})
	names := make([]string, 0, len(tools))
	for _, one := range tools {
		names = append(names, one.Name)
	}
	require.ElementsMatch(t, []string{"list_categories", "update_transaction"}, names)

	// The mode outranks the list.
	observe := domain.AutomationToolsFor(domain.AutomationModeObserve,
		[]string{"list_categories", "update_transaction"})
	require.Len(t, observe, 1)
	require.Equal(t, "list_categories", observe[0].Name)
}

func TestTheRunPromptCarriesTheModeAndTheFraming(t *testing.T) {
	today := domain.DateOf(time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC))

	propose := domain.AutomationPrompt(domain.AutomationModePropose, "Check the category.", today, "EUR")
	require.Contains(t, propose, "never say you have changed something",
		"a proposing run has to be told nothing it does takes effect")
	require.Contains(t, propose, "not in a conversation")
	require.Contains(t, propose, "never end with a question")
	require.Contains(t, propose, "## Your task\n\nCheck the category.")
	require.Contains(t, propose, "Today is 2026-09-04.")
	require.Contains(t, propose, "State every amount in EUR with its € sign")

	apply := domain.AutomationPrompt(domain.AutomationModeApply, "x", today, "USD")
	require.Contains(t, apply, "takes effect immediately")

	observe := domain.AutomationPrompt(domain.AutomationModeObserve, "x", today, "USD")
	require.Contains(t, observe, "You can only read")
}

func TestTheCategoryCheckFilesARefundUnderWhatWasBought(t *testing.T) {
	template, ok := domain.AutomationTemplateByKey(domain.AutomationTemplateCheckCategory)
	require.True(t, ok)
	prompt := template.Prompt
	direction := prompt[strings.Index(prompt, "1. Direction."):strings.Index(prompt, "2. The row itself.")]

	require.Contains(t, direction, "never propose an income\n   category for money out")
	require.Contains(t, direction, "A refund, return or credit from a merchant is not income")
	require.Contains(t, direction, "expense category of what was bought")
	require.Contains(t, direction, "On a credit card or a gift card")
	require.Contains(t, direction, "A dividend is Dividend Income and\n   interest is Interest Earned")
	require.NotContains(t, direction, "Never propose a category that runs the wrong way",
		"a blanket direction rule makes the model refuse every refund on a card")
	require.NotContains(t, direction, "Money in belongs in an income category")
}

func TestAnAnswerNamesACategoryOnlyByAnIdOfThisSpace(t *testing.T) {
	water := "5e1f0c2a-9b3d-4e7a-8c6f-1a2b3c4d5e6f"
	food := "7a8b9c0d-1e2f-4a3b-9c4d-5e6f7a8b9c0d"
	row := "c0ffee00-1111-4222-8333-444455556666"
	known := map[string]bool{water: true, food: true}

	for answer, want := range map[string]string{
		"Category: Water (id " + water + ")\nRow " + row + ".\nConfidence: 0.92": water,
		"Category: Water (id " + strings.ToUpper(water) + ")":                    water,
		"Water (" + water + "), as before: " + water:                             water,
		"Water (" + water + ") or Food (" + food + ")":                           "",
		"Row " + row + " is a water bill.":                                       "",
		"Could not guess: maybe Water (" + water + ").":                          "",
		"No id here.": "",
	} {
		got, ok := domain.StatedCategory(answer, known)
		require.Equal(t, want, got, answer)
		require.Equal(t, want != "", ok, answer)
	}
}

func TestEveryTemplateIsSaveable(t *testing.T) {
	require.NotEmpty(t, domain.AutomationTemplates)
	seen := map[string]bool{}
	for _, one := range domain.AutomationTemplates {
		require.False(t, seen[one.Key], "duplicate template %q", one.Key)
		seen[one.Key] = true
		require.NotEmpty(t, one.Name)
		require.NotEmpty(t, strings.TrimSpace(one.Prompt))
		require.True(t, domain.AutomationTriggerIsValid(one.Trigger), "%q: trigger", one.Key)
		require.True(t, domain.AutomationModeIsValid(one.Mode), "%q: mode", one.Key)
		if one.Trigger == domain.AutomationTriggerDaily {
			require.Regexp(t, `^\d\d:\d\d$`, one.TriggerConfig.At, "%q: time", one.Key)
		}
		for _, name := range one.Tools {
			tool, known := domain.AssistantToolByName(name)
			require.True(t, known, "%q names a tool that does not exist: %q", one.Key, name)
			if one.Mode == domain.AutomationModeObserve {
				require.False(t, tool.Writes, "%q observes and names a change tool", one.Key)
			}
		}
		_, found := domain.AutomationTemplateByKey(one.Key)
		require.True(t, found)
	}
}

func TestASubjectReadsAsWordsNotIds(t *testing.T) {
	require.Equal(t, "COSTCO WHSE · -140.00 · 2026-09-03",
		domain.AutomationSubject("COSTCO WHSE", "-140.00", "2026-09-03"))
	require.Equal(t, "-140.00 · 2026-09-03", domain.AutomationSubject("  ", "-140.00", "2026-09-03"))
}

func TestAForecastAnswerIsRecognizedByTemplateOrByWhatTheRunWasHanded(t *testing.T) {
	require.True(t, domain.AutomationAnswersWithForecast(
		domain.AutomationTemplateCashFlowForecast, domain.AutomationContext{}))
	require.True(t, domain.AutomationAnswersWithForecast("",
		domain.AutomationContext{CashFlowHistory: true}))
	require.False(t, domain.AutomationAnswersWithForecast(
		domain.AutomationTemplateCheckCategory, domain.AutomationContext{Transaction: true}))
}

func TestTheStatedConfidenceIsTheAnswersLastConfidenceLine(t *testing.T) {
	cases := []struct {
		answer string
		want   float64
		found  bool
	}{
		{"Proposed Groceries.\nConfidence: 0.62", 0.62, true},
		{"Proposed Groceries.\n**Confidence:** 62%", 0.62, true},
		{"Proposed Groceries.\nconfidence: 0.7.\n", 0.7, true},
		{"Confidence: 0.3\nOn reflection, Dining.\nConfidence: 0.8", 0.8, true},
		{"Proposed Groceries.\nConfidence: 1.4", 0, false},
		{"Proposed Groceries.\nConfidence: NaN", 0, false},
		{"Proposed Groceries.\nConfidence: high", 0, false},
		{"Could not guess: a bare reference number.", 0, false},
	}
	for _, one := range cases {
		got, found := domain.StatedConfidence(one.answer)
		require.Equal(t, one.found, found, one.answer)
		require.InDelta(t, one.want, got, 0.0001, one.answer)
	}
}

func TestTheCategoryCheckAsksForAGuessAndAConfidence(t *testing.T) {
	template, ok := domain.AutomationTemplateByKey(domain.AutomationTemplateCheckCategory)
	require.True(t, ok)
	for _, phrase := range []string{
		"it is not needed for one",
		"A later rule outranks an earlier one.",
		"weak evidence and never on their own a reason to\n   propose nothing",
		"mostly transfer legs mean the row is a transfer",
		"Could not guess:",
		"- 0.9 or more:",
		"- Under 0.4:",
		"Propose your best guess at whatever confidence it has",
		`"Confidence: 0.62"`,
	} {
		require.Contains(t, template.Prompt, phrase)
	}
	order := []string{"1. Direction.", "2. The row itself.", "3. History.", "4. If the row",
		"5. If the context", "6. If \"Corrections the household has made\"",
		"7. If \"The household's guidance\""}
	last := -1
	for _, heading := range order {
		at := strings.Index(template.Prompt, heading)
		require.Greater(t, at, last, heading)
		last = at
	}
	stated, found := domain.StatedConfidence("Proposed it.\nConfidence: 0.62")
	require.True(t, found, "the line the prompt asks for is the line the run reads")
	require.InDelta(t, 0.62, stated, 0.0001)
	require.Equal(t, 0.85, template.ConfidenceThreshold)
}

func TestAutomationTextIsTheSameHoweverItIsWrapped(t *testing.T) {
	require.True(t, domain.SameAutomationText("Find the rows.\n  Propose one.", " Find the rows. Propose one.\n"))
	require.True(t, domain.SameAutomationText("one\r\n\ttwo", "one two"))
	require.False(t, domain.SameAutomationText("Find the rows.", "Find the row."))
	require.False(t, domain.SameAutomationText("onetwo", "one two"))
}
