package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewUnusedServiceHandler(unusedService{env}, opts...)
	})
}

type unusedService struct{ env *Env }

func (s unusedService) ListUnused(
	ctx context.Context, _ *agentifiv1.ListUnusedRequest,
) (*agentifiv1.ListUnusedResponse, error) {
	sp := spaceFrom(ctx)
	categories, err := s.env.DB.ListCategories(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	categoryUses, err := s.env.DB.CategoryUses(ctx, sp.ID(), nil)
	if err != nil {
		return nil, err
	}
	tags, err := s.env.DB.ListTags(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	tagUses, err := s.env.DB.TagUses(ctx, sp.ID(), nil)
	if err != nil {
		return nil, err
	}

	byID := make(map[uuid.UUID]store.Category, len(categories))
	want := make(map[uuid.UUID]bool, len(categories))
	for _, category := range categories {
		byID[category.ID] = category
		want[category.ID] = true
	}
	unused, _ := prunableCategories(categories, categoryUses, want)

	out := &agentifiv1.ListUnusedResponse{
		Categories:        []*agentifiv1.UnusedCategory{},
		Tags:              []*agentifiv1.Tag{},
		CategoriesChecked: append(store.CategoryUseLabels(), "subcategories"),
		TagsChecked:       store.TagUseLabels(),
	}
	for _, category := range categories {
		if !unused[category.ID] {
			continue
		}
		out.Categories = append(out.Categories, &agentifiv1.UnusedCategory{
			Id:       category.ID.String(),
			ParentId: nullUUIDString(category.ParentID),
			Name:     category.Name,
			Kind:     string(category.Kind),
			Path:     categoryPath(byID, category),
		})
	}
	for _, tag := range tags {
		if len(tagUses[tag.ID]) == 0 {
			out.Tags = append(out.Tags, tagProto(tag))
		}
	}
	return out, nil
}

func (s unusedService) PurgeUnused(
	ctx context.Context, req *agentifiv1.PurgeUnusedRequest,
) (*agentifiv1.PurgeUnusedResponse, error) {
	sp := spaceFrom(ctx)
	requestedCategories, err := uuidsField(req.GetCategoryIds(), "body", "category_ids")
	if err != nil {
		return nil, err
	}
	requestedTags, err := uuidsField(req.GetTagIds(), "body", "tag_ids")
	if err != nil {
		return nil, err
	}
	// Deduplicated: the write counts the rows it changed, and the same id twice
	// would look like a row that had gone.
	categoryIDs, tagIDs := textutil.Distinct(requestedCategories), textutil.Distinct(requestedTags)
	if len(categoryIDs) == 0 && len(tagIDs) == 0 {
		return nil, errInvalid("missing", []string{"body"},
			"category_ids or tag_ids must name at least one category or tag")
	}

	out := &agentifiv1.PurgeUnusedResponse{}
	var resuggest []uuid.UUID
	err = s.env.DB.InTx(ctx, func(tx *store.Store) error {
		if err := tx.LockForPurge(ctx, sp.ID(), categoryIDs, tagIDs); err != nil {
			return err
		}
		refused, err := refuseCategories(ctx, tx, sp, categoryIDs)
		if err != nil {
			return err
		}
		refusedTags, err := refuseTags(ctx, tx, sp, tagIDs)
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
		out = &agentifiv1.PurgeUnusedResponse{
			CategoriesDeleted: int32(categories.Deleted),
			TagsDeleted:       int32(tags.Deleted),
			FiltersRepaired:   int32(categories.FiltersRepaired + tags.FiltersRepaired),
			FiltersRetired:    int32(categories.FiltersRetired + tags.FiltersRetired),
			Resuggested:       int32(len(categories.Resuggest)),
		}
		resuggest = categories.Resuggest
		return nil
	})
	if errors.Is(err, store.ErrPurgeEmptiesFilter) {
		return nil, errConflict("%s", "nothing was deleted: a saved filter names only these, and a "+
			"filter naming none of them selects nothing at all. Point it somewhere else first.")
	}
	if err != nil {
		return nil, err
	}
	// After the commit, so a run reads the row uncategorized. The purge stands
	// if queueing fails, with the rows uncategorized and unreviewed.
	if len(resuggest) > 0 {
		if automations, err := s.env.automations(); err == nil {
			if _, err := automations.EnqueueForRows(ctx, sp.ID(), resuggest,
				domain.AutomationFiredByManual, false); err != nil {
				slog.WarnContext(ctx, "unused: could not ask for new suggestions", "error", err)
			}
		}
	}
	return out, nil
}

// refuseCategories re-asks, inside the purge's transaction, whether each
// category can go. An id that is not a live category in this space is not
// found rather than refused: a 409 would confirm it exists.
func refuseCategories(
	ctx context.Context, tx *store.Store, sp auth.SpaceContext, ids []uuid.UUID,
) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	categories, err := tx.ListCategories(ctx, sp.ID(), false)
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
	uses, err := tx.CategoryUses(ctx, sp.ID(), ids)
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
	ctx context.Context, tx *store.Store, sp auth.SpaceContext, ids []uuid.UUID,
) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	tags, err := tx.ListTags(ctx, sp.ID(), false)
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
	uses, err := tx.TagUses(ctx, sp.ID(), ids)
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
