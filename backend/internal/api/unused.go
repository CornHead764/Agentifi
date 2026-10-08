package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The categories and tags nothing uses, and a purge for them.
//
// A use is store.CategoryUses and store.TagUses. The two rules that depend on
// the rest of the batch live here:
//
//   - a category whose subcategory stays behind cannot go, or the child keeps
//     a parent_id nothing resolves;
//   - a category the app maintains never goes.
//
// The purge does not trust the list: inside one transaction it locks the rows,
// asks again, and refuses the whole request if any has since been used. Once
// it commits, the transaction automations fire again at the unreviewed rows an
// automation had filed under a purged category.

func init() {
	Register(Resource{Prefix: "/unused", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listUnused)
		rt.Write(http.MethodPost, "/purge", purgeUnused)
	}})
}

type UnusedCategory struct {
	ID       uuid.UUID           `json:"id"`
	ParentID *uuid.UUID          `json:"parent_id"`
	Name     string              `json:"name"`
	Kind     domain.CategoryKind `json:"kind"`
	// Path is the whole branch, "Bills & Utilities · Rent", since two
	// categories may share a name under different parents.
	Path string `json:"path"`
}

type UnusedResponse struct {
	Categories []UnusedCategory `json:"categories"`
	Tags       []TagResponse    `json:"tags"`
	// The *Checked lists name every reference the answer was tested against,
	// so the screen can say what "unused" covered without keeping its own list.
	CategoriesChecked []string `json:"categories_checked"`
	TagsChecked       []string `json:"tags_checked"`
}

type PurgeRequest struct {
	CategoryIDs []uuid.UUID `json:"category_ids"`
	TagIDs      []uuid.UUID `json:"tag_ids"`
}

type PurgeResponse struct {
	CategoriesDeleted int `json:"categories_deleted"`
	TagsDeleted       int `json:"tags_deleted"`
	// FiltersRepaired is the broad filter items — "Uncategorized", "has any
	// tag", Simplifi's "everything except" — the purged ids were dropped from.
	FiltersRepaired int `json:"filters_repaired"`
	// FiltersRetired is the register searches that named nothing else.
	FiltersRetired int `json:"filters_retired"`
	// Resuggested is the unreviewed rows whose suggested category went, left
	// uncategorized and handed back to the automations.
	Resuggested int `json:"resuggested"`
}

func listUnused(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	ctx := r.Context()
	categories, err := env.DB.ListCategories(ctx, sp.ID(), false)
	if err != nil {
		return err
	}
	categoryUses, err := env.DB.CategoryUses(ctx, sp.ID(), nil)
	if err != nil {
		return err
	}
	tags, err := env.DB.ListTags(ctx, sp.ID(), false)
	if err != nil {
		return err
	}
	tagUses, err := env.DB.TagUses(ctx, sp.ID(), nil)
	if err != nil {
		return err
	}

	byID := make(map[uuid.UUID]store.Category, len(categories))
	want := make(map[uuid.UUID]bool, len(categories))
	for _, category := range categories {
		byID[category.ID] = category
		want[category.ID] = true
	}
	unused, _ := prunableCategories(categories, categoryUses, want)

	out := UnusedResponse{
		Categories:        []UnusedCategory{},
		Tags:              []TagResponse{},
		CategoriesChecked: append(store.CategoryUseLabels(), "subcategories"),
		TagsChecked:       store.TagUseLabels(),
	}
	for _, category := range categories {
		if !unused[category.ID] {
			continue
		}
		out.Categories = append(out.Categories, UnusedCategory{
			ID:       category.ID,
			ParentID: dbconv.NullUUID(category.ParentID),
			Name:     category.Name,
			Kind:     category.Kind,
			Path:     categoryPath(byID, category),
		})
	}
	for _, tag := range tags {
		if len(tagUses[tag.ID]) == 0 {
			out.Tags = append(out.Tags, tagResponse(tag))
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func purgeUnused(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body PurgeRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	// Deduplicated: the write counts the rows it changed, and the same id twice
	// would look like a row that had gone.
	categoryIDs, tagIDs := textutil.Distinct(body.CategoryIDs), textutil.Distinct(body.TagIDs)
	if len(categoryIDs) == 0 && len(tagIDs) == 0 {
		return errInvalid("missing", []string{"body"},
			"category_ids or tag_ids must name at least one category or tag")
	}

	ctx := r.Context()
	var out PurgeResponse
	var resuggest []uuid.UUID
	err := env.DB.InTx(ctx, func(tx *store.Store) error {
		if err := tx.LockForPurge(ctx, sp.ID(), categoryIDs, tagIDs); err != nil {
			return err
		}
		refused, err := refuseCategories(tx, r, sp, categoryIDs)
		if err != nil {
			return err
		}
		refusedTags, err := refuseTags(tx, r, sp, tagIDs)
		if err != nil {
			return err
		}
		if refused = append(refused, refusedTags...); len(refused) > 0 {
			return errConflict("nothing was deleted: %s", refusalMessage(refused))
		}

		categories, err := tx.PruneCategories(ctx, sp.ID(), categoryIDs)
		if err != nil {
			return err
		}
		tags, err := tx.PruneTags(ctx, sp.ID(), tagIDs)
		if err != nil {
			return err
		}
		out = PurgeResponse{
			CategoriesDeleted: categories.Deleted,
			TagsDeleted:       tags.Deleted,
			FiltersRepaired:   categories.FiltersRepaired + tags.FiltersRepaired,
			FiltersRetired:    categories.FiltersRetired + tags.FiltersRetired,
			Resuggested:       len(categories.Resuggest),
		}
		resuggest = categories.Resuggest
		return nil
	})
	if errors.Is(err, store.ErrPurgeEmptiesFilter) {
		return errConflict("%s", "nothing was deleted: a saved filter names only these, and a "+
			"filter naming none of them selects nothing at all. Point it somewhere else first.")
	}
	if err != nil {
		return err
	}
	// After the commit, so a run reads the row uncategorized. The purge stands
	// if queueing fails, with the rows uncategorized and unreviewed.
	if len(resuggest) > 0 {
		if automations, err := env.automations(); err == nil {
			if _, err := automations.EnqueueForRows(ctx, sp.ID(), resuggest,
				domain.AutomationFiredByManual, false); err != nil {
				slog.WarnContext(ctx, "unused: could not ask for new suggestions", "error", err)
			}
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

// refuseCategories re-asks, inside the purge's transaction, whether each
// category can go. An id that is not a live category in this space is not
// found rather than refused: a 409 would confirm it exists.
func refuseCategories(
	tx *store.Store, r *http.Request, sp auth.SpaceContext, ids []uuid.UUID,
) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	categories, err := tx.ListCategories(r.Context(), sp.ID(), false)
	if err != nil {
		return nil, err
	}
	live := make(map[uuid.UUID]store.Category, len(categories))
	for _, category := range categories {
		live[category.ID] = category
	}
	want := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if _, ok := live[id]; !ok {
			return nil, errNotFound("Category")
		}
		want[id] = true
	}
	uses, err := tx.CategoryUses(r.Context(), sp.ID(), ids)
	if err != nil {
		return nil, err
	}
	ok, why := prunableCategories(categories, uses, want)
	var refused []string
	for _, id := range ids {
		if !ok[id] {
			refused = append(refused, fmt.Sprintf("%q — %s", live[id].Name, why[id]))
		}
	}
	return refused, nil
}

// refuseTags is refuseCategories for tags, which have no tree and no tag the
// app maintains.
func refuseTags(
	tx *store.Store, r *http.Request, sp auth.SpaceContext, ids []uuid.UUID,
) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	tags, err := tx.ListTags(r.Context(), sp.ID(), false)
	if err != nil {
		return nil, err
	}
	live := make(map[uuid.UUID]store.Tag, len(tags))
	for _, tag := range tags {
		live[tag.ID] = tag
	}
	for _, id := range ids {
		if _, ok := live[id]; !ok {
			return nil, errNotFound("Tag")
		}
	}
	uses, err := tx.TagUses(r.Context(), sp.ID(), ids)
	if err != nil {
		return nil, err
	}
	var refused []string
	for _, id := range ids {
		if len(uses[id]) > 0 {
			refused = append(refused,
				fmt.Sprintf("tag %q — it is used by %s", live[id].Name, joinEnglish(uses[id])))
		}
	}
	return refused, nil
}

// prunableCategories works out which wanted categories can go. Refusals
// cascade (a held subcategory holds its parent and grandparent), so it repeats
// until it settles.
func prunableCategories(
	categories []store.Category, uses map[uuid.UUID][]string, want map[uuid.UUID]bool,
) (map[uuid.UUID]bool, map[uuid.UUID]string) {
	ok := map[uuid.UUID]bool{}
	why := map[uuid.UUID]string{}
	for _, category := range categories {
		if !want[category.ID] {
			continue
		}
		reason := domain.CategoryProtection(category.KnownCategoryID, category.IsEditable)
		switch {
		case reason != "":
			why[category.ID] = reason
		case len(uses[category.ID]) > 0:
			why[category.ID] = "it is used by " + joinEnglish(uses[category.ID])
		default:
			ok[category.ID] = true
		}
	}

	for changed := true; changed; {
		changed = false
		for _, category := range categories {
			if ok[category.ID] || category.ParentID == uuid.Nil || !ok[category.ParentID] {
				continue
			}
			delete(ok, category.ParentID)
			why[category.ParentID] = fmt.Sprintf("its subcategory %q stays", category.Name)
			changed = true
		}
	}
	return ok, why
}

// refusalMessage names what a purge will not take, at most three and then how
// many more.
func refusalMessage(refused []string) string {
	slices.Sort(refused)
	shown := refused[:min(len(refused), 3)]
	out := joinEnglish(shown)
	if extra := len(refused) - len(shown); extra > 0 {
		out += fmt.Sprintf(", and %d more", extra)
	}
	return out
}

// categoryPath is the branch a category sits on, so two rows with the same name
// can be told apart. A parent that is missing from the list is not walked past.
func categoryPath(byID map[uuid.UUID]store.Category, category store.Category) string {
	parts := domain.CategoryPath(category.ID, func(id uuid.UUID) (string, uuid.UUID, bool) {
		found, ok := byID[id]
		if !ok {
			return "", uuid.Nil, false
		}
		return found.Name, found.ParentID, true
	})
	return strings.Join(parts, " · ")
}

// joinEnglish is "a", "a and b", "a, b and c" — a list a person reads.
func joinEnglish(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}
