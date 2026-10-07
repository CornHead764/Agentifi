package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestEveryToolIsNamedDescribedAndValidSchema(t *testing.T) {
	require.NotEmpty(t, domain.AssistantTools)
	require.NotEmpty(t, domain.AssistantWriteTools)
	seen := map[string]bool{}
	for _, one := range domain.AssistantToolsFor(true) {
		require.False(t, seen[one.Name], "duplicate tool %q", one.Name)
		seen[one.Name] = true
		require.NotEmpty(t, one.Description, "%q has no description", one.Name)
		require.Equal(t, "object", one.Parameters["type"], "%q is not an object schema", one.Name)

		// It goes to a provider as JSON; anything unencodable fails at runtime.
		_, err := json.Marshal(one.Parameters)
		require.NoError(t, err, "%q has parameters that will not encode", one.Name)
	}
}

func TestNoReadToolCanChangeAnything(t *testing.T) {
	// The read catalogue is on by default, so it must stay questions only.
	for _, one := range domain.AssistantTools {
		require.False(t, one.Writes, "%q is in the read catalogue and marked as writing", one.Name)
		for _, forbidden := range []string{
			"create", "update", "delete", "pay", "transfer", "move", "set_", "add_", "remove",
		} {
			require.NotContains(t, one.Name, forbidden,
				"%q looks like it changes something; put it in AssistantWriteTools", one.Name)
		}
	}
}

func TestEveryWriteToolIsMarkedAndAsksForASummary(t *testing.T) {
	// Writes is what the gate reads. The summary is what the person approving
	// reads; a raw method and path is not a decision anybody can make.
	for _, one := range domain.AssistantWriteTools {
		require.True(t, one.Writes, "%q proposes a change and is not marked as writing", one.Name)

		properties, _ := one.Parameters["properties"].(map[string]any)
		require.Contains(t, properties, "summary", "%q has no summary argument", one.Name)
		required, _ := one.Parameters["required"].([]string)
		require.Contains(t, required, "summary", "%q does not require a summary", one.Name)
	}
}

func TestWriteToolsAreOnlyOfferedWhenChangesAreOn(t *testing.T) {
	read := domain.AssistantToolsFor(false)
	require.Equal(t, len(domain.AssistantTools), len(read))
	for _, one := range read {
		require.False(t, one.Writes, "%q is offered to a space with changes off", one.Name)
	}

	both := domain.AssistantToolsFor(true)
	require.Len(t, both, len(domain.AssistantTools)+len(domain.AssistantWriteTools))
}

func TestAToolNobodyWroteIsNotInTheCatalogue(t *testing.T) {
	_, ok := domain.AssistantToolByName("drop_everything")
	require.False(t, ok)

	for _, one := range domain.AssistantTools {
		found, ok := domain.AssistantToolByName(one.Name)
		require.True(t, ok, "%q is unreachable by name", one.Name)
		require.Equal(t, one.Description, found.Description)
	}
}

func TestADateRangeToolSaysWhichArgumentsItNeeds(t *testing.T) {
	// A model that guesses at a date range answers about the wrong months.
	for _, name := range []string{
		"search_transactions", "spending_by_category", "upcoming_bills",
		"income_and_expense", "cash_flow",
	} {
		tool, ok := domain.AssistantToolByName(name)
		require.True(t, ok)
		required, _ := tool.Parameters["required"].([]string)
		require.Contains(t, required, "from", "%q does not require a start", name)
		require.Contains(t, required, "to", "%q does not require an end", name)
	}
}

func TestTheSystemPromptForbidsInventedFiguresAndAdvice(t *testing.T) {
	// The two failures that matter most: a plausible number the model made up,
	// and telling somebody what to do with their retirement account.
	for _, prompt := range []string{
		domain.AssistantPromptFor(false, false),
		domain.AssistantPromptFor(true, false),
		domain.AssistantPromptFor(true, true),
	} {
		require.Contains(t, prompt, "Never state a figure you have not read from a tool")
		require.Contains(t, prompt, "not a licensed financial adviser")
		require.True(t, strings.Contains(prompt, "negative for money leaving"),
			"the sign convention has to be stated or every answer inverts it")
	}
}

func TestThePromptSaysWhatTheAssistantCanDo(t *testing.T) {
	require.Contains(t, domain.AssistantPromptFor(false, false), "You can only read")

	// With changes on, the sentence that matters is the one about not having
	// made them.
	writing := domain.AssistantPromptFor(true, false)
	require.NotContains(t, writing, "You can only read")
	require.Contains(t, writing, "only propose them")
	require.Contains(t, writing, "never say you have changed something")

	// With automatic application on, the instruction inverts completely and
	// gains "stop when a change is refused".
	auto := domain.AssistantPromptFor(true, true)
	require.NotContains(t, auto, "only propose them")
	require.NotContains(t, auto, "never say you have changed something")
	require.Contains(t, auto, "takes effect immediately")
	require.Contains(t, auto, "in the past tense")
	require.Contains(t, auto, "say so and\n  stop")
}

func TestApplyingWithoutAskingMeansNothingWithChangesOff(t *testing.T) {
	require.Equal(t,
		domain.AssistantPromptFor(false, false), domain.AssistantPromptFor(false, true))
}

func TestTheFactsNameTheHouseholdsCurrencyAndItsSign(t *testing.T) {
	today := domain.NewDate(2026, time.March, 9)

	pounds := domain.AssistantFacts(today, "GBP")
	require.Contains(t, pounds, "Today is 2026-03-09.")
	require.Contains(t, pounds, "This household's currency is GBP.")
	require.Contains(t, pounds, "Every amount a tool returns is in GBP")
	require.Contains(t, pounds, "State every amount in GBP with its £ sign")

	require.Contains(t, domain.AssistantFacts(today, "usd"), "State every amount in USD with its $ sign")
	// A code with no sign of its own is stated by its code alone.
	require.Contains(t, domain.AssistantFacts(today, "CHF"), "State every amount in CHF, never")
	// A space always has a currency; an empty one reads as the store's default.
	require.Contains(t, domain.AssistantFacts(today, ""), "This household's currency is USD.")
}
