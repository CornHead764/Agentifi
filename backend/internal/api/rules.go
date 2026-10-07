package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Rules — *If a transaction matches this* ⁄ *Then make these changes*.
//
// The conditions are a filter (ground rule 3), written through the same
// buildFilterItems as /filters. Inline conditions get a filter of their own,
// scoped "rule", which the rule owns and edits in place; a rule pointing at a
// shared filter is refused the inline edit, which would rewrite whatever else
// reads it.
//
// Rules run lowest priority first and the first to set a field wins; a new rule
// lands last.
//
// Nothing reaches existing rows without an explicit apply: GET /{id}/preview
// writes nothing, and POST /{id}/apply commits for the rows the caller names,
// so a row arriving between the two is not swept in.
//
// Matching reads statement_name and the rename writes payee (ground rule 5), so
// a rule matching on the payee stops firing once its own rename lands. Both are
// allowed, as in Simplifi.

func init() {
	Register(Resource{Prefix: "/rules", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listRules)
		rt.Write(http.MethodPost, "/", createRule)
		rt.Write(http.MethodPost, "/reorder", reorderRules)
		rt.Read(http.MethodGet, "/{rule_id}", readRule)
		rt.Write(http.MethodPatch, "/{rule_id}", updateRule)
		rt.Write(http.MethodDelete, "/{rule_id}", deleteRule)
		rt.Read(http.MethodGet, "/{rule_id}/preview", previewRule)
		rt.Write(http.MethodPost, "/{rule_id}/apply", applyRule)
	}})
}

// RuleFilterScope marks the filters this resource creates and owns.
const RuleFilterScope = "rule"

// maxPreviewChanges bounds one preview response. The counts stay exact.
const maxPreviewChanges = 500

// --- Wire types --------------------------------------------------------------

// RuleActionsWire is the *Then make these changes* column, in both directions.
// Null means "leave this alone", which for the flags differs from false
// (*Include everywhere* is a real action). The two exclusions are never
// combined.
type RuleActionsWire struct {
	SetPayee      *string     `json:"set_payee"`
	SetCategoryID *uuid.UUID  `json:"set_category_id"`
	AddTagIDs     []uuid.UUID `json:"add_tag_ids"`
	SetNotes      *string     `json:"set_notes"`

	SetExcludedFromReports      *bool `json:"set_excluded_from_reports"`
	SetExcludedFromSpendingPlan *bool `json:"set_excluded_from_spending_plan"`
	SetIsReviewed               *bool `json:"set_is_reviewed"`
}

// RuleActionsWrite is the same column on a patch: absent leaves the stored
// action, null clears it.
type RuleActionsWrite struct {
	SetPayee      Opt[string]    `json:"set_payee"`
	SetCategoryID Opt[uuid.UUID] `json:"set_category_id"`
	AddTagIDs     *[]uuid.UUID   `json:"add_tag_ids"`
	SetNotes      Opt[string]    `json:"set_notes"`

	SetExcludedFromReports      Opt[bool] `json:"set_excluded_from_reports"`
	SetExcludedFromSpendingPlan Opt[bool] `json:"set_excluded_from_spending_plan"`
	SetIsReviewed               Opt[bool] `json:"set_is_reviewed"`
}

type RuleResponse struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Filter is inlined so the settings list can render every rule's chips
	// without a fetch per row.
	FilterID uuid.UUID      `json:"filter_id"`
	Filter   FilterResponse `json:"filter"`
	// OwnsFilter reports a filter this rule created and may edit in place. A
	// shared one is edited through /filters.
	OwnsFilter bool            `json:"owns_filter"`
	Priority   int             `json:"priority"`
	IsActive   bool            `json:"is_active"`
	Actions    RuleActionsWire `json:"actions"`
}

type RuleCreate struct {
	Name string `json:"name"`
	// FilterID points at an existing saved filter; Conditions is the builder's
	// own clause list. Exactly one of the two.
	FilterID   *uuid.UUID        `json:"filter_id"`
	Conditions []FilterItemWrite `json:"conditions"`
	IsActive   *bool             `json:"is_active"`
	Actions    RuleActionsWrite  `json:"actions"`
}

type RuleUpdate struct {
	Name       Opt[string]        `json:"name"`
	FilterID   Opt[uuid.UUID]     `json:"filter_id"`
	Conditions *[]FilterItemWrite `json:"conditions"`
	// IsActive false stops the rule firing on what syncs next and touches
	// nothing it already did: a rule is not an undo.
	IsActive Opt[bool]         `json:"is_active"`
	Actions  *RuleActionsWrite `json:"actions"`
}

// RuleReorder is the whole live set in the order it should run. Partial lists
// are refused, as they leave an order nobody chose.
type RuleReorder struct {
	RuleIDs []uuid.UUID `json:"rule_ids"`
}

// RuleChangeWire is one row the preview would move: enough of the row to
// recognize it, including the statement name it matched on, and only the
// fields that change.
type RuleChangeWire struct {
	TransactionID uuid.UUID    `json:"transaction_id"`
	Date          Date         `json:"date"`
	AccountName   string       `json:"account_name"`
	StatementName string       `json:"statement_name"`
	Payee         string       `json:"payee"`
	Amount        domain.Money `json:"amount"`

	Actions RuleActionsWire `json:"actions"`
}

type RulePreviewResponse struct {
	RuleID uuid.UUID `json:"rule_id"`
	// Matched is every row the conditions select; Changed is the subset the
	// actions actually move.
	Matched   int  `json:"matched"`
	Changed   int  `json:"changed"`
	Unchanged int  `json:"unchanged"`
	Truncated bool `json:"truncated"`
	// Since echoes the window the preview ran over, so the apply that follows
	// can send the same one.
	Since   *Date            `json:"since"`
	Changes []RuleChangeWire `json:"changes"`
}

// RuleApply commits a preview. TransactionIDs bounds it to the rows the user
// approved; omitted, it applies to every match in the window ("apply to all").
type RuleApply struct {
	Since          *Date       `json:"since"`
	TransactionIDs []uuid.UUID `json:"transaction_ids"`
}

type RuleApplyResponse struct {
	RuleID  uuid.UUID `json:"rule_id"`
	Matched int       `json:"matched"`
	Applied int       `json:"applied"`
}

// --- Handlers ----------------------------------------------------------------

func listRules(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListRules(r.Context(), sp.ID(), store.RuleQuery{})
	if err != nil {
		return err
	}
	out := make([]RuleResponse, 0, len(rows))
	for _, row := range rows {
		response, err := ruleResponse(r.Context(), env, sp, row)
		if err != nil {
			return err
		}
		out = append(out, response)
	}
	return writeJSON(w, http.StatusOK, out)
}

func createRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body RuleCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Name) == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if (body.FilterID == nil) == (len(body.Conditions) == 0) {
		// Neither would be a rule rewriting every row; both is ambiguous.
		return errInvalid("missing", []string{"body", "conditions"},
			"send either filter_id or a non-empty conditions")
	}

	rule := &store.Rule{Name: strings.TrimSpace(body.Name), IsActive: true}
	if body.IsActive != nil {
		rule.IsActive = *body.IsActive
	}
	if err := applyRuleActions(r.Context(), env, sp, rule, body.Actions); err != nil {
		return err
	}
	if err := checkRuleActs(*rule); err != nil {
		return err
	}

	if body.FilterID != nil {
		filter, err := requireRuleFilter(r.Context(), env, sp, *body.FilterID)
		if err != nil {
			return err
		}
		rule.FilterID = filter.ID
	} else {
		filter, err := ruleConditions.writeOwnedFilter(
			r.Context(), env, sp, rule.Name, body.Conditions)
		if err != nil {
			return err
		}
		rule.FilterID = filter.ID
	}

	priority, err := env.DB.NextRulePriority(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	rule.Priority = priority
	if err := env.DB.CreateRule(r.Context(), sp.ID(), rule); err != nil {
		return err
	}

	response, err := ruleResponse(r.Context(), env, sp, *rule)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, response)
}

func readRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rule, err := liveRule(r, env, sp)
	if err != nil {
		return err
	}
	response, err := ruleResponse(r.Context(), env, sp, rule)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, response)
}

func updateRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rule, err := liveRule(r, env, sp)
	if err != nil {
		return err
	}
	var body RuleUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyRequired("name", body.Name, &rule.Name); err != nil {
		return err
	}
	if err := applyRequired("is_active", body.IsActive, &rule.IsActive); err != nil {
		return err
	}
	if body.FilterID.Cleared() {
		return errConflict("filter_id cannot be cleared")
	}
	if body.FilterID.Present() {
		filter, err := requireRuleFilter(r.Context(), env, sp, body.FilterID.Value)
		if err != nil {
			return err
		}
		rule.FilterID = filter.ID
	}
	if body.Actions != nil {
		if err := applyRuleActions(r.Context(), env, sp, &rule, *body.Actions); err != nil {
			return err
		}
	}
	if err := checkRuleActs(rule); err != nil {
		return err
	}

	if body.Conditions != nil {
		if err := ruleConditions.replaceOwnedConditions(
			r.Context(), env, sp, rule.ID, rule.FilterID, *body.Conditions); err != nil {
			return err
		}
	}
	if err := env.DB.UpdateRule(r.Context(), sp.ID(), &rule); err != nil {
		return err
	}

	response, err := ruleResponse(r.Context(), env, sp, rule)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, response)
}

func deleteRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rule, err := liveRule(r, env, sp)
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteRule(r.Context(), sp.ID(), rule.ID), "Rule")
}

func reorderRules(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body RuleReorder
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	live, err := env.DB.ListRules(r.Context(), sp.ID(), store.RuleQuery{})
	if err != nil {
		return err
	}
	if err := checkReorderCovers(live, body.RuleIDs); err != nil {
		return err
	}
	if err := env.DB.ReorderRules(r.Context(), sp.ID(), body.RuleIDs); err != nil {
		return notFoundAs(err, "Rule")
	}
	return listRules(env, w, r, sp)
}

// checkReorderCovers refuses a partial order. Every live rule has to appear
// exactly once, because a priority is only meaningful relative to the others.
func checkReorderCovers(live []store.Rule, ids []uuid.UUID) error {
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return errConflict("rule %s appears twice in rule_ids", id)
		}
		seen[id] = true
	}
	if len(ids) != len(live) {
		return errConflict("rule_ids must list all %d rules, got %d", len(live), len(ids))
	}
	for _, rule := range live {
		if !seen[rule.ID] {
			return errConflict("rule_ids does not list rule %s", rule.ID)
		}
	}
	return nil
}

// previewRule answers "what happens if I run this over what I already have?"
// and writes nothing. A viewer may ask: the answer is a read of the ledger.
func previewRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rule, err := liveRule(r, env, sp)
	if err != nil {
		return err
	}
	since, hasSince, err := queryDate(r, "since")
	if err != nil {
		return err
	}

	plan, err := planRuleRun(r.Context(), env, sp, rule.ID, service.CandidateQuery{Since: since})
	if err != nil {
		return err
	}
	out := RulePreviewResponse{
		RuleID:    plan.preview.RuleID,
		Matched:   plan.preview.Matched,
		Changed:   len(plan.preview.Changes),
		Unchanged: plan.preview.Unchanged(),
		Changes:   make([]RuleChangeWire, 0, len(plan.preview.Changes)),
	}
	if hasSince {
		wire := Date(since)
		out.Since = &wire
	}
	for _, change := range plan.preview.Changes {
		if len(out.Changes) == maxPreviewChanges {
			out.Truncated = true
			break
		}
		out.Changes = append(out.Changes, ruleChangeWire(change, plan.rows[change.TransactionID]))
	}
	return writeJSON(w, http.StatusOK, out)
}

// applyRule commits the preview for the rows the caller approved. The diff is
// re-planned rather than trusted from the body, over exactly the named rows.
func applyRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rule, err := liveRule(r, env, sp)
	if err != nil {
		return err
	}
	var body RuleApply
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	query := service.CandidateQuery{}
	if body.TransactionIDs != nil {
		query.TransactionIDs = body.TransactionIDs
	} else if body.Since != nil {
		query.Since = domain.Date(*body.Since)
	}

	plan, err := planRuleRun(r.Context(), env, sp, rule.ID, query)
	if err != nil {
		return err
	}
	applied, err := plan.rules.ApplyChanges(r.Context(), sp.ID(), plan.preview.Changes)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, RuleApplyResponse{
		RuleID:  rule.ID,
		Matched: plan.preview.Matched,
		Applied: applied,
	})
}

// --- The run ------------------------------------------------------------------

// rulePlan is one rule run that has decided everything and written nothing.
// rows is kept so the preview can name them; a RuleChange carries only fields.
type rulePlan struct {
	rules   *service.Rules
	preview service.RulePreview
	rows    map[uuid.UUID]service.RuleCandidate
}

// planRuleRun is service.PreviewRule with the matched rows indexed by id.
func planRuleRun(
	ctx context.Context,
	env *Env,
	sp auth.SpaceContext,
	ruleID uuid.UUID,
	query service.CandidateQuery,
) (rulePlan, error) {
	rules := service.NewRules(env.DB)
	preview, err := rules.PreviewRule(ctx, sp.ID(), ruleID, query)
	if err != nil {
		return rulePlan{}, ruleLoadError(err)
	}
	rows := make(map[uuid.UUID]service.RuleCandidate, len(preview.Rows))
	for _, row := range preview.Rows {
		rows[row.ID] = row
	}
	return rulePlan{rules: rules, preview: preview, rows: rows}, nil
}

// ruleLoadError turns the engine's refusal to prepare a rule into a 409 naming
// the reason, rather than a 500.
func ruleLoadError(err error) error {
	if err == nil || isNotFound(err) {
		return err
	}
	if strings.HasPrefix(err.Error(), "service: rule ") {
		return errConflict("%s", strings.TrimPrefix(err.Error(), "service: "))
	}
	return err
}

// --- Conditions ---------------------------------------------------------------

// conditionOwner is what differs between the two builders that own a filter, a
// rule and a guidance note: the scope the filter is stamped with, and the noun
// the refusals use.
type conditionOwner struct {
	scope string
	noun  string
}

var (
	ruleConditions     = conditionOwner{scope: RuleFilterScope, noun: "rule"}
	guidanceConditions = conditionOwner{scope: GuidanceFilterScope, noun: "guidance note"}
)

// writeOwnedFilter stores a builder's clause list as a filter of its own.
func (o conditionOwner) writeOwnedFilter(
	ctx context.Context, env *Env, sp auth.SpaceContext, name string, writes []FilterItemWrite,
) (*store.Filter, error) {
	items, err := buildRuleItems(o.noun, writes)
	if err != nil {
		return nil, err
	}
	filter := &store.Filter{Name: name, Scope: o.scope, Items: items}
	if err := env.DB.CreateFilter(ctx, sp.ID(), filter); err != nil {
		return nil, err
	}
	return filter, nil
}

// replaceOwnedConditions edits the conditions of a row that owns its filter,
// and refuses one that points at a shared saved filter.
func (o conditionOwner) replaceOwnedConditions(
	ctx context.Context, env *Env, sp auth.SpaceContext, id, filterID uuid.UUID,
	writes []FilterItemWrite,
) error {
	filter, err := requireFilter(ctx, env, sp, filterID)
	if err != nil {
		return err
	}
	if filter.Scope != o.scope {
		return errConflict(
			"%s %s points at a shared filter; edit its conditions through /filters", o.noun, id)
	}
	items, err := buildRuleItems(o.noun, writes)
	if err != nil {
		return err
	}
	filter.Items = items
	return env.DB.ReplaceFilterItems(ctx, sp.ID(), &filter)
}

// buildRuleItems is /filters' own item builder with the two conditions a rule
// cannot carry refused: an empty clause list, which matches nothing in the
// engine (unlike an empty register filter), and the bill/subscription facet,
// which is the series matcher's verdict and fails silently in the evaluator.
// /guidance builds through here too, with `what` naming the thing saved.
func buildRuleItems(what string, writes []FilterItemWrite) ([]store.FilterItem, error) {
	if len(writes) == 0 {
		return nil, errInvalid("missing", []string{"body", "conditions"},
			"a %s needs at least one condition", what)
	}
	for _, write := range writes {
		if domain.RuleCannotMatch(write.Field) {
			return nil, errInvalid("enum", []string{"body", "conditions", "field"},
				"a %s cannot match on %s", what, write.Field)
		}
	}
	return buildFilterItems(writes)
}

// --- Actions -------------------------------------------------------------------

func applyRuleActions(
	ctx context.Context, env *Env, sp auth.SpaceContext, rule *store.Rule, body RuleActionsWrite,
) error {
	applyNullable(body.SetPayee, &rule.SetPayee)
	applyNullable(body.SetNotes, &rule.SetNotes)
	applyNullable(body.SetCategoryID, &rule.SetCategoryID)
	if err := checkCategory(ctx, env, sp, rule.SetCategoryID); err != nil {
		return err
	}
	if body.AddTagIDs != nil {
		tags, err := resolveTags(ctx, env, sp, *body.AddTagIDs)
		if err != nil {
			return err
		}
		rule.AddTagIDs = tags
	}
	applyTriState(body.SetExcludedFromReports, &rule.SetExcludedFromReports)
	applyTriState(body.SetExcludedFromSpendingPlan, &rule.SetExcludedFromSpendingPlan)
	applyTriState(body.SetIsReviewed, &rule.SetIsReviewed)
	return nil
}

// applyTriState writes the three-state action flags, where null on the wire is
// "leave the row alone" and is not the same as false.
func applyTriState(opt Opt[bool], dst **bool) {
	if !opt.Set {
		return
	}
	if opt.Null {
		*dst = nil
		return
	}
	value := opt.Value
	*dst = &value
}

// checkRuleActs refuses a rule that would do nothing, which would otherwise
// preview as matches with no changes and read as broken.
func checkRuleActs(rule store.Rule) error {
	acts := rule.SetPayee != "" || rule.SetCategoryID != uuid.Nil || rule.SetNotes != "" ||
		len(rule.AddTagIDs) > 0 || rule.SetExcludedFromReports != nil ||
		rule.SetExcludedFromSpendingPlan != nil || rule.SetIsReviewed != nil
	if !acts {
		return errInvalid("missing", []string{"body", "actions"},
			"a rule needs at least one action")
	}
	return nil
}

// --- Loading and rendering -------------------------------------------------------

func liveRule(r *http.Request, env *Env, sp auth.SpaceContext) (store.Rule, error) {
	rule, err := fromPath(r, sp, "rule_id", "Rule", env.DB.GetRule)
	if err != nil {
		return store.Rule{}, err
	}
	if rule.IsDeleted {
		return store.Rule{}, errNotFound("Rule")
	}
	return rule, nil
}

// requireFilter refuses a filter id that is not in this space, as a conflict:
// the id came from a well-formed body.
func requireFilter(
	ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID,
) (store.Filter, error) {
	filter, err := env.DB.GetFilter(ctx, sp.ID(), id)
	if err != nil {
		if isNotFound(err) {
			return store.Filter{}, errConflict("filter %s is not in this space", id)
		}
		return store.Filter{}, err
	}
	if filter.IsDeleted {
		return store.Filter{}, errConflict("filter %s is not in this space", id)
	}
	return filter, nil
}

// loadFilter is requireFilter's filter in the domain's shape, refused as a
// conflict when it cannot be evaluated.
func loadFilter(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) (domain.Filter, error) {
	stored, err := requireFilter(ctx, env, sp, id)
	if err != nil {
		return domain.Filter{}, err
	}
	return service.ValidFilter(stored)
}

// requireRuleFilter is requireFilter with the conditions a rule cannot run on
// refused before the rule is written, rather than when it first runs.
func requireRuleFilter(
	ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID,
) (store.Filter, error) {
	filter, err := requireFilter(ctx, env, sp, id)
	if err != nil {
		return store.Filter{}, err
	}
	if len(filter.Items) == 0 {
		// An empty filter on the register is the unfiltered register; on a
		// rule it would recategorize every row the user owns.
		return store.Filter{}, errConflict(
			"filter %s has no conditions, so a rule on it would match every row", id)
	}
	for _, item := range filter.Items {
		if domain.RuleCannotMatch(domain.FilterField(item.Field)) {
			return store.Filter{}, errConflict("a rule cannot match on %s", item.Field)
		}
	}
	if _, err := service.ValidFilter(filter); err != nil {
		return store.Filter{}, err
	}
	return filter, nil
}

func ruleResponse(
	ctx context.Context, env *Env, sp auth.SpaceContext, rule store.Rule,
) (RuleResponse, error) {
	filter, err := env.DB.GetFilter(ctx, sp.ID(), rule.FilterID)
	if err != nil && !isNotFound(err) {
		return RuleResponse{}, err
	}
	return RuleResponse{
		ID:         rule.ID,
		Name:       rule.Name,
		FilterID:   rule.FilterID,
		Filter:     filterResponse(filter),
		OwnsFilter: filter.Scope == RuleFilterScope,
		Priority:   rule.Priority,
		IsActive:   rule.IsActive,
		Actions: RuleActionsWire{
			SetPayee:                    pgconv.NullText(rule.SetPayee),
			SetCategoryID:               pgconv.NullUUID(rule.SetCategoryID),
			AddTagIDs:                   store.NonNil(rule.AddTagIDs),
			SetNotes:                    pgconv.NullText(rule.SetNotes),
			SetExcludedFromReports:      rule.SetExcludedFromReports,
			SetExcludedFromSpendingPlan: rule.SetExcludedFromSpendingPlan,
			SetIsReviewed:               rule.SetIsReviewed,
		},
	}, nil
}

func ruleChangeWire(change service.RuleChange, row service.RuleCandidate) RuleChangeWire {
	txn := row.Posting.Txn
	wire := RuleChangeWire{
		TransactionID: change.TransactionID,
		Date:          Date(txn.Date),
		AccountName:   row.Posting.Account.Name,
		StatementName: txn.StatementName,
		Payee:         txn.Payee,
		Amount:        txn.Amount,
		Actions: RuleActionsWire{
			AddTagIDs: store.NonNil(change.AddTagIDs),
		},
	}
	if change.HasPayee {
		wire.Actions.SetPayee = &change.Payee
	}
	if change.HasCategoryID {
		wire.Actions.SetCategoryID = pgconv.NullUUID(change.CategoryID)
	}
	if change.HasNotes {
		wire.Actions.SetNotes = &change.Notes
	}
	if change.HasExcludedFromReports {
		wire.Actions.SetExcludedFromReports = &change.ExcludedFromReports
	}
	if change.HasExcludedFromSpendingPlan {
		wire.Actions.SetExcludedFromSpendingPlan = &change.ExcludedFromSpendingPlan
	}
	if change.HasIsReviewed {
		wire.Actions.SetIsReviewed = &change.IsReviewed
	}
	return wire
}
