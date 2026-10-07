package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// What a run is handed, and who it runs as. A run reads through the same tool
// runner and loaders as the screens, not a second rendering of a row.

// automationHost is the service's window into the API layer.
type automationHost struct {
	env *Env
}

// ToolsFor builds the tool runner acting as one member of a space. The
// membership is read fresh, so a removed or demoted member stops writing
// immediately.
func (h automationHost) ToolsFor(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
) (service.ToolRunner, error) {
	sp, err := h.contextFor(ctx, spaceID, userID)
	if err != nil {
		return nil, err
	}
	return assistantTools{env: h.env, sp: sp, strictKinds: true}, nil
}

func (h automationHost) contextFor(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
) (auth.SpaceContext, error) {
	space, err := h.env.DB.GetSpace(ctx, spaceID)
	if err != nil {
		return auth.SpaceContext{}, fmt.Errorf("the space no longer exists")
	}
	membership, err := h.env.DB.GetMembership(ctx, spaceID, userID)
	if err != nil || !membership.IsAccepted() {
		return auth.SpaceContext{}, fmt.Errorf(
			"the person who set this automation up is no longer a member of the space")
	}
	user, err := h.env.DB.GetUser(ctx, userID)
	if err != nil || !user.IsActive {
		return auth.SpaceContext{}, fmt.Errorf(
			"the person who set this automation up no longer has an active account")
	}
	return auth.SpaceContext{Space: space, Membership: membership, User: user}, nil
}

// ContextFor renders the opening message: the facts the automation asked for,
// as Markdown headings with JSON under them. The message is stored, so the run
// shows exactly what the model saw.
func (h automationHost) ContextFor(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
	automation store.Automation, transactionID uuid.UUID, blind bool,
) (string, error) {
	sp, err := h.contextFor(ctx, spaceID, userID)
	if err != nil {
		return "", err
	}
	tools := assistantTools{env: h.env, sp: sp}
	wants := automation.Context

	out := &strings.Builder{}
	out.WriteString("## Context\n")

	var subject *store.Transaction
	if transactionID != uuid.Nil {
		txn, err := h.env.DB.GetTransaction(ctx, spaceID, transactionID)
		if err != nil {
			return "", err
		}
		if blind {
			txn.CategoryID = uuid.Nil
		}
		subject = &txn
	}

	names, err := h.names(ctx, spaceID)
	if err != nil {
		return "", err
	}

	if subject != nil && wants.Transaction {
		section(out, "The transaction", names.subject(*subject))
	}
	var enrichment *service.MerchantEnrichment
	if subject != nil {
		if enrichment, err = merchantService(h.env).EnrichmentFor(ctx, spaceID, *subject); err != nil {
			return "", err
		}
	}
	if subject != nil && wants.Transaction {
		habit, err := h.accountHabit(ctx, spaceID, *subject, names)
		if err != nil {
			return "", err
		}
		section(out, "The account", habit)
		if enrichment != nil {
			section(out, fmt.Sprintf("The %s %s behind this charge (decide from the items, not the payee)",
				enrichment.Merchant().Name, enrichment.Merchant().Noun), merchantSection(enrichment))
		}
	}
	if subject != nil && wants.SimilarTransactions > 0 {
		scored, basis, err := h.similar(ctx, spaceID, *subject, wants.SimilarTransactions)
		if err != nil {
			return "", err
		}
		rows := make([]map[string]any, 0, len(scored))
		for _, one := range scored {
			rows = append(rows, names.transaction(one.row))
		}
		title := fmt.Sprintf("Similar past transactions (%d most relevant, newest first)", len(rows))
		if basis == historyFromAccount {
			title = fmt.Sprintf("Past rows in this account with money the same way (%d most "+
				"recent) — the wording is generic, so the account stands in for the payee", len(rows))
		}
		if len(rows) == 0 {
			title = "Similar past transactions"
			section(out, title, "None found: this payee has not been seen before.")
		} else {
			section(out, title, rows)
		}
		// The vote reads a wider sample than the model is shown, so a mostly
		// uncategorized payee is seen as such.
		voters, basis, err := h.similar(ctx, spaceID, *subject, categoryVoteSample)
		if err != nil {
			return "", err
		}
		assessment := h.assess(*subject, voters, basis, automation.ConfidenceThreshold, names)
		if enrichment != nil {
			assessment.Text += merchantAttachedNote(enrichment)
		}
		section(out, "Agentifi's assessment", assessment.Text)
	}
	if wants.Corrections {
		corrections, err := h.corrections(ctx, spaceID, subject, names)
		if err != nil {
			return "", err
		}
		if len(corrections) > 0 {
			section(out, "Corrections the household has made to earlier proposals "+
				"(what was proposed, and what they chose instead — the ones about this "+
				"payee first)", corrections)
		}
	}
	if wants.Categories {
		categories, err := tools.categories(ctx)
		if err != nil {
			return "", err
		}
		section(out, "Categories (reference a category by its id)", categories)
	}
	if wants.Rules {
		rules, err := tools.rules(ctx)
		if err != nil {
			return "", err
		}
		section(out, "Rules that run on arriving transactions", rules)
	}
	if wants.Accounts {
		accounts, err := tools.accounts(ctx)
		if err != nil {
			return "", err
		}
		section(out, "Accounts", accounts)
	}
	// Last on purpose: the guidance outranks everything above it, and a model
	// weights what it read most recently.
	if wants.Guidance {
		guidance, err := h.guidance(ctx, spaceID, subject, names)
		if err != nil {
			return "", err
		}
		if guidance != "" {
			section(out, "The household's guidance", guidance)
		}
	}
	if wants.CashFlowHistory {
		months, days, err := h.cashFlowHistory(ctx, spaceID)
		if err != nil {
			return "", err
		}
		section(out, "Money in and out, by month (the last 12 months, oldest first)",
			monthlyCashFlowSection(months))
		section(out, "Money in and out, by day — one row per day something moved, "+
			"[date, in, out]", dailyCashFlowSection(days))
		reminders, err := h.forecastReminders(ctx, sp)
		if err != nil {
			return "", err
		}
		section(out, "Reminders — the bills and income already scheduled through the end "+
			"of the sixth month, and the bills from the last two months still unpaid "+
			"(past_due), [due_on, in, out, status]", remindersSection(reminders))
	}
	return out.String(), nil
}

// cashFlowHistoryMonths is how far back the forecast run is shown.
const cashFlowHistoryMonths = 12

// cashFlowForecastMonths is how far forward the forecast run estimates,
// starting with the month today falls in.
const cashFlowForecastMonths = 6

// forecastReminders is what the Reminders strip would list, from its reach
// into the past through the last forecast month.
func (h automationHost) forecastReminders(
	ctx context.Context, sp auth.SpaceContext,
) ([]reminder, error) {
	today := domain.DateOf(h.env.now())
	through := domain.MonthOf(today).Shift(cashFlowForecastMonths - 1).LastDay()
	return loadReminders(ctx, h.env, sp, today.AddDays(-reminderDaysBack), through)
}

// remindersSection renders the reminders as the daily series is rendered:
// dates and amounts, no names. Transfers and card payments are left out, as
// the history leaves out transfer legs.
func remindersSection(reminders []reminder) [][]string {
	out := make([][]string, 0, len(reminders))
	for _, one := range reminders {
		if one.Kind.NetsToZero() {
			continue
		}
		in, spent := domain.Zero, domain.Zero
		if one.Amount.IsNegative() {
			spent = one.Amount.Abs()
		} else {
			in = one.Amount
		}
		out = append(out, []string{
			one.DueOn.String(), in.Round().String(), spent.Round().String(), one.Status,
		})
	}
	return out
}

// cashFlowHistory is the series a forecast run reads: the last twelve months of
// money in and out, by month and by day. A date and two amounts only, so the
// ledger is not narrated to a model.
//
// Transfer legs are dropped (money between the household's own accounts), as
// are provider forecast rows (a forecast built on expectations forecasts
// itself). Posted dates, since the balance projection moves on posting.
func (h automationHost) cashFlowHistory(
	ctx context.Context, spaceID store.SpaceID,
) ([]domain.CashFlowMonth, []domain.CashFlowDay, error) {
	today := domain.DateOf(h.env.now())
	from := domain.MonthOf(today).Shift(-(cashFlowHistoryMonths - 1)).FirstDay()
	rows, err := h.env.DB.ListTransactions(ctx, spaceID, store.TransactionQuery{
		From: from, To: today, ExcludePending: true,
	})
	if err != nil {
		return nil, nil, err
	}
	categoryRows, err := h.env.DB.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nil, nil, err
	}
	categories := make(map[uuid.UUID]store.Category, len(categoryRows))
	for _, category := range categoryRows {
		categories[category.ID] = category
	}
	flows := make([]domain.CashFlow, 0, len(rows))
	for _, row := range rows {
		if store.IsTransfer(row, categories) {
			continue
		}
		flows = append(flows, domain.CashFlow{On: row.Date, Amount: row.Amount})
	}
	return domain.SummarizeCashFlowMonths(flows), domain.SummarizeCashFlowDays(flows), nil
}

// monthlyCashFlowSection renders the monthly rollup for the model.
func monthlyCashFlowSection(months []domain.CashFlowMonth) []map[string]any {
	out := make([]map[string]any, 0, len(months))
	for _, one := range months {
		out = append(out, map[string]any{
			"month": one.Month.String(), "money_in": one.In.String(),
			"money_out": one.Out.String(), "net": one.Net().String(),
		})
	}
	return out
}

// dailyCashFlowSection renders the daily series as arrays rather than objects,
// so a year of days does not repeat each field name. The heading names the
// columns.
func dailyCashFlowSection(days []domain.CashFlowDay) [][]string {
	out := make([][]string, 0, len(days))
	for _, one := range days {
		out = append(out, []string{one.On.String(), one.In.String(), one.Out.String()})
	}
	return out
}

// Record keeps what a finished run produced, when the answer is data. Only the
// cash-flow forecast does; an answer that does not parse, covers the wrong
// months, or is unlike the household's rhythm fails the run and leaves the
// last good forecast standing.
func (h automationHost) Record(
	ctx context.Context, run store.AutomationRun, automation store.Automation,
	model, output string,
) error {
	if !domain.AutomationAnswersWithForecast(automation.TemplateKey, automation.Context) {
		return nil
	}
	forecast, err := domain.ParseCashFlowForecast(output)
	if err != nil {
		return err
	}
	today := domain.DateOf(h.env.now())
	if err := domain.CashFlowForecastCoversFrom(forecast, today); err != nil {
		return err
	}
	months, _, err := h.cashFlowHistory(ctx, run.SpaceID)
	if err != nil {
		return err
	}
	if err := domain.CheckCashFlowForecast(forecast, months); err != nil {
		return err
	}
	row := store.CashFlowForecast{
		AutomationID: automation.ID, RunID: run.ID, Model: model, GeneratedOn: today,
		Forecast: forecast, RawAnswer: output,
	}
	return h.env.DB.SaveCashFlowForecast(ctx, run.SpaceID, &row)
}

func section(out *strings.Builder, title string, body any) {
	fmt.Fprintf(out, "\n### %s\n", title)
	if text, ok := body.(string); ok {
		out.WriteString(text + "\n")
		return
	}
	encoded, err := json.MarshalIndent(body, "", " ")
	if err != nil {
		encoded = []byte("(unavailable)")
	}
	out.WriteString("```json\n")
	out.Write(encoded)
	out.WriteString("\n```\n")
}

// nameLookup turns the ids on a row into the words a model can reason about.
type nameLookup struct {
	accounts   map[uuid.UUID]store.Account
	categories map[uuid.UUID]store.Category
}

func (h automationHost) names(ctx context.Context, spaceID store.SpaceID) (nameLookup, error) {
	accounts, err := h.env.DB.ListAccounts(ctx, spaceID,
		store.AccountQuery{IncludeClosed: true, IncludeDeleted: true})
	if err != nil {
		return nameLookup{}, err
	}
	categories, err := h.env.DB.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nameLookup{}, err
	}
	names := nameLookup{
		accounts:   make(map[uuid.UUID]store.Account, len(accounts)),
		categories: make(map[uuid.UUID]store.Category, len(categories)),
	}
	for _, one := range accounts {
		names.accounts[one.ID] = one
	}
	for _, one := range categories {
		names.categories[one.ID] = one
	}
	return names, nil
}

// posting joins a row to its account and category, the shape domain.Matches
// evaluates. An unknown account or category leaves the zero value rather than
// failing like store.BuildPostings: a guidance note that stops applying beats a
// run that fails over a retired category.
func (n nameLookup) posting(txn store.Transaction) domain.Posting {
	posting := domain.Posting{Txn: store.DomainTransaction(txn)}
	if account, ok := n.accounts[txn.AccountID]; ok {
		posting.Account = store.DomainAccount(account)
	}
	if category, ok := n.categories[txn.CategoryID]; ok && txn.CategoryID != uuid.Nil {
		posting.Category, posting.HasCategory = store.DomainCategory(category), true
	}
	return posting
}

// filterValues is the id→name map a filter description prints: accounts and
// categories, the id-valued fields the guidance builder offers.
func (n nameLookup) filterValues() map[domain.ID]string {
	labels := make(map[domain.ID]string, len(n.accounts)+len(n.categories))
	for id, account := range n.accounts {
		labels[domain.ID(id.String())] = account.Name
	}
	for id, category := range n.categories {
		labels[domain.ID(id.String())] = category.Name
	}
	return labels
}

// reviewedByHousehold is domain.ReviewedByHousehold for a stored row.
func (n nameLookup) reviewedByHousehold(txn store.Transaction) bool {
	return domain.ReviewedByHousehold(txn.IsReviewed, txn.Source, n.accounts[txn.AccountID].Kind)
}

// subject is the row being decided: transaction, with what its own category
// is worth beside it.
func (n nameLookup) subject(txn store.Transaction) map[string]any {
	row := n.transaction(txn)
	stands := domain.ReviewedCategoryStands(txn.IsReviewed, n.accounts[txn.AccountID].Kind)
	standing := domain.CategoryStanding(len(txn.Splits) == 0 && txn.CategoryID != uuid.Nil, stands)
	if standing == "" {
		return row
	}
	row["category_standing"] = standing
	if stands {
		delete(row, "reviewed_on_arrival")
	}
	return row
}

// transaction is the row as the model sees it, with the bank's wording and the
// cleaned payee kept apart.
func (n nameLookup) transaction(txn store.Transaction) map[string]any {
	row := map[string]any{
		"id":             txn.ID.String(),
		"date":           txn.Date.String(),
		"amount":         txn.Amount.String(),
		"currency":       txn.Currency,
		"statement_name": txn.StatementName,
		"payee":          txn.Payee,
		"notes":          txn.Notes,
		"source":         string(txn.Source),
		"is_pending":     txn.IsPending,
		"is_reviewed":    txn.IsReviewed,
		"is_transfer":    store.IsTransfer(txn, n.categories),
	}
	if txn.IsReviewed && !n.reviewedByHousehold(txn) {
		row["reviewed_on_arrival"] = true
	}
	// Memo and the connector's classification often say what "SQ *XKCD4821"
	// does not. Omitted when empty, so the model keeps looking at it.
	if txn.Memo != "" {
		row["memo"] = txn.Memo
	}
	if !txn.TransactedOn.IsZero() {
		row["transacted_on"] = txn.TransactedOn.String()
	}
	if len(txn.ProviderExtra) > 0 {
		var extra map[string]any
		if err := json.Unmarshal(txn.ProviderExtra, &extra); err == nil && len(extra) > 0 {
			row["bank_data"] = extra
		}
	}
	if account, ok := n.accounts[txn.AccountID]; ok {
		row["account"] = map[string]any{
			"id": account.ID.String(), "name": account.Name, "kind": string(account.Kind),
		}
	}
	// A split row's categories are its splits'; the parent's empty one would
	// read as a row nobody filed.
	if len(txn.Splits) == 0 {
		row["category"] = n.category(txn.CategoryID)
		return row
	}
	splits := make([]map[string]any, 0, len(txn.Splits))
	for _, split := range txn.Splits {
		splits = append(splits, map[string]any{
			"amount": split.Amount.String(), "category": n.category(split.CategoryID),
		})
	}
	row["splits"] = splits
	return row
}

// category is one category as the model sees it; nil for none.
func (n nameLookup) category(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	category, ok := n.categories[id]
	if !ok {
		return map[string]any{"id": id.String()}
	}
	entry := map[string]any{"id": category.ID.String(), "name": category.Name}
	if parent, ok := n.categories[category.ParentID]; ok {
		entry["parent"] = parent.Name
	}
	return entry
}

// similar finds the rows this payee produced before, keyed on the bank's
// wording: domain.MerchantTokens of the statement name and payee. Candidates
// must pass domain.MerchantWordingOverlap and rank by how many words they
// share. Category and review state never enter the score, or the sample would
// misread the household's habit.
//
// When the wording is only boilerplate ("Payment" on a loan), the account is
// the payee: the fallback reads the account's rows with money running the same
// way, and says so.
func (h automationHost) similar(
	ctx context.Context, spaceID store.SpaceID, subject store.Transaction, limit int,
) ([]scoredTransaction, historyBasis, error) {
	wording := subject.StatementName + " " + subject.Payee
	tokens := domain.MerchantTokens(wording)
	if len(tokens) == 0 {
		rows, err := h.sameAccount(ctx, spaceID, subject, limit)
		return rows, historyFromAccount, err
	}
	longest := tokens[0]
	for _, token := range tokens {
		if len(token) > len(longest) {
			longest = token
		}
	}
	// Two years before the row to today: for a new row the same window the
	// suggestions read, for an old one its contemporaries and since.
	to := subject.Date
	if today := domain.DateOf(h.env.now()); today.Time().After(to.Time()) {
		to = today
	}
	rows, err := h.env.DB.ListTransactions(ctx, spaceID, store.TransactionQuery{
		From: subject.Date.AddDays(-service.HistoryDays), To: to,
		SearchText: spelledAs(wording, longest), ExcludePending: true, Limit: 600,
	})
	if err != nil {
		return nil, historyFromPayee, err
	}
	var candidates []scoredTransaction
	for _, row := range rows {
		if row.ID == subject.ID {
			continue
		}
		score, same := domain.MerchantWordingOverlap(wording, row.StatementName+" "+row.Payee)
		if !same {
			continue
		}
		candidates = append(candidates, scoredTransaction{row: row, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].row.Date.Time().After(candidates[j].row.Date.Time())
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, historyFromPayee, nil
}

// spelledAs is a folded token as the wording writes it: the SQL search is
// case-insensitive but not accent-insensitive, so it looks for the subject's
// own spelling.
func spelledAs(wording, token string) string {
	for _, word := range strings.FieldsFunc(wording, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if domain.FoldName(word) == token {
			return word
		}
	}
	return token
}

// corrections is what the household changed about earlier proposals: this
// payee's first, then the most recent others, which carry the household's
// taste. Matched by domain.SharesMerchantWording, since the same merchant's
// wording varies.
func (h automationHost) corrections(
	ctx context.Context, spaceID store.SpaceID, subject *store.Transaction, names nameLookup,
) ([]map[string]any, error) {
	rows, err := h.env.DB.ListAssistantCorrections(ctx, spaceID, correctionSample)
	if err != nil {
		return nil, err
	}
	var wording string
	if subject != nil {
		wording = subject.StatementName + " " + subject.Payee
	}

	var here, elsewhere []map[string]any
	for _, one := range rows {
		matched := domain.SharesMerchantWording(wording, one.StatementName+" "+one.Payee)
		if matched {
			if len(here) < correctionsForThisPayee {
				here = append(here, h.correction(one, names, true))
			}
			continue
		}
		if len(elsewhere) < correctionsElsewhere {
			elsewhere = append(elsewhere, h.correction(one, names, false))
		}
	}
	return append(here, elsewhere...), nil
}

// guidance is the household's standing instructions, as prose. With a row in
// hand only the notes whose conditions select it are shown, so the one that
// matters is not buried. A run about no single row gets all of them, each with
// the rows it applies to.
func (h automationHost) guidance(
	ctx context.Context, spaceID store.SpaceID, subject *store.Transaction, names nameLookup,
) (string, error) {
	rows, err := h.env.DB.ListGuidance(ctx, spaceID, store.GuidanceQuery{ActiveOnly: true})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	// One query for every note's conditions; this runs twice per row.
	filters, err := h.env.DB.ListFilters(ctx, spaceID, false)
	if err != nil {
		return "", err
	}
	byID := make(map[uuid.UUID]store.Filter, len(filters))
	for _, filter := range filters {
		byID[filter.ID] = filter
	}
	notes := make([]domain.Guidance, 0, len(rows))
	for _, row := range rows {
		note := domain.Guidance{
			ID: domain.ID(row.ID.String()), Name: row.Name, Instruction: row.Instruction,
		}
		if row.FilterID != uuid.Nil {
			filter, ok := byID[row.FilterID]
			// A note whose conditions are gone is dropped rather than widened to
			// every row.
			if !ok {
				continue
			}
			note.Filter = store.DomainFilter(filter)
		}
		notes = append(notes, note)
	}

	preamble := domain.GuidanceNoSubjectPreamble
	if subject != nil {
		preamble = domain.GuidancePreamble
		// The facets the rules engine passes, so a note about a flag or the
		// bill/subscription verdict can match.
		facets := domain.Facets(store.Facets(*subject))
		notes = domain.SelectGuidance(notes, names.posting(*subject), facets)
	}
	if len(notes) == 0 {
		return "", nil
	}

	labels := names.filterValues()
	out := &strings.Builder{}
	out.WriteString(preamble + "\n")
	for i, note := range notes {
		fmt.Fprintf(out, "\n%d. **%s** — applies to %s\n%s\n", i+1, note.Name,
			domain.DescribeFilter(note.Filter, labels), strings.TrimSpace(note.Instruction))
	}
	return out.String(), nil
}

// correction renders one, in the words the model reads.
func (h automationHost) correction(
	one store.AssistantCorrection, names nameLookup, samePayee bool,
) map[string]any {
	row := map[string]any{
		"when":         one.CreatedAt.Format("2006-01-02"),
		"payee":        strings.TrimSpace(one.Payee + " (" + one.StatementName + ")"),
		"you_proposed": names.categoryLabel(one.ProposedCategoryID),
		"they_chose":   names.categoryLabel(one.ChosenCategoryID),
	}
	if samePayee {
		row["about"] = "this payee"
	}
	if one.Memo != "" {
		row["item"] = one.Memo
	}
	if one.HasAmount {
		row["amount"] = one.Amount.String()
	}
	if one.ToolName == "split_transaction" {
		row["part_of_a_split"] = true
	}
	return row
}

// categoryLabel is a category id as the model should read it: the name, with
// the parent when there is one, "no category" for none, never a bare id.
func (n nameLookup) categoryLabel(id uuid.UUID) string {
	if id == uuid.Nil {
		return "no category"
	}
	category, ok := n.categories[id]
	if !ok {
		return id.String()
	}
	if parent, ok := n.categories[category.ParentID]; ok {
		return parent.Name + " › " + category.Name
	}
	return category.Name
}

const (
	// correctionSample is how many of the household's most recent corrections
	// are read before any of them are matched to this row.
	correctionSample = 120
	// How many reach the model: enough of this payee's to establish a habit,
	// and a few others as taste.
	correctionsForThisPayee = 8
	correctionsElsewhere    = 6
)

// historyBasis is what the similar rows have in common with the subject.
type historyBasis string

const (
	historyFromPayee   historyBasis = "payee"
	historyFromAccount historyBasis = "account"
)

// sameAccount is the history for a row whose wording names nobody: the
// account's other rows, newest first, with money running the same way. Rows
// with the very same wording come first, and stand alone when there are enough
// of them ("Deposit" and "Interest" on a savings account are different things).
func (h automationHost) sameAccount(
	ctx context.Context, spaceID store.SpaceID, subject store.Transaction, limit int,
) ([]scoredTransaction, error) {
	rows, err := h.accountRows(ctx, spaceID, subject)
	if err != nil {
		return nil, err
	}
	wording := genericWording(subject)
	var same, others []scoredTransaction
	for _, row := range rows {
		if row.ID == subject.ID || row.Amount.IsPositive() != subject.Amount.IsPositive() {
			continue
		}
		if genericWording(row) == wording {
			same = append(same, scoredTransaction{row: row, score: 2})
		} else {
			others = append(others, scoredTransaction{row: row, score: 1})
		}
	}
	out := same
	if len(same) < sameWordingEnough {
		out = append(out, others...)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// sameWordingEnough is how many rows with the subject's exact wording make
// the rest of the account irrelevant.
const sameWordingEnough = 3

func genericWording(txn store.Transaction) string {
	return strings.ToLower(strings.TrimSpace(txn.StatementName + " " + txn.Payee))
}

// accountRows is the account's settled rows in the history window, newest
// first, capped the way the payee search is.
func (h automationHost) accountRows(
	ctx context.Context, spaceID store.SpaceID, subject store.Transaction,
) ([]store.Transaction, error) {
	to := subject.Date
	if today := domain.DateOf(h.env.now()); today.Time().After(to.Time()) {
		to = today
	}
	return h.env.DB.ListTransactions(ctx, spaceID, store.TransactionQuery{
		AccountIDs: []uuid.UUID{subject.AccountID},
		From:       subject.Date.AddDays(-service.HistoryDays), To: to,
		ExcludePending: true, Limit: 600,
	})
}

// accountHabit is the account as the model should know it: what it is, and
// what the household files its rows under.
func (h automationHost) accountHabit(
	ctx context.Context, spaceID store.SpaceID, subject store.Transaction, names nameLookup,
) (map[string]any, error) {
	out := map[string]any{"id": subject.AccountID.String()}
	if account, ok := names.accounts[subject.AccountID]; ok {
		out["name"], out["kind"], out["type"] = account.Name, string(account.Kind), account.Type
		switch account.Kind {
		case domain.KindLoan:
			out["note"] = "A loan: money in is a payment on the loan, money out is interest or " +
				"a fee. Both belong in expense categories."
		case domain.KindCreditCard:
			out["note"] = "A credit card: money out is a charge or interest, an expense. Money " +
				"in is a payment on the card (a transfer), or a refund, return or credit from a " +
				"merchant, which belongs in the expense category of what was bought. Only " +
				"rewards and cashback are income."
		}
	}
	rows, err := h.accountRows(ctx, spaceID, subject)
	if err != nil {
		return nil, err
	}
	counts := map[uuid.UUID]int{}
	uncategorized, transfers := 0, 0
	for _, row := range rows {
		if row.ID == subject.ID {
			continue
		}
		txn := store.DomainTransaction(row)
		switch {
		case store.IsTransfer(row, names.categories):
			transfers++
		case txn.IsUncategorized():
			uncategorized++
		default:
			seen := map[uuid.UUID]bool{}
			for _, id := range txn.CategoryIDs() {
				if parsed, err := store.ParseID(id); err == nil && !seen[parsed] {
					seen[parsed] = true
					counts[parsed]++
				}
			}
		}
	}
	type share struct {
		Category string `json:"category"`
		ID       string `json:"id"`
		Rows     int    `json:"rows"`
	}
	var top []share
	for id, n := range counts {
		name := id.String()
		if category, ok := names.categories[id]; ok {
			name = category.Name
		}
		top = append(top, share{Category: name, ID: id.String(), Rows: n})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Rows != top[j].Rows {
			return top[i].Rows > top[j].Rows
		}
		return top[i].Category < top[j].Category
	})
	if len(top) > 5 {
		top = top[:5]
	}
	out["rows_in_the_last_two_years"] = len(rows)
	out["most_used_categories"] = top
	out["uncategorized_rows"] = uncategorized
	out["transfer_legs"] = transfers
	return out, nil
}

// categoryVoteSample is how many past rows the vote reads.
const categoryVoteSample = 40

// scoredTransaction is a past row with how alike its wording was.
type scoredTransaction struct {
	row   store.Transaction
	score int
}

// Assess puts the payee's history to the vote for one row.
func (h automationHost) Assess(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
	automation store.Automation, transactionID uuid.UUID, blind bool,
) (service.CategoryAssessment, error) {
	subject, err := h.env.DB.GetTransaction(ctx, spaceID, transactionID)
	if err != nil {
		return service.CategoryAssessment{}, err
	}
	if blind {
		subject.CategoryID = uuid.Nil
	}
	names, err := h.names(ctx, spaceID)
	if err != nil {
		return service.CategoryAssessment{}, err
	}
	// Forty rows, however few the context shows: a vote needs the sample.
	scored, basis, err := h.similar(ctx, spaceID, subject, categoryVoteSample)
	if err != nil {
		return service.CategoryAssessment{}, err
	}
	assessment := h.assess(subject, scored, basis, automation.ConfidenceThreshold, names)
	// An attached order settles a merchant row the payee history cannot.
	enrichment, err := merchantService(h.env).EnrichmentFor(ctx, spaceID, subject)
	if err != nil {
		return service.CategoryAssessment{}, err
	}
	if enrichment != nil {
		assessment.Enriched = true
		assessment.Text += merchantAttachedNote(enrichment)
	}
	// Whether a note covers this row: the vote must not settle a row somebody
	// wrote an instruction about. Only when the automation is handed the notes.
	if automation.Context.Guidance {
		guidance, err := h.guidance(ctx, spaceID, &subject, names)
		if err != nil {
			return service.CategoryAssessment{}, err
		}
		assessment.Guided = guidance != ""
	}
	return assessment, nil
}

// merchantAttachedNote follows the vote when the row's order is known.
func merchantAttachedNote(enrichment *service.MerchantEnrichment) string {
	m := enrichment.Merchant()
	article := "A"
	if strings.ContainsRune("AEIOU", []rune(m.Name)[0]) {
		article = "An"
	}
	return fmt.Sprintf("%s %s %s is attached to this row (see the order section): "+
		"the items decide the category, and the payee's history above is only what the "+
		"household chose when it could not see them.\n", article, m.Name, m.Noun)
}

// assess is the vote itself, and the words for it.
func (h automationHost) assess(
	subject store.Transaction, scored []scoredTransaction, basis historyBasis, threshold float64,
	names nameLookup,
) service.CategoryAssessment {
	evidence := make([]domain.CategoryEvidence, 0, len(scored))
	credit := store.DomainTransaction(subject)
	for _, one := range scored {
		age := domain.DaysBetween(one.row.Date, subject.Date)
		transfer := store.IsTransfer(one.row, names.categories)
		// A retired category is no answer to propose, so its rows do not vote.
		if category, ok := names.categories[one.row.CategoryID]; ok && category.IsDeleted && !transfer {
			continue
		}
		id := ""
		if one.row.CategoryID != uuid.Nil {
			id = one.row.CategoryID.String()
		}
		past := store.DomainTransaction(one.row)
		purchaseAge, purchase := domain.OriginalPurchaseAge(credit, past)
		byHousehold := names.reviewedByHousehold(one.row)
		evidence = append(evidence, domain.CategoryEvidence{
			CategoryID: id, IsSplit: len(past.Splits) > 0 && !past.IsUncategorized(),
			Similarity: one.score, AgeDays: age,
			IsReviewed: byHousehold, ReviewedOnArrival: one.row.IsReviewed && !byHousehold,
			IsTransfer: transfer, IsCharge: one.row.Amount.IsNegative(),
			IsPurchase: purchase, PurchaseAgeDays: purchaseAge,
			TxnID: one.row.ID.String(),
		})
	}
	verdict := domain.VoteCategory(evidence, threshold)
	// On a loan a payment arrives and is still spending, so the direction check
	// would flag every correct row.
	account, known := names.accounts[subject.AccountID]
	onLoan := known && account.Kind == domain.KindLoan
	if subject.Amount.IsPositive() && !onLoan {
		if refund, merchant := domain.VoteRefundCategory(evidence, threshold); merchant && refund.Voters > 0 {
			verdict = refund
		}
	}
	if parsed, err := uuid.Parse(verdict.CategoryID); err == nil && !onLoan {
		if category, ok := names.categories[parsed]; ok {
			verdict = verdict.WithKindCheck(subject.Amount.IsPositive(),
				category.Kind == domain.CategoryIncome)
		}
	}
	name := func(id string) string {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return id
		}
		if category, ok := names.categories[parsed]; ok {
			return category.Name
		}
		return id
	}

	text := &strings.Builder{}
	if basis == historyFromAccount {
		where := "this account"
		if known {
			where = fmt.Sprintf("the %s account %q", account.Kind, account.Name)
		}
		fmt.Fprintf(text, "The wording (%q) is a bank's boilerplate and names no payee, so the "+
			"history here is %s's own rows with money running the same way: on this account "+
			"that is what such a row is.\n", strings.TrimSpace(subject.StatementName+" "+subject.Payee),
			where)
	}
	if verdict.IsRefund {
		fmt.Fprintf(text, "This row is money coming back from a merchant the household buys from, "+
			"so the vote below is over its %d categorized purchases only: a refund belongs in the "+
			"category of what it gives back, not under income.\n", verdict.Voters)
		for _, one := range scored {
			if one.row.ID.String() != verdict.RefundOf {
				continue
			}
			fmt.Fprintf(text, "It matches the purchase of %s on %s (id %s), filed under %s, to "+
				"the cent: that is the purchase it gives back, and its category is the answer.\n",
				one.row.Amount.Abs().String(), one.row.Date.String(), verdict.RefundOf,
				name(verdict.CategoryID))
		}
		if verdict.Kind != domain.CategoryVerdictConfident {
			text.WriteString("The purchases do not settle it on their own. Propose the category " +
				"of what was bought at whatever confidence it has, never an income category; a " +
				"person confirms every refund.\n")
		}
	}
	switch verdict.Kind {
	case domain.CategoryVerdictNoHistory:
		switch {
		case verdict.Total == 0 && basis == historyFromAccount:
			text.WriteString("The account has no other such rows yet.\n")
		case verdict.Total == 0:
			text.WriteString("No past transactions from this payee.\n")
		case basis == historyFromAccount:
			fmt.Fprintf(text, "None of the account's %d such rows has a category: %s.\n",
				verdict.Total, uncategorizedHistory(verdict))
		default:
			fmt.Fprintf(text, "None of the %d past rows from this payee has a category: %s.\n",
				verdict.Total, uncategorizedHistory(verdict))
		}
		text.WriteString("There is no categorized history to lean on, which lowers confidence " +
			"but does not stop a guess. Decide from the row itself (its wording, memo, account " +
			"and amount) and the category list. A well-known chain or brand, a fund or security " +
			"on an investment account, a dividend, interest, a fee or a loan payment places a row " +
			"on its own. A bare code or an unfamiliar name on an everyday account does not: say " +
			"you could not guess.\n")
	case domain.CategoryVerdictTransfer:
		fmt.Fprintf(text, "%d of %d past rows for this payee are transfer legs. This is a "+
			"transfer counterparty, not spending: leave the row alone.\n",
			verdict.Transfers, verdict.Total)
	default:
		fmt.Fprintf(text, "Confidence %.2f (acts alone at %.2f or above). ", verdict.Confidence,
			threshold)
		fmt.Fprintf(text, "Leading category: %s (id %s), %d of %d categorized rows",
			name(verdict.CategoryID), verdict.CategoryID, verdict.Agreeing, verdict.Voters)
		if rest := uncategorizedHistory(verdict); rest != "" {
			fmt.Fprintf(text, "; of the rest, %s", rest)
		}
		text.WriteString(".\n")
		if verdict.KindConflict && subject.Amount.IsPositive() {
			text.WriteString("That is an expense category and this row is money in. That is " +
				"right for a refund, return or credit from a merchant, which goes in the category " +
				"of what was bought; it is wrong for pay, interest or a dividend, and then the " +
				"history is itself wrong (a misfired rule, a bulk edit). Decide from what the " +
				"row is, and say so.\n")
		} else if verdict.KindConflict {
			text.WriteString("But that is an income category and this row is money out, which " +
				"is the shape of a history that is itself wrong (a misfired rule, a bulk edit). " +
				"Do not follow it: decide from what the row is, and say so.\n")
		} else if verdict.Kind == domain.CategoryVerdictConfident {
			text.WriteString("The history settles this.\n")
		} else {
			text.WriteString("The history does not settle this on its own; the leading " +
				"category is the default and needs a reason to be overridden.\n")
		}
		text.WriteString("Vote:")
		for _, share := range verdict.Shares {
			fmt.Fprintf(text, "\n- %s (id %s): %.0f%%, %d rows", name(share.CategoryID),
				share.CategoryID, share.Share*100, share.Rows)
		}
		text.WriteString("\n")
	}
	return service.CategoryAssessment{
		Verdict: verdict, CategoryName: name(verdict.CategoryID), Text: text.String(),
	}
}

// uncategorizedHistory says what the history's rows without a category are
// worth, kind by kind, so the model weighs each as the prompt says.
func uncategorizedHistory(verdict domain.CategoryVerdict) string {
	var parts []string
	if verdict.AwaitingReview > 0 {
		parts = append(parts, fmt.Sprintf("%d nobody has reviewed yet, which is not a decision",
			verdict.AwaitingReview))
	}
	if verdict.ArrivedReviewed > 0 {
		parts = append(parts, fmt.Sprintf("%d arrived marked reviewed (an import, or an account "+
			"that marks every row reviewed), which is not a decision either", verdict.ArrivedReviewed))
	}
	if verdict.LeftAlone > 0 {
		parts = append(parts, fmt.Sprintf("%d the household reviewed and kept uncategorized, "+
			"which lowers confidence in a category but does not forbid one", verdict.LeftAlone))
	}
	if verdict.Splits > 0 {
		parts = append(parts, fmt.Sprintf("%d were split and every part filed, so they name "+
			"no one category (their splits are listed with them)", verdict.Splits))
	}
	if verdict.Transfers > 0 {
		parts = append(parts, fmt.Sprintf("%d are transfer legs", verdict.Transfers))
	}
	return strings.Join(parts, "; ")
}

// NewAutomations builds the automation service over this environment: once by
// serve, and lazily in a process that did not start the worker.
func NewAutomations(env *Env) (*service.Automations, error) {
	sealed, err := sealedStore(env)
	if err != nil {
		return nil, err
	}
	automations := service.NewAutomations(sealed, automationHost{env: env})
	automations.Now = env.Now
	return automations, nil
}
