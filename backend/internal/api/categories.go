package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
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
	Register(Resource{Prefix: "/categories", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listCategories)
		rt.Write(http.MethodPost, "/", createCategory)
		rt.Write(http.MethodPost, "/defaults", seedDefaultCategories)
		rt.Read(http.MethodGet, "/{category_id}", readCategory)
		rt.Write(http.MethodPatch, "/{category_id}", updateCategory)
		rt.Write(http.MethodDelete, "/{category_id}", deleteCategory)
	}})
}

type CategoryResponse struct {
	ID       uuid.UUID           `json:"id"`
	ParentID *uuid.UUID          `json:"parent_id"`
	Name     string              `json:"name"`
	Kind     domain.CategoryKind `json:"kind"`
	// KnownCategoryID is the system marker; TxfID is the Tax eXchange Format
	// code the Taxes report is a grouping over.
	KnownCategoryID          *string  `json:"known_category_id"`
	TxfID                    *string  `json:"txf_id"`
	TxfIDs                   []string `json:"txf_ids"`
	IsUserAssignable         bool     `json:"is_user_assignable"`
	IsEditable               bool     `json:"is_editable"`
	ExcludedFromReports      bool     `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan bool     `json:"excluded_from_spending_plan"`
	ExcludedFromCategoryList bool     `json:"excluded_from_category_list"`
	SortOrder                int      `json:"sort_order"`
	// ProtectedReason says why the category cannot be deleted; null when it can.
	ProtectedReason *string `json:"protected_reason"`
}

type CategoryCreate struct {
	Name                     string              `json:"name"`
	Kind                     domain.CategoryKind `json:"kind"`
	ParentID                 Opt[uuid.UUID]      `json:"parent_id"`
	TxfID                    Opt[string]         `json:"txf_id"`
	TxfIDs                   Opt[[]string]       `json:"txf_ids"`
	ExcludedFromReports      Opt[bool]           `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan Opt[bool]           `json:"excluded_from_spending_plan"`
	ExcludedFromCategoryList Opt[bool]           `json:"excluded_from_category_list"`
	SortOrder                Opt[int]            `json:"sort_order"`
}

type CategoryUpdate struct {
	Name                     Opt[string]              `json:"name"`
	Kind                     Opt[domain.CategoryKind] `json:"kind"`
	ParentID                 Opt[uuid.UUID]           `json:"parent_id"`
	TxfID                    Opt[string]              `json:"txf_id"`
	TxfIDs                   Opt[[]string]            `json:"txf_ids"`
	ExcludedFromReports      Opt[bool]                `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan Opt[bool]                `json:"excluded_from_spending_plan"`
	ExcludedFromCategoryList Opt[bool]                `json:"excluded_from_category_list"`
	SortOrder                Opt[int]                 `json:"sort_order"`
}

func listCategories(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListCategories(r.Context(), sp.ID(), false)
	if err != nil {
		return err
	}
	out := make([]CategoryResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryResponse(row))
	}
	return writeJSON(w, http.StatusOK, out)
}

func createCategory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body CategoryCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Name == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkCategoryKind(body.Kind); err != nil {
		return err
	}

	category := &store.Category{
		Name:             body.Name,
		Kind:             body.Kind,
		IsUserAssignable: true,
		IsEditable:       true,
		TxfIDs:           []string{},
	}
	applyNullable(body.ParentID, &category.ParentID)
	if err := checkParentCategory(env, r, sp, category.ParentID, uuid.Nil); err != nil {
		return err
	}
	applyNullable(body.TxfID, &category.TxfID)
	applyTxfIDs(body.TxfIDs, &category.TxfIDs)
	if err := applyRequired("sort_order", body.SortOrder, &category.SortOrder); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_reports", body.ExcludedFromReports, &category.ExcludedFromReports); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_spending_plan", body.ExcludedFromSpendingPlan, &category.ExcludedFromSpendingPlan); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_category_list", body.ExcludedFromCategoryList, &category.ExcludedFromCategoryList); err != nil {
		return err
	}

	if err := env.DB.CreateCategory(r.Context(), sp.ID(), category); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, categoryResponse(*category))
}

func readCategory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	category, err := liveCategory(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, categoryResponse(category))
}

func updateCategory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	category, err := liveCategory(r, env, sp)
	if err != nil {
		return err
	}
	if err := checkCategoryEditable(category); err != nil {
		return err
	}
	var body CategoryUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	if err := applyRequired("name", body.Name, &category.Name); err != nil {
		return err
	}
	if body.Kind.Set {
		if body.Kind.Null {
			return errConflict("kind cannot be cleared")
		}
		if err := checkCategoryKind(body.Kind.Value); err != nil {
			return err
		}
		if reason, depended := domain.CategoryDependedOn(category.KnownCategoryID); depended &&
			body.Kind.Value != category.Kind {
			return errConflict("the kind of %q cannot change: %s", category.Name, reason)
		}
		category.Kind = body.Kind.Value
	}
	if body.ParentID.Set {
		applyNullable(body.ParentID, &category.ParentID)
		if err := checkParentCategory(env, r, sp, category.ParentID, category.ID); err != nil {
			return err
		}
	}
	applyNullable(body.TxfID, &category.TxfID)
	applyTxfIDs(body.TxfIDs, &category.TxfIDs)
	if err := applyRequired("sort_order", body.SortOrder, &category.SortOrder); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_reports", body.ExcludedFromReports, &category.ExcludedFromReports); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_spending_plan", body.ExcludedFromSpendingPlan, &category.ExcludedFromSpendingPlan); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_category_list", body.ExcludedFromCategoryList, &category.ExcludedFromCategoryList); err != nil {
		return err
	}

	if err := env.DB.UpdateCategory(r.Context(), sp.ID(), &category); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, categoryResponse(category))
}

func deleteCategory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	category, err := liveCategory(r, env, sp)
	if err != nil {
		return err
	}
	if reason := domain.CategoryProtection(category.KnownCategoryID, category.IsEditable); reason != "" {
		return errConflict("%q cannot be deleted: %s", category.Name, reason)
	}
	return deleted(w, env.DB.DeleteCategory(r.Context(), sp.ID(), category.ID), "Category")
}

func liveCategory(r *http.Request, env *Env, sp auth.SpaceContext) (store.Category, error) {
	category, err := fromPath(r, sp, "category_id", "Category", env.DB.GetCategory)
	if err != nil {
		return store.Category{}, err
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

func checkParentCategory(env *Env, r *http.Request, sp auth.SpaceContext, parentID, self uuid.UUID) error {
	if parentID == uuid.Nil {
		return nil
	}
	if parentID == self {
		return errConflict("a category cannot be its own parent")
	}
	parent, err := env.DB.GetCategory(r.Context(), sp.ID(), parentID)
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

func applyTxfIDs(opt Opt[[]string], dst *[]string) {
	if !opt.Set {
		return
	}
	if opt.Null || opt.Value == nil {
		*dst = []string{}
		return
	}
	*dst = opt.Value
}

func categoryResponse(c store.Category) CategoryResponse {
	txf := c.TxfIDs
	if txf == nil {
		txf = []string{}
	}
	return CategoryResponse{
		ID:                       c.ID,
		ParentID:                 dbconv.NullUUID(c.ParentID),
		Name:                     c.Name,
		Kind:                     c.Kind,
		KnownCategoryID:          dbconv.NullText(c.KnownCategoryID),
		TxfID:                    dbconv.NullText(c.TxfID),
		TxfIDs:                   txf,
		IsUserAssignable:         c.IsUserAssignable,
		IsEditable:               c.IsEditable,
		ExcludedFromReports:      c.ExcludedFromReports,
		ExcludedFromSpendingPlan: c.ExcludedFromSpendingPlan,
		ExcludedFromCategoryList: c.ExcludedFromCategoryList,
		SortOrder:                c.SortOrder,
		ProtectedReason:          dbconv.NullText(domain.CategoryProtection(c.KnownCategoryID, c.IsEditable)),
	}
}
