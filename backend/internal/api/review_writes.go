package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Review's writes. Each goes through the route a person would call on the
// screen that owns the row or the item, so corrections, declined suggestions
// and every other side effect are the same wherever the decision is taken.

// reviewApproved is what one approval did.
type reviewApproved struct {
	Approved []uuid.UUID
	Skipped  []reviewRow
	Undo     string
}

// approveReviewRows approves each row still in the row lane: it applies the
// row's pending suggestion, or marks it reviewed. A duplicate leg and a row
// with no category cannot be approved by a tick; with onlyIfLooksRight, a row
// that needs a look now is skipped too (calculations.md §15).
func approveReviewRows(ctx context.Context, env *Env, sp auth.SpaceContext, ids []uuid.UUID, onlyIfLooksRight bool) (reviewApproved, error) {
	var out reviewApproved
	if err := sp.RequireWrite(); err != nil {
		return out, err
	}
	lane, err := reviewLane(ctx, env, sp)
	if err != nil {
		return out, err
	}
	var undo []reviewUndoRow
	for _, id := range ids {
		row, ok := lane[id]
		if !ok {
			continue
		}
		blocked := row.Reason == domain.ReviewPossibleDuplicate || row.Reason == domain.ReviewNoCategory
		if blocked || (onlyIfLooksRight && !row.Reason.LooksRight()) {
			out.Skipped = append(out.Skipped, row)
			continue
		}
		before := reviewStateOf(row.Txn)
		if row.Suggestion != nil {
			_, err = env.callAsPerson(ctx, sp, http.MethodPost, "/assistant-actions/"+row.Suggestion.ActionID.String()+"/apply", map[string]any{})
		}
		if err == nil {
			// Applying marks the row reviewed only as a best effort, so this
			// makes sure of it.
			_, err = env.callAsPerson(ctx, sp, http.MethodPatch, "/transactions/"+id.String(), map[string]any{"is_reviewed": true})
		}
		if err != nil {
			if len(ids) == 1 {
				return out, err
			}
			out.Skipped = append(out.Skipped, row)
			err = nil
			continue
		}
		after, err := env.DB.GetTransaction(ctx, sp.ID(), id)
		if err != nil {
			return out, err
		}
		out.Approved = append(out.Approved, id)
		undo = append(undo, reviewUndoRow{ID: id, Before: before, After: reviewStateOf(after)})
	}
	out.Undo, err = env.signReviewUndo(sp, undo)
	return out, err
}

// fileReviewRow files a row under a category and approves it. Filing by hand
// declines the row's pending suggestions and records the correction, as the
// register's category picker does.
func fileReviewRow(ctx context.Context, env *Env, sp auth.SpaceContext, id, categoryID uuid.UUID) (string, error) {
	if err := sp.RequireWrite(); err != nil {
		return "", err
	}
	row, err := env.DB.GetTransaction(ctx, sp.ID(), id)
	if err == nil && row.IsDeleted {
		err = errNotFound("Transaction")
	}
	if err != nil {
		return "", notFoundAs(err, "Transaction")
	}
	before := reviewStateOf(row)
	body := map[string]any{"category_id": categoryID, "is_reviewed": true}
	if len(row.Splits) > 0 {
		body["splits"] = []SplitWrite{}
	}
	if _, err := env.callAsPerson(ctx, sp, http.MethodPatch, "/transactions/"+id.String(), body); err != nil {
		return "", err
	}
	after, err := env.DB.GetTransaction(ctx, sp.ID(), id)
	if err != nil {
		return "", err
	}
	return env.signReviewUndo(sp, []reviewUndoRow{{ID: id, Before: before, After: reviewStateOf(after)}})
}

// undoReview puts each row back while it is exactly as the review write
// left it; a row changed since is reported and left alone.
func undoReview(ctx context.Context, env *Env, sp auth.SpaceContext, token string) (restored, changed []uuid.UUID, err error) {
	if err := sp.RequireWrite(); err != nil {
		return nil, nil, err
	}
	undo, err := env.openReviewUndo(sp, token)
	if err != nil {
		return nil, nil, err
	}
	for _, one := range undo.Rows {
		row, err := env.DB.GetTransaction(ctx, sp.ID(), one.ID)
		if isNotFound(err) || (err == nil && row.IsDeleted) {
			changed = append(changed, one.ID)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		current := reviewStateOf(row)
		if !current.same(one.After) {
			changed = append(changed, one.ID)
			continue
		}
		if _, err := env.callAsPerson(ctx, sp, http.MethodPatch, "/transactions/"+one.ID.String(), one.Before.restoreBody(current)); err != nil {
			return nil, nil, err
		}
		restored = append(restored, one.ID)
	}
	return restored, changed, nil
}

// reviewLane is the row lane by transaction id.
func reviewLane(ctx context.Context, env *Env, sp auth.SpaceContext) (map[uuid.UUID]reviewRow, error) {
	snapshot, err := gatherReview(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]reviewRow{}
	for _, day := range snapshot.Days {
		for _, row := range day.Rows {
			out[row.Txn.ID] = row
		}
	}
	return out, nil
}

// actOnReviewItem decides an item through its source's route. The item is
// read again, so a decision on an item already settled elsewhere is a 404.
func actOnReviewItem(ctx context.Context, env *Env, sp auth.SpaceContext, key, action, text string) error {
	if err := sp.RequireWrite(); err != nil {
		return err
	}
	snapshot, err := gatherReview(ctx, env, sp)
	if err != nil {
		return err
	}
	var item *reviewItem
	for i := range snapshot.Items {
		if snapshot.Items[i].Key == key {
			item = &snapshot.Items[i]
			break
		}
	}
	if item == nil {
		return errNotFound("Review item")
	}
	offered := false
	for _, one := range item.Actions {
		offered = offered || one.Action == action
	}
	if !offered {
		return errConflict("%s is not a decision this item offers", action)
	}

	target := item.Target
	call := func(method, path string, body any) error {
		_, err := env.callAsPerson(ctx, sp, method, path, body)
		return err
	}
	switch {
	case action == "answer_code":
		return call(http.MethodPost, "/bills/challenges/"+target.ID.String()+"/answer", map[string]any{"code": text})
	case action == "mark_paid" && item.Kind == domain.ReviewStatementDue:
		return call(http.MethodPost, "/bills/statements/"+target.ID.String()+"/paid", nil)
	case action == "mark_paid":
		return call(http.MethodPost, "/occurrences/accept", map[string]any{"series_id": target.SeriesID, "due_on": target.DueOn})
	case action == "skip_occurrence":
		return call(http.MethodPost, "/occurrences/skip", map[string]any{"series_id": target.SeriesID, "due_on": target.DueOn})
	case action == "keep_suggested":
		return call(http.MethodPost, "/transaction-duplicates/"+target.ID.String()+"/decide", map[string]any{"verdict": store.VerdictDuplicate, "keep_id": target.KeepID})
	case action == "keep_other":
		return call(http.MethodPost, "/transaction-duplicates/"+target.ID.String()+"/decide", map[string]any{"verdict": store.VerdictDuplicate, "keep_id": target.OtherID})
	case action == "both_real":
		return call(http.MethodPost, "/transaction-duplicates/"+target.ID.String()+"/decide", map[string]any{"verdict": store.VerdictDistinct})
	case action == "accept_balance":
		return call(http.MethodPost, "/held-balances/"+target.ID.String()+"/accept", nil)
	case action == "repair_transfers":
		return call(http.MethodPost, "/transfers/orphans/repair", nil)
	case action == "accept_proposal":
		return call(http.MethodPost, "/assistant-actions/"+target.ID.String()+"/apply", map[string]any{})
	case action == "decline_proposal":
		return call(http.MethodPost, "/assistant-actions/"+target.ID.String()+"/decline", map[string]any{"reason": text})
	}
	return errConflict("%s is not a decision this item offers", action)
}
