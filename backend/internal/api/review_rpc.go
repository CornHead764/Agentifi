package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewReviewServiceHandler(reviewService{env}, opts...)
	})
}

type reviewService struct{ env *Env }

// reviewDaysPage is how many days ListReviewDays answers when asked for none.
const reviewDaysPage = 7

func (s reviewService) GetReviewSummary(ctx context.Context, _ *agentifiv1.GetReviewSummaryRequest) (*agentifiv1.GetReviewSummaryResponse, error) {
	snapshot, err := gatherReview(ctx, s.env, spaceFrom(ctx))
	if err != nil {
		return nil, err
	}
	sum := snapshot.Summary
	out := &agentifiv1.GetReviewSummaryResponse{
		ActNow: int32(sum.ActNow), MoneyAtStake: int32(sum.MoneyAtStake), TidyUp: int32(sum.TidyUp),
		Ideas: int32(sum.Ideas), NewRows: int32(sum.New), NeedsALook: int32(sum.NeedsALook), Badge: int32(sum.Badge()),
	}
	for _, kind := range domain.ReviewKinds {
		if n := snapshot.Kinds[kind]; n > 0 {
			out.Kinds = append(out.Kinds, &agentifiv1.ReviewKindCount{Kind: reviewKindProto(kind), Count: int32(n)})
		}
	}
	return out, nil
}

func (s reviewService) ListReviewItems(ctx context.Context, _ *agentifiv1.ListReviewItemsRequest) (*agentifiv1.ListReviewItemsResponse, error) {
	snapshot, err := gatherReview(ctx, s.env, spaceFrom(ctx))
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListReviewItemsResponse{Items: make([]*agentifiv1.ReviewItem, 0, len(snapshot.Items))}
	for _, item := range snapshot.Items {
		out.Items = append(out.Items, reviewItemProto(item))
	}
	return out, nil
}

func (s reviewService) ListReviewDays(ctx context.Context, req *agentifiv1.ListReviewDaysRequest) (*agentifiv1.ListReviewDaysResponse, error) {
	var before domain.Date
	if req.Before != nil {
		on, err := domain.ParseDate(req.GetBefore())
		if err != nil {
			return nil, errInvalid("date_from_datetime_parsing", []string{"body", "before"}, "before is not a date")
		}
		before = on
	}
	size := int(req.GetDays())
	if size <= 0 {
		size = reviewDaysPage
	}
	snapshot, err := gatherReview(ctx, s.env, spaceFrom(ctx))
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListReviewDaysResponse{}
	for _, day := range snapshot.Days {
		if req.Before != nil && !day.Date.Before(before) {
			continue
		}
		if len(out.Days) == size {
			next := out.Days[len(out.Days)-1].Date
			out.NextBefore = &next
			break
		}
		out.Days = append(out.Days, reviewDayProto(day, snapshot.Categories))
	}
	return out, nil
}

func (s reviewService) ApproveRows(ctx context.Context, req *agentifiv1.ApproveRowsRequest) (*agentifiv1.ApproveRowsResponse, error) {
	ids, err := reviewIDs(req.GetTransactionIds(), "transaction_ids")
	if err != nil {
		return nil, err
	}
	done, err := approveReviewRows(ctx, s.env, spaceFrom(ctx), ids, req.GetOnlyIfLooksRight())
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ApproveRowsResponse{UndoToken: done.Undo, ApprovedIds: idStrings(done.Approved)}
	if len(done.Skipped) > 0 {
		categories, err := reviewCategoryNames(ctx, s.env)
		if err != nil {
			return nil, err
		}
		for _, row := range done.Skipped {
			out.Skipped = append(out.Skipped, reviewRowProto(row, categories))
		}
	}
	return out, nil
}

func (s reviewService) FileRow(ctx context.Context, req *agentifiv1.FileRowRequest) (*agentifiv1.FileRowResponse, error) {
	id, err := idFrom(req.GetTransactionId(), "transaction_id")
	if err != nil {
		return nil, err
	}
	category, err := idFrom(req.GetCategoryId(), "category_id")
	if err != nil {
		return nil, err
	}
	token, err := fileReviewRow(ctx, s.env, spaceFrom(ctx), id, category)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.FileRowResponse{UndoToken: token}, nil
}

func (s reviewService) UndoReview(ctx context.Context, req *agentifiv1.UndoReviewRequest) (*agentifiv1.UndoReviewResponse, error) {
	restored, changed, err := undoReview(ctx, s.env, spaceFrom(ctx), req.GetUndoToken())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UndoReviewResponse{RestoredIds: idStrings(restored), ChangedIds: idStrings(changed)}, nil
}

func (s reviewService) Act(ctx context.Context, req *agentifiv1.ActRequest) (*agentifiv1.ActResponse, error) {
	action := strings.ToLower(strings.TrimPrefix(req.GetAction().String(), "REVIEW_ACTION_"))
	if req.GetAction() == agentifiv1.ReviewAction_REVIEW_ACTION_UNSPECIFIED {
		return nil, errInvalid("missing", []string{"body", "action"}, "action is required")
	}
	if err := actOnReviewItem(ctx, s.env, spaceFrom(ctx), req.GetKey(), action, req.GetText()); err != nil {
		return nil, err
	}
	return &agentifiv1.ActResponse{}, nil
}

// --- Wire shapes ---------------------------------------------------------------

func reviewKindProto(kind domain.ReviewKind) agentifiv1.ReviewKind {
	return agentifiv1.ReviewKind(agentifiv1.ReviewKind_value["REVIEW_KIND_"+strings.ToUpper(string(kind))])
}

func reviewReasonProto(reason domain.ReviewReason) agentifiv1.ReviewReason {
	return agentifiv1.ReviewReason(agentifiv1.ReviewReason_value["REVIEW_REASON_"+strings.ToUpper(string(reason))])
}

func reviewItemProto(item reviewItem) *agentifiv1.ReviewItem {
	out := &agentifiv1.ReviewItem{
		Key: item.Key, Kind: reviewKindProto(item.Kind), Tier: agentifiv1.ReviewTier(item.Kind.Tier()),
		Title: item.Title, Detail: item.Detail, SubjectIds: item.Subjects, Link: item.Link, Count: int32(item.Count),
	}
	if !item.DueAt.IsZero() {
		due := item.DueAt.UTC().Format(time.RFC3339)
		out.DueAt = &due
	}
	if !item.Amount.IsZero() {
		out.Amount = moneyProto(item.Amount)
	}
	for _, one := range item.Actions {
		out.Actions = append(out.Actions, &agentifiv1.ReviewActionOption{
			Action:  agentifiv1.ReviewAction(agentifiv1.ReviewAction_value["REVIEW_ACTION_"+strings.ToUpper(one.Action)]),
			Label:   one.Label,
			Confirm: one.Confirm,
		})
	}
	return out
}

func reviewDayProto(day reviewDay, categories map[uuid.UUID]store.Category) *agentifiv1.ReviewDay {
	out := &agentifiv1.ReviewDay{Date: day.Date.String(), Out: moneyProto(day.Out), NeedsALook: int32(day.NeedsALook)}
	for _, row := range day.Rows {
		out.Rows = append(out.Rows, reviewRowProto(row, categories))
	}
	return out
}

func reviewRowProto(row reviewRow, categories map[uuid.UUID]store.Category) *agentifiv1.ReviewRow {
	txn := row.Txn
	out := &agentifiv1.ReviewRow{
		TransactionId: txn.ID.String(),
		Date:          txn.Date.String(),
		EffectiveDate: store.DomainTransaction(txn).ReportingDate(domain.DateEffective).String(),
		Payee:         displayPayee(txn),
		StatementName: txn.StatementName,
		Amount:        moneyProto(txn.Amount),
		AccountId:     txn.AccountID.String(),
		AccountName:   row.AccountName,
		AccountMask:   row.AccountMask,
		IsPending:     txn.IsPending,
		Reason:        reviewReasonProto(row.Reason),
		DuplicateKey:  row.DuplicateKey,
		Notes:         txn.Notes,
	}
	if txn.CategoryID != uuid.Nil {
		id := txn.CategoryID.String()
		out.CategoryId, out.CategoryName = &id, categories[txn.CategoryID].Name
	}
	for _, split := range txn.Splits {
		out.Splits = append(out.Splits, reviewSplitProto(split.CategoryID, split.Amount, split.Memo, categories))
	}
	if suggestion := row.Suggestion; suggestion != nil {
		wire := &agentifiv1.ReviewSuggestion{
			ActionId: suggestion.ActionID.String(), Confidence: suggestion.Confidence, Basis: suggestion.Basis,
		}
		if suggestion.CategoryID != nil {
			id := suggestion.CategoryID.String()
			wire.CategoryId, wire.CategoryName = &id, categories[*suggestion.CategoryID].Name
		}
		for _, split := range suggestion.Splits {
			var category uuid.UUID
			if split.CategoryID != nil {
				category = *split.CategoryID
			}
			wire.Splits = append(wire.Splits, reviewSplitProto(category, split.Amount, split.Memo, categories))
		}
		out.Suggestion = wire
	}
	return out
}

func reviewSplitProto(category uuid.UUID, amount domain.Money, memo string, categories map[uuid.UUID]store.Category) *agentifiv1.ReviewSplit {
	out := &agentifiv1.ReviewSplit{Amount: moneyProto(amount), Memo: memo}
	if category != uuid.Nil {
		id := category.String()
		out.CategoryId, out.CategoryName = &id, categories[category].Name
	}
	return out
}

func reviewCategoryNames(ctx context.Context, env *Env) (map[uuid.UUID]store.Category, error) {
	rows, err := env.DB.ListCategories(ctx, spaceFrom(ctx).ID(), true)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]store.Category, len(rows))
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, nil
}

func reviewIDs(raw []string, field string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, one := range raw {
		id, err := idFrom(one, field)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func idStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
