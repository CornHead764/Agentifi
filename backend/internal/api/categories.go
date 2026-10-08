package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Categories.
//
// Deletion is soft and leaves the transactions filed under the category, so
// history keeps resolving its name rather than turning uncategorized.
//
// known_category_id is read-only: it is the stable system marker, and a client
// that could set it could make any category behave like a transfer.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewCategoryServiceHandler(categoryService{env}, opts...)
	})
}

type categoryService struct{ env *Env }

func (s categoryService) ListCategories(
	ctx context.Context, _ *agentifiv1.ListCategoriesRequest,
) (*agentifiv1.ListCategoriesResponse, error) {
	rows, err := s.env.DB.ListCategories(ctx, spaceFrom(ctx).ID(), false)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListCategoriesResponse{Categories: categoryProtos(rows)}, nil
}

func (s categoryService) CreateCategory(
	ctx context.Context, req *agentifiv1.CreateCategoryRequest,
) (*agentifiv1.CreateCategoryResponse, error) {
	sp := spaceFrom(ctx)
	if req.GetName() == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	kind := domain.CategoryKind(req.GetKind())
	if err := checkCategoryKind(kind); err != nil {
		return nil, err
	}

	category := &store.Category{
		Name:             req.GetName(),
		Kind:             kind,
		IsUserAssignable: true,
		IsEditable:       true,
		TxfIDs:           store.NonNil(req.GetTxfIds()),
	}
	parentID, err := uuidField(req.GetParentId(), "body", "parent_id")
	if err != nil {
		return nil, err
	}
	category.ParentID = parentID
	if err := checkParentCategory(ctx, s.env, sp, category.ParentID, uuid.Nil); err != nil {
		return nil, err
	}
	category.TxfID = req.GetTxfId()
	category.SortOrder = int(req.GetSortOrder())
	category.ExcludedFromReports = req.GetExcludedFromReports()
	category.ExcludedFromSpendingPlan = req.GetExcludedFromSpendingPlan()
	category.ExcludedFromCategoryList = req.GetExcludedFromCategoryList()

	if err := s.env.DB.CreateCategory(ctx, sp.ID(), category); err != nil {
		return nil, err
	}
	return &agentifiv1.CreateCategoryResponse{Category: categoryProto(*category)}, nil
}

// SeedDefaultCategories seeds the starter tree. Seeding is idempotent: a
// category that already exists by name is counted as existing and left as the
// person has it, so running it on a space that already has categories adds
// only what is missing.
func (s categoryService) SeedDefaultCategories(
	ctx context.Context, _ *agentifiv1.SeedDefaultCategoriesRequest,
) (*agentifiv1.SeedDefaultCategoriesResponse, error) {
	sp := spaceFrom(ctx)
	result, err := s.env.DB.SeedCategories(ctx, sp.ID(), domain.DefaultCategories)
	if err != nil {
		return nil, err
	}
	rows, err := s.env.DB.ListCategories(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	if result.Created > 0 {
		setRESTStatus(ctx, http.StatusCreated)
	}
	return &agentifiv1.SeedDefaultCategoriesResponse{
		Created:    int32(result.Created),
		Existing:   int32(result.Existing),
		Categories: categoryProtos(rows),
	}, nil
}

func (s categoryService) GetCategory(
	ctx context.Context, req *agentifiv1.GetCategoryRequest,
) (*agentifiv1.GetCategoryResponse, error) {
	category, err := liveCategory(ctx, s.env, spaceFrom(ctx), req.GetCategoryId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetCategoryResponse{Category: categoryProto(category)}, nil
}

func (s categoryService) UpdateCategory(
	ctx context.Context, req *agentifiv1.UpdateCategoryRequest,
) (*agentifiv1.UpdateCategoryResponse, error) {
	sp := spaceFrom(ctx)
	category, err := liveCategory(ctx, s.env, sp, req.GetCategoryId())
	if err != nil {
		return nil, err
	}
	if err := checkCategoryEditable(category); err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	parentID, err := optUUIDOf(mask, "parent_id", req.ParentId)
	if err != nil {
		return nil, err
	}

	if err := applyRequired("name", optOf(mask, "name", req.Name), &category.Name); err != nil {
		return nil, err
	}
	if kind := optOf(mask, "kind", req.Kind); kind.Set {
		if kind.Null {
			return nil, errConflict("kind cannot be cleared")
		}
		value := domain.CategoryKind(kind.Value)
		if err := checkCategoryKind(value); err != nil {
			return nil, err
		}
		if reason, depended := domain.CategoryDependedOn(category.KnownCategoryID); depended &&
			value != category.Kind {
			return nil, errConflict("the kind of %q cannot change: %s", category.Name, reason)
		}
		category.Kind = value
	}
	if parentID.Set {
		applyNullable(parentID, &category.ParentID)
		if err := checkParentCategory(ctx, s.env, sp, category.ParentID, category.ID); err != nil {
			return nil, err
		}
	}
	applyNullable(optOf(mask, "txf_id", req.TxfId), &category.TxfID)
	if mask["txf_ids"] {
		category.TxfIDs = store.NonNil(req.GetTxfIds())
	}
	sortOrder := int32(category.SortOrder)
	if err := applyRequired("sort_order", optOf(mask, "sort_order", req.SortOrder), &sortOrder); err != nil {
		return nil, err
	}
	category.SortOrder = int(sortOrder)
	for _, flag := range []struct {
		name  string
		value *bool
		dst   *bool
	}{
		{"excluded_from_reports", req.ExcludedFromReports, &category.ExcludedFromReports},
		{"excluded_from_spending_plan", req.ExcludedFromSpendingPlan, &category.ExcludedFromSpendingPlan},
		{"excluded_from_category_list", req.ExcludedFromCategoryList, &category.ExcludedFromCategoryList},
	} {
		if err := applyRequired(flag.name, optOf(mask, flag.name, flag.value), flag.dst); err != nil {
			return nil, err
		}
	}

	if err := s.env.DB.UpdateCategory(ctx, sp.ID(), &category); err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateCategoryResponse{Category: categoryProto(category)}, nil
}

func (s categoryService) DeleteCategory(
	ctx context.Context, req *agentifiv1.DeleteCategoryRequest,
) (*agentifiv1.DeleteCategoryResponse, error) {
	sp := spaceFrom(ctx)
	category, err := liveCategory(ctx, s.env, sp, req.GetCategoryId())
	if err != nil {
		return nil, err
	}
	if reason := domain.CategoryProtection(category.KnownCategoryID, category.IsEditable); reason != "" {
		return nil, errConflict("%q cannot be deleted: %s", category.Name, reason)
	}
	if err := s.env.DB.DeleteCategory(ctx, sp.ID(), category.ID); err != nil {
		return nil, notFoundAs(err, "Category")
	}
	return &agentifiv1.DeleteCategoryResponse{}, nil
}

func liveCategory(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Category, error) {
	id, err := idFrom(rawID, "Category")
	if err != nil {
		return store.Category{}, err
	}
	category, err := env.DB.GetCategory(ctx, sp.ID(), id)
	if err != nil {
		return store.Category{}, notFoundAs(err, "Category")
	}
	if category.IsDeleted {
		return store.Category{}, errNotFound("Category")
	}
	return category, nil
}

// checkCategoryEditable refuses an edit to one of the engine's own categories.
// Opening Balance and Balance Adjustment are read through known_category_id,
// so renaming or retiring one turns its rows back into ordinary spending.
func checkCategoryEditable(category store.Category) error {
	if !category.IsEditable {
		return errConflict("category %q is maintained by the system", category.Name)
	}
	return nil
}

func checkParentCategory(ctx context.Context, env *Env, sp auth.SpaceContext, parentID, self uuid.UUID) error {
	if parentID == uuid.Nil {
		return nil
	}
	if parentID == self {
		return errConflict("a category cannot be its own parent")
	}
	parent, err := env.DB.GetCategory(ctx, sp.ID(), parentID)
	if err != nil {
		if isNotFound(err) {
			return errConflict("parent category %s is not in this space", parentID)
		}
		return err
	}
	if parent.IsDeleted {
		return errConflict("parent category %s is not in this space", parentID)
	}
	return nil
}

func checkCategoryKind(kind domain.CategoryKind) error {
	switch kind {
	case domain.CategoryIncome, domain.CategoryExpense, domain.CategoryTransfer:
		return nil
	default:
		return errInvalid("enum", []string{"body", "kind"}, "%q is not a category kind", kind)
	}
}

func categoryProtos(rows []store.Category) []*agentifiv1.Category {
	out := make([]*agentifiv1.Category, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryProto(row))
	}
	return out
}

func categoryProto(c store.Category) *agentifiv1.Category {
	return &agentifiv1.Category{
		Id:                       c.ID.String(),
		ParentId:                 nullUUIDString(c.ParentID),
		Name:                     c.Name,
		Kind:                     string(c.Kind),
		KnownCategoryId:          dbconv.NullText(c.KnownCategoryID),
		TxfId:                    dbconv.NullText(c.TxfID),
		TxfIds:                   store.NonNil(c.TxfIDs),
		IsUserAssignable:         c.IsUserAssignable,
		IsEditable:               c.IsEditable,
		ExcludedFromReports:      c.ExcludedFromReports,
		ExcludedFromSpendingPlan: c.ExcludedFromSpendingPlan,
		ExcludedFromCategoryList: c.ExcludedFromCategoryList,
		SortOrder:                int32(c.SortOrder),
		ProtectedReason:          dbconv.NullText(domain.CategoryProtection(c.KnownCategoryID, c.IsEditable)),
	}
}
