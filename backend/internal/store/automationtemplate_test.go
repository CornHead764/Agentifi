package store_test

import (
	"strings"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteAutomationTemplates(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	space := storetest.NewHousehold(t, st)
	template, ok := domain.AutomationTemplateByKey(domain.AutomationTemplateCheckCategory)
	require.True(t, ok)
	one := store.Automation{CreatedBy: uuid.New(), Name: "Check the category", Description: template.Description,
		IsEnabled: true, Trigger: domain.AutomationTriggerTransaction,
		Prompt: "  " + strings.ReplaceAll(template.Prompt, "\n", "\n\t") + "\n", Mode: domain.AutomationModePropose,
		TemplateKey: template.Key}
	require.NoError(t, st.CreateAutomation(ctx, space, &one))
	require.True(t, one.PromptFromTemplate)
	db, err := st.HouseholdDB(ctx, space)
	require.NoError(t, err)
	_, err = db.Write().ExecContext(ctx, `UPDATE assistant_automations SET prompt = ?, description = ? WHERE id = ?`, "Old prompt", "Old description", one.ID)
	require.NoError(t, err)
	got, err := st.GetAutomation(ctx, space, one.ID)
	require.NoError(t, err)
	require.Equal(t, template.Prompt, got.Prompt)
	require.Equal(t, template.Description, got.Description)
	listed, err := st.ListAutomations(ctx, space)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, template.Prompt, listed[0].Prompt)
	got.Prompt = template.Prompt + "\nThe gym membership is Fitness."
	require.NoError(t, st.UpdateAutomation(ctx, space, &got))
	edited, err := st.GetAutomation(ctx, space, one.ID)
	require.NoError(t, err)
	require.False(t, edited.PromptFromTemplate)
	require.True(t, edited.DescriptionFromTemplate)
	require.Equal(t, got.Prompt, edited.Prompt)
	edited.Prompt, edited.Description = template.Prompt, "Our own words."
	require.NoError(t, st.UpdateAutomation(ctx, space, &edited))
	again, err := st.GetAutomation(ctx, space, one.ID)
	require.NoError(t, err)
	require.True(t, again.PromptFromTemplate)
	require.False(t, again.DescriptionFromTemplate)
	require.Equal(t, "Our own words.", again.Description)
	scratch := store.Automation{CreatedBy: uuid.New(), Name: "Mine", Description: template.Description,
		IsEnabled: true, Trigger: domain.AutomationTriggerManual, Prompt: template.Prompt, Mode: domain.AutomationModeObserve}
	require.NoError(t, st.CreateAutomation(ctx, space, &scratch))
	require.False(t, scratch.PromptFromTemplate)
	require.False(t, scratch.DescriptionFromTemplate)
}
