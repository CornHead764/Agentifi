package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/categoryeval"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type evalCountingTransport struct {
	base  http.RoundTripper
	calls *atomic.Int32
}

func (r evalCountingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return r.base.RoundTrip(request)
}

func TestEvalCategorizeModelOptIn(t *testing.T) {
	var networkCalls atomic.Int32
	transport := http.DefaultTransport
	http.DefaultTransport = evalCountingTransport{base: transport, calls: &networkCalls}
	t.Cleanup(func() { http.DefaultTransport = transport })
	ctx := t.Context()
	dir := t.TempDir()
	st, err := store.OpenSQLite(ctx, dir, time.Now)
	require.NoError(t, err)
	defer st.Close()
	cipher, err := store.NewCipher("evaluation-test-key")
	require.NoError(t, err)
	st = st.WithCipher(cipher)
	opts := seed.DefaultOptions()
	opts.Households = 1
	opts.Transactions = 48
	opts.Today = time.Date(2025, time.June, 30, 0, 0, 0, 0, time.UTC)
	opts.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(ctx, st, opts))
	spaces, err := st.ListSpaces(ctx)
	require.NoError(t, err)
	space := spaces[0].ID
	accounts, err := st.ListAccounts(ctx, space, store.AccountQuery{})
	require.NoError(t, err)
	var account uuid.UUID
	for _, a := range accounts {
		if a.Kind == domain.KindCash {
			account = a.ID
			break
		}
	}
	require.NotEqual(t, uuid.Nil, account)
	categories, err := st.ListCategories(ctx, space, false)
	require.NoError(t, err)
	var expense []uuid.UUID
	for _, c := range categories {
		if c.Kind == domain.CategoryExpense && store.DomainCategory(c).CanBeSuggested() {
			expense = append(expense, c.ID)
		}
	}
	require.GreaterOrEqual(t, len(expense), 2)
	date := func(s string) domain.Date { d, e := domain.ParseDate(s); require.NoError(t, e); return d }
	create := func(on, wording string, category uuid.UUID) store.Transaction {
		row := store.Transaction{AccountID: account, Date: date(on), Amount: domain.MustFromString("-20.00"), Currency: "USD", StatementName: wording,
			Payee: "PRIVATE CLEANED PAYEE", Notes: "PRIVATE CATEGORY NOTE", CategoryID: category, Source: domain.SourceManual, IsReviewed: true}
		require.NoError(t, st.CreateTransaction(ctx, space, &row))
		return row
	}
	for i := 1; i <= 6; i++ {
		create(fmt.Sprintf("2025-07-%02d", i), "Invented Redwood Market", expense[0])
	}
	create("2026-01-02", "Invented Redwood Market", expense[0])
	parent := store.Transaction{AccountID: account, Date: date("2026-01-03"), Amount: domain.MustFromString("-40.00"), Currency: "USD",
		StatementName: "Invented Redwood Market", Source: domain.SourceManual, IsReviewed: true,
		Splits: []store.Split{{Amount: domain.MustFromString("-20.00"), CategoryID: expense[0]}, {Amount: domain.MustFromString("-20.00"), CategoryID: expense[1]}}}
	require.NoError(t, st.CreateTransaction(ctx, space, &parent))
	// A future row with the same wording would decide this held-out row if leaked.
	effective := create("2025-12-30", "Imaginary Quillfeather Shop", expense[1])
	effective.EffectiveDate = date("2026-01-04")
	require.NoError(t, st.UpdateTransaction(ctx, space, &effective))
	for i := 5; i <= 10; i++ {
		row := create(fmt.Sprintf("2026-01-%02d", i), "Imaginary Quillfeather Shop", expense[1])
		row.IsReviewed = false
		require.NoError(t, st.UpdateTransaction(ctx, space, &row))
	}
	create("2026-01-04", "Imaginary Rule Shop", expense[1])
	filter := store.Filter{Name: "Invented rule filter", Scope: "rule", Items: []store.FilterItem{{Field: string(domain.FieldStatementName), Operator: string(domain.OpContains), ValueTexts: []string{"Imaginary Rule Shop"}}, {Field: "date", Operator: "between", DatePreset: "this-month"}}}
	require.NoError(t, st.CreateFilter(ctx, space, &filter))
	rule := store.Rule{Name: "Invented category rule", FilterID: filter.ID, IsActive: true, SetCategoryID: expense[1]}
	require.NoError(t, st.CreateRule(ctx, space, &rule))
	tag := store.Tag{Name: "Automated rule tag"}
	require.NoError(t, st.CreateTag(ctx, space, &tag))
	metadataFilter := store.Filter{Name: "Metadata rule filter", Scope: "rule", Items: []store.FilterItem{{Field: "statement_name", Operator: "contains", ValueTexts: []string{"Imaginary Quillfeather Shop"}}}}
	require.NoError(t, st.CreateFilter(ctx, space, &metadataFilter))
	metadataRule := store.Rule{Name: "Rule-generated context", FilterID: metadataFilter.ID, IsActive: true, SetNotes: "AUTOMATED RULE NOTE", AddTagIDs: []uuid.UUID{tag.ID}}
	require.NoError(t, st.CreateRule(ctx, space, &metadataRule))
	guidanceFilter := store.Filter{Name: "Rule tag guidance filter", Scope: "guidance", Items: []store.FilterItem{{Field: "tag", Operator: "any", ValueIDs: []uuid.UUID{tag.ID}}}}
	require.NoError(t, st.CreateFilter(ctx, space, &guidanceFilter))
	guidance := store.Guidance{Name: "Rule tag guidance", Instruction: "RULE TAG GUIDANCE", FilterID: guidanceFilter.ID, IsActive: true}
	require.NoError(t, st.CreateGuidance(ctx, space, &guidance))
	db, err := st.HouseholdDB(ctx, space)
	require.NoError(t, err)
	for _, table := range []string{"rules", "filters", "filter_items", "assistant_guidance"} {
		_, err = db.Write().ExecContext(ctx, `UPDATE `+table+` SET updated_at = ?1`, date("2025-07-01").Time().UnixMilli())
		require.NoError(t, err)
	}
	var calls atomic.Int32
	var sawRuleNote, sawTagGuidance atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err == nil {
			if strings.Contains(string(body), "AUTOMATED RULE NOTE") {
				sawRuleNote.Store(true)
			}
			if strings.Contains(string(body), "RULE TAG GUIDANCE") {
				sawTagGuidance.Store(true)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Could not guess: insufficient evidence."}}]}`)
	}))
	defer model.Close()
	connection := store.AssistantConnection{Name: "Poison model", BaseURL: model.URL, Model: "invented-model", IsEnabled: true, AllowWrites: true}
	require.NoError(t, st.SaveAssistantConnection(ctx, space, &connection, "invented-key"))
	before, err := st.ListTransactions(ctx, space, store.TransactionQuery{})
	require.NoError(t, err)
	cfg := &config.Config{DataDir: dir, CredentialEncryptionKey: "evaluation-test-key"}
	args := []string{"categorize", "--household", space.String(), "--cutoff", "2026-01-01"}
	var out, summary bytes.Buffer
	require.NoError(t, evalCommand(ctx, cfg, args, &out, &summary))
	require.Zero(t, calls.Load())
	require.Zero(t, networkCalls.Load(), "offline evaluation must make no HTTP requests")
	var report categoryeval.Report
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	require.Equal(t, 1, report.SchemaVersion)
	require.Equal(t, "2026-01-01", report.Cutoff)
	require.Equal(t, 5, report.HeldOutRows)
	require.Equal(t, 4, report.Score.Overall.Suggested)
	require.Equal(t, 3, report.Score.Overall.Correct)
	require.NotNil(t, report.Score.Overall.Coverage)
	require.Equal(t, 0.8, *report.Score.Overall.Coverage)
	require.NotNil(t, report.Score.Overall.Accuracy)
	require.Equal(t, 0.75, *report.Score.Overall.Accuracy)
	require.Equal(t, 3, report.Score.ByBasis[domain.SuggestionBasisHistory].Suggested)
	require.Equal(t, 1, report.Score.ByBasis["rule"].Suggested)
	require.Len(t, report.Score.Confusions, 1)
	for _, sensitive := range []string{"Invented Redwood", "Imaginary Quillfeather", "PRIVATE CLEANED PAYEE", "PRIVATE CATEGORY NOTE", "-20.00"} {
		require.NotContains(t, out.String(), sensitive)
		require.NotContains(t, summary.String(), sensitive)
	}
	after, err := st.ListTransactions(ctx, space, store.TransactionQuery{})
	require.NoError(t, err)
	require.Equal(t, before, after, "evaluation must not mutate the original household")
	out.Reset()
	summary.Reset()
	require.NoError(t, evalCommand(ctx, cfg, append(args, "--with-model"), &out, &summary))
	require.Positive(t, calls.Load(), "explicit opt-in must enable the actual model path")
	require.True(t, sawRuleNote.Load(), "safe rule-generated notes must reach the live context")
	require.True(t, sawTagGuidance.Load(), "safe rule-generated tags must select guidance")
	require.NoError(t, st.DeleteAssistantConnection(ctx, space))
	out.Reset()
	summary.Reset()
	require.NoError(t, evalCommand(ctx, cfg, args, &out, &summary), "offline runs need no model configuration")
	out.Reset()
	summary.Reset()
	require.ErrorContains(t, evalCommand(ctx, cfg, append(args, "--out", filepath.Join(dir, "server.db")), &out, &summary), "outside DATA_DIR")
	require.Empty(t, out.String())
}

func TestEvalPercent(t *testing.T) {
	require.Equal(t, "n/a", evalPercent(nil))
	zero := 0.0
	require.Equal(t, "0.0%", evalPercent(&zero))
}
