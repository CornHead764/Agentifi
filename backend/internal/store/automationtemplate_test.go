package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// An automation started from a built-in reads the template's prompt and
// description until the household changes them.

func checkCategoryTemplate(t *testing.T) domain.AutomationTemplate {
	t.Helper()
	template, ok := domain.AutomationTemplateByKey(domain.AutomationTemplateCheckCategory)
	require.True(t, ok)
	return template
}

func storedCheckCategory(t *testing.T, space SpaceID, prompt, description string) uuid.UUID {
	t.Helper()
	one := &Automation{
		CreatedBy:   newUser(t).ID,
		Name:        "Check the category",
		Description: description,
		IsEnabled:   true,
		Trigger:     domain.AutomationTriggerTransaction,
		Prompt:      prompt,
		Mode:        domain.AutomationModePropose,
		TemplateKey: domain.AutomationTemplateCheckCategory,
	}
	require.NoError(t, db(t).CreateAutomation(t.Context(), space, one))
	return one.ID
}

// storedAsWritten sets a row's texts and flags directly, below the store's own
// write, which would mark them from the text.
func storedAsWritten(t *testing.T, id uuid.UUID, prompt, description string, fromTemplate bool) {
	t.Helper()
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE assistant_automations
		    SET prompt = $2, description = $3,
		        prompt_from_template = $4, description_from_template = $4
		  WHERE id = $1`, id, prompt, description, fromTemplate)
	require.NoError(t, err)
}

func TestAnUntouchedCopyReadsItsTemplateAsItStands(t *testing.T) {
	template := checkCategoryTemplate(t)
	space := newSpace(t)
	id := storedCheckCategory(t, space, "  "+strings.ReplaceAll(template.Prompt, "\n", "\n\t")+"\n",
		template.Description)
	storedAsWritten(t, id, "Another wording of the prompt.", "Another description.", true)

	got, err := db(t).GetAutomation(t.Context(), space, id)
	require.NoError(t, err)
	require.True(t, got.PromptFromTemplate)
	require.Equal(t, template.Prompt, got.Prompt)
	require.Equal(t, template.Description, got.Description)
	listed, err := db(t).ListAutomations(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, template.Prompt, listed[0].Prompt)
}

func TestAnEditedCopyKeepsItsOwnWordsUntilTheyAreTheTemplatesAgain(t *testing.T) {
	template := checkCategoryTemplate(t)
	space := newSpace(t)
	id := storedCheckCategory(t, space, template.Prompt, template.Description)
	one, err := db(t).GetAutomation(t.Context(), space, id)
	require.NoError(t, err)
	require.True(t, one.PromptFromTemplate)
	require.True(t, one.DescriptionFromTemplate)

	ours := template.Prompt + "\nThe gym membership is Fitness."
	one.Prompt = ours
	require.NoError(t, db(t).UpdateAutomation(t.Context(), space, &one))
	got, err := db(t).GetAutomation(t.Context(), space, id)
	require.NoError(t, err)
	require.False(t, got.PromptFromTemplate)
	require.True(t, got.DescriptionFromTemplate)
	require.Equal(t, ours, got.Prompt)

	got.Prompt = template.Prompt
	got.Description = "Our own words."
	require.NoError(t, db(t).UpdateAutomation(t.Context(), space, &got))
	again, err := db(t).GetAutomation(t.Context(), space, id)
	require.NoError(t, err)
	require.True(t, again.PromptFromTemplate)
	require.False(t, again.DescriptionFromTemplate)
	require.Equal(t, "Our own words.", again.Description)
}

func TestAnAutomationWrittenFromScratchNeverFollowsATemplate(t *testing.T) {
	template := checkCategoryTemplate(t)
	space := newSpace(t)
	one := &Automation{
		CreatedBy: newUser(t).ID, Name: "Mine", Description: template.Description,
		IsEnabled: true, Trigger: domain.AutomationTriggerManual, Prompt: template.Prompt,
		Mode: domain.AutomationModeObserve,
	}
	require.NoError(t, db(t).CreateAutomation(t.Context(), space, one))
	require.False(t, one.PromptFromTemplate)
	require.False(t, one.DescriptionFromTemplate)
}
