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
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Filters: one vocabulary, mounted everywhere (ground rule 3). Items carry
// group_index because a rule can have alternative condition sets: items in
// one group are AND'd, groups are OR'd.
//
// Deletion is soft, because envelopes, watchlists and rules keep their
// filter_id, and a missing filter would leave them matching everything.
//
// FilterItemWrite and FilterResponse are the REST shape the resources that
// embed a filter (rules, reports, watchlists) still write.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewFilterServiceHandler(filterService{env}, opts...)
	})
}

type filterService struct{ env *Env }

// knownFilterScopes is the set of scopes the application mounts. A client may
// not mint one: a filter claiming scope "report" would be listed by the
// reports page as if saved there.
var knownFilterScopes = map[string]bool{
	"envelope":   true,
	"watchlist":  true,
	"report":     true,
	"saved_view": true,
	"rule":       true,
	"ad_hoc":     true,
}

func checkFilterScope(scope string) error {
	return checkFilterScopeAt(scope, "body")
}

func checkFilterScopeAt(scope, in string) error {
	if !knownFilterScopes[scope] {
		return errInvalid("scope_unknown", []string{in, "scope"},
			"scope must be one of envelope, watchlist, report, saved_view, rule, ad_hoc")
	}
	return nil
}

// DefaultFilterScope is what an unlabelled filter was built for: a single
// search the register created, not something a surface points at by id.
const DefaultFilterScope = "ad_hoc"

type FilterItemResponse struct {
	ID         uuid.UUID             `json:"id"`
	Field      domain.FilterField    `json:"field"`
	Operator   domain.FilterOperator `json:"operator"`
	GroupIndex int                   `json:"group_index"`
	Position   int                   `json:"position"`
	// Negated is "is not" on this item alone, not on its group: an excluded
	// three-category item excludes all three.
	Negated    bool          `json:"negated"`
	ValueIDs   []uuid.UUID   `json:"value_ids"`
	ValueTexts []string      `json:"value_texts"`
	Text       *string       `json:"text"`
	AmountMin  *domain.Money `json:"amount_min"`
	AmountMax  *domain.Money `json:"amount_max"`
	DateFrom   *Date         `json:"date_from"`
	DateTo     *Date         `json:"date_to"`
	// DatePreset is a relative token (this-month, -30d), stored so "this
	// month" keeps meaning this month.
	DatePreset *string `json:"date_preset"`
	State      *bool   `json:"state"`
}

type FilterItemWrite struct {
	Field      domain.FilterField    `json:"field"`
	Operator   domain.FilterOperator `json:"operator"`
	GroupIndex int                   `json:"group_index"`
	Position   int                   `json:"position"`
	Negated    bool                  `json:"negated"`
	ValueIDs   []uuid.UUID           `json:"value_ids"`
	ValueTexts []string              `json:"value_texts"`
	Text       *string               `json:"text"`
	AmountMin  *domain.Money         `json:"amount_min"`
	AmountMax  *domain.Money         `json:"amount_max"`
	DateFrom   *Date                 `json:"date_from"`
	DateTo     *Date                 `json:"date_to"`
	DatePreset *string               `json:"date_preset"`
	State      *bool                 `json:"state"`
}

type FilterResponse struct {
	ID    uuid.UUID `json:"id"`
	Name  *string   `json:"name"`
	Scope string    `json:"scope"`
	// QueryText is what the user typed in the search box, kept so the box
	// repopulates with that rather than with a re-rendering of the parse.
	QueryText *string              `json:"query_text"`
	Position  int                  `json:"position"`
	Items     []FilterItemResponse `json:"items"`
}

// ListFilters answers every live filter, or one scope's, in the order their
// positions give.
func (s filterService) ListFilters(
	ctx context.Context, req *agentifiv1.ListFiltersRequest,
) (*agentifiv1.ListFiltersResponse, error) {
	sp := spaceFrom(ctx)
	var rows []store.Filter
	var err error
	if scope := strings.TrimSpace(req.GetScope()); scope != "" {
		if err := checkFilterScopeAt(scope, "query"); err != nil {
			return nil, err
		}
		rows, err = s.env.DB.ListFiltersInScope(ctx, sp.ID(), scope)
	} else {
		rows, err = s.env.DB.ListFilters(ctx, sp.ID(), false)
	}
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListFiltersResponse{Filters: make([]*agentifiv1.Filter, 0, len(rows))}
	for _, row := range rows {
		out.Filters = append(out.Filters, filterProto(row))
	}
	return out, nil
}

func (s filterService) CreateFilter(
	ctx context.Context, req *agentifiv1.CreateFilterRequest,
) (*agentifiv1.CreateFilterResponse, error) {
	sp := spaceFrom(ctx)
	writes, err := filterItemWrites(req.GetItems(), "items")
	if err != nil {
		return nil, err
	}
	items, err := buildFilterItems(writes)
	if err != nil {
		return nil, err
	}

	filter := &store.Filter{
		Name:      req.GetName(),
		Scope:     DefaultFilterScope,
		QueryText: req.GetQueryText(),
		Position:  int(req.GetPosition()),
		Items:     items,
	}
	if req.Scope != nil {
		if err := checkFilterScope(req.GetScope()); err != nil {
			return nil, err
		}
		filter.Scope = req.GetScope()
	}

	if err := s.env.DB.CreateFilter(ctx, sp.ID(), filter); err != nil {
		return nil, err
	}
	return &agentifiv1.CreateFilterResponse{Filter: filterProto(*filter)}, nil
}

func (s filterService) GetFilter(
	ctx context.Context, req *agentifiv1.GetFilterRequest,
) (*agentifiv1.GetFilterResponse, error) {
	filter, err := liveFilter(ctx, s.env, spaceFrom(ctx), req.GetFilterId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetFilterResponse{Filter: filterProto(filter)}, nil
}

func (s filterService) UpdateFilter(
	ctx context.Context, req *agentifiv1.UpdateFilterRequest,
) (*agentifiv1.UpdateFilterResponse, error) {
	sp := spaceFrom(ctx)
	filter, err := liveFilter(ctx, s.env, sp, req.GetFilterId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}

	applyNullable(optOf(mask, "name", req.Name), &filter.Name)
	scope := optOf(mask, "scope", req.Scope)
	if err := applyRequired("scope", scope, &filter.Scope); err != nil {
		return nil, err
	}
	if scope.Set {
		if err := checkFilterScope(filter.Scope); err != nil {
			return nil, err
		}
	}
	applyNullable(optOf(mask, "query_text", req.QueryText), &filter.QueryText)
	position := int32(filter.Position)
	if err := applyRequired("position", optOf(mask, "position", req.Position), &position); err != nil {
		return nil, err
	}
	filter.Position = int(position)
	if err := s.env.DB.UpdateFilter(ctx, sp.ID(), &filter); err != nil {
		return nil, err
	}

	if mask["items"] {
		writes, err := filterItemWrites(req.GetItems(), "items")
		if err != nil {
			return nil, err
		}
		items, err := buildFilterItems(writes)
		if err != nil {
			return nil, err
		}
		// A filter with no items matches everything, which is harmless for a
		// saved view but not for one money derives from: an emptied envelope
		// claims the whole ledger. The envelope routes refuse it too.
		if len(items) == 0 && filterScopeNeedsItems(filter.Scope) {
			return nil, errInvalid("missing", []string{"body", "items"},
				"a %s filter must keep at least one item", filter.Scope)
		}
		filter.Items = items
		if err := s.env.DB.ReplaceFilterItems(ctx, sp.ID(), &filter); err != nil {
			return nil, err
		}
	}
	return &agentifiv1.UpdateFilterResponse{Filter: filterProto(filter)}, nil
}

// filterScopeNeedsItems is the scopes where "matches everything" is a wrong
// answer rather than an unfiltered one.
func filterScopeNeedsItems(scope string) bool {
	return scope == EnvelopeFilterScope || scope == WatchlistFilterScope
}

func (s filterService) DeleteFilter(
	ctx context.Context, req *agentifiv1.DeleteFilterRequest,
) (*agentifiv1.DeleteFilterResponse, error) {
	sp := spaceFrom(ctx)
	filter, err := liveFilter(ctx, s.env, sp, req.GetFilterId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteFilter(ctx, sp.ID(), filter.ID); err != nil {
		return nil, notFoundAs(err, "Filter")
	}
	return &agentifiv1.DeleteFilterResponse{}, nil
}

func liveFilter(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Filter, error) {
	id, err := idFrom(rawID, "Filter")
	if err != nil {
		return store.Filter{}, err
	}
	filter, err := env.DB.GetFilter(ctx, sp.ID(), id)
	if err != nil {
		return store.Filter{}, notFoundAs(err, "Filter")
	}
	if filter.IsDeleted {
		return store.Filter{}, errNotFound("Filter")
	}
	return filter, nil
}

func buildFilterItems(writes []FilterItemWrite) ([]store.FilterItem, error) {
	items := make([]store.FilterItem, 0, len(writes))
	for i, write := range writes {
		if !filterFieldIsEvaluable(write.Field) {
			return nil, errInvalid("enum", []string{"body", "items", "field"},
				"%q is not a filter field", write.Field)
		}
		operator := write.Operator
		if operator == "" {
			operator = domain.OpIn
		}
		if !filterOperatorIsKnown(operator) {
			return nil, errInvalid("enum", []string{"body", "items", "operator"},
				"%q is not a filter operator", operator)
		}
		if operator == domain.OpMatches {
			for _, value := range write.ValueTexts {
				if err := domain.ValidPattern(value); err != nil {
					return nil, errInvalid("pattern", []string{"body", "items", "value_texts"},
						"%q is not a valid pattern: %v", value, err)
				}
			}
		}
		if err := checkFilterItemValues(write, operator); err != nil {
			return nil, err
		}

		item := store.FilterItem{
			Field:      string(write.Field),
			Operator:   string(operator),
			GroupIndex: write.GroupIndex,
			Position:   write.Position,
			Negated:    write.Negated,
			ValueIDs:   write.ValueIDs,
			ValueTexts: write.ValueTexts,
			State:      write.State,
		}
		if item.ValueIDs == nil {
			item.ValueIDs = []uuid.UUID{}
		}
		if item.ValueTexts == nil {
			item.ValueTexts = []string{}
		}
		if write.Text != nil {
			item.Text = *write.Text
		}
		if write.DatePreset != nil {
			item.DatePreset = *write.DatePreset
		}
		if write.AmountMin != nil {
			item.AmountMin, item.HasAmountMin = *write.AmountMin, true
		}
		if write.AmountMax != nil {
			item.AmountMax, item.HasAmountMax = *write.AmountMax, true
		}
		if write.DateFrom != nil {
			item.DateFrom = domain.Date(*write.DateFrom)
		}
		if write.DateTo != nil {
			item.DateTo = domain.Date(*write.DateTo)
		}
		// Position defaults to the order the client sent them in, so a client
		// that does not care about ordering does not have to number them.
		if write.Position == 0 {
			item.Position = i
		}
		items = append(items, item)
	}
	return items, nil
}

// checkFilterItemValues refuses an item the evaluator would read as nothing or
// as everything: a membership test with no values matches no row, a range
// with no bounds and no direction matches every row, and a range whose
// minimum is above its maximum matches none. Equals, greater_than and
// less_than without their bound pass, because the amount editor sends one
// while the bound is still being typed.
func checkFilterItemValues(write FilterItemWrite, operator domain.FilterOperator) error {
	switch write.Field {
	case domain.FieldPayee, domain.FieldStatementName, domain.FieldFlag:
		for _, value := range write.ValueTexts {
			if strings.TrimSpace(value) != "" {
				return nil
			}
		}
		return errInvalid("missing", []string{"body", "items", "value_texts"},
			"a %s condition needs at least one value", write.Field)
	case domain.FieldCategory, domain.FieldTag, domain.FieldAccount:
		if len(write.ValueIDs) == 0 {
			return errInvalid("missing", []string{"body", "items", "value_ids"},
				"a %s condition needs at least one value", write.Field)
		}
	case domain.FieldAmount:
		switch operator {
		case domain.OpEquals, domain.OpGreaterThan, domain.OpLessThan:
			return nil
		}
		if write.AmountMin == nil && write.AmountMax == nil && write.State == nil {
			return errInvalid("missing", []string{"body", "items", "amount_min"},
				"an amount %s condition needs amount_min, amount_max or both", operator)
		}
		if write.AmountMin != nil && write.AmountMax != nil &&
			write.AmountMin.Abs().GreaterThan(write.AmountMax.Abs()) {
			return errInvalid("value_error", []string{"body", "items", "amount_min"},
				"amount_min %s is greater than amount_max %s",
				write.AmountMin.Abs().String(), write.AmountMax.Abs().String())
		}
	}
	return nil
}

// filterFieldIsEvaluable reports a field the evaluator reads. Checked here
// because the evaluator fails an unknown field's item, so the filter would
// quietly match less instead of being refused.
func filterFieldIsEvaluable(field domain.FilterField) bool {
	switch field {
	case domain.FieldCategory, domain.FieldPayee, domain.FieldTag, domain.FieldAccount,
		domain.FieldFlag, domain.FieldText, domain.FieldAmount, domain.FieldDate,
		domain.FieldIsBillOrSubscription, domain.FieldIsExcludedFromReports,
		domain.FieldIsExcludedFromSpendingPlan, domain.FieldIsReviewed,
		domain.FieldStatementName, domain.FieldIsPending, domain.FieldIsUncategorized,
		domain.FieldHasTag, domain.FieldIsCategoryUndetermined,
		domain.FieldHasCategorySuggestion, domain.FieldHasAttachment,
		domain.FieldIsMissingReceipt:
		return true
	}
	return false
}

func filterOperatorIsKnown(operator domain.FilterOperator) bool {
	switch operator {
	case domain.OpIn, domain.OpContains, domain.OpIsExactly, domain.OpEquals,
		domain.OpBetween, domain.OpGreaterThan, domain.OpLessThan, domain.OpIsTrue,
		domain.OpMatches:
		return true
	}
	return false
}

func filterResponse(f store.Filter) FilterResponse {
	items := make([]FilterItemResponse, 0, len(f.Items))
	for _, item := range f.Items {
		items = append(items, FilterItemResponse{
			ID:         item.ID,
			Field:      domain.FilterField(item.Field),
			Operator:   domain.FilterOperator(item.Operator),
			GroupIndex: item.GroupIndex,
			Position:   item.Position,
			Negated:    item.Negated,
			ValueIDs:   store.NonNil(item.ValueIDs),
			ValueTexts: store.NonNil(item.ValueTexts),
			Text:       dbconv.NullText(item.Text),
			AmountMin:  store.PtrIf(item.AmountMin, item.HasAmountMin),
			AmountMax:  store.PtrIf(item.AmountMax, item.HasAmountMax),
			DateFrom:   nullableDate(item.DateFrom),
			DateTo:     nullableDate(item.DateTo),
			DatePreset: dbconv.NullText(item.DatePreset),
			State:      item.State,
		})
	}
	return FilterResponse{
		ID:        f.ID,
		Name:      dbconv.NullText(f.Name),
		Scope:     f.Scope,
		QueryText: dbconv.NullText(f.QueryText),
		Position:  f.Position,
		Items:     items,
	}
}
