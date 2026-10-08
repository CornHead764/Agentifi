package api

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
// Nothing reaches existing rows without an explicit apply: PreviewRule writes
// nothing, and ApplyRule commits for the rows the caller names, so a row
// arriving between the two is not swept in.
//
// Matching reads statement_name and the rename writes payee (ground rule 5), so
// a rule matching on the payee stops firing once its own rename lands. Both are
// allowed, as in Simplifi.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewRuleServiceHandler(ruleService{env}, opts...)
	})
}

type ruleService struct{ env *Env }

// RuleFilterScope marks the filters this resource creates and owns.
const RuleFilterScope = "rule"

// maxPreviewChanges bounds one preview response. The counts stay exact.
const maxPreviewChanges = 500

// RuleResponse is a rule as GET /rules/{rule_id} answers it, which the
// assistant reads in-process.
type RuleResponse struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	FilterID   uuid.UUID       `json:"filter_id"`
	Filter     FilterResponse  `json:"filter"`
	OwnsFilter bool            `json:"owns_filter"`
	Priority   int             `json:"priority"`
	IsActive   bool            `json:"is_active"`
	Actions    RuleActionsWire `json:"actions"`
}

// RuleActionsWire is RuleResponse's actions. Null means "leave this alone",
// which for the flags differs from false.
type RuleActionsWire struct {
	SetPayee      *string     `json:"set_payee"`
	SetCategoryID *uuid.UUID  `json:"set_category_id"`
	AddTagIDs     []uuid.UUID `json:"add_tag_ids"`
	SetNotes      *string     `json:"set_notes"`

	SetExcludedFromReports      *bool `json:"set_excluded_from_reports"`
	SetExcludedFromSpendingPlan *bool `json:"set_excluded_from_spending_plan"`
	SetIsReviewed               *bool `json:"set_is_reviewed"`
}

// --- Handlers ----------------------------------------------------------------

func (s ruleService) ListRules(ctx context.Context, _ *agentifiv1.ListRulesRequest) (*agentifiv1.ListRulesResponse, error) {
	rules, err := listRuleProtos(ctx, s.env, spaceFrom(ctx))
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListRulesResponse{Rules: rules}, nil
}

func listRuleProtos(ctx context.Context, env *Env, sp auth.SpaceContext) ([]*agentifiv1.Rule, error) {
	rows, err := env.DB.ListRules(ctx, sp.ID(), store.RuleQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*agentifiv1.Rule, 0, len(rows))
	for _, row := range rows {
		response, err := ruleProto(ctx, env, sp, row)
		if err != nil {
			return nil, err
		}
		out = append(out, response)
	}
	return out, nil
}

func (s ruleService) CreateRule(ctx context.Context, req *agentifiv1.CreateRuleRequest) (*agentifiv1.CreateRuleResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if (req.FilterId == nil) == (len(req.GetConditions()) == 0) {
		// Neither would be a rule rewriting every row; both is ambiguous.
		return nil, errInvalid("missing", []string{"body", "conditions"},
			"send either filter_id or a non-empty conditions")
	}

	rule := &store.Rule{
		Name:     strings.TrimSpace(req.GetName()),
		IsActive: req.IsActive == nil || req.GetIsActive(),
	}
	if req.Actions != nil {
		if err := applyRuleActions(ctx, env, sp, rule, req.Actions); err != nil {
			return nil, err
		}
	}
	if err := checkRuleActs(*rule); err != nil {
		return nil, err
	}

	if req.FilterId != nil {
		id, err := uuidFrom(req.GetFilterId(), "body", "filter_id")
		if err != nil {
			return nil, err
		}
		filter, err := requireRuleFilter(ctx, env, sp, id)
		if err != nil {
			return nil, err
		}
		rule.FilterID = filter.ID
	} else {
		writes, err := filterItemWrites(req.GetConditions(), "conditions")
		if err != nil {
			return nil, err
		}
		filter, err := ruleConditions.writeOwnedFilter(ctx, env, sp, rule.Name, writes)
		if err != nil {
			return nil, err
		}
		rule.FilterID = filter.ID
	}

	priority, err := env.DB.NextRulePriority(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	rule.Priority = priority
	if err := env.DB.CreateRule(ctx, sp.ID(), rule); err != nil {
		return nil, err
	}

	response, err := ruleProto(ctx, env, sp, *rule)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateRuleResponse{Rule: response}, nil
}

func (s ruleService) GetRule(ctx context.Context, req *agentifiv1.GetRuleRequest) (*agentifiv1.GetRuleResponse, error) {
	sp := spaceFrom(ctx)
	rule, err := liveRule(ctx, s.env, sp, req.GetRuleId())
	if err != nil {
		return nil, err
	}
	response, err := ruleProto(ctx, s.env, sp, rule)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetRuleResponse{Rule: response}, nil
}

func (s ruleService) UpdateRule(ctx context.Context, req *agentifiv1.UpdateRuleRequest) (*agentifiv1.UpdateRuleResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	rule, err := liveRule(ctx, env, sp, req.GetRuleId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("name", optOf(mask, "name", req.Name), &rule.Name); err != nil {
		return nil, err
	}
	if err := applyRequired("is_active", optOf(mask, "is_active", req.IsActive), &rule.IsActive); err != nil {
		return nil, err
	}
	filterID := optOf(mask, "filter_id", req.FilterId)
	if filterID.Cleared() {
		return nil, errConflict("filter_id cannot be cleared")
	}
	if filterID.Present() {
		id, err := uuidFrom(filterID.Value, "body", "filter_id")
		if err != nil {
			return nil, err
		}
		filter, err := requireRuleFilter(ctx, env, sp, id)
		if err != nil {
			return nil, err
		}
		rule.FilterID = filter.ID
	}
	if req.Actions != nil {
		if err := applyRuleActions(ctx, env, sp, &rule, req.Actions); err != nil {
			return nil, err
		}
	}
	if err := checkRuleActs(rule); err != nil {
		return nil, err
	}

	if mask["conditions"] {
		writes, err := filterItemWrites(req.GetConditions(), "conditions")
		if err != nil {
			return nil, err
		}
		if err := ruleConditions.replaceOwnedConditions(
			ctx, env, sp, rule.ID, rule.FilterID, writes); err != nil {
			return nil, err
		}
	}
	if err := env.DB.UpdateRule(ctx, sp.ID(), &rule); err != nil {
		return nil, err
	}

	response, err := ruleProto(ctx, env, sp, rule)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateRuleResponse{Rule: response}, nil
}

func (s ruleService) DeleteRule(ctx context.Context, req *agentifiv1.DeleteRuleRequest) (*agentifiv1.DeleteRuleResponse, error) {
	sp := spaceFrom(ctx)
	rule, err := liveRule(ctx, s.env, sp, req.GetRuleId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteRule(ctx, sp.ID(), rule.ID); err != nil {
		return nil, notFoundAs(err, "Rule")
	}
	return &agentifiv1.DeleteRuleResponse{}, nil
}

func (s ruleService) ReorderRules(ctx context.Context, req *agentifiv1.ReorderRulesRequest) (*agentifiv1.ReorderRulesResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	ids, err := uuidsFrom(req.GetRuleIds(), "body", "rule_ids")
	if err != nil {
		return nil, err
	}
	live, err := env.DB.ListRules(ctx, sp.ID(), store.RuleQuery{})
	if err != nil {
		return nil, err
	}
	if err := checkReorderCovers(live, ids); err != nil {
		return nil, err
	}
	if err := env.DB.ReorderRules(ctx, sp.ID(), ids); err != nil {
		return nil, notFoundAs(err, "Rule")
	}
	rules, err := listRuleProtos(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ReorderRulesResponse{Rules: rules}, nil
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

// PreviewRule answers "what happens if I run this over what I already have?"
// and writes nothing. A viewer may ask: the answer is a read of the ledger.
func (s ruleService) PreviewRule(ctx context.Context, req *agentifiv1.PreviewRuleRequest) (*agentifiv1.PreviewRuleResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	rule, err := liveRule(ctx, env, sp, req.GetRuleId())
	if err != nil {
		return nil, err
	}
	var since domain.Date
	if raw := strings.TrimSpace(req.GetSince()); raw != "" {
		if since, err = dateFrom(raw, "query", "since"); err != nil {
			return nil, err
		}
	}

	plan, err := planRuleRun(ctx, env, sp, rule.ID, service.CandidateQuery{Since: since})
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.PreviewRuleResponse{
		RuleId:    plan.preview.RuleID.String(),
		Matched:   int32(plan.preview.Matched),
		Changed:   int32(len(plan.preview.Changes)),
		Unchanged: int32(plan.preview.Unchanged()),
		Since:     dateProto(since),
		Changes:   make([]*agentifiv1.RuleChange, 0, len(plan.preview.Changes)),
	}
	for _, change := range plan.preview.Changes {
		if len(out.Changes) == maxPreviewChanges {
			out.Truncated = true
			break
		}
		out.Changes = append(out.Changes, ruleChangeProto(change, plan.rows[change.TransactionID]))
	}
	return out, nil
}

// ApplyRule commits the preview for the rows the caller approved. The diff is
// re-planned rather than trusted from the request, over exactly the named rows.
func (s ruleService) ApplyRule(ctx context.Context, req *agentifiv1.ApplyRuleRequest) (*agentifiv1.ApplyRuleResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	rule, err := liveRule(ctx, env, sp, req.GetRuleId())
	if err != nil {
		return nil, err
	}
	approved, err := idSetFrom(req.TransactionIds, "transaction_ids")
	if err != nil {
		return nil, err
	}

	query := service.CandidateQuery{}
	if approved != nil {
		query.TransactionIDs = *approved
	} else if req.Since != nil {
		if query.Since, err = dateFrom(req.GetSince(), "body", "since"); err != nil {
			return nil, err
		}
	}

	plan, err := planRuleRun(ctx, env, sp, rule.ID, query)
	if err != nil {
		return nil, err
	}
	applied, err := plan.rules.ApplyChanges(ctx, sp.ID(), plan.preview.Changes)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ApplyRuleResponse{
		RuleId:  rule.ID.String(),
		Matched: int32(plan.preview.Matched),
		Applied: int32(applied),
	}, nil
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

// applyRuleActions writes the actions a request names: a field named and unset
// clears that action, and a field not named keeps it.
func applyRuleActions(
	ctx context.Context, env *Env, sp auth.SpaceContext, rule *store.Rule, sent *agentifiv1.RuleActionsWrite,
) error {
	mask, err := maskOf(sent)
	if err != nil {
		return err
	}
	applyNullable(optOf(mask, "set_payee", sent.SetPayee), &rule.SetPayee)
	applyNullable(optOf(mask, "set_notes", sent.SetNotes), &rule.SetNotes)
	if category := optOf(mask, "set_category_id", sent.SetCategoryId); category.Set {
		rule.SetCategoryID = uuid.Nil
		if category.Present() {
			if rule.SetCategoryID, err = uuidFrom(category.Value, "body", "actions", "set_category_id"); err != nil {
				return err
			}
		}
	}
	if err := checkCategory(ctx, env, sp, rule.SetCategoryID); err != nil {
		return err
	}
	tagIDs, err := idSetFrom(sent.AddTagIds, "add_tag_ids")
	if err != nil {
		return err
	}
	if tagIDs != nil {
		tags, err := resolveTags(ctx, env, sp, *tagIDs)
		if err != nil {
			return err
		}
		rule.AddTagIDs = tags
	}
	applyTriState(optOf(mask, "set_excluded_from_reports", sent.SetExcludedFromReports), &rule.SetExcludedFromReports)
	applyTriState(optOf(mask, "set_excluded_from_spending_plan", sent.SetExcludedFromSpendingPlan),
		&rule.SetExcludedFromSpendingPlan)
	applyTriState(optOf(mask, "set_is_reviewed", sent.SetIsReviewed), &rule.SetIsReviewed)
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

func liveRule(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Rule, error) {
	id, err := idFrom(rawID, "Rule")
	if err != nil {
		return store.Rule{}, err
	}
	rule, err := env.DB.GetRule(ctx, sp.ID(), id)
	if err != nil {
		return store.Rule{}, notFoundAs(err, "Rule")
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

// ruleProto renders one rule. A missing filter is tolerated, so one deleted
// row cannot 409 the whole settings list.
func ruleProto(
	ctx context.Context, env *Env, sp auth.SpaceContext, rule store.Rule,
) (*agentifiv1.Rule, error) {
	filter, err := env.DB.GetFilter(ctx, sp.ID(), rule.FilterID)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	var category *string
	if rule.SetCategoryID != uuid.Nil {
		text := rule.SetCategoryID.String()
		category = &text
	}
	return &agentifiv1.Rule{
		Id:         rule.ID.String(),
		Name:       rule.Name,
		FilterId:   rule.FilterID.String(),
		Filter:     filterProto(filter),
		OwnsFilter: filter.Scope == RuleFilterScope,
		Priority:   int32(rule.Priority),
		IsActive:   rule.IsActive,
		Actions: &agentifiv1.RuleActions{
			SetPayee:                    dbconv.NullText(rule.SetPayee),
			SetCategoryId:               category,
			AddTagIds:                   uuidStrings(rule.AddTagIDs),
			SetNotes:                    dbconv.NullText(rule.SetNotes),
			SetExcludedFromReports:      rule.SetExcludedFromReports,
			SetExcludedFromSpendingPlan: rule.SetExcludedFromSpendingPlan,
			SetIsReviewed:               rule.SetIsReviewed,
		},
	}, nil
}

func ruleChangeProto(change service.RuleChange, row service.RuleCandidate) *agentifiv1.RuleChange {
	txn := row.Posting.Txn
	actions := &agentifiv1.RuleActions{AddTagIds: uuidStrings(change.AddTagIDs)}
	if change.HasPayee {
		actions.SetPayee = &change.Payee
	}
	if change.HasCategoryID && change.CategoryID != uuid.Nil {
		text := change.CategoryID.String()
		actions.SetCategoryId = &text
	}
	if change.HasNotes {
		actions.SetNotes = &change.Notes
	}
	if change.HasExcludedFromReports {
		actions.SetExcludedFromReports = &change.ExcludedFromReports
	}
	if change.HasExcludedFromSpendingPlan {
		actions.SetExcludedFromSpendingPlan = &change.ExcludedFromSpendingPlan
	}
	if change.HasIsReviewed {
		actions.SetIsReviewed = &change.IsReviewed
	}
	return &agentifiv1.RuleChange{
		TransactionId: change.TransactionID.String(),
		Date:          txn.Date.String(),
		AccountName:   row.Posting.Account.Name,
		StatementName: txn.StatementName,
		Payee:         txn.Payee,
		Amount:        moneyProto(txn.Amount),
		Actions:       actions,
	}
}
