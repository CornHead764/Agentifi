package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The register, and every edit that can be made to a row.
//
//   - statement_name is the bank's wording, set once at ingest and never
//     editable. The payee is the name a person edits.
//   - The register's window comes from windowOf, the same resolver the
//     account summary uses (trap 5).
//   - Deleting a transfer leg releases its partner in the same database
//     transaction.
//   - An effective date derived from the statement cycle follows the row when
//     it is edited or moved; one a person corrected by hand survives.
//
// The register loads every matching row rather than paginating in SQL: the
// filter chip's net comes from internal/domain over the whole result set, and
// a SUM() would be a second implementation that could disagree.
//
// TransactionResponse is the REST shape the series routes still write; a
// procedure builds one and converts it with transactionProto.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewTransactionServiceHandler(transactionService{env}, opts...)
	})
}

type transactionService struct{ env *Env }

const defaultPageSize = 100

// maxPageSize bounds one request. The register is virtualized on the client
// and a five-hundred-row page is already more than a screen can use.
const maxPageSize = 500

// RegisterQuery is everything the transaction list is narrowed by. One value,
// so "mark all as reviewed" and the list cannot disagree about which rows.
type RegisterQuery struct {
	Window    Window
	Accounts  accountFilter
	FilterID  uuid.UUID
	HasFilter bool
	// Search is the plain search box: the substring match over both names and
	// the note. The search DSL parses to a Filter instead.
	Search      string
	IsReviewed  bool
	HasReviewed bool
	// HidePadding leaves padding income rows (domain.Transaction.IsPadding)
	// out of the list and the bulk review, reporting them beside the page.
	// The aggregate ignores it: the pads are income there as everywhere else.
	HidePadding bool
	Descending  bool
	Limit       int
	Offset      int
}

// registerQuery is the register query a REST route's parameters name.
func registerQuery(r *http.Request) (RegisterQuery, error) {
	window, err := WindowFromRequest(r)
	if err != nil {
		return RegisterQuery{}, err
	}
	accounts, err := queryAccountFilter(r)
	if err != nil {
		return RegisterQuery{}, err
	}
	filterID, hasFilter, err := queryUUID(r, "filter_id")
	if err != nil {
		return RegisterQuery{}, err
	}
	reviewed, hasReviewed, err := queryBool(r, "reviewed")
	if err != nil {
		return RegisterQuery{}, err
	}
	limit, err := queryInt(r, "limit", defaultPageSize, 1, maxPageSize)
	if err != nil {
		return RegisterQuery{}, err
	}
	offset, err := queryInt(r, "offset", 0, 0, 1<<31-1)
	if err != nil {
		return RegisterQuery{}, err
	}
	query := RegisterQuery{
		Window:      window,
		Accounts:    accounts,
		FilterID:    filterID,
		HasFilter:   hasFilter,
		Search:      strings.TrimSpace(r.URL.Query().Get("search")),
		IsReviewed:  reviewed,
		HasReviewed: hasReviewed,
		Limit:       limit,
		Offset:      offset,
	}
	return query, registerOrdering(&query, r.URL.Query().Get("padding"), r.URL.Query().Get("order"))
}

// registerRequest is the register query a procedure's request carries; the
// list, the aggregate and the bulk review all carry it.
type registerRequest interface {
	GetFrom() string
	GetTo() string
	GetDateField() string
	GetAccountId() *agentifiv1.IdSet
	GetFilterId() string
	GetSearch() string
	GetPadding() string
	GetOrder() string
}

// registerQueryOf is registerQuery for a procedure. reviewed, limit and
// offset are the request's optional fields, which an interface cannot reach.
func registerQueryOf(req registerRequest, reviewed *bool, limit, offset *int32) (RegisterQuery, error) {
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return RegisterQuery{}, err
	}
	var accounts accountFilter
	if set := req.GetAccountId(); set != nil {
		ids, err := uuidsField(set.GetIds(), "query", "account_id")
		if err != nil {
			return RegisterQuery{}, err
		}
		accounts = accountFilter{IDs: ids, Given: true}
	}
	filterID, err := uuidField(strings.TrimSpace(req.GetFilterId()), "query", "filter_id")
	if err != nil {
		return RegisterQuery{}, err
	}
	pageSize, err := limitField("limit", limit, defaultPageSize, 1, maxPageSize)
	if err != nil {
		return RegisterQuery{}, err
	}
	skip, err := limitField("offset", offset, 0, 0, 1<<31-1)
	if err != nil {
		return RegisterQuery{}, err
	}
	query := RegisterQuery{
		Window:      window,
		Accounts:    accounts,
		FilterID:    filterID,
		HasFilter:   filterID != uuid.Nil,
		Search:      strings.TrimSpace(req.GetSearch()),
		IsReviewed:  reviewed != nil && *reviewed,
		HasReviewed: reviewed != nil,
		Limit:       pageSize,
		Offset:      skip,
	}
	return query, registerOrdering(&query, req.GetPadding(), req.GetOrder())
}

func registerOrdering(query *RegisterQuery, padding, order string) error {
	switch padding {
	case "", "show":
	case "hide":
		query.HidePadding = true
	default:
		return errInvalid("enum", []string{"query", "padding"},
			"padding must be show or hide, got %q", padding)
	}
	query.Descending = true
	switch order {
	case "", "desc":
	case "asc":
		query.Descending = false
	default:
		return errInvalid("enum", []string{"query", "order"},
			"order must be asc or desc, got %q", order)
	}
	return nil
}

type SplitResponse struct {
	ID         uuid.UUID    `json:"id"`
	Position   int          `json:"position"`
	Amount     domain.Money `json:"amount"`
	CategoryID *uuid.UUID   `json:"category_id"`
	Memo       *string      `json:"memo"`
	TagIDs     []uuid.UUID  `json:"tag_ids"`
}

type TransactionResponse struct {
	ID        uuid.UUID `json:"id"`
	AccountID uuid.UUID `json:"account_id"`
	// Date is when it happened. This is the date the register shows.
	Date Date `json:"date"`
	// EffectiveDate is when it hits cash flow — for a card charge, the due
	// date of the statement it lands in. Null means "same as date".
	EffectiveDate *Date `json:"effective_date"`

	Amount   domain.Money `json:"amount"`
	Currency string       `json:"currency"`
	// AmountPrimary is the amount in the space's primary currency. Every
	// aggregate sums this.
	AmountPrimary *domain.Money `json:"amount_primary"`
	FxRateUsed    *domain.Rate  `json:"fx_rate_used"`

	// StatementName is the bank's wording, read-only. Payee is the display
	// name, editable; every display path reads it.
	StatementName string `json:"statement_name"`
	Payee         string `json:"payee"`
	// Memo is the provider's second line about the charge, read-only like the
	// statement name. Notes is the household's own.
	Memo string `json:"memo"`
	// TransactedOn is when the purchase happened, where the feed said so
	// separately from the posting date; null otherwise.
	TransactedOn *Date `json:"transacted_on"`
	// ProviderExtra is whatever else the feed sent, passed through as stored.
	ProviderExtra json.RawMessage `json:"provider_extra,omitempty"`
	Notes         *string         `json:"notes"`
	CheckNumber   *string         `json:"check_number"`
	CategoryID    *uuid.UUID      `json:"category_id"`
	Source        domain.Source   `json:"source"`

	IsPending                bool `json:"is_pending"`
	IsReviewed               bool `json:"is_reviewed"`
	ExcludedFromReports      bool `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan bool `json:"excluded_from_spending_plan"`
	IsBill                   bool `json:"is_bill"`
	IsSubscription           bool `json:"is_subscription"`

	// TransferPairID is written on *both* legs of a matched transfer, and
	// released from the survivor when one leg is deleted.
	TransferPairID *uuid.UUID `json:"transfer_pair_id"`
	// PaddedTxnID, on the income row a padding mail rule writes, is the
	// purchase it pads; PaddingTxnID, on that purchase, is the income row.
	PaddedTxnID  *uuid.UUID `json:"padded_txn_id"`
	PaddingTxnID *uuid.UUID `json:"padding_txn_id"`

	UserFlag     *string    `json:"user_flag"`
	UserFlagNote *string    `json:"user_flag_note"`
	SeriesID     *uuid.UUID `json:"series_id"`
	SeriesDueOn  *Date      `json:"series_due_on"`
	// Balance is the stored running balance for this row's account.
	Balance *domain.Money `json:"balance"`

	Splits []SplitResponse `json:"splits"`
	// MatchedSplitIDs and MatchedAmount are set only in a filtered list, on a
	// split row the filter kept part of: the splits it kept and their sum in
	// the primary currency.
	MatchedSplitIDs []uuid.UUID   `json:"matched_split_ids"`
	MatchedAmount   *domain.Money `json:"matched_amount"`
	TagIDs          []uuid.UUID   `json:"tag_ids"`
	// AttachmentCount is how many files hang off the row, so the register can
	// draw a paperclip without asking per row what it would find there.
	AttachmentCount int `json:"attachment_count"`
	// ReceiptStatus is domain.ReceiptStatusOf: missing, on_file or not_needed
	// on a row its account holds to a receipt, null on every other row.
	ReceiptStatus *domain.ReceiptStatus `json:"receipt_status"`
	// ReceiptNotNeeded is a person saying the row needs no receipt.
	ReceiptNotNeeded bool `json:"receipt_not_needed"`
	// Suggestion is the assistant's pending proposal for this row, or null.
	Suggestion *TransactionSuggestion `json:"suggestion"`

	// CheckingCategory is a category check queued or running for this row,
	// read off the run table so a returning register still shows the spinner.
	CheckingCategory bool `json:"checking_category"`
	// CategoryCheckedAt and CategoryCheckNote are the last check that filed and
	// proposed nothing. The client derives the state from the pair, since a
	// hand edit would stale a verdict.
	CategoryCheckedAt *time.Time `json:"category_checked_at"`
	CategoryCheckNote string     `json:"category_check_note"`
	// CategoryCheckRunID is the assistant run that last decided this row's
	// category. Null on a row nobody checked and on one filed by hand.
	CategoryCheckRunID *uuid.UUID `json:"category_check_run_id"`
}

// TransactionSuggestion is one waiting proposal, as the register reads it: a
// projection of the pending card. Applying still goes through
// `/assistant-actions/{id}/apply`.
type TransactionSuggestion struct {
	ActionID       uuid.UUID `json:"action_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	// RunID is the automation run behind it, null for a proposal made in a
	// conversation somebody was having.
	RunID *uuid.UUID `json:"run_id"`
	Tool  string     `json:"tool"`
	// Summary is the model's own line about why, which is the evidence a
	// person reads before agreeing.
	Summary string `json:"summary"`
	// CategoryID is what an `update_transaction` proposal would file the row
	// under; null on a proposal that files nothing.
	CategoryID *uuid.UUID `json:"category_id"`
	// Splits is what a `split_transaction` proposal would replace the row's
	// allocations with, in the order it asked for. Empty on anything else.
	Splits    []TransactionSuggestionSplit `json:"splits"`
	CreatedAt time.Time                    `json:"created_at"`
}

// TransactionSuggestionSplit is one part of a proposed split, in the shape the
// write carries: nothing has been written, so there is no split id.
type TransactionSuggestionSplit struct {
	Amount     domain.Money `json:"amount"`
	CategoryID *uuid.UUID   `json:"category_id"`
	Memo       string       `json:"memo"`
}

// splitWrite is one allocation a request asks for, read off the wire.
type splitWrite struct {
	Amount     domain.Money
	CategoryID uuid.UUID
	Memo       string
	TagIDs     []uuid.UUID
}

func splitWrites(writes []*agentifiv1.SplitWrite) ([]splitWrite, error) {
	out := make([]splitWrite, 0, len(writes))
	for _, write := range writes {
		var one splitWrite
		if write.GetAmount() != nil {
			amount, err := moneyFrom(write.GetAmount(), "body", "splits", "amount")
			if err != nil {
				return nil, err
			}
			one.Amount = amount
		}
		categoryID, err := uuidField(write.GetCategoryId(), "body", "splits", "category_id")
		if err != nil {
			return nil, err
		}
		tagIDs, err := uuidsField(write.GetTagIds(), "body", "splits", "tag_ids")
		if err != nil {
			return nil, err
		}
		one.CategoryID, one.Memo, one.TagIDs = categoryID, write.GetMemo(), tagIDs
		out = append(out, one)
	}
	return out, nil
}

func (s transactionService) ListTransactions(
	ctx context.Context, req *agentifiv1.ListTransactionsRequest,
) (*agentifiv1.ListTransactionsResponse, error) {
	sp := spaceFrom(ctx)
	query, err := registerQueryOf(req, req.Reviewed, req.Limit, req.Offset)
	if err != nil {
		return nil, err
	}
	matched, err := matchRegister(ctx, s.env, sp, query)
	if err != nil {
		return nil, err
	}
	postings, rows := matched.Postings, matched.Rows
	var padding []domain.Posting
	if query.HidePadding {
		postings, padding = domain.FoldPadding(postings)
	}

	page := postings
	if query.Offset < len(page) {
		page = page[query.Offset:]
	} else {
		page = nil
	}
	if len(page) > query.Limit {
		page = page[:query.Limit]
	}

	items := make([]TransactionResponse, 0, len(page))
	for _, posting := range page {
		key, err := store.ParseID(posting.Txn.ID)
		if err != nil {
			return nil, err
		}
		item := transactionResponse(rows[key])
		if parts, ok := matched.Partial[posting.Txn.ID]; ok {
			item.MatchedSplitIDs = make([]uuid.UUID, 0, len(parts))
			for _, part := range parts {
				splitID, err := store.ParseID(part.Split.ID)
				if err != nil {
					return nil, err
				}
				item.MatchedSplitIDs = append(item.MatchedSplitIDs, splitID)
			}
			amount := domain.MatchedAmount(posting, parts)
			item.MatchedAmount = &amount
		}
		items = append(items, item)
	}
	if err := decorateTransactions(ctx, s.env, sp, items, rows); err != nil {
		return nil, err
	}

	partialCount := 0
	for _, posting := range postings {
		if _, ok := matched.Partial[posting.Txn.ID]; ok {
			partialCount++
		}
	}
	kept := func(p domain.Posting) domain.Money {
		return domain.MatchedAmount(p, matched.Partial[p.Txn.ID])
	}
	out := &agentifiv1.ListTransactionsResponse{
		Items:        make([]*agentifiv1.Transaction, 0, len(items)),
		Count:        int32(len(postings)),
		Total:        moneyProto(domain.Sum(postings, kept)),
		FullTotal:    moneyProto(domain.Sum(postings, domain.Posting.Amount)),
		PartialCount: int32(partialCount),
		PaddingCount: int32(len(padding)),
		PaddingTotal: moneyProto(domain.Sum(padding, kept)),
		Window:       windowProto(query.Window),
		Limit:        int32(query.Limit),
		Offset:       int32(query.Offset),
	}
	for _, item := range items {
		out.Items = append(out.Items, transactionProto(item))
	}
	return out, nil
}

// AggregateTransactions is the Spending and Income tabs, answered in one call.
// It reads the register's query the same way as the list, so the chart and
// the table under it describe the same rows (trap 5).
func (s transactionService) AggregateTransactions(
	ctx context.Context, req *agentifiv1.AggregateTransactionsRequest,
) (*agentifiv1.AggregateTransactionsResponse, error) {
	sp := spaceFrom(ctx)
	query, err := registerQueryOf(req, req.Reviewed, req.Limit, req.Offset)
	if err != nil {
		return nil, err
	}
	options, err := aggregateOptionsOf(req.GetDirection(), req.GetGroupBy(), req.GetUnder())
	if err != nil {
		return nil, err
	}
	options.Mode = query.Window.Mode

	matched, err := matchRegister(ctx, s.env, sp, query)
	if err != nil {
		return nil, err
	}
	options.Partial = matched.Partial
	if err := nameAggregate(ctx, s.env, sp, &options); err != nil {
		return nil, err
	}

	result := domain.Aggregate(matched.Postings, options)
	months := make([]*agentifiv1.TransactionAggregateMonth, 0, len(result.Months))
	for _, month := range result.Months {
		months = append(months, &agentifiv1.TransactionAggregateMonth{
			Month:   month.Month,
			Buckets: aggregateBuckets(month.Buckets),
		})
	}
	return &agentifiv1.AggregateTransactionsResponse{
		Direction: string(options.Direction),
		GroupBy:   string(options.GroupBy),
		Total:     moneyProto(result.Total),
		Count:     int32(result.Count),
		Buckets:   aggregateBuckets(result.Buckets),
		Months:    months,
		Window:    windowProto(query.Window),
	}, nil
}

func aggregateBuckets(buckets []domain.AggregateBucket) []*agentifiv1.TransactionAggregateBucket {
	out := make([]*agentifiv1.TransactionAggregateBucket, 0, len(buckets))
	for _, bucket := range buckets {
		out = append(out, &agentifiv1.TransactionAggregateBucket{
			Key: bucket.Key, Label: bucket.Label, Total: moneyProto(bucket.Total),
		})
	}
	return out
}

// nameAggregate gives the aggregate the space's categories and tags. Deleted
// rows included: a deleted category still labels and excludes the spending
// filed under it.
func nameAggregate(ctx context.Context, env *Env, sp auth.SpaceContext, options *domain.AggregateOptions) error {
	categoryRows, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return err
	}
	options.Categories = make(map[domain.ID]domain.Category, len(categoryRows))
	for _, row := range categoryRows {
		options.Categories[domain.ID(row.ID.String())] = store.DomainCategory(row)
	}
	tagRows, err := env.DB.ListTags(ctx, sp.ID(), true)
	if err != nil {
		return err
	}
	options.Tags = make(map[domain.ID]string, len(tagRows))
	for _, row := range tagRows {
		options.Tags[domain.ID(row.ID.String())] = row.Name
	}
	return nil
}

// aggregateOptionsOf reads the aggregate's own knobs. visible_accounts_only is
// deliberately not one: history still counts spending in an account since
// closed, as the report engine does.
func aggregateOptionsOf(direction, groupBy, under string) (domain.AggregateOptions, error) {
	options := domain.AggregateOptions{
		Direction: domain.AggregateSpending,
		GroupBy:   domain.AggregateByCategory,
	}
	switch direction {
	case "", "spending":
	case "income":
		options.Direction = domain.AggregateIncome
	default:
		return options, errInvalid("enum", []string{"query", "direction"},
			"direction must be spending or income, got %q", direction)
	}
	switch groupBy {
	case "", "category":
	case "payee":
		options.GroupBy = domain.AggregateByPayee
	case "tag":
		options.GroupBy = domain.AggregateByTag
	case "none":
		options.GroupBy = domain.AggregateByNone
	default:
		return options, errInvalid("enum", []string{"query", "group_by"},
			"group_by must be category, payee, tag or none, got %q", groupBy)
	}
	if under != "" {
		id, err := uuid.Parse(under)
		if err != nil {
			return options, errInvalid("uuid_parsing", []string{"query", "under"},
				"under must be a category id, got %q", under)
		}
		options.Under = domain.ID(id.String())
	}
	return options, nil
}

// MarkTransactionsReviewed is the review queue's primary button, over the
// whole query rather than the page on screen.
func (s transactionService) MarkTransactionsReviewed(
	ctx context.Context, req *agentifiv1.MarkTransactionsReviewedRequest,
) (*agentifiv1.MarkTransactionsReviewedResponse, error) {
	sp := spaceFrom(ctx)
	query, err := registerQueryOf(req, req.Reviewed, req.Limit, req.Offset)
	if err != nil {
		return nil, err
	}
	reviewed := req.IsReviewed == nil || req.GetIsReviewed()

	postings, rows, err := matchingRegister(ctx, s.env, sp, query)
	if err != nil {
		return nil, err
	}
	if query.HidePadding {
		postings, _ = domain.FoldPadding(postings)
	}

	// Only the rows whose flag actually moves, so the reported count is what
	// changed rather than what the query selected.
	changed := make([]uuid.UUID, 0, len(postings))
	for _, posting := range postings {
		key, err := store.ParseID(posting.Txn.ID)
		if err != nil {
			return nil, err
		}
		if rows[key].IsReviewed == reviewed {
			continue
		}
		changed = append(changed, key)
	}
	if err := s.env.DB.SetTransactionsReviewed(ctx, sp.ID(), changed, reviewed); err != nil {
		return nil, err
	}
	return &agentifiv1.MarkTransactionsReviewedResponse{
		Updated: int32(len(changed)),
		Window:  windowProto(query.Window),
	}, nil
}

func (s transactionService) CreateTransaction(
	ctx context.Context, req *agentifiv1.CreateTransactionRequest,
) (*agentifiv1.CreateTransactionResponse, error) {
	sp := spaceFrom(ctx)
	accountID, err := uuidField(req.GetAccountId(), "body", "account_id")
	if err != nil {
		return nil, err
	}
	date, err := dateField(req.GetDate(), "body", "date")
	if err != nil {
		return nil, err
	}
	amount := domain.Zero
	if req.GetAmount() != nil {
		if amount, err = moneyFrom(req.GetAmount(), "body", "amount"); err != nil {
			return nil, err
		}
	}
	effectiveDate, err := dateField(req.GetEffectiveDate(), "body", "effective_date")
	if err != nil {
		return nil, err
	}
	categoryID, err := uuidField(req.GetCategoryId(), "body", "category_id")
	if err != nil {
		return nil, err
	}
	requestedTags, err := uuidsField(req.GetTagIds(), "body", "tag_ids")
	if err != nil {
		return nil, err
	}
	writes, err := splitWrites(req.GetSplits())
	if err != nil {
		return nil, err
	}

	account, err := requireAccount(ctx, s.env, sp, accountID)
	if err != nil {
		return nil, err
	}
	if err := checkCategory(ctx, s.env, sp, categoryID); err != nil {
		return nil, err
	}
	tagIDs, err := resolveTags(ctx, s.env, sp, requestedTags)
	if err != nil {
		return nil, err
	}

	row := &store.Transaction{
		AccountID:     account.ID,
		Date:          date,
		EffectiveDate: effectiveDate,
		Amount:        amount,
		Currency:      store.Deref(req.Currency, account.Currency),
		// The bank's wording, recorded once. Every later edit goes to Payee.
		StatementName:            req.GetStatementName(),
		Payee:                    req.GetPayee(),
		Notes:                    req.GetNotes(),
		CheckNumber:              req.GetCheckNumber(),
		CategoryID:               categoryID,
		Source:                   domain.SourceManual,
		IsPending:                req.GetIsPending(),
		IsReviewed:               req.GetIsReviewed() || account.Kind.BornReviewed(),
		ExcludedFromReports:      req.GetExcludedFromReports(),
		ExcludedFromSpendingPlan: req.GetExcludedFromSpendingPlan(),
		IsBill:                   req.GetIsBill(),
		IsSubscription:           req.GetIsSubscription(),
		UserFlag:                 req.GetUserFlag(),
		UserFlagNote:             req.GetUserFlagNote(),
		TagIDs:                   tagIDs,
	}
	if len(writes) > 0 {
		splits, err := buildSplits(ctx, s.env, sp, row.Amount, writes)
		if err != nil {
			return nil, err
		}
		row.Splits = splits
		row.CategoryID = uuid.Nil
	}

	// An effective date the client did not send is derived from the statement
	// cycle, so a hand-entered card charge files like a synced one.
	if req.EffectiveDate == nil {
		service.ApplyEffectiveDate(row, account, domain.Date{}, false)
	}

	if err := stampForeignAmount(ctx, s.env, sp, row); err != nil {
		return nil, err
	}
	if err := s.env.DB.CreateTransaction(ctx, sp.ID(), row); err != nil {
		return nil, err
	}
	if err := recomputeRunningBalances(ctx, s.env, sp, row.AccountID); err != nil {
		return nil, err
	}
	txn, err := transactionByID(ctx, s.env, sp, row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateTransactionResponse{Transaction: txn}, nil
}

func (s transactionService) GetTransaction(
	ctx context.Context, req *agentifiv1.GetTransactionRequest,
) (*agentifiv1.GetTransactionResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveTransactionOf(ctx, s.env, sp, req.GetTransactionId())
	if err != nil {
		return nil, err
	}
	item, err := oneTransaction(ctx, s.env, sp, row)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetTransactionResponse{Transaction: transactionProto(item)}, nil
}

func (s transactionService) UpdateTransaction(
	ctx context.Context, req *agentifiv1.UpdateTransactionRequest,
) (*agentifiv1.UpdateTransactionResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveTransactionOf(ctx, s.env, sp, req.GetTransactionId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	accountID, err := optUUIDOf(mask, "account_id", req.AccountId)
	if err != nil {
		return nil, err
	}
	date, err := optDateOf(mask, "date", req.Date)
	if err != nil {
		return nil, err
	}
	effectiveDate, err := optDateOf(mask, "effective_date", req.EffectiveDate)
	if err != nil {
		return nil, err
	}
	amount, err := optMoneyOf(mask, "amount", req.Amount)
	if err != nil {
		return nil, err
	}
	categoryID, err := optUUIDOf(mask, "category_id", req.CategoryId)
	if err != nil {
		return nil, err
	}
	currency := optOf(mask, "currency", req.Currency)

	// The allocations were written against the old figure; rescaling them
	// would invent a split, and keeping them would disagree with the amount.
	if amount.Present() && len(row.Splits) > 0 && !amount.Value.Equal(row.Amount) {
		return nil, errConflict("re-split the transaction after changing its amount")
	}

	// Nothing revisits a transfer pair once the token is written, so neither
	// leg may move to the other's account or change amount alone.
	if row.TransferPairID != uuid.Nil {
		if accountID.Present() && accountID.Value != row.AccountID {
			return nil, errConflict("unlink the transfer before moving this row to another account")
		}
		if amount.Present() && !amount.Value.Equal(row.Amount) {
			return nil, errConflict("unlink the transfer before changing this row's amount")
		}
	}

	refiled := categoryID.Set && valueOrNil(categoryID) != row.CategoryID && !dispatched(ctx)
	subject := correctionFacts{
		transactionID: row.ID, statementName: row.StatementName, payee: row.Payee,
		amount: row.Amount, hasAmount: true,
	}
	previousAccount := row.AccountID
	previousAmount := row.Amount
	previousCurrency := row.Currency
	moved := accountID.Present() && accountID.Value != row.AccountID
	var newAccount, oldAccount store.Account
	if moved {
		if newAccount, err = requireAccount(ctx, s.env, sp, accountID.Value); err != nil {
			return nil, err
		}
		if oldAccount, err = s.env.DB.GetAccount(ctx, sp.ID(), previousAccount); err != nil {
			return nil, err
		}
	}
	// Decided before the edit moves anything. A derived effective date is
	// unset or exactly what the old cycle would produce; a hand-corrected one
	// survives the move.
	derivedEffectiveDate := moved && (row.EffectiveDate.IsZero() ||
		row.EffectiveDate == domain.EffectiveDateFor(
			row.Date, oldAccount.Kind == domain.KindCreditCard,
			service.StatementCloseDay(oldAccount), service.PaymentDueDay(oldAccount),
			domain.Date{}))
	derivedCurrency := moved && row.Currency == oldAccount.Currency

	if accountID.Cleared() {
		return nil, errConflict("account_id cannot be cleared")
	}
	if categoryID.Set {
		if err := checkCategory(ctx, s.env, sp, valueOrNil(categoryID)); err != nil {
			return nil, err
		}
	}
	if mask["tag_ids"] {
		requested, err := uuidsField(req.GetTagIds(), "body", "tag_ids")
		if err != nil {
			return nil, err
		}
		tagIDs, err := resolveTags(ctx, s.env, sp, requested)
		if err != nil {
			return nil, err
		}
		row.TagIDs = tagIDs
	}

	if accountID.Present() {
		row.AccountID = accountID.Value
	}
	if err := applyRequired("date", date, &row.Date); err != nil {
		return nil, err
	}
	applyNullable(effectiveDate, &row.EffectiveDate)
	if err := applyRequired("amount", amount, &row.Amount); err != nil {
		return nil, err
	}
	if err := applyRequired("currency", currency, &row.Currency); err != nil {
		return nil, err
	}
	if err := applyRequired("payee", optOf(mask, "payee", req.Payee), &row.Payee); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "notes", req.Notes), &row.Notes)
	applyNullable(optOf(mask, "check_number", req.CheckNumber), &row.CheckNumber)
	applyNullable(categoryID, &row.CategoryID)
	// A split row's categories are its splits', and reports read only those:
	// one category for the row files every split under it, and the parent
	// keeps none.
	if len(row.Splits) > 0 && row.CategoryID != uuid.Nil {
		for i := range row.Splits {
			row.Splits[i].CategoryID = row.CategoryID
		}
		row.CategoryID = uuid.Nil
	}
	for _, flag := range []struct {
		name  string
		value *bool
		dst   *bool
	}{
		{"is_pending", req.IsPending, &row.IsPending},
		{"is_reviewed", req.IsReviewed, &row.IsReviewed},
		{"excluded_from_reports", req.ExcludedFromReports, &row.ExcludedFromReports},
		{"excluded_from_spending_plan", req.ExcludedFromSpendingPlan, &row.ExcludedFromSpendingPlan},
		{"is_bill", req.IsBill, &row.IsBill},
		{"is_subscription", req.IsSubscription, &row.IsSubscription},
	} {
		if err := applyRequired(flag.name, optOf(mask, flag.name, flag.value), flag.dst); err != nil {
			return nil, err
		}
	}
	applyNullable(optOf(mask, "user_flag", req.UserFlag), &row.UserFlag)
	applyNullable(optOf(mask, "user_flag_note", req.UserFlagNote), &row.UserFlagNote)
	if err := applyRequired("receipt_not_needed", optOf(mask, "receipt_not_needed", req.ReceiptNotNeeded),
		&row.ReceiptNotNeeded); err != nil {
		return nil, err
	}

	if moved {
		// The old account's cycle and currency no longer describe this row. A
		// value sent with the move wins over both.
		if !effectiveDate.Set && derivedEffectiveDate {
			service.ApplyEffectiveDate(&row, newAccount, domain.Date{}, true)
		}
		if !currency.Set && derivedCurrency {
			row.Currency = newAccount.Currency
		}
	}

	// A changed amount or currency invalidates the stored conversion.
	if !row.Amount.Equal(previousAmount) || row.Currency != previousCurrency {
		service.ClearPrimaryAmount(&row)
		// Re-derived now: an install with no sync or import has no next sweep,
		// and the foreign amount would count at face value.
		if err := stampForeignAmount(ctx, s.env, sp, &row); err != nil {
			return nil, err
		}
	}

	if err := s.env.DB.UpdateTransaction(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	// Only these three move the stored running balance. A pending row already
	// counts, and a review or a rename does not.
	if amount.Set || date.Set || accountID.Set {
		if err := recomputeRunningBalances(ctx, s.env, sp, row.AccountID); err != nil {
			return nil, err
		}
		if row.AccountID != previousAccount {
			if err := recomputeRunningBalances(ctx, s.env, sp, previousAccount); err != nil {
				return nil, err
			}
		}
	}
	if refiled {
		settleRowSuggestions(ctx, s.env, sp, subject, valueOrNil(categoryID))
	}
	txn, err := transactionByID(ctx, s.env, sp, row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateTransactionResponse{Transaction: txn}, nil
}

// DeleteTransaction soft-deletes a row. store.DeleteTransaction releases the
// transfer partner and deletes a purchase's padding income row in the same
// database transaction; the pad may be in another account, whose running
// balance is recomputed too.
func (s transactionService) DeleteTransaction(
	ctx context.Context, req *agentifiv1.DeleteTransactionRequest,
) (*agentifiv1.DeleteTransactionResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveTransactionOf(ctx, s.env, sp, req.GetTransactionId())
	if err != nil {
		return nil, err
	}
	pads, err := s.env.DB.PaddingOf(ctx, sp.ID(), []uuid.UUID{row.ID})
	if err != nil {
		return nil, err
	}
	accounts := []uuid.UUID{row.AccountID}
	for _, padID := range pads {
		pad, err := s.env.DB.GetTransaction(ctx, sp.ID(), padID)
		if err != nil {
			return nil, err
		}
		if pad.AccountID != row.AccountID {
			accounts = append(accounts, pad.AccountID)
		}
	}
	if err := purgeAttachments(ctx, s.env, sp, row.ID); err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteTransaction(ctx, sp.ID(), row.ID); err != nil {
		return nil, notFoundAs(err, "Transaction")
	}
	for _, accountID := range accounts {
		if err := recomputeRunningBalances(ctx, s.env, sp, accountID); err != nil {
			return nil, err
		}
	}
	return &agentifiv1.DeleteTransactionResponse{}, nil
}

// SetTransactionSplits replaces a row's allocations. Splits must sum to the
// amount: reports read the splits when they exist and the parent otherwise.
func (s transactionService) SetTransactionSplits(
	ctx context.Context, req *agentifiv1.SetTransactionSplitsRequest,
) (*agentifiv1.SetTransactionSplitsResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveTransactionOf(ctx, s.env, sp, req.GetTransactionId())
	if err != nil {
		return nil, err
	}
	writes, err := splitWrites(req.GetSplits())
	if err != nil {
		return nil, err
	}
	splits, err := buildSplits(ctx, s.env, sp, row.Amount, writes)
	if err != nil {
		return nil, err
	}

	row.Splits = splits
	if len(splits) > 0 {
		// Reports read the splits when they exist and the parent otherwise;
		// leaving a category on the parent as well counts the row twice.
		row.CategoryID = uuid.Nil
	}
	if err := s.env.DB.UpdateTransaction(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	if len(splits) > 0 && !dispatched(ctx) {
		settleRowSuggestions(ctx, s.env, sp, correctionFacts{
			transactionID: row.ID, statementName: row.StatementName, payee: row.Payee,
			amount: row.Amount, hasAmount: true,
		}, uuid.Nil)
	}
	txn, err := transactionByID(ctx, s.env, sp, row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.SetTransactionSplitsResponse{Transaction: txn}, nil
}

// LinkTransactionSeries records the row as one occurrence of a series: the
// occurrence named, or the one nearest the row's date.
func (s transactionService) LinkTransactionSeries(
	ctx context.Context, req *agentifiv1.LinkTransactionSeriesRequest,
) (*agentifiv1.LinkTransactionSeriesResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveTransactionOf(ctx, s.env, sp, req.GetTransactionId())
	if err != nil {
		return nil, err
	}
	seriesID, err := uuidField(req.GetSeriesId(), "body", "series_id")
	if err != nil {
		return nil, err
	}
	if seriesID == uuid.Nil {
		return nil, errBadRequest("series_id names the series to link to")
	}

	var (
		seriesRow service.SeriesRow
		dueOn     domain.Date
	)
	if req.DueOn != nil {
		named, err := dateField(req.GetDueOn(), "body", "due_on")
		if err != nil {
			return nil, err
		}
		seriesRow, _, dueOn, err = occurrenceTarget(ctx, s.env, sp, seriesID, named)
		if err != nil {
			return nil, err
		}
	} else {
		seriesRow, err = service.NewSeriesMatcher(s.env.DB).GetSeries(ctx, sp.ID(), seriesID)
		if err != nil {
			if isNotFound(err) || strings.Contains(err.Error(), "no rows") {
				return nil, errNotFound("Series")
			}
			return nil, err
		}
		if seriesRow.IsDeleted {
			return nil, errNotFound("Series")
		}
		series := service.ToDomainSeries(seriesRow)
		var found bool
		if dueOn, found = nearestDueDate(series, row.Date); !found {
			return nil, errConflict("%s has no occurrences to link to", series.Label())
		}
	}

	// One charge per slot. A forecast in the slot does not count (see
	// store.SeriesSlotSettled): the link upgrades it, as the matcher does.
	settled, taken, err := s.env.DB.SeriesSlotSettled(ctx, sp.ID(), seriesRow.ID, dueOn)
	if err != nil {
		return nil, err
	}
	if taken && settled.ID != row.ID {
		return nil, errSlotTaken(settled)
	}

	outcome, err := service.NewSeriesMatcher(s.env.DB).LinkByHand(ctx, sp.ID(), row, seriesRow, dueOn)
	if err != nil {
		return nil, err
	}
	if outcome.RetiredID != uuid.Nil {
		if err := recomputeRunningBalances(ctx, s.env, sp, row.AccountID); err != nil {
			return nil, err
		}
	}
	txn, err := transactionByID(ctx, s.env, sp, outcome.SurvivingID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.LinkTransactionSeriesResponse{Transaction: txn}, nil
}

// UnlinkTransactionSeries releases the row from its occurrence. The series'
// pointer is left alone.
func (s transactionService) UnlinkTransactionSeries(
	ctx context.Context, req *agentifiv1.UnlinkTransactionSeriesRequest,
) (*agentifiv1.UnlinkTransactionSeriesResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveTransactionOf(ctx, s.env, sp, req.GetTransactionId())
	if err != nil {
		return nil, err
	}
	if row.SeriesID == uuid.Nil {
		return nil, errConflict("this transaction is not linked to a series")
	}
	row.SeriesID = uuid.Nil
	row.SeriesDueOn = domain.Date{}
	if err := s.env.DB.UpdateTransaction(ctx, sp.ID(), &row); err != nil {
		return nil, err
	}
	item, err := oneTransaction(ctx, s.env, sp, row)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UnlinkTransactionSeriesResponse{Transaction: transactionProto(item)}, nil
}

// matchingRegister is the full ordered result set, as postings, plus the rows
// they came from.
func matchingRegister(ctx context.Context, env *Env, sp auth.SpaceContext, query RegisterQuery) ([]domain.Posting, map[uuid.UUID]store.Transaction, error) {
	matched, err := matchRegister(ctx, env, sp, query)
	return matched.Postings, matched.Rows, err
}

// registerMatch is the register's result set and what the filter kept of each
// split row it kept only part of.
type registerMatch struct {
	Postings []domain.Posting
	Rows     map[uuid.UUID]store.Transaction
	// Partial is domain.PartialParts by row id; a row kept whole is absent.
	Partial map[domain.ID][]domain.Match
}

// matchRegister is matchingRegister with the partial split rows. A stored
// filter is applied here rather than in SQL because it matches per allocation,
// and the one implementation of that is in internal/domain.
func matchRegister(ctx context.Context, env *Env, sp auth.SpaceContext, query RegisterQuery) (registerMatch, error) {
	// A selection of no accounts is not the whole ledger: an empty result is the
	// honest answer, and loading every row would answer a different question.
	if query.Accounts.selectsNothing() {
		return registerMatch{Rows: map[uuid.UUID]store.Transaction{}}, nil
	}

	from, to, mode := query.Window.storeQuery()
	postings, rows, err := service.LoadPostings(ctx, env.DB, sp.ID(), store.TransactionQuery{
		AccountIDs: query.Accounts.IDs,
		From:       from,
		To:         to,
		DateMode:   mode,
	})
	if err != nil {
		return registerMatch{}, err
	}

	kept := make([]domain.Posting, 0, len(postings))
	needle := strings.ToLower(query.Search)
	for _, posting := range postings {
		key, err := store.ParseID(posting.Txn.ID)
		if err != nil {
			return registerMatch{}, err
		}
		row := rows[key]
		// An ignored account's rows are in its own register and nobody else's.
		if posting.Account.IsIgnored && len(query.Accounts.IDs) == 0 {
			continue
		}
		if query.HasReviewed && row.IsReviewed != query.IsReviewed {
			continue
		}
		// Both names, because a row somebody renamed is still findable by what
		// the bank called it.
		if needle != "" && !matchesSearch(row, needle) {
			continue
		}
		kept = append(kept, posting)
	}

	partial := map[domain.ID][]domain.Match{}
	if query.HasFilter {
		evaluated, err := loadFilter(ctx, env, sp, query.FilterID)
		if err != nil {
			return registerMatch{}, err
		}
		facets, err := service.FilterFacets(ctx, env.DB, sp.ID(), rows, evaluated)
		if err != nil {
			return registerMatch{}, err
		}
		kept = domain.Select(evaluated, kept, facets, query.Window.Mode)
		for _, posting := range kept {
			if parts := domain.PartialParts(evaluated, posting, facets[posting.Txn.ID], query.Window.Mode); parts != nil {
				partial[posting.Txn.ID] = parts
			}
		}
	}

	// Sorted here so `order=asc` is exactly the reverse of `order=desc`; the id
	// breaks ties so paging does not drop rows at the seam.
	sort.SliceStable(kept, func(i, j int) bool {
		left := domain.ReportingDate(kept[i].Txn, query.Window.Mode)
		right := domain.ReportingDate(kept[j].Txn, query.Window.Mode)
		if left != right {
			if query.Descending {
				return right.Before(left)
			}
			return left.Before(right)
		}
		if query.Descending {
			return kept[i].Txn.ID > kept[j].Txn.ID
		}
		return kept[i].Txn.ID < kept[j].Txn.ID
	})
	return registerMatch{Postings: kept, Rows: rows, Partial: partial}, nil
}

func matchesSearch(row store.Transaction, needle string) bool {
	return strings.Contains(strings.ToLower(row.Payee), needle) ||
		strings.Contains(strings.ToLower(row.StatementName), needle) ||
		strings.Contains(strings.ToLower(row.Notes), needle)
}

func buildSplits(ctx context.Context, env *Env, sp auth.SpaceContext, amount domain.Money, writes []splitWrite) ([]store.Split, error) {
	if len(writes) > 0 {
		allocated := domain.Sum(writes, func(s splitWrite) domain.Money { return s.Amount })
		if !allocated.Equal(amount.Round()) {
			return nil, errConflict("splits total %s but the transaction is %s", allocated, amount)
		}
	}

	splits := make([]store.Split, 0, len(writes))
	for position, write := range writes {
		if err := checkCategory(ctx, env, sp, write.CategoryID); err != nil {
			return nil, err
		}
		tagIDs, err := resolveTags(ctx, env, sp, write.TagIDs)
		if err != nil {
			return nil, err
		}
		splits = append(splits, store.Split{
			Position:   position,
			Amount:     write.Amount,
			CategoryID: write.CategoryID,
			Memo:       write.Memo,
			TagIDs:     tagIDs,
		})
	}
	return splits, nil
}

// liveTransactionOf reads one row, treating a soft-deleted one and one in
// another space as the same 404.
func liveTransactionOf(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Transaction, error) {
	id, err := idFrom(rawID, "Transaction")
	if err != nil {
		return store.Transaction{}, err
	}
	row, err := env.DB.GetTransaction(ctx, sp.ID(), id)
	if err != nil {
		return store.Transaction{}, notFoundAs(err, "Transaction")
	}
	if row.IsDeleted {
		return store.Transaction{}, errNotFound("Transaction")
	}
	return row, nil
}

// transactionByID re-reads a row through the space-scoped query, as a
// procedure answers it.
func transactionByID(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) (*agentifiv1.Transaction, error) {
	row, err := env.DB.GetTransaction(ctx, sp.ID(), id)
	if err != nil {
		return nil, err
	}
	item, err := oneTransaction(ctx, env, sp, row)
	if err != nil {
		return nil, err
	}
	return transactionProto(item), nil
}

// oneTransaction is a single row with everything the register shows beside
// it, so the detail panel and the register agree about whether a file is
// there.
func oneTransaction(ctx context.Context, env *Env, sp auth.SpaceContext, row store.Transaction) (TransactionResponse, error) {
	items := []TransactionResponse{transactionResponse(row)}
	if err := decorateTransactions(ctx, env, sp, items, map[uuid.UUID]store.Transaction{row.ID: row}); err != nil {
		return TransactionResponse{}, err
	}
	return items[0], nil
}

// decorateTransactions fills in what a row alone does not carry, one query per
// kind for the whole page.
func decorateTransactions(
	ctx context.Context, env *Env, sp auth.SpaceContext, items []TransactionResponse,
	rows map[uuid.UUID]store.Transaction,
) error {
	if err := countAttachmentsInto(ctx, env, sp, items); err != nil {
		return err
	}
	if err := fillReceiptsInto(ctx, env, sp, items, rows); err != nil {
		return err
	}
	if err := fillSuggestionsInto(ctx, env, sp, items); err != nil {
		return err
	}
	if err := fillCategoryChecksInto(ctx, env, sp, items); err != nil {
		return err
	}
	return fillPaddingInto(ctx, env, sp, items)
}

// fillSuggestionsInto hangs each row's waiting proposal on it, one query per
// page.
func fillSuggestionsInto(ctx context.Context, env *Env, sp auth.SpaceContext, items []TransactionResponse) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	waiting, err := env.DB.PendingActionsForTransactions(ctx, sp.ID(), ids)
	if err != nil {
		return err
	}
	for i := range items {
		pending, ok := waiting[items[i].ID]
		if !ok {
			continue
		}
		suggestion := transactionSuggestion(pending)
		if restatesRowCategory(items[i], suggestion) {
			continue
		}
		items[i].Suggestion = suggestion
	}
	return nil
}

// restatesRowCategory is a suggestion of the category an unsplit row already
// has, which the category cell would offer as a change that changes nothing.
func restatesRowCategory(row TransactionResponse, suggestion *TransactionSuggestion) bool {
	return suggestion != nil && suggestion.CategoryID != nil && len(suggestion.Splits) == 0 &&
		len(row.Splits) == 0 && row.CategoryID != nil && *row.CategoryID == *suggestion.CategoryID
}

// maxCheckedRows is the most rows one progress read may name. A long batch's
// progress is the batch's own read; this one patches the rows on screen.
const maxCheckedRows = 200

// GetCategoryCheckProgress answers where a named set of rows has got to.
func (s transactionService) GetCategoryCheckProgress(
	ctx context.Context, req *agentifiv1.GetCategoryCheckProgressRequest,
) (*agentifiv1.GetCategoryCheckProgressResponse, error) {
	sp := spaceFrom(ctx)
	ids, err := uuidsField(req.GetId().GetIds(), "query", "id")
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errBadRequest("Name at least one transaction")
	}
	if len(ids) > maxCheckedRows {
		return nil, errBadRequest("At most %d transactions at once", maxCheckedRows)
	}
	// Deduplicated before anything is counted: a batch that named a row twice
	// would otherwise report a total the rows can never reach.
	seen := make(map[uuid.UUID]bool, len(ids))
	unique := ids[:0]
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	ids = unique

	rows, err := s.env.DB.ListTransactions(ctx, sp.ID(), store.TransactionQuery{IDs: ids, IncludeEstimates: true})
	if err != nil {
		return nil, err
	}
	items := make([]TransactionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, transactionResponse(row))
	}
	if err := fillSuggestionsInto(ctx, s.env, sp, items); err != nil {
		return nil, err
	}
	if err := fillCategoryChecksInto(ctx, s.env, sp, items); err != nil {
		return nil, err
	}

	out := &agentifiv1.GetCategoryCheckProgressResponse{
		Total: int32(len(ids)),
		Rows:  make([]*agentifiv1.CategoryCheckRow, 0, len(items)),
	}
	for _, item := range items {
		out.Rows = append(out.Rows, &agentifiv1.CategoryCheckRow{
			TransactionId:      item.ID.String(),
			Checking:           item.CheckingCategory,
			CategoryId:         uuidPtrString(item.CategoryID),
			CategoryCheckedAt:  timestampOf(item.CategoryCheckedAt),
			CategoryCheckNote:  item.CategoryCheckNote,
			CategoryCheckRunId: uuidPtrString(item.CategoryCheckRunID),
			Suggestion:         transactionSuggestionProto(item.Suggestion),
		})
		if !item.CheckingCategory {
			out.Done++
		}
	}
	out.Done += int32(len(ids) - len(out.Rows))
	return out, nil
}

// fillCategoryChecksInto marks the rows a check is working on, one query per
// page.
func fillCategoryChecksInto(ctx context.Context, env *Env, sp auth.SpaceContext, items []TransactionResponse) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	checking, err := env.DB.PendingCategoryChecks(ctx, sp.ID(), ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].CheckingCategory = checking[items[i].ID]
	}
	return nil
}

func transactionSuggestion(pending store.PendingActionFor) *TransactionSuggestion {
	action := pending.Action
	// The store already leaves these out; said again here so a caller holding
	// an action from elsewhere cannot put "Uncategorized" in the cell.
	if action.ToolName == "update_transaction" {
		if _, named := action.Body["category_id"]; !named {
			return nil
		}
	}
	out := &TransactionSuggestion{
		ActionID: action.ID, ConversationID: action.ConversationID,
		Tool: action.ToolName, Summary: action.Summary, CreatedAt: action.CreatedAt,
		Splits: []TransactionSuggestionSplit{},
	}
	if pending.RunID != uuid.Nil {
		id := pending.RunID
		out.RunID = &id
	}
	if raw, ok := action.Body["category_id"].(string); ok {
		if id, err := uuid.Parse(raw); err == nil {
			out.CategoryID = &id
		}
	}
	// An amount that will not parse is left out rather than rendered as $0.00.
	for _, split := range bodySplits(action.Body) {
		text, _ := split["amount"].(string)
		amount, err := domain.FromString(text)
		if err != nil {
			continue
		}
		one := TransactionSuggestionSplit{Amount: amount}
		if memo, ok := split["memo"].(string); ok {
			one.Memo = memo
		}
		if raw, ok := split["category_id"].(string); ok {
			if id, err := uuid.Parse(raw); err == nil {
				one.CategoryID = &id
			}
		}
		out.Splits = append(out.Splits, one)
	}
	return out
}

// fillPaddingInto names each purchase's padding income row, one query per
// page; the pad may sit in another account or outside the window.
func fillPaddingInto(ctx context.Context, env *Env, sp auth.SpaceContext, items []TransactionResponse) error {
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	pads, err := env.DB.PaddingOf(ctx, sp.ID(), ids)
	if err != nil {
		return err
	}
	for i := range items {
		if pad, ok := pads[items[i].ID]; ok {
			items[i].PaddingTxnID = &pad
		}
	}
	return nil
}

// countAttachmentsInto fills AttachmentCount over a whole page in one query.
func countAttachmentsInto(ctx context.Context, env *Env, sp auth.SpaceContext, items []TransactionResponse) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	counts, err := env.DB.CountDocumentsOnTransactions(ctx, sp.ID(), ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].AttachmentCount = counts[items[i].ID]
	}
	return nil
}

// fillReceiptsInto sets ReceiptStatus over a page from the attachment counts
// countAttachmentsInto filled, so the marker and the paperclip agree.
func fillReceiptsInto(
	ctx context.Context, env *Env, sp auth.SpaceContext, items []TransactionResponse,
	rows map[uuid.UUID]store.Transaction,
) error {
	page := make(map[uuid.UUID]store.Transaction, len(items))
	documented := make(map[uuid.UUID]bool, len(items))
	for _, item := range items {
		if row, ok := rows[item.ID]; ok {
			page[item.ID] = row
		}
		documented[item.ID] = item.AttachmentCount > 0
	}
	statuses, err := service.ReceiptStatuses(ctx, env.DB, sp.ID(), page, documented)
	if err != nil {
		return err
	}
	for i := range items {
		if status, ok := statuses[items[i].ID]; ok {
			items[i].ReceiptStatus = &status
		}
	}
	return nil
}

func transactionResponse(t store.Transaction) TransactionResponse {
	splits := make([]SplitResponse, 0, len(t.Splits))
	for _, split := range t.Splits {
		splits = append(splits, SplitResponse{
			ID:         split.ID,
			Position:   split.Position,
			Amount:     split.Amount,
			CategoryID: dbconv.NullUUID(split.CategoryID),
			Memo:       dbconv.NullText(split.Memo),
			TagIDs:     store.NonNil(split.TagIDs),
		})
	}
	return TransactionResponse{
		ID:                       t.ID,
		AccountID:                t.AccountID,
		Date:                     Date(t.Date),
		EffectiveDate:            nullableDate(t.EffectiveDate),
		Amount:                   t.Amount,
		Currency:                 t.Currency,
		AmountPrimary:            store.PtrIf(t.AmountPrimary, t.HasAmountPrimary),
		FxRateUsed:               store.PtrIf(t.FxRateUsed, t.HasFxRateUsed),
		StatementName:            t.StatementName,
		Payee:                    t.Payee,
		Memo:                     t.Memo,
		TransactedOn:             nullableDate(t.TransactedOn),
		ProviderExtra:            t.ProviderExtra,
		Notes:                    dbconv.NullText(t.Notes),
		CheckNumber:              dbconv.NullText(t.CheckNumber),
		CategoryID:               dbconv.NullUUID(t.CategoryID),
		Source:                   t.Source,
		IsPending:                t.IsPending,
		IsReviewed:               t.IsReviewed,
		ExcludedFromReports:      t.ExcludedFromReports,
		ExcludedFromSpendingPlan: t.ExcludedFromSpendingPlan,
		IsBill:                   t.IsBill,
		IsSubscription:           t.IsSubscription,
		TransferPairID:           dbconv.NullUUID(t.TransferPairID),
		PaddedTxnID:              dbconv.NullUUID(t.PaddedTxnID),
		UserFlag:                 dbconv.NullText(t.UserFlag),
		UserFlagNote:             dbconv.NullText(t.UserFlagNote),
		ReceiptNotNeeded:         t.ReceiptNotNeeded,
		SeriesID:                 dbconv.NullUUID(t.SeriesID),
		SeriesDueOn:              nullableDate(t.SeriesDueOn),
		Balance:                  store.PtrIf(t.Balance, t.HasBalance),
		Splits:                   splits,
		TagIDs:                   store.NonNil(t.TagIDs),
		CategoryCheckedAt:        t.CategoryCheckedAt,
		CategoryCheckNote:        t.CategoryCheckNote,
		CategoryCheckRunID:       t.CategoryCheckRunID,
	}
}

// transactionProto is a decorated row on a procedure's wire.
func transactionProto(t TransactionResponse) *agentifiv1.Transaction {
	splits := make([]*agentifiv1.Split, 0, len(t.Splits))
	for _, split := range t.Splits {
		splits = append(splits, &agentifiv1.Split{
			Id:         split.ID.String(),
			Position:   int32(split.Position),
			Amount:     moneyProto(split.Amount),
			CategoryId: uuidPtrString(split.CategoryID),
			Memo:       split.Memo,
			TagIds:     uuidStrings(split.TagIDs),
		})
	}
	out := &agentifiv1.Transaction{
		Id:                       t.ID.String(),
		AccountId:                t.AccountID.String(),
		Date:                     domain.Date(t.Date).String(),
		EffectiveDate:            datePtrString(t.EffectiveDate),
		Amount:                   moneyProto(t.Amount),
		Currency:                 t.Currency,
		AmountPrimary:            moneyPtrProto(t.AmountPrimary),
		StatementName:            t.StatementName,
		Payee:                    t.Payee,
		Memo:                     t.Memo,
		TransactedOn:             datePtrString(t.TransactedOn),
		ProviderExtra:            jsonValue(t.ProviderExtra),
		Notes:                    t.Notes,
		CheckNumber:              t.CheckNumber,
		CategoryId:               uuidPtrString(t.CategoryID),
		Source:                   string(t.Source),
		IsPending:                t.IsPending,
		IsReviewed:               t.IsReviewed,
		ExcludedFromReports:      t.ExcludedFromReports,
		ExcludedFromSpendingPlan: t.ExcludedFromSpendingPlan,
		IsBill:                   t.IsBill,
		IsSubscription:           t.IsSubscription,
		TransferPairId:           uuidPtrString(t.TransferPairID),
		PaddedTxnId:              uuidPtrString(t.PaddedTxnID),
		PaddingTxnId:             uuidPtrString(t.PaddingTxnID),
		UserFlag:                 t.UserFlag,
		UserFlagNote:             t.UserFlagNote,
		SeriesId:                 uuidPtrString(t.SeriesID),
		SeriesDueOn:              datePtrString(t.SeriesDueOn),
		Balance:                  moneyPtrProto(t.Balance),
		Splits:                   splits,
		MatchedAmount:            moneyPtrProto(t.MatchedAmount),
		TagIds:                   uuidStrings(t.TagIDs),
		AttachmentCount:          int32(t.AttachmentCount),
		ReceiptNotNeeded:         t.ReceiptNotNeeded,
		Suggestion:               transactionSuggestionProto(t.Suggestion),
		CheckingCategory:         t.CheckingCategory,
		CategoryCheckedAt:        timestampOf(t.CategoryCheckedAt),
		CategoryCheckNote:        t.CategoryCheckNote,
		CategoryCheckRunId:       uuidPtrString(t.CategoryCheckRunID),
	}
	if t.FxRateUsed != nil {
		out.FxRateUsed = rateProto(*t.FxRateUsed, true)
	}
	if t.MatchedSplitIDs != nil {
		out.MatchedSplitIds = &agentifiv1.IdSet{Ids: uuidStrings(t.MatchedSplitIDs)}
	}
	if t.ReceiptStatus != nil {
		out.ReceiptStatus = proto.String(string(*t.ReceiptStatus))
	}
	return out
}

func transactionSuggestionProto(s *TransactionSuggestion) *agentifiv1.TransactionSuggestion {
	if s == nil {
		return nil
	}
	splits := make([]*agentifiv1.TransactionSuggestionSplit, 0, len(s.Splits))
	for _, split := range s.Splits {
		splits = append(splits, &agentifiv1.TransactionSuggestionSplit{
			Amount:     moneyProto(split.Amount),
			CategoryId: uuidPtrString(split.CategoryID),
			Memo:       split.Memo,
		})
	}
	return &agentifiv1.TransactionSuggestion{
		ActionId:       s.ActionID.String(),
		ConversationId: s.ConversationID.String(),
		RunId:          uuidPtrString(s.RunID),
		Tool:           s.Tool,
		Summary:        s.Summary,
		CategoryId:     uuidPtrString(s.CategoryID),
		Splits:         splits,
		CreatedAt:      timestampOf(&s.CreatedAt),
	}
}

func uuidPtrString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	return proto.String(id.String())
}

func datePtrString(d *Date) *string {
	if d == nil {
		return nil
	}
	return proto.String(domain.Date(*d).String())
}

func moneyPtrProto(m *domain.Money) *agentifiv1.NullableMoney {
	if m == nil {
		return nil
	}
	return nullableMoneyProto(*m, true)
}

// jsonValue is stored JSON as a Value, unset when nothing was stored or what
// was stored does not parse.
func jsonValue(raw json.RawMessage) *structpb.Value {
	if len(raw) == 0 {
		return nil
	}
	value := &structpb.Value{}
	if err := protojson.Unmarshal(raw, value); err != nil {
		return nil
	}
	return value
}

func valueOrNil(opt Opt[uuid.UUID]) uuid.UUID {
	if opt.Present() {
		return opt.Value
	}
	return uuid.Nil
}

func dateOrZero(d *Date) domain.Date {
	if d == nil {
		return domain.Date{}
	}
	return domain.Date(*d)
}

// maxPayees is how many distinct payees the picker is offered; the list is
// ordered so the ones worth offering survive the cap.
const maxPayees = 500

// ListPayees lists the distinct payees in the space. A payee is a column, not
// a row, so nothing else can.
func (s transactionService) ListPayees(
	ctx context.Context, req *agentifiv1.ListPayeesRequest,
) (*agentifiv1.ListPayeesResponse, error) {
	limit, err := limitField("limit", req.Limit, maxPayees, 1, maxPayees)
	if err != nil {
		return nil, err
	}
	names, err := s.env.DB.ListPayees(ctx, spaceFrom(ctx).ID(), limit)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListPayeesResponse{Payees: names}, nil
}
