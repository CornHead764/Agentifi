package seed

import (
	"context"
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func seedAssistant(ctx context.Context, h *household) error {
	st, sid := h.st, h.space.ID
	if err := st.SaveAssistantConnection(ctx, sid, &store.AssistantConnection{Name: "Offline seed assistant", BaseURL: "https://assistant.example.invalid/v1", Model: "seed-model", IsEnabled: false, ToolCallStyle: "native"}, "seed-not-a-real-api-key"); err != nil {
		return err
	}
	conv := store.AssistantConversation{UserID: h.users[0].ID, Title: "Review the café spending"}
	if err := st.CreateAssistantConversation(ctx, sid, &conv); err != nil {
		return err
	}
	for _, m := range []store.AssistantMessage{{ConversationID: conv.ID, Role: "user", Content: "Categorize my café purchase."}, {ConversationID: conv.ID, Role: "assistant", Content: "I propose Restaurants & cafés."}} {
		if err := st.AddAssistantMessage(ctx, sid, &m); err != nil {
			return err
		}
	}
	action := store.AssistantAction{ConversationID: conv.ID, ToolName: "update_transaction", Summary: "File café purchase", Method: "PATCH", Path: "/transactions/" + h.txns[18].ID.String(), Body: map[string]any{"category_id": h.categories[2].ID.String()}, Status: "pending"}
	if err := st.CreateAssistantAction(ctx, sid, &action); err != nil {
		return err
	}
	if err := st.CreateAssistantCorrections(ctx, sid, []store.AssistantCorrection{{ActionID: action.ID, TransactionID: h.txns[18].ID, StatementName: h.txns[18].StatementName, Payee: h.txns[18].Payee, Amount: h.txns[18].Amount, HasAmount: true, ToolName: action.ToolName, ProposedCategoryID: h.categories[2].ID, ChosenCategoryID: h.categories[1].ID, CorrectedBy: h.users[0].ID, CreatedAt: h.today}}); err != nil {
		return err
	}
	if err := st.CreateGuidance(ctx, sid, &store.Guidance{Name: "Household café policy", Instruction: "Lunch at the café belongs to restaurants, not groceries.", FilterID: h.filter.ID, IsActive: true, CreatedBy: h.users[0].ID}); err != nil {
		return err
	}
	automation := store.Automation{CreatedBy: h.users[0].ID, Name: "Seed review café", Description: "Offline invented automation", Trigger: "transaction_created", FilterID: h.filter.ID, Prompt: "Review this purchase.", Mode: "suggest", Model: "seed-model", MaxToolRounds: 2, ConfidenceThreshold: 0.85}
	if err := st.CreateAutomation(ctx, sid, &automation); err != nil {
		return err
	}
	batch := store.SuggestionBatch{CreatedBy: h.users[0].ID, RowCount: 1}
	if err := st.CreateSuggestionBatch(ctx, sid, &batch); err != nil {
		return err
	}
	run := store.AutomationRun{AutomationID: automation.ID, TransactionID: h.txns[18].ID, FiredBy: "manual", Subject: "Café review", BatchID: batch.ID}
	if _, err := st.QueueAutomationRun(ctx, sid, &run); err != nil {
		return err
	}
	if err := st.DismissSuggestion(ctx, sid, "seed:café:groceries"); err != nil {
		return err
	}
	if err := st.SaveCashFlowForecast(ctx, sid, &store.CashFlowForecast{AutomationID: automation.ID, RunID: run.ID, AccountID: h.accounts[0].ID, Method: "model", Model: "seed-model", GeneratedAt: h.today, GeneratedOn: h.date(0, 0), Forecast: domain.CashFlowForecast{Months: []domain.CashFlowMonth{{Month: domain.MonthOf(h.date(1, 0)), In: money("4200"), Out: money("3100")}}, Narrative: "Invented household forecast"}}); err != nil {
		return err
	}
	alert := store.AlertRule{AlertType: domain.AlertLowBankBalance, AccountID: h.accounts[0].ID, IsEnabled: true, ChannelInApp: true, HasThresholdAmount: true, ThresholdAmount: money("250")}
	if err := st.SaveAlertRule(ctx, sid, h.users[0].ID, &alert); err != nil {
		return err
	}
	if _, err := st.RecordNotification(ctx, sid, h.users[0].ID, &store.Notification{AlertType: alert.AlertType, Title: "Seed low balance warning", Body: "An invented example notification", DedupeKey: "seed-low-balance-" + sid.String()}); err != nil {
		return err
	}
	return st.SavePushSubscription(ctx, sid, h.users[0].ID, &store.PushSubscription{Endpoint: fmt.Sprintf("https://push.example.invalid/%s", sid.String()), P256dh: "invented-public-key", Auth: "invented-auth", UserAgent: "Seed browser"})
}
