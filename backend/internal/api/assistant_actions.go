package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// What the assistant proposes, and who applies it.
//
// A write tool does not write. It turns its arguments into the request it
// would issue — method, path and body, against this server's own API — and
// stores that as a pending action shown as a card. Apply runs the request
// through the same handler a browser reaches, with the same validation and
// tenancy. The card renders the stored request itself, so what is reviewed and
// what runs cannot drift apart.
//
// Deciding a card writes a line into the thread, so the model's next answer
// knows the outcome rather than proposing it again.
//
// A viewer can read a card and nothing more: every route but its GET is
// registered with Write.

func init() {
	Register(Resource{Prefix: "/assistant-actions", Routes: func(rt *Routes) {
		rt.Write(http.MethodPost, "/apply-many", applyManyActions)
		rt.Read(http.MethodGet, "/{action_id}", readAction)
		rt.Write(http.MethodPost, "/{action_id}/apply", applyAction)
		rt.Write(http.MethodPost, "/{action_id}/discard", discardAction)
		rt.Write(http.MethodPost, "/{action_id}/decline", discardAction)
	}})
}

// ActionResponse is one proposed change, as the card renders it.
type ActionResponse struct {
	ID   uuid.UUID `json:"id"`
	Tool string    `json:"tool"`
	// Summary is the model's own line about what this does; the request is
	// the truth.
	Summary string         `json:"summary"`
	Method  string         `json:"method"`
	Path    string         `json:"path"`
	Body    map[string]any `json:"body"`
	// ProposedBody is what the model asked for, present only when somebody
	// changed a category before applying.
	ProposedBody map[string]any `json:"proposed_body,omitempty"`
	// Status is pending, applying, applied, failed, discarded or simulated.
	Status string `json:"status"`
	// Preview is the change with every id it names already named, as the
	// tool resolved them: `resource`, `verb`, `fields` and `names`.
	Preview map[string]any `json:"preview"`
	// GroupID joins the cards one bulk tool call proposed; null on its own.
	GroupID *uuid.UUID `json:"group_id"`
	// ResourceID is what the request created or changed, once it has run.
	ResourceID    *uuid.UUID `json:"resource_id"`
	DeclineReason string     `json:"decline_reason"`
	// Result is what the API answered, or why it refused. Empty while pending.
	Result     string     `json:"result"`
	StatusCode int        `json:"status_code"`
	CreatedAt  time.Time  `json:"created_at"`
	DecidedAt  *time.Time `json:"decided_at"`
	// About is the row this change is about, when it is about one.
	About *ActionAbout `json:"about,omitempty"`
}

// ActionAbout is the row a change is about, in the words somebody needs to
// recognize it. The request itself is rendered from itself, not from this.
type ActionAbout struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	// AccountID is what the card's link to the register opens on.
	AccountID     uuid.UUID    `json:"account_id"`
	Date          Date         `json:"date"`
	StatementName string       `json:"statement_name"`
	Payee         string       `json:"payee"`
	Amount        domain.Money `json:"amount"`
	// Items is what the order behind the row was for, in split order. Absent
	// when no order stands behind the row, and for a split.
	Items []ActionAboutItem `json:"items,omitempty"`
}

// ActionAboutItem is one thing that was bought, and what it cost of the charge.
type ActionAboutItem struct {
	Title string `json:"title"`
	// Amount is the item's share of the charge, absent when the order cannot
	// be divided that way — no prices, or a partial match.
	Amount *domain.Money `json:"amount,omitempty"`
}

func actionResponse(one store.AssistantAction) ActionResponse {
	out := ActionResponse{
		ID: one.ID, Tool: one.ToolName, Summary: one.Summary,
		Method: one.Method, Path: one.Path, Body: one.Body, ProposedBody: one.ProposedBody,
		Status: one.Status, Result: one.Result, StatusCode: one.StatusCode,
		CreatedAt: one.CreatedAt, DecidedAt: one.DecidedAt,
		Preview: one.Preview, DeclineReason: one.DeclineReason,
	}
	if one.GroupID != uuid.Nil {
		group := one.GroupID
		out.GroupID = &group
	}
	if one.ResourceID != uuid.Nil {
		resource := one.ResourceID
		out.ResourceID = &resource
	}
	return out
}

// actionAbout reads the row a change is about, for the card. Best effort and
// silent: a non-transaction change, a deleted row or an unmatched order is no
// reason to fail a list of cards.
func actionAbout(
	ctx context.Context, env *Env, sp auth.SpaceContext, action store.AssistantAction,
) *ActionAbout {
	id, ok := transactionIDFromPath(action.Path)
	if !ok {
		return nil
	}
	txn, err := env.DB.GetTransaction(ctx, sp.ID(), id)
	if err != nil {
		return nil
	}
	about := &ActionAbout{
		TransactionID: txn.ID, AccountID: txn.AccountID, Date: Date(txn.Date),
		StatementName: txn.StatementName, Payee: txn.Payee, Amount: txn.Amount,
	}
	if action.ToolName == "split_transaction" {
		return about
	}
	enrichment, err := merchantService(env).EnrichmentFor(ctx, sp.ID(), txn)
	if err != nil || enrichment == nil {
		return about
	}
	shares := enrichment.Shares()
	for i, item := range enrichment.Items() {
		one := ActionAboutItem{Title: domain.ItemMemo(item.Title)}
		if i < len(shares) {
			amount := shares[i]
			one.Amount = &amount
		}
		about.Items = append(about.Items, one)
	}
	return about
}

// actionResponseFor is actionResponse with the subject resolved, for a card
// shown to a person.
func actionResponseFor(
	ctx context.Context, env *Env, sp auth.SpaceContext, one store.AssistantAction,
) ActionResponse {
	out := actionResponse(one)
	out.About = actionAbout(ctx, env, sp, one)
	return out
}

// --- Proposing ---------------------------------------------------------------

// Propose records what a write tool would do and stops.
//
// Arguments are validated now, while the model is still listening and can
// correct them, rather than failing in front of a person at apply time.
func (a assistantTools) Propose(
	ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
) (any, error) {
	actions, err := a.record(ctx, conversationID, name, arguments)
	if agreed, ok := asRestatement(err); ok {
		return agreed, nil
	}
	if err != nil {
		return nil, err
	}

	// Says nothing has happened, so the answer cannot report a change nobody
	// made.
	out := map[string]any{
		"proposed": true,
		"summary":  toolString(arguments, "summary"),
		"status": "waiting for the person, shown as a card with Accept and Decline. Nothing " +
			"has changed yet. You will be told what they decided with their next message.",
	}
	if len(actions) == 1 {
		out["action_id"] = actions[0].ID.String()
		out["request"] = actions[0].Method + " " + actions[0].Path
		return out, nil
	}
	ids := make([]string, 0, len(actions))
	for _, one := range actions {
		ids = append(ids, one.ID.String())
	}
	out["action_ids"] = ids
	out["count"] = len(actions)
	return out, nil
}

// ApplyNow records the same change and issues it, for a household that has
// asked not to approve them one at a time. Same row, same dispatch as the
// Apply button, so every bound is enforced on both.
//
// The row is written before the request is issued: a crash between the two
// leaves a pending card, never an unrecorded ledger edit.
func (a assistantTools) ApplyNow(
	ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
) (any, error) {
	// Also checked inside the dispatcher; refusing here keeps a viewer's
	// attempt from leaving a card nobody can apply.
	if err := a.sp.RequireWrite(); err != nil {
		return nil, fmt.Errorf("you do not have permission to change anything here")
	}
	// A refund is a person's call even where changes apply unasked: it waits
	// as a card.
	if a.strictKinds {
		if refund, err := a.checkDirection(ctx, name, arguments); err == nil && refund {
			return a.Propose(ctx, conversationID, name, arguments)
		}
	}

	actions, err := a.record(ctx, conversationID, name, arguments)
	if agreed, ok := asRestatement(err); ok {
		return agreed, nil
	}
	if err != nil {
		return nil, err
	}
	applied, refusals := 0, []string{}
	var last store.AssistantAction
	for _, action := range actions {
		settled, err := applyStoredAction(ctx, a.env, a.sp, action)
		if err != nil {
			return nil, err
		}
		last = settled
		if settled.Status == domain.AssistantActionApplied {
			applied++
		} else {
			refusals = append(refusals, settled.Result)
		}
	}

	// A refusal comes back as a refusal rather than an error, so the model can
	// tell the person; the prompt tells it to stop rather than try another
	// way.
	out := map[string]any{
		"action_id": last.ID.String(),
		"summary":   toolString(arguments, "summary"),
		"request":   last.Method + " " + last.Path,
		"applied":   len(refusals) == 0,
	}
	if len(actions) > 1 {
		out["count"], out["applied_count"] = len(actions), applied
	}
	if len(refusals) == 0 {
		out["status"] = "done. This change has been made. Say so in the past tense."
		return out, nil
	}
	out["status"] = "refused, and nothing changed. Tell them it was refused and why; " +
		"do not try another way round it."
	if applied > 0 {
		out["status"] = fmt.Sprintf("%d of %d were made and the rest were refused. Say exactly "+
			"that; do not try another way round the refusals.", applied, len(actions))
	}
	out["refusal"] = refusals[0]
	return out, nil
}

// Simulate records the change a dry run would have made, stored terminal so it
// can never be issued. The model is told nothing is pending, or it reports a
// card that is not there.
func (a assistantTools) Simulate(
	ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
) (any, error) {
	actions, err := a.record(ctx, conversationID, name, arguments, domain.AssistantActionSimulated)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"simulated": true,
		"action_id": actions[0].ID.String(),
		"summary":   toolString(arguments, "summary"),
		"request":   actions[0].Method + " " + actions[0].Path,
		"status": "done: recorded as a dry run, as what you would have done. That is the " +
			"whole outcome for this change — do not call this tool again for it. Nothing in " +
			"the ledger changed and nothing is waiting for anybody; say what you would have done.",
	}, nil
}

// record validates a write tool's arguments and stores the change, or the
// group of changes, it stands for.
func (a assistantTools) record(
	ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
	status ...string,
) ([]store.AssistantAction, error) {
	tool, known := domain.AssistantToolByName(name)
	if !known || !tool.Writes {
		return nil, fmt.Errorf("%q is not a tool that changes anything", name)
	}

	actions, names, err := a.build(ctx, conversationID, name, arguments)
	if err != nil {
		return nil, err
	}
	// A dry run keeps its card: a blind one is compared with the very
	// category it was not shown.
	if len(actions) == 1 && name == "update_transaction" && len(status) == 0 {
		if agreed := a.restatedCategory(ctx, actions[0]); agreed != "" {
			return nil, restatesCategory{category: agreed}
		}
	}
	if a.strictKinds && len(actions) == 1 {
		if _, err := a.checkDirection(ctx, name, arguments); err != nil {
			return nil, err
		}
		if name == "split_transaction" {
			if err := a.refuseUnfaithfulMerchantSplit(ctx, arguments); err != nil {
				return nil, err
			}
		}
	}
	if name == "split_transaction" {
		if err := a.refuseMisSummedSplit(ctx, arguments); err != nil {
			return nil, err
		}
		a.nameSplitsFromTheOrder(ctx, arguments, &actions[0])
	}
	summary := toolString(arguments, "summary")
	if summary == "" {
		return nil, fmt.Errorf("`summary` is required: say in one line what this changes")
	}

	group := uuid.Nil
	if len(actions) > 1 {
		group = uuid.New()
	}
	for i := range actions {
		action := &actions[i]
		action.ConversationID = conversationID
		action.ToolName = name
		action.GroupID = group
		if len(status) > 0 {
			action.Status = status[0]
		}
		action.Preview = actionPreview(*action, names)
		if group == uuid.Nil || action.Summary == "" {
			action.Summary = summary
		} else {
			action.Preview["group_summary"] = summary
		}
		if err := a.env.DB.CreateAssistantAction(ctx, a.sp.ID(), action); err != nil {
			return nil, err
		}
	}
	return actions, nil
}

// restatesCategory is an update_transaction whose only change is the category
// the row already has. Agreeing with a row is not a suggestion, so nothing is
// recorded and the model is told the category stands.
type restatesCategory struct{ category string }

func (e restatesCategory) Error() string {
	return e.category + " is already this row's category"
}

func asRestatement(err error) (map[string]any, bool) {
	var agreed restatesCategory
	if !errors.As(err, &agreed) {
		return nil, false
	}
	return map[string]any{
		"proposed": false,
		"status": fmt.Sprintf("nothing recorded: %s is already this row's category, so "+
			"proposing it changes nothing and nobody is shown it. The category stands. Do "+
			"not call a change tool for it again; finish and say you agree with it.",
			agreed.category),
	}, true
}

// restatedCategory names the row's category when the change sets that
// category and nothing else, and is blank otherwise.
func (a assistantTools) restatedCategory(ctx context.Context, action store.AssistantAction) string {
	id, ok := transactionIDFromPath(action.Path)
	if !ok {
		return ""
	}
	txn, err := a.env.DB.GetTransaction(ctx, a.sp.ID(), id)
	if err != nil || !onlySetsCategory(txn, action.Body) {
		return ""
	}
	category, err := a.env.DB.GetCategory(ctx, a.sp.ID(), txn.CategoryID)
	if err != nil {
		return "That category"
	}
	return fmt.Sprintf("%q", category.Name)
}

// onlySetsCategory reports a change body that sets an unsplit row's own
// category and changes nothing else.
func onlySetsCategory(txn store.Transaction, body map[string]any) bool {
	if len(txn.Splits) > 0 || txn.CategoryID == uuid.Nil || len(body) != 1 {
		return false
	}
	return bodyUUID(body, "category_id") == txn.CategoryID
}

// build turns one write tool call into the request, or requests, it stands for,
// and says what every id in them is called.
func (a assistantTools) build(
	ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
) ([]store.AssistantAction, map[string]string, error) {
	r := a.resolver(ctx, conversationID)
	var actions []store.AssistantAction
	if builder, typed := typedProposals[name]; typed {
		built, err := builder(r, arguments)
		if err != nil {
			return nil, nil, err
		}
		actions = built
	} else {
		if err := r.resolveLegacyArguments(arguments); err != nil {
			return nil, nil, err
		}
		action, err := buildAction(name, arguments)
		if err != nil {
			return nil, nil, err
		}
		actions = []store.AssistantAction{action}
	}
	for _, action := range actions {
		r.nameIDs(action.Body)
		r.nameSubject(action.Path)
	}
	return actions, r.names, nil
}

// checkDirection is the guard behind strictKinds, for the two tools that set
// a category on a row whose amount is known. A model following a mis-filed
// history faithfully will extend it (pay under groceries), so money out under
// an income category is refused, naming the money's direction while the model
// can still act. Money in under an expense category is a refund, return or
// credit from a merchant, and is reported as one for a person to confirm.
func (a assistantTools) checkDirection(
	ctx context.Context, name string, arguments map[string]any,
) (refund bool, err error) {
	if name == "split_transaction" {
		return a.checkDirectionInSplits(ctx, arguments)
	}
	categoryID, err := uuid.Parse(toolString(arguments, "category_id"))
	if err != nil {
		return false, nil
	}
	var (
		amount    domain.Money
		accountID uuid.UUID
	)
	switch name {
	case "update_transaction":
		id, err := uuid.Parse(toolString(arguments, "transaction_id"))
		if err != nil {
			return false, nil
		}
		txn, err := a.env.DB.GetTransaction(ctx, a.sp.ID(), id)
		if err != nil {
			return false, nil
		}
		amount, accountID = txn.Amount, txn.AccountID
	case "create_transaction":
		parsed, err := domain.FromString(toolString(arguments, "amount"))
		if err != nil {
			return false, nil
		}
		amount = parsed
		accountID, _ = uuid.Parse(toolString(arguments, "account_id"))
	default:
		return false, nil
	}
	if amount.IsZero() || a.onLoan(ctx, accountID) {
		return false, nil
	}
	category, err := a.env.DB.GetCategory(ctx, a.sp.ID(), categoryID)
	if err != nil {
		return false, nil
	}
	income := category.Kind == domain.CategoryIncome
	switch {
	case amount.IsPositive() && !income:
		return true, nil
	case amount.IsNegative() && income:
		return false, fmt.Errorf("%q is an income category and this transaction is money out (%s). "+
			"Choose an expense category that fits, or propose nothing", category.Name,
			amount.String())
	}
	return false, nil
}

// checkDirectionInSplits is the same check, once per split: each carries its
// own amount and category.
func (a assistantTools) checkDirectionInSplits(
	ctx context.Context, arguments map[string]any,
) (refund bool, err error) {
	id, err := uuid.Parse(toolString(arguments, "transaction_id"))
	if err != nil {
		return false, nil
	}
	txn, err := a.env.DB.GetTransaction(ctx, a.sp.ID(), id)
	if err != nil || a.onLoan(ctx, txn.AccountID) {
		return false, nil
	}
	for _, split := range toolSplits(arguments) {
		categoryID, err := uuid.Parse(toolString(split, "category_id"))
		if err != nil {
			continue
		}
		amount, err := domain.FromString(toolString(split, "amount"))
		if err != nil || amount.IsZero() {
			continue
		}
		category, err := a.env.DB.GetCategory(ctx, a.sp.ID(), categoryID)
		if err != nil {
			continue
		}
		income := category.Kind == domain.CategoryIncome
		switch {
		case amount.IsPositive() && !income:
			refund = true
		case amount.IsNegative() && income:
			return false, fmt.Errorf("%q is an income category and the split for %s is money out. "+
				"Choose an expense category for it, or propose nothing", category.Name, amount.String())
		}
	}
	return refund, nil
}

// onLoan reports whether the account is a loan. A payment arrives at a loan
// as money in and is spending all the same, so the direction says nothing
// about the category there.
func (a assistantTools) onLoan(ctx context.Context, accountID uuid.UUID) bool {
	if accountID == uuid.Nil {
		return false
	}
	account, err := a.env.DB.GetAccount(ctx, a.sp.ID(), accountID)
	return err == nil && account.Kind == domain.KindLoan
}

// nameSplitsFromTheOrder writes each item's real name into the split that is
// its share. The model paraphrases titles or sends empty memos, and the card
// then cannot name what it categorizes.
//
// Runs only when the splits line up share for share, in order, with the items
// behind the row: a name on the wrong amount is worse than none. Otherwise the
// model's memos are left alone. Not behind strictKinds: chat splits reach the
// same ledger.
func (a assistantTools) nameSplitsFromTheOrder(
	ctx context.Context, arguments map[string]any, action *store.AssistantAction,
) {
	id, err := uuid.Parse(toolString(arguments, "transaction_id"))
	if err != nil {
		return
	}
	txn, err := a.env.DB.GetTransaction(ctx, a.sp.ID(), id)
	if err != nil {
		return
	}
	enrichment, err := merchantService(a.env).EnrichmentFor(ctx, a.sp.ID(), txn)
	if err != nil || enrichment == nil {
		return
	}
	items, shares := enrichment.Items(), enrichment.Shares()
	rows := bodySplits(action.Body)
	if shares == nil || len(items) != len(shares) {
		return
	}
	// One split per item: write each item's real name as the memo.
	if len(rows) == len(shares) {
		allMatch := true
		for i, row := range rows {
			amount, err := domain.FromString(toolString(row, "amount"))
			if err != nil || !amount.Equal(shares[i]) {
				allMatch = false
				break
			}
		}
		if allMatch {
			for i, row := range rows {
				row["memo"] = domain.ItemMemo(items[i].DisplayTitle())
			}
			return
		}
	}
}

// refuseMisSummedSplit names the mismatch itself, rather than leaving a split
// whose parts do not add up to the transaction's own amount to the real
// endpoint's generic conflict.
func (a assistantTools) refuseMisSummedSplit(ctx context.Context, arguments map[string]any) error {
	id, err := uuid.Parse(toolString(arguments, "transaction_id"))
	if err != nil {
		return nil
	}
	txn, err := a.env.DB.GetTransaction(ctx, a.sp.ID(), id)
	if err != nil {
		return nil
	}
	total := domain.Money{}
	for _, split := range toolSplits(arguments) {
		amount, err := domain.FromString(toolString(split, "amount"))
		if err != nil {
			return nil
		}
		total = total.Add(amount)
	}
	if total.Equal(txn.Amount.Round()) {
		return nil
	}
	return fmt.Errorf("the splits add up to %s, but the transaction is %s; fix an amount "+
		"rather than proposing it", total.String(), txn.Amount.String())
}

// refuseUnfaithfulMerchantSplit keeps a split of a matched row honest about
// the order's items. Items may be grouped, but every item's share (which
// already includes tax and shipping) must land in exactly one split, and no
// line may be invented for shipping, tax or fees.
func (a assistantTools) refuseUnfaithfulMerchantSplit(ctx context.Context, arguments map[string]any) error {
	id, err := uuid.Parse(toolString(arguments, "transaction_id"))
	if err != nil {
		return nil
	}
	txn, err := a.env.DB.GetTransaction(ctx, a.sp.ID(), id)
	if err != nil {
		return nil
	}
	enrichment, err := merchantService(a.env).EnrichmentFor(ctx, a.sp.ID(), txn)
	if err != nil || enrichment == nil {
		return nil
	}
	items := enrichment.Items()
	if len(items) == 1 {
		return fmt.Errorf("this %s %s has one item, and tax and shipping are part of what it "+
			"cost: the whole charge is %q. Do not split it; propose update_transaction with one "+
			"category", enrichment.Merchant().Name, enrichment.Merchant().Noun, items[0].DisplayTitle())
	}
	shares := enrichment.Shares()
	if shares == nil {
		return nil
	}
	// Each proposed split's amount must be the exact sum of one or more item
	// shares, and every item share must appear in exactly one split. The model
	// groups items by category, so the number of splits is <= items.
	available := make(map[string]int, len(shares))
	for _, share := range shares {
		available[share.String()]++
	}
	splits := toolSplits(arguments)
	if len(splits) == 0 || len(splits) > len(shares) {
		return splitGuideError(shares)
	}
	// Try to account for each split's amount as a sum of remaining shares.
	remaining := make(map[string]int, len(available))
	for k, v := range available {
		remaining[k] = v
	}
	for _, split := range splits {
		amount, err := domain.FromString(toolString(split, "amount"))
		if err != nil {
			return splitGuideError(shares)
		}
		// The simple case: the split's amount is one item's share exactly.
		if remaining[amount.String()] > 0 {
			remaining[amount.String()]--
			continue
		}
		// The grouped case: the split's amount is the sum of several shares.
		// Greedily match the largest shares first.
		target := amount
		matched := false
		trial := make(map[string]int, len(remaining))
		for k, v := range remaining {
			trial[k] = v
		}
		for !target.IsZero() {
			found := false
			for key, count := range trial {
				if count <= 0 {
					continue
				}
				share, _ := domain.FromString(key)
				// A split for a negative charge (a return) has negative amounts.
				if (target.IsNegative() && share.IsNegative() && share.Cmp(target) >= 0) ||
					(target.IsPositive() && share.IsPositive() && share.Cmp(target) <= 0) ||
					share.Equal(target) {
					target = target.Sub(share)
					trial[key]--
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
		if !target.IsZero() {
			return splitGuideError(shares)
		}
		matched = true
		if matched {
			for k, v := range trial {
				remaining[k] = v
			}
		}
	}
	// Every share must have been consumed.
	for _, count := range remaining {
		if count > 0 {
			return splitGuideError(shares)
		}
	}
	return nil
}

func splitGuideError(shares []domain.Money) error {
	want := make([]string, 0, len(shares))
	for _, share := range shares {
		want = append(want, share.String())
	}
	return fmt.Errorf("the split must account for every item on the order. The items' shares "+
		"are %s — each already includes tax and shipping. Group items by category: each split's "+
		"amount is the sum of its items' shares. Do not add a line for shipping, tax or fees",
		strings.Join(want, ", "))
}

// toolSplits reads the splits argument as a list of objects, tolerating the
// shapes a model produces.
func toolSplits(arguments map[string]any) []map[string]any {
	raw, _ := arguments["splits"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, one := range raw {
		if m, ok := one.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// buildAction turns one write tool's arguments into the request it stands for:
// a path this server serves and a body in the handler's own field names.
func buildAction(name string, arguments map[string]any) (store.AssistantAction, error) {
	action := store.AssistantAction{Summary: toolString(arguments, "summary")}

	switch name {
	case "update_transaction":
		id, err := toolUUID(arguments, "transaction_id")
		if err != nil {
			return action, err
		}
		body := map[string]any{}
		if err := putString(body, arguments, "payee", "payee"); err != nil {
			return action, err
		}
		if err := putString(body, arguments, "notes", "notes"); err != nil {
			return action, err
		}
		if err := putUUID(body, arguments, "category_id", "category_id"); err != nil {
			return action, err
		}
		if err := putUUIDs(body, arguments, "tag_ids", "tag_ids"); err != nil {
			return action, err
		}
		putBool(body, arguments, "is_reviewed", "is_reviewed")
		putBool(body, arguments, "excluded_from_reports", "excluded_from_reports")
		putBool(body, arguments, "excluded_from_spending_plan", "excluded_from_spending_plan")
		// A model sometimes sends every field empty. Empty means "not
		// changing", not "clear it": clearing is for a person.
		for _, field := range []string{"payee", "notes"} {
			if text, ok := body[field].(string); ok && strings.TrimSpace(text) == "" {
				delete(body, field)
			}
		}
		if tags, ok := body["tag_ids"].([]string); ok && len(tags) == 0 {
			delete(body, "tag_ids")
		}
		if len(body) == 0 {
			return action, fmt.Errorf("that would change nothing; say what to set")
		}
		action.Method, action.Path, action.Body = http.MethodPatch,
			"/transactions/"+id.String(), body

	case "split_transaction":
		id, err := toolUUID(arguments, "transaction_id")
		if err != nil {
			return action, err
		}
		splits := toolSplits(arguments)
		if len(splits) < 2 {
			return action, fmt.Errorf("a split needs at least two parts; give each its amount")
		}
		rows := make([]map[string]any, 0, len(splits))
		for _, split := range splits {
			amount, err := domain.FromString(toolString(split, "amount"))
			if err != nil {
				return action, fmt.Errorf("each split needs an amount like \"-30.00\"")
			}
			row := map[string]any{"amount": amount.String()}
			if err := putUUID(row, split, "category_id", "category_id"); err != nil {
				return action, err
			}
			// A part with no category is not a proposal about that part, and
			// the register cannot offer "uncategorized" as something to approve.
			if _, named := row["category_id"]; !named {
				return action, fmt.Errorf("each split part needs a category_id; leave a " +
					"part out rather than proposing it uncategorized")
			}
			if err := putString(row, split, "memo", "memo"); err != nil {
				return action, err
			}
			rows = append(rows, row)
		}
		action.Method, action.Path, action.Body = http.MethodPut,
			"/transactions/"+id.String()+"/splits", map[string]any{"splits": rows}
		action.Summary = toolString(arguments, "summary")

	case "create_transaction":
		accountID, err := toolUUID(arguments, "account_id")
		if err != nil {
			return action, err
		}
		date, err := toolRequiredDate(arguments, "date")
		if err != nil {
			return action, err
		}
		amount, err := toolMoney(arguments, "amount")
		if err != nil {
			return action, err
		}
		body := map[string]any{
			"account_id": accountID.String(), "date": date.String(), "amount": amount.String(),
		}
		if err := putString(body, arguments, "payee", "payee"); err != nil {
			return action, err
		}
		if err := putString(body, arguments, "notes", "notes"); err != nil {
			return action, err
		}
		if err := putUUID(body, arguments, "category_id", "category_id"); err != nil {
			return action, err
		}
		action.Method, action.Path, action.Body = http.MethodPost, "/transactions", body

	case "delete_transaction":
		id, err := toolUUID(arguments, "transaction_id")
		if err != nil {
			return action, err
		}
		action.Method, action.Path = http.MethodDelete, "/transactions/"+id.String()

	case "create_category":
		body := map[string]any{}
		if err := putName(body, arguments, "name", "name"); err != nil {
			return action, err
		}
		kind := strings.ToLower(toolString(arguments, "kind"))
		if kind != "income" && kind != "expense" {
			return action, fmt.Errorf("`kind` has to be \"income\" or \"expense\"")
		}
		body["kind"] = kind
		if err := putUUID(body, arguments, "parent_id", "parent_id"); err != nil {
			return action, err
		}
		action.Method, action.Path, action.Body = http.MethodPost, "/categories", body

	case "create_tag":
		body := map[string]any{}
		if err := putName(body, arguments, "name", "name"); err != nil {
			return action, err
		}
		action.Method, action.Path, action.Body = http.MethodPost, "/tags", body

	case "create_goal":
		accountID, err := toolUUID(arguments, "account_id")
		if err != nil {
			return action, err
		}
		amount, err := toolMoney(arguments, "target_amount")
		if err != nil {
			return action, err
		}
		body := map[string]any{
			"account_id": accountID.String(), "target_amount": amount.String(),
		}
		if err := putName(body, arguments, "name", "name"); err != nil {
			return action, err
		}
		if err := putString(body, arguments, "emoji", "emoji"); err != nil {
			return action, err
		}
		if err := putDate(body, arguments, "target_on", "target_on"); err != nil {
			return action, err
		}
		action.Method, action.Path, action.Body = http.MethodPost, "/goals", body

	case "update_goal":
		id, err := toolUUID(arguments, "goal_id")
		if err != nil {
			return action, err
		}
		body := map[string]any{}
		if err := putString(body, arguments, "name", "name"); err != nil {
			return action, err
		}
		if err := putDate(body, arguments, "target_on", "target_on"); err != nil {
			return action, err
		}
		if raw := toolString(arguments, "target_amount"); raw != "" {
			amount, err := toolMoney(arguments, "target_amount")
			if err != nil {
				return action, err
			}
			body["target_amount"] = amount.String()
		}
		if len(body) == 0 {
			return action, fmt.Errorf("that would change nothing; say what to set")
		}
		action.Method, action.Path, action.Body = http.MethodPatch, "/goals/"+id.String(), body

	case "change_endpoint":
		method := strings.ToUpper(toolString(arguments, "method"))
		switch method {
		case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		default:
			return action, fmt.Errorf(
				"`method` has to be POST, PATCH, PUT or DELETE, not %q", method)
		}
		path := normalizeDispatchPath(toolString(arguments, "path"))
		if path == "" {
			return action, fmt.Errorf("`path` is required; call list_endpoints for what there is")
		}
		if strings.Contains(path, "{") {
			return action, fmt.Errorf(
				"%q still has a placeholder in it — put a real id in place of it", path)
		}
		if err := refuseDeniedPath(path); err != nil {
			return action, err
		}
		body, err := toolObject(arguments, "body")
		if err != nil {
			return action, err
		}
		action.Method, action.Path, action.Body = method, path, body

	default:
		return action, fmt.Errorf("there is no change tool called %q", name)
	}

	return action, nil
}

// --- Applying ----------------------------------------------------------------

func readAction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	action, err := actionFromPath(env, r, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, actionResponseFor(r.Context(), env, sp, action))
}

// ApplyBody is what a person may change about a proposal before it goes out:
// categories, and nothing else. Letting Apply rewrite an amount or path would
// make the card a blank form against the API rather than a reviewed change.
//
// An absent `category_id` leaves the model's choice alone; `""` is the empty
// category.
type ApplyBody struct {
	CategoryID      *string                 `json:"category_id"`
	SplitCategories []SplitCategoryOverride `json:"split_categories"`
}

// SplitCategoryOverride names one part of a proposed split by its position in
// the list the card shows.
type SplitCategoryOverride struct {
	Index      int    `json:"index"`
	CategoryID string `json:"category_id"`
}

// categoryChange is one category somebody replaced, for the corrections log.
type categoryChange struct {
	// index is the split it was in, or -1 for the change's own category.
	index     int
	proposed  uuid.UUID
	chosen    uuid.UUID
	memo      string
	amount    domain.Money
	hasAmount bool
}

// applyAction issues the stored request, with the categories the person chose.
//
// Only a pending row can be claimed, so a double click or a second tab issues
// the request once. A changed category is written to the row before the
// request goes out, with the model's choice kept beside it, and recorded as a
// correction once the change lands.
func applyAction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	action, err := actionFromPath(env, r, sp)
	if err != nil {
		return err
	}
	if !applicable(action) {
		return errConflict("that change was already %s", action.Status)
	}
	var body ApplyBody
	if err := decodeOptionalBody(r, &body); err != nil {
		return err
	}
	changes, err := reviseCategories(&action, body)
	if err != nil {
		return err
	}
	for _, change := range changes {
		if err := checkCategory(r.Context(), env, sp, change.chosen); err != nil {
			return err
		}
	}
	// Read before the change lands: the request itself may rewrite the payee.
	subject := correctionSubject(r.Context(), env, sp, action)
	if len(changes) > 0 {
		if err := env.DB.ReviseAssistantAction(
			r.Context(), sp.ID(), action.ID, action.Body, action.ProposedBody); err != nil {
			if isNotFound(err) {
				return errConflict("somebody else decided that one first")
			}
			return err
		}
	}
	settled, err := applyStoredAction(r.Context(), env, sp, action)
	if err != nil {
		return err
	}
	if settled.Status == domain.AssistantActionApplied {
		recordCorrections(r.Context(), env, sp, settled, subject, changes)
		markSubjectReviewed(r.Context(), env, sp, settled)
	}
	recordOutcome(r.Context(), env, sp, settled)
	return writeJSON(w, http.StatusOK, actionResponseFor(r.Context(), env, sp, settled))
}

// applicable is a card Apply may claim: one still waiting, or one whose
// request was refused and so changed nothing, which is a retry.
func applicable(action store.AssistantAction) bool {
	return action.Status == domain.AssistantActionPending ||
		action.Status == domain.AssistantActionFailed
}

// ApplyManyBody names the cards "Accept all" applies.
type ApplyManyBody struct {
	ActionIDs []uuid.UUID `json:"action_ids"`
}

// ApplyManyResponse is every named card as it stands afterwards, in the order
// they were applied.
type ApplyManyResponse struct {
	Actions []ActionResponse `json:"actions"`
}

// applyManyActions is "Accept all": each card is applied as its own Apply
// would, one at a time, in proposal order, because a later card may name an
// id an earlier one creates. A card already decided is reported and left
// alone.
func applyManyActions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body ApplyManyBody
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if len(body.ActionIDs) == 0 {
		return errBadRequest("name the changes to apply")
	}
	if len(body.ActionIDs) > 2*maxBulk {
		return errBadRequest("at most %d changes at once", 2*maxBulk)
	}
	actions := make([]store.AssistantAction, 0, len(body.ActionIDs))
	seen := map[uuid.UUID]bool{}
	for _, id := range body.ActionIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		action, err := env.DB.GetAssistantAction(r.Context(), sp.ID(), sp.UserID(), id)
		if err != nil {
			return notFoundAs(err, "Change")
		}
		if action.Status == domain.AssistantActionApplying {
			if err := releaseStaleActions(r.Context(), env, sp, action.ConversationID); err != nil {
				return err
			}
			if action, err = env.DB.GetAssistantAction(r.Context(), sp.ID(), sp.UserID(), id); err != nil {
				return err
			}
		}
		actions = append(actions, action)
	}
	sort.SliceStable(actions, func(i, j int) bool {
		return actions[i].CreatedAt.Before(actions[j].CreatedAt)
	})

	out := ApplyManyResponse{Actions: make([]ActionResponse, 0, len(actions))}
	for _, action := range actions {
		if !applicable(action) {
			out.Actions = append(out.Actions, actionResponse(action))
			continue
		}
		settled, err := applyStoredAction(r.Context(), env, sp, action)
		if err != nil {
			if isConflict(err) {
				current, readErr := env.DB.GetAssistantAction(r.Context(), sp.ID(), sp.UserID(), action.ID)
				if readErr != nil {
					return readErr
				}
				out.Actions = append(out.Actions, actionResponse(current))
				continue
			}
			return err
		}
		if settled.Status == domain.AssistantActionApplied {
			markSubjectReviewed(r.Context(), env, sp, settled)
		}
		recordOutcome(r.Context(), env, sp, settled)
		out.Actions = append(out.Actions, actionResponse(settled))
	}
	return writeJSON(w, http.StatusOK, out)
}

// recordOutcome writes what happened to a card into its thread, as the
// application speaking, so the model's next answer knows it. Best effort: the
// decision has already landed.
func recordOutcome(ctx context.Context, env *Env, sp auth.SpaceContext, action store.AssistantAction) {
	line := outcomeLine(action)
	if line == "" {
		return
	}
	message := store.AssistantMessage{
		ConversationID: action.ConversationID, Role: domain.AssistantRoleAction, Content: line,
		ToolName: action.ToolName,
		ToolArguments: map[string]any{
			"action_id": action.ID.String(), "status": action.Status,
		},
	}
	if err := env.DB.AddAssistantMessage(ctx, sp.ID(), &message); err != nil {
		slog.WarnContext(ctx, "assistant: could not record what happened to a card",
			"action", action.ID, "error", err)
	}
}

// outcomeLine says what happened to one card, in the words the model reads.
func outcomeLine(action store.AssistantAction) string {
	what := fmt.Sprintf("%q (%s %s)", action.Summary, action.Method, action.Path)
	switch action.Status {
	case domain.AssistantActionApplied:
		line := "The person accepted " + what + " and it was applied."
		if action.ResourceID != uuid.Nil {
			line += " Its id is " + action.ResourceID.String() + "."
		}
		return line
	case domain.AssistantActionFailed:
		reason := textutil.ClipMarked(action.Result, 300)
		return fmt.Sprintf("The person accepted %s, but the app refused it (%d): %s. Nothing changed.",
			what, action.StatusCode, reason)
	case domain.AssistantActionDiscarded:
		line := "The person declined " + what + ". Nothing changed."
		if reason := strings.TrimSpace(action.DeclineReason); reason != "" {
			line += fmt.Sprintf(" Their reason: %q.", reason)
		}
		return line
	}
	return ""
}

// markSubjectReviewed ticks the row a person just decided a card about, since
// that is the same act the register's review tick records.
//
// Only on the Apply path: a change ApplyNow made without asking still needs a
// look. Best effort, since the change has already landed.
func markSubjectReviewed(
	ctx context.Context, env *Env, sp auth.SpaceContext, action store.AssistantAction,
) {
	id, ok := transactionIDFromPath(action.Path)
	if !ok {
		return
	}
	// A card that set the flag itself is not overruled, except a suggestion an
	// automation left on the row: automations are told the tick is the
	// person's act, and accepting the suggestion is that act.
	if _, stated := action.Body["is_reviewed"]; stated &&
		!(isRowSuggestion(action) && fromAutomation(ctx, env, sp, action)) {
		return
	}
	if err := env.DB.SetTransactionsReviewed(ctx, sp.ID(), []uuid.UUID{id}, true); err != nil {
		slog.WarnContext(ctx, "assistant: could not mark the applied row reviewed",
			"action", action.ID, "transaction", id, "error", err)
	}
}

// reviseCategories puts the person's categories into the request the card will
// issue, and reports which ones they changed. An override that names nothing
// in the proposal is refused: the page and the proposal have drifted, and
// applying the model's choice would apply what the person rejected.
func reviseCategories(action *store.AssistantAction, body ApplyBody) ([]categoryChange, error) {
	if body.CategoryID == nil && len(body.SplitCategories) == 0 {
		return nil, nil
	}
	revised, err := cloneBody(action.Body)
	if err != nil {
		return nil, err
	}

	var changes []categoryChange
	if body.CategoryID != nil {
		switch action.ToolName {
		case "update_transaction", "create_transaction":
		default:
			return nil, errBadRequest("that change does not file a transaction under a category")
		}
		chosen, err := parseOverride(*body.CategoryID)
		if err != nil {
			return nil, err
		}
		if was := bodyUUID(revised, "category_id"); was != chosen {
			changes = append(changes, categoryChange{index: -1, proposed: was, chosen: chosen})
			setCategory(revised, chosen)
		}
	}

	if len(body.SplitCategories) > 0 {
		if action.ToolName != "split_transaction" {
			return nil, errBadRequest("that change is not a split")
		}
		rows := bodySplits(revised)
		for _, override := range body.SplitCategories {
			if override.Index < 0 || override.Index >= len(rows) {
				return nil, errBadRequest("that split has no part %d", override.Index+1)
			}
			row := rows[override.Index]
			chosen, err := parseOverride(override.CategoryID)
			if err != nil {
				return nil, err
			}
			was := bodyUUID(row, "category_id")
			if was == chosen {
				continue
			}
			change := categoryChange{
				index: override.Index, proposed: was, chosen: chosen,
				memo: toolString(row, "memo"),
			}
			if amount, err := domain.FromString(toolString(row, "amount")); err == nil {
				change.amount, change.hasAmount = amount, true
			}
			changes = append(changes, change)
			setCategory(row, chosen)
		}
	}

	if len(changes) == 0 {
		return nil, nil
	}
	action.ProposedBody, action.Body = action.Body, revised
	return changes, nil
}

// parseOverride reads a chosen category: an id, or "" for none at all.
func parseOverride(raw string) (uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errBadRequest("%q is not a category id", raw)
	}
	return id, nil
}

// setCategory writes a chosen category into a request body. Nil is JSON null:
// on a patch, absent means "leave it" and null means "no category".
func setCategory(body map[string]any, id uuid.UUID) {
	if id == uuid.Nil {
		body["category_id"] = nil
		return
	}
	body["category_id"] = id.String()
}

func bodyUUID(body map[string]any, key string) uuid.UUID {
	id, err := uuid.Parse(toolString(body, key))
	if err != nil {
		return uuid.Nil
	}
	return id
}

// bodySplits reads the splits out of a stored body, as []map[string]any when
// built in process or []any when read back from the database.
func bodySplits(body map[string]any) []map[string]any {
	switch rows := body["splits"].(type) {
	case []map[string]any:
		return rows
	case []any:
		out := make([]map[string]any, 0, len(rows))
		for _, one := range rows {
			if row, ok := one.(map[string]any); ok {
				out = append(out, row)
			}
		}
		return out
	}
	return nil
}

// cloneBody copies a request body deeply, so revising it leaves the model's
// proposal untouched beside it.
func cloneBody(body map[string]any) (map[string]any, error) {
	if body == nil {
		return nil, errBadRequest("that change carries nothing to alter")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// correctionFacts is the row a correction was about, as it read at the time.
type correctionFacts struct {
	transactionID uuid.UUID
	statementName string
	payee         string
	amount        domain.Money
	hasAmount     bool
}

// correctionSubject reads the transaction a card is about, from the
// /transactions/{id} path, or from the body for a card that creates a row.
func correctionSubject(
	ctx context.Context, env *Env, sp auth.SpaceContext, action store.AssistantAction,
) correctionFacts {
	if id, ok := transactionIDFromPath(action.Path); ok {
		txn, err := env.DB.GetTransaction(ctx, sp.ID(), id)
		if err != nil {
			return correctionFacts{transactionID: id}
		}
		return correctionFacts{
			transactionID: txn.ID, statementName: txn.StatementName, payee: txn.Payee,
			amount: txn.Amount, hasAmount: true,
		}
	}
	facts := correctionFacts{payee: toolString(action.Body, "payee")}
	if amount, err := domain.FromString(toolString(action.Body, "amount")); err == nil {
		facts.amount, facts.hasAmount = amount, true
	}
	return facts
}

// settleRowSuggestions declines what is still suggested for a row a person
// has just filed themselves, so the register stops offering it. A category
// the person chose over the model's is recorded as a correction, as it would
// be at Accept. Best effort: the edit has already landed.
func settleRowSuggestions(
	ctx context.Context, env *Env, sp auth.SpaceContext, subject correctionFacts, chosen uuid.UUID,
) {
	declined, err := env.DB.DeclineRowSuggestions(ctx, sp.ID(), subject.transactionID,
		"They filed the transaction themselves.")
	if err != nil {
		slog.WarnContext(ctx, "assistant: could not settle a row's suggestions",
			"transaction", subject.transactionID, "error", err)
		return
	}
	for _, action := range declined {
		recordOutcome(ctx, env, sp, action)
		if action.ToolName != "update_transaction" {
			continue
		}
		if proposed := bodyUUID(action.Body, "category_id"); proposed != chosen {
			recordCorrections(ctx, env, sp, action, subject,
				[]categoryChange{{index: -1, proposed: proposed, chosen: chosen}})
		}
	}
}

// isRowSuggestion is a card the register draws in a row's category cell, as
// store.PendingActionsForTransactions selects them.
func isRowSuggestion(action store.AssistantAction) bool {
	if _, ok := transactionIDFromPath(action.Path); !ok {
		return false
	}
	switch action.ToolName {
	case "mark_reviewed":
		return false
	case "update_transaction", "update_transactions":
		_, named := action.Body["category_id"]
		return named
	}
	return true
}

// fromAutomation reports whether an automation run wrote the card's thread.
func fromAutomation(
	ctx context.Context, env *Env, sp auth.SpaceContext, action store.AssistantAction,
) bool {
	thread, err := env.DB.GetAssistantConversation(ctx, sp.ID(), sp.UserID(), action.ConversationID)
	return err == nil && thread.AutomationRunID != uuid.Nil
}

func transactionIDFromPath(path string) (uuid.UUID, bool) {
	rest, found := strings.CutPrefix(path, "/transactions/")
	if !found {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.SplitN(rest, "/", 2)[0])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// recordCorrections writes the log a later run is shown. Best effort: the
// change has already been made.
func recordCorrections(
	ctx context.Context, env *Env, sp auth.SpaceContext, action store.AssistantAction,
	subject correctionFacts, changes []categoryChange,
) {
	if len(changes) == 0 {
		return
	}
	rows := make([]store.AssistantCorrection, 0, len(changes))
	for _, change := range changes {
		one := store.AssistantCorrection{
			ActionID: action.ID, TransactionID: subject.transactionID,
			StatementName: subject.statementName, Payee: subject.payee,
			Amount: subject.amount, HasAmount: subject.hasAmount,
			ToolName: action.ToolName, Memo: change.memo,
			ProposedCategoryID: change.proposed, ChosenCategoryID: change.chosen,
			CorrectedBy: sp.UserID(),
		}
		if change.hasAmount {
			one.Amount, one.HasAmount = change.amount, true
		}
		rows = append(rows, one)
	}
	if err := env.DB.CreateAssistantCorrections(ctx, sp.ID(), rows); err != nil {
		slog.Warn("recording an assistant correction failed", "action", action.ID, "error", err)
	}
}

// applyStoredAction issues one recorded request and settles the row. The one
// implementation, shared by the Apply button and by ApplyNow.
//
// The row is claimed before the request is issued, and only a pending or
// failed row can be claimed, so concurrent presses issue it once. Once claimed
// the row is always settled: a request that could not be built is a failed
// card with the reason, not one stuck running.
func applyStoredAction(
	ctx context.Context, env *Env, sp auth.SpaceContext, action store.AssistantAction,
) (store.AssistantAction, error) {
	claimed, err := env.DB.ClaimAssistantAction(ctx, sp.ID(), action.ID)
	if err != nil {
		if isNotFound(err) {
			return store.AssistantAction{}, errConflict("somebody else decided that one first")
		}
		return store.AssistantAction{}, err
	}
	fail := func(result string) (store.AssistantAction, error) {
		if err := env.DB.FinishAssistantAction(ctx, sp.ID(), claimed.ID,
			domain.AssistantActionFailed, result, 0, uuid.Nil); err != nil {
			return store.AssistantAction{}, err
		}
		return env.DB.GetAssistantAction(ctx, sp.ID(), sp.UserID(), claimed.ID)
	}

	requestBody, err := fillPlaceholders(ctx, env, sp, claimed.Body)
	if err != nil {
		return fail(err.Error())
	}
	var body []byte
	if len(requestBody) > 0 {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fail(err.Error())
		}
		body = encoded
	}
	response, err := env.dispatch(ctx, sp, claimed.Method, claimed.Path, nil, body)
	if err != nil {
		return fail(err.Error())
	}

	status, result := domain.AssistantActionApplied, strings.TrimSpace(response.Body)
	resource := uuid.Nil
	if response.OK() {
		resource = createdResource(response, claimed.Path)
	} else {
		status = domain.AssistantActionFailed
	}
	result = textutil.ClipMarked(result, 4000)
	if err := env.DB.FinishAssistantAction(
		ctx, sp.ID(), claimed.ID, status, result, response.Status, resource); err != nil {
		return store.AssistantAction{}, err
	}
	return env.DB.GetAssistantAction(ctx, sp.ID(), sp.UserID(), claimed.ID)
}

// fillPlaceholders puts the real id in place of every "@action:<id>" in a
// request body: the row another card in the thread created. If that card has
// not been applied this fails, naming it. The stored body keeps its
// placeholder.
func fillPlaceholders(
	ctx context.Context, env *Env, sp auth.SpaceContext, body map[string]any,
) (map[string]any, error) {
	if body == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(encoded), actionPlaceholder) {
		return body, nil
	}
	var fill func(value any) (any, error)
	fill = func(value any) (any, error) {
		switch v := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			for key, inner := range v {
				filled, err := fill(inner)
				if err != nil {
					return nil, err
				}
				out[key] = filled
			}
			return out, nil
		case []any:
			out := make([]any, 0, len(v))
			for _, inner := range v {
				filled, err := fill(inner)
				if err != nil {
					return nil, err
				}
				out = append(out, filled)
			}
			return out, nil
		case string:
			rest, found := strings.CutPrefix(v, actionPlaceholder)
			if !found {
				return v, nil
			}
			id, err := uuid.Parse(rest)
			if err != nil {
				return nil, fmt.Errorf("%q names no change", v)
			}
			dependency, err := env.DB.GetAssistantAction(ctx, sp.ID(), sp.UserID(), id)
			if err != nil {
				return nil, fmt.Errorf("this depends on a change that is no longer here")
			}
			if dependency.Status != domain.AssistantActionApplied || dependency.ResourceID == uuid.Nil {
				return nil, fmt.Errorf("this depends on %q, which has not been applied yet. "+
					"Apply that first, then try this again", dependency.Summary)
			}
			return dependency.ResourceID.String(), nil
		}
		return value, nil
	}
	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, err
	}
	filled, err := fill(generic)
	if err != nil {
		return nil, err
	}
	return filled.(map[string]any), nil
}

// createdResource is the row a request created or changed: the `id` its answer
// carries, or failing that the id in its own path.
func createdResource(response dispatchResponse, path string) uuid.UUID {
	if response.IsJSON() {
		var answered struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(response.Body), &answered); err == nil {
			if id, err := uuid.Parse(answered.ID); err == nil {
				return id
			}
		}
	}
	if id, ok := subjectID(path); ok {
		return id
	}
	return uuid.Nil
}

// subjectID is the id a path is about: the second segment of
// "/rules/{id}/…", when it is one.
func subjectID(path string) (uuid.UUID, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for _, segment := range segments[1:] {
		if id, err := uuid.Parse(segment); err == nil {
			return id, true
		}
	}
	return uuid.Nil, false
}

// DeclineBody is optional: why the person said no, for the model to read.
type DeclineBody struct {
	Reason string `json:"reason"`
}

func discardAction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	action, err := actionFromPath(env, r, sp)
	if err != nil {
		return err
	}
	var body DeclineBody
	if err := decodeOptionalBody(r, &body); err != nil {
		return err
	}
	reason := textutil.Clip(strings.TrimSpace(body.Reason), 1000)
	if err := env.DB.DeclineAssistantAction(r.Context(), sp.ID(), action.ID, reason); err != nil {
		if isNotFound(err) {
			return errConflict("that change was already decided")
		}
		return err
	}
	settled, err := env.DB.GetAssistantAction(r.Context(), sp.ID(), sp.UserID(), action.ID)
	if err != nil {
		return err
	}
	recordOutcome(r.Context(), env, sp, settled)
	return writeJSON(w, http.StatusOK, actionResponseFor(r.Context(), env, sp, settled))
}

func actionFromPath(
	env *Env, r *http.Request, sp auth.SpaceContext,
) (store.AssistantAction, error) {
	id, err := pathUUID(r, "action_id", "Change")
	if err != nil {
		return store.AssistantAction{}, err
	}
	action, err := env.DB.GetAssistantAction(r.Context(), sp.ID(), sp.UserID(), id)
	if err != nil {
		return store.AssistantAction{}, notFoundAs(err, "Change")
	}
	if action.Status == domain.AssistantActionApplying {
		if err := releaseStaleActions(r.Context(), env, sp, action.ConversationID); err != nil {
			return store.AssistantAction{}, err
		}
		return env.DB.GetAssistantAction(r.Context(), sp.ID(), sp.UserID(), id)
	}
	return action, nil
}

// staleApplying is how long a card may say it is running before it is taken
// to have been interrupted by a process that died mid-request.
const staleApplying = 2 * time.Minute

// staleResult does not claim nothing changed: the request may have landed.
const staleResult = "Result unknown: interrupted before the app answered. Check whether the " +
	"change landed before retrying."

// releaseStaleActions settles a conversation's interrupted cards as failed, so
// they offer Try again and Decline. Run whenever cards are read.
func releaseStaleActions(
	ctx context.Context, env *Env, sp auth.SpaceContext, conversationID uuid.UUID,
) error {
	_, err := env.DB.ReleaseStaleAssistantActions(ctx, sp.ID(), conversationID,
		env.now().Add(-staleApplying), staleResult)
	return err
}

// --- Argument reading --------------------------------------------------------
//
// Each of these refuses rather than coercing, so the model is told on the
// round it can still act on.

func toolUUID(arguments map[string]any, key string) (uuid.UUID, error) {
	raw := toolString(arguments, key)
	if raw == "" {
		return uuid.Nil, fmt.Errorf("`%s` is required", key)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("`%s` has to be an id, not %q — read one from a tool first",
			key, raw)
	}
	return id, nil
}

func toolMoney(arguments map[string]any, key string) (domain.Money, error) {
	raw := toolString(arguments, key)
	if raw == "" {
		return domain.Zero, fmt.Errorf("`%s` is required, as a string such as \"42.10\"", key)
	}
	amount, err := parseMoney(raw)
	if err != nil {
		return domain.Zero, fmt.Errorf("`%s` has to be an amount such as \"42.10\", not %q",
			key, raw)
	}
	return amount, nil
}

func toolRequiredDate(arguments map[string]any, key string) (domain.Date, error) {
	raw := toolString(arguments, key)
	if raw == "" {
		return domain.Date{}, fmt.Errorf("`%s` is required, as YYYY-MM-DD", key)
	}
	parsed, err := parseDate(raw)
	if err != nil {
		return domain.Date{}, fmt.Errorf("`%s` has to be a date like 2026-08-01, not %q", key, raw)
	}
	return parsed, nil
}

func putString(body, arguments map[string]any, key, field string) error {
	raw, present := arguments[key]
	if !present || raw == nil {
		return nil
	}
	value, ok := raw.(string)
	if !ok {
		return fmt.Errorf("`%s` has to be text", key)
	}
	body[field] = value
	return nil
}

// putName is putString for a field that must not be blank.
func putName(body, arguments map[string]any, key, field string) error {
	if err := putString(body, arguments, key, field); err != nil {
		return err
	}
	if text, _ := body[field].(string); strings.TrimSpace(text) == "" {
		return fmt.Errorf("`%s` is required", key)
	}
	return nil
}

func putBool(body, arguments map[string]any, key, field string) {
	if value, ok := arguments[key].(bool); ok {
		body[field] = value
	}
}

func putUUID(body, arguments map[string]any, key, field string) error {
	if toolString(arguments, key) == "" {
		return nil
	}
	if value := toolString(arguments, key); strings.HasPrefix(value, actionPlaceholder) {
		body[field] = value
		return nil
	}
	id, err := toolUUID(arguments, key)
	if err != nil {
		return err
	}
	body[field] = id.String()
	return nil
}

func putUUIDs(body, arguments map[string]any, key, field string) error {
	raw, present := arguments[key]
	if !present || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("`%s` has to be a list of ids", key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return fmt.Errorf("`%s` has to be a list of ids", key)
		}
		if strings.HasPrefix(text, actionPlaceholder) {
			out = append(out, text)
			continue
		}
		id, err := uuid.Parse(text)
		if err != nil {
			return fmt.Errorf("`%s` holds %q, which is not an id", key, text)
		}
		out = append(out, id.String())
	}
	body[field] = out
	return nil
}

func putDate(body, arguments map[string]any, key, field string) error {
	raw := toolString(arguments, key)
	if raw == "" {
		return nil
	}
	parsed, err := parseDate(raw)
	if err != nil {
		return fmt.Errorf("`%s` has to be a date like 2026-08-01, not %q", key, raw)
	}
	body[field] = parsed.String()
	return nil
}

func toolObject(arguments map[string]any, key string) (map[string]any, error) {
	raw, present := arguments[key]
	if !present || raw == nil {
		return nil, nil
	}
	body, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`%s` has to be an object", key)
	}
	return body, nil
}
