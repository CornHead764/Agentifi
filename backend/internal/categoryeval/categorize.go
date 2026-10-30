// Package categoryeval measures category suggestions without changing the ledger.
package categoryeval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/api"
	"github.com/CornHead764/agentifi/backend/internal/buildinfo"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
)

type Options struct {
	Household store.SpaceID
	Holdout   float64
	Cutoff    domain.Date
	WithModel bool
}

type Report struct {
	SchemaVersion   int                      `json:"schema_version"`
	PipelineVersion string                   `json:"pipeline_version"`
	Commit          string                   `json:"commit"`
	WithModel       bool                     `json:"with_model"`
	Model           string                   `json:"model,omitempty"`
	Cutoff          string                   `json:"cutoff"`
	LabelledRows    int                      `json:"labelled_rows"`
	TrainingRows    int                      `json:"training_rows"`
	HistoryRows     int                      `json:"history_rows"`
	HeldOutRows     int                      `json:"held_out_rows"`
	Score           domain.CategoryEvalScore `json:"score"`
	Limitations     []string                 `json:"limitations"`
}

// Run snapshots only the requested household and server metadata into a private
// directory. Each held-out posting is the only post-cutoff row visible to a run.
func Run(ctx context.Context, cfg *config.Config, opts Options) (Report, error) {
	var report Report
	scratch, err := os.MkdirTemp("", "agentifi-eval-*")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(scratch)
	if err := os.Mkdir(filepath.Join(scratch, "households"), 0700); err != nil {
		return report, err
	}
	for _, name := range []string{"server.db", filepath.Join("households", opts.Household.String()+".db")} {
		source := filepath.Join(cfg.DataDir, name)
		if _, err := os.Stat(source); err != nil {
			return report, fmt.Errorf("evaluation requires an existing household: %w", err)
		}
		db, err := sqlitedb.Open(ctx, source, time.Now)
		if err != nil {
			return report, err
		}
		err = db.Snapshot(ctx, filepath.Join(scratch, name))
		closeErr := db.Close()
		if err != nil {
			return report, err
		}
		if closeErr != nil {
			return report, closeErr
		}
	}
	evaluationTime := time.Now()
	st, err := store.OpenSQLite(ctx, scratch, func() time.Time { return evaluationTime })
	if err != nil {
		return report, err
	}
	defer st.Close()
	if opts.WithModel {
		cipher, err := store.NewCipher(cfg.CredentialKey())
		if err != nil {
			return report, err
		}
		st = st.WithCipher(cipher)
	}
	rows, err := st.ListTransactions(ctx, opts.Household, store.TransactionQuery{ExcludePending: true})
	if err != nil {
		return report, err
	}
	accounts, err := st.ListAccounts(ctx, opts.Household, store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return report, err
	}
	categories, err := st.ListCategories(ctx, opts.Household, true)
	if err != nil {
		return report, err
	}
	kinds := map[uuid.UUID]domain.AccountKind{}
	for _, a := range accounts {
		kinds[a.ID] = a.Kind
	}
	cats := map[uuid.UUID]store.Category{}
	categoryNames := map[string]string{}
	for _, c := range categories {
		cats[c.ID] = c
	}
	for _, c := range categories {
		name := c.Name
		seen := map[uuid.UUID]bool{c.ID: true}
		for parent := c.ParentID; parent != uuid.Nil && !seen[parent]; {
			seen[parent] = true
			p, ok := cats[parent]
			if !ok {
				break
			}
			name = p.Name + " / " + name
			parent = p.ParentID
		}
		categoryNames[c.ID.String()] = name
	}
	labels := []domain.CategoryEvalLabel{}
	subjects := map[string]store.Transaction{}
	for _, row := range rows {
		if store.IsTransfer(row, cats) || !domain.ReviewedCategoryStands(row.IsReviewed, kinds[row.AccountID]) {
			continue
		}
		add := func(key string, category uuid.UUID, subject store.Transaction) {
			c, ok := cats[category]
			if !ok || !store.DomainCategory(c).CanBeSuggested() {
				return
			}
			labels = append(labels, domain.CategoryEvalLabel{Key: key, Date: store.DomainTransaction(row).ReportingDate(domain.DateEffective), Category: category.String()})
			subjects[key] = subject
		}
		if len(row.Splits) == 0 {
			add(row.ID.String(), row.CategoryID, row)
		} else {
			for _, split := range row.Splits {
				subject := row
				subject.Amount = split.Amount
				subject.HasAmountPrimary = false
				add(split.ID.String(), split.CategoryID, subject)
			}
		}
	}
	train, test, cutoff, err := domain.SplitCategoryEvaluation(labels, opts.Holdout, opts.Cutoff)
	if err != nil {
		return report, err
	}
	evaluationTime = cutoff.Time()
	clock := func() time.Time { return evaluationTime }
	db, err := st.HouseholdDB(ctx, opts.Household)
	if err != nil {
		return report, err
	}
	// Historical revisions are unavailable: remove instructions changed since
	// the boundary rather than let today's corrections disclose a held-out label.
	for _, query := range []string{
		`DELETE FROM assistant_corrections`,
		`DELETE FROM assistant_conversations`,
		`DELETE FROM merchant_matches`,
		`DELETE FROM transactions WHERE COALESCE(effective_date,date) >= ?1`,
		`UPDATE rules SET is_active = 0 WHERE updated_at >= ?1 OR filter_id IN (SELECT id FROM filters WHERE updated_at >= ?1 UNION SELECT filter_id FROM filter_items WHERE updated_at >= ?1)`,
		`UPDATE assistant_guidance SET is_active = 0 WHERE updated_at >= ?1 OR filter_id IN (SELECT id FROM filters WHERE updated_at >= ?1 UNION SELECT filter_id FROM filter_items WHERE updated_at >= ?1)`,
	} {
		var arg any = cutoff.Time().UnixMilli()
		if query == `DELETE FROM transactions WHERE COALESCE(effective_date,date) >= ?1` {
			arg = cutoff.String()
		}
		if _, err := db.Write().ExecContext(ctx, query, arg); err != nil {
			return report, err
		}
	}
	history, err := st.ListTransactions(ctx, opts.Household, store.TransactionQuery{ExcludePending: true})
	if err != nil {
		return report, err
	}
	members, err := st.ListMemberships(ctx, opts.Household)
	if err != nil {
		return report, err
	}
	var owner uuid.UUID
	for _, m := range members {
		if !m.IsAccepted() || !m.Role.CanWrite() {
			continue
		}
		user, err := st.GetUser(ctx, m.UserID)
		if err == nil && user.IsActive {
			owner = m.UserID
			break
		}
	}
	if owner == uuid.Nil {
		return report, fmt.Errorf("evaluation requires an active household member")
	}
	template := domain.AutomationTemplates[0]
	for _, t := range domain.AutomationTemplates {
		if t.Key == domain.AutomationTemplateCheckCategory {
			template = t
			break
		}
	}
	automation := store.Automation{CreatedBy: owner, Name: template.Name, Trigger: template.Trigger, TriggerConfig: template.TriggerConfig,
		Prompt: template.Prompt, Context: template.Context, Mode: template.Mode, Tools: template.Tools, ConfidenceThreshold: template.ConfidenceThreshold, MaxToolRounds: 8}
	if err := st.CreateAutomation(ctx, opts.Household, &automation); err != nil {
		return report, err
	}
	evaluator := api.NewCategoryEvaluator(&api.Env{DB: st, Cfg: cfg, Now: clock})
	rules := service.NewRules(st)
	predictions := make([]domain.CategoryEvalPrediction, 0, len(test))
	for _, label := range test {
		original := subjects[label.Key]
		evaluationTime = label.Date.Time()
		prepared, err := rules.LoadRules(ctx, opts.Household)
		if err != nil {
			return report, err
		}
		// A new row carries the bank's wording, not the person's cleaned payee,
		// notes, tags, rule, split categories, links or previous check's answer.
		subject := store.Transaction{ID: uuid.New(), AccountID: original.AccountID, Date: original.Date, EffectiveDate: original.EffectiveDate,
			Amount: original.Amount, Currency: original.Currency, StatementName: original.StatementName, Memo: original.Memo, Source: original.Source}
		if err := st.CreateTransaction(ctx, opts.Household, &subject); err != nil {
			return report, err
		}
		prediction := domain.CategoryEvalPrediction{TrueCategory: label.Category, Basis: "abstain"}
		candidates, err := rules.LoadCandidates(ctx, opts.Household, service.CandidateQuery{TransactionIDs: []uuid.UUID{subject.ID}})
		if err != nil {
			return report, err
		}
		changes := service.PlanChanges(prepared, candidates)
		if len(changes) > 0 {
			if _, err := rules.ApplyChanges(ctx, opts.Household, changes); err != nil {
				return report, err
			}
		}
		for _, change := range changes {
			if change.HasCategoryID {
				prediction.SuggestedCategory = change.CategoryID.String()
				prediction.Basis = "rule"
			}
		}
		if prediction.SuggestedCategory == "" {
			run := store.AutomationRun{AutomationID: automation.ID, TransactionID: subject.ID, DryRun: true, Blind: true, FiredBy: "evaluation"}
			queued, err := st.QueueAutomationRun(ctx, opts.Household, &run)
			if err != nil {
				return report, err
			}
			if !queued {
				return report, fmt.Errorf("evaluation run was not queued")
			}
			finished, err := evaluator.EvaluateCategory(ctx, run, opts.WithModel)
			if err != nil {
				return report, err
			}
			if finished.Status == domain.AutomationRunFailed {
				return report, fmt.Errorf("category evaluation failed (details omitted to protect transaction data)")
			}
			actions, err := st.ListAssistantActions(ctx, opts.Household, finished.ConversationID)
			if err != nil {
				return report, err
			}
			for _, action := range actions {
				id, _ := action.Body["category_id"].(string)
				if action.ToolName != "update_transaction" || action.Path != "/transactions/"+subject.ID.String() || id == "" {
					continue
				}
				prediction.SuggestedCategory = id
				prediction.Basis, _ = action.Preview["basis"].(string)
				if prediction.Basis == "" {
					prediction.Basis, _ = action.Body["basis"].(string)
				}
				if prediction.Basis == "" {
					prediction.Basis = finished.DecidedBy
				}
				prediction.Confidence = finished.Confidence
				if confidence, ok := action.StatedConfidence(); ok {
					prediction.Confidence = &confidence
				}
			}
			// Split proposals cannot assign one category to the posting being scored.
		}
		prediction.TrueCategoryName = categoryNames[prediction.TrueCategory]
		prediction.SuggestedCategoryName = categoryNames[prediction.SuggestedCategory]
		predictions = append(predictions, prediction)
		if _, err := db.Write().ExecContext(ctx, `DELETE FROM transactions WHERE id = ?1`, subject.ID); err != nil {
			return report, err
		}
		if _, err := db.Write().ExecContext(ctx, `DELETE FROM assistant_conversations`); err != nil {
			return report, err
		}
	}
	info := buildinfo.Read()
	report = Report{SchemaVersion: 1, PipelineVersion: "categorize-eval-v1", Commit: info.Commit, WithModel: opts.WithModel,
		Cutoff: cutoff.String(), LabelledRows: len(labels), TrainingRows: len(train), HistoryRows: len(history), HeldOutRows: len(test), Score: domain.ScoreCategoryEvaluation(predictions),
		Limitations: []string{"Category vocabulary and account metadata are current, not historical revisions.", "History uses pre-cutoff effective dates and current stored labels; review timestamps are not retained.", "Rules and guidance changed since cutoff are omitted; corrections and merchant attachments are omitted.", "Split postings are evaluated independently using the parent bank wording; split proposals abstain.", "Held-out payees, notes, tags, categories and links are hidden; training is frozen across the holdout."}}
	if opts.WithModel {
		connection, err := st.GetAssistantConnection(ctx, opts.Household)
		if err != nil {
			return Report{}, err
		}
		report.Model = connection.Model
	}
	return report, nil
}

func (r Report) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }
