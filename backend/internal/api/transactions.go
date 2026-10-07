package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The register, and every edit that can be made to a row.
//
//   - statement_name is the bank's wording, set once at ingest and never
//     editable. The payee is the name a person edits.
//   - The register's window comes from WindowFromRequest, the same resolver
//     the account summary uses (trap 5).
//   - Deleting a transfer leg releases its partner in the same database
//     transaction.
//   - An effective date derived from the statement cycle follows the row when
//     it is edited or moved; one a person corrected by hand survives.
//
// The register loads every matching row rather than paginating in SQL: the
// filter chip's net comes from internal/domain over the whole result set, and
// a SUM() would be a second implementation that could disagree.

func init() {
	Register(Resource{Prefix: "/transactions", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listTransactions)
		rt.Read(http.MethodGet, "/aggregate", aggregateTransactions)
		rt.Read(http.MethodGet, "/payees", listPayees)
		rt.Read(http.MethodGet, "/category-checks", categoryChecks)
		rt.Write(http.MethodPost, "/mark-reviewed", markAllReviewed)
		rt.Write(http.MethodPost, "/", createTransaction)
		rt.Read(http.MethodGet, "/{transaction_id}", readTransaction)
		rt.Write(http.MethodPatch, "/{transaction_id}", updateTransaction)
		rt.Write(http.MethodDelete, "/{transaction_id}", deleteTransaction)
		rt.Write(http.MethodPut, "/{transaction_id}/splits", setSplits)
		rt.Write(http.MethodPost, "/{transaction_id}/link-series", linkTransactionSeries)
		rt.Write(http.MethodPost, "/{transaction_id}/unlink-series", unlinkTransactionSeries)
	}})
}

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

// registerQuery is the one place request parameters become a register query.
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
	hidePadding := false
	switch padding := r.URL.Query().Get("padding"); padding {
	case "", "show":
	case "hide":
		hidePadding = true
	default:
		return RegisterQuery{}, errInvalid("enum", []string{"query", "padding"},
			"padding must be show or hide, got %q", padding)
	}
	descending := true
	switch order := r.URL.Query().Get("order"); order {
	case "", "desc":
	case "asc":
		descending = false
	default:
		return RegisterQuery{}, errInvalid("enum", []string{"query", "order"},
			"order must be asc or desc, got %q", order)
	}

	return RegisterQuery{
		Window:      window,
		Accounts:    accounts,
		FilterID:    filterID,
		HasFilter:   hasFilter,
		Search:      strings.TrimSpace(r.URL.Query().Get("search")),
		IsReviewed:  reviewed,
		HasReviewed: hasReviewed,
		HidePadding: hidePadding,
		Descending:  descending,
		Limit:       limit,
		Offset:      offset,
	}, nil
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

// TransactionPage is one page of the register, with the window it was computed
// over.
//
// Count and Total describe every listed row, not this page. Total counts
// what the filter kept, as reports do: a split row the filter kept part of
// contributes only those splits. FullTotal is the same rows at their whole
// amounts, and PartialCount is how many rows differ between the two.
//
// With padding hidden, the padding rows the query matched are not listed and
// not in those four; PaddingCount and PaddingTotal are them, so the listed
// total plus PaddingTotal is what every matching row nets to.
type TransactionPage struct {
	Items        []TransactionResponse `json:"items"`
	Count        int                   `json:"count"`
	Total        domain.Money          `json:"total"`
	FullTotal    domain.Money          `json:"full_total"`
	PartialCount int                   `json:"partial_count"`
	PaddingCount int                   `json:"padding_count"`
	PaddingTotal domain.Money          `json:"padding_total"`
	Window       WindowResponse        `json:"window"`
	Limit        int                   `json:"limit"`
	Offset       int                   `json:"offset"`
}

type SplitWrite struct {
	Amount     domain.Money `json:"amount"`
	CategoryID *uuid.UUID   `json:"category_id"`
	Memo       *string      `json:"memo"`
	TagIDs     []uuid.UUID  `json:"tag_ids"`
}

type TransactionCreate struct {
	AccountID     uuid.UUID    `json:"account_id"`
	Date          Date         `json:"date"`
	Amount        domain.Money `json:"amount"`
	EffectiveDate *Date        `json:"effective_date"`
	Currency      *string      `json:"currency"`
	// StatementName is settable once, at ingest. There is deliberately no way
	// to change it later.
	StatementName            *string      `json:"statement_name"`
	Payee                    *string      `json:"payee"`
	Notes                    *string      `json:"notes"`
	CheckNumber              *string      `json:"check_number"`
	CategoryID               *uuid.UUID   `json:"category_id"`
	IsPending                bool         `json:"is_pending"`
	IsReviewed               bool         `json:"is_reviewed"`
	ExcludedFromReports      bool         `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan bool         `json:"excluded_from_spending_plan"`
	IsBill                   bool         `json:"is_bill"`
	IsSubscription           bool         `json:"is_subscription"`
	UserFlag                 *string      `json:"user_flag"`
	UserFlagNote             *string      `json:"user_flag_note"`
	TagIDs                   []uuid.UUID  `json:"tag_ids"`
	Splits                   []SplitWrite `json:"splits"`
}

// TransactionUpdate is everything a person may edit. statement_name is not on
// it, and the decoder forbids extras, so sending it is a 422.
type TransactionUpdate struct {
	AccountID                Opt[uuid.UUID]    `json:"account_id"`
	Date                     Opt[Date]         `json:"date"`
	EffectiveDate            Opt[Date]         `json:"effective_date"`
	Amount                   Opt[domain.Money] `json:"amount"`
	Currency                 Opt[string]       `json:"currency"`
	Payee                    Opt[string]       `json:"payee"`
	Notes                    Opt[string]       `json:"notes"`
	CheckNumber              Opt[string]       `json:"check_number"`
	CategoryID               Opt[uuid.UUID]    `json:"category_id"`
	IsPending                Opt[bool]         `json:"is_pending"`
	IsReviewed               Opt[bool]         `json:"is_reviewed"`
	ExcludedFromReports      Opt[bool]         `json:"excluded_from_reports"`
	ExcludedFromSpendingPlan Opt[bool]         `json:"excluded_from_spending_plan"`
	IsBill                   Opt[bool]         `json:"is_bill"`
	IsSubscription           Opt[bool]         `json:"is_subscription"`
	UserFlag                 Opt[string]       `json:"user_flag"`
	UserFlagNote             Opt[string]       `json:"user_flag_note"`
	ReceiptNotNeeded         Opt[bool]         `json:"receipt_not_needed"`
	TagIDs                   *[]uuid.UUID      `json:"tag_ids"`
}

type SplitsWrite struct {
	Splits []SplitWrite `json:"splits"`
}

// ReviewedWrite defaults to true: the button says "mark as reviewed".
type ReviewedWrite struct {
	IsReviewed *bool `json:"is_reviewed"`
}

func (b ReviewedWrite) value() bool { return b.IsReviewed == nil || *b.IsReviewed }

// BulkReviewResult is what "mark all as reviewed" touched. It applies to the
// query, not the page, so the count is how the client learns what happened.
type BulkReviewResult struct {
	Updated int            `json:"updated"`
	Window  WindowResponse `json:"window"`
}

func listTransactions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	query, err := registerQuery(r)
	if err != nil {
		return err
	}
	matched, err := matchRegister(r.Context(), env, sp, query)
	if err != nil {
		return err
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
			return err
		}
		item := transactionResponse(rows[key])
		if parts, ok := matched.Partial[posting.Txn.ID]; ok {
			item.MatchedSplitIDs = make([]uuid.UUID, 0, len(parts))
			for _, part := range parts {
				splitID, err := store.ParseID(part.Split.ID)
				if err != nil {
					return err
				}
				item.MatchedSplitIDs = append(item.MatchedSplitIDs, splitID)
			}
			amount := domain.MatchedAmount(posting, parts)
			item.MatchedAmount = &amount
		}
		items = append(items, item)
	}
	if err := countAttachmentsInto(env, r, sp, items); err != nil {
		return err
	}
	if err := fillReceiptsInto(env, r, sp, items, rows); err != nil {
		return err
	}
	if err := fillSuggestionsInto(env, r, sp, items); err != nil {
		return err
	}
	if err := fillCategoryChecksInto(env, r, sp, items); err != nil {
		return err
	}
	if err := fillPaddingInto(env, r, sp, items); err != nil {
		return err
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
	return writeJSON(w, http.StatusOK, TransactionPage{
		Items:        items,
		Count:        len(postings),
		Total:        domain.Sum(postings, kept),
		FullTotal:    domain.Sum(postings, domain.Posting.Amount),
		PartialCount: partialCount,
		PaddingCount: len(padding),
		PaddingTotal: domain.Sum(padding, kept),
		Window:       windowResponse(query.Window),
		Limit:        query.Limit,
		Offset:       query.Offset,
	})
}

// TransactionAggregateBucket is one slice of the donut and one bar of the
// chart.
type TransactionAggregateBucket struct {
	Key   string       `json:"key"`
	Label string       `json:"label"`
	Total domain.Money `json:"total"`
}

// TransactionAggregateMonth is one cluster of the over-time chart. Only
// non-empty buckets are listed; the client fills the rest with zero.
type TransactionAggregateMonth struct {
	Month   string                       `json:"month"`
	Buckets []TransactionAggregateBucket `json:"buckets"`
}

// TransactionAggregate is the Spending and Income tabs' headline and chart.
type TransactionAggregate struct {
	Direction string `json:"direction"`
	GroupBy   string `json:"group_by"`
	// Total is the net of every contributing allocation. A tagged allocation
	// counts under every tag it carries, so the buckets can sum to more.
	Total domain.Money `json:"total"`
	// Count is contributing allocations, not rows.
	Count   int                          `json:"count"`
	Buckets []TransactionAggregateBucket `json:"buckets"`
	Months  []TransactionAggregateMonth  `json:"months"`
	Window  WindowResponse               `json:"window"`
}

// aggregateTransactions is the Spending and Income tabs, answered in one call.
// It parses the register's query with the same parser as the list, so the
// chart and the table under it describe the same rows (trap 5).
func aggregateTransactions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	query, err := registerQuery(r)
	if err != nil {
		return err
	}
	options, err := aggregateOptions(r)
	if err != nil {
		return err
	}
	options.Mode = query.Window.Mode

	matched, err := matchRegister(r.Context(), env, sp, query)
	if err != nil {
		return err
	}
	postings := matched.Postings
	options.Partial = matched.Partial
	if err := nameAggregate(r.Context(), env, sp, &options); err != nil {
		return err
	}

	result := domain.Aggregate(postings, options)
	months := make([]TransactionAggregateMonth, 0, len(result.Months))
	for _, month := range result.Months {
		months = append(months, TransactionAggregateMonth{
			Month:   month.Month,
			Buckets: aggregateBuckets(month.Buckets),
		})
	}

	return writeJSON(w, http.StatusOK, TransactionAggregate{
		Direction: string(options.Direction),
		GroupBy:   string(options.GroupBy),
		Total:     result.Total,
		Count:     result.Count,
		Buckets:   aggregateBuckets(result.Buckets),
		Months:    months,
		Window:    windowResponse(query.Window),
	})
}

func aggregateBuckets(buckets []domain.AggregateBucket) []TransactionAggregateBucket {
	out := make([]TransactionAggregateBucket, 0, len(buckets))
	for _, bucket := range buckets {
		out = append(out, TransactionAggregateBucket{
			Key: bucket.Key, Label: bucket.Label, Total: bucket.Total,
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

// aggregateOptions reads the knobs the register query does not carry.
// visible_accounts_only is deliberately not one: history still counts spending
// in an account since closed, as the report engine does.
func aggregateOptions(r *http.Request) (domain.AggregateOptions, error) {
	options := domain.AggregateOptions{
		Direction: domain.AggregateSpending,
		GroupBy:   domain.AggregateByCategory,
	}
	switch value := r.URL.Query().Get("direction"); value {
	case "", "spending":
	case "income":
		options.Direction = domain.AggregateIncome
	default:
		return options, errInvalid("enum", []string{"query", "direction"},
			"direction must be spending or income, got %q", value)
	}
	switch value := r.URL.Query().Get("group_by"); value {
	case "", "category":
	case "payee":
		options.GroupBy = domain.AggregateByPayee
	case "tag":
		options.GroupBy = domain.AggregateByTag
	case "none":
		options.GroupBy = domain.AggregateByNone
	default:
		return options, errInvalid("enum", []string{"query", "group_by"},
			"group_by must be category, payee, tag or none, got %q", value)
	}
	if value := r.URL.Query().Get("under"); value != "" {
		id, err := uuid.Parse(value)
		if err != nil {
			return options, errInvalid("uuid_parsing", []string{"query", "under"},
				"under must be a category id, got %q", value)
		}
		options.Under = domain.ID(id.String())
	}
	return options, nil
}

// markAllReviewed is the review queue's primary button, over the whole query
// rather than the page on screen.
func markAllReviewed(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	query, err := registerQuery(r)
	if err != nil {
		return err
	}
	var body ReviewedWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	reviewed := body.value()

	postings, rows, err := matchingRegister(r.Context(), env, sp, query)
	if err != nil {
		return err
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
			return err
		}
		if rows[key].IsReviewed == reviewed {
			continue
		}
		changed = append(changed, key)
	}
	if err := env.DB.SetTransactionsReviewed(r.Context(), sp.ID(), changed, reviewed); err != nil {
		return err
	}

	return writeJSON(w, http.StatusOK, BulkReviewResult{
		Updated: len(changed),
		Window:  windowResponse(query.Window),
	})
}

func createTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body TransactionCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	account, err := requireAccount(r.Context(), env, sp, body.AccountID)
	if err != nil {
		return err
	}
	categoryID := store.Deref(body.CategoryID, uuid.Nil)
	if err := checkCategory(r.Context(), env, sp, categoryID); err != nil {
		return err
	}
	tagIDs, err := resolveTags(r.Context(), env, sp, body.TagIDs)
	if err != nil {
		return err
	}

	row := &store.Transaction{
		AccountID:     account.ID,
		Date:          domain.Date(body.Date),
		EffectiveDate: dateOrZero(body.EffectiveDate),
		Amount:        body.Amount,
		Currency:      store.Deref(body.Currency, account.Currency),
		// The bank's wording, recorded once. Every later edit goes to Payee.
		StatementName:            store.Deref(body.StatementName, ""),
		Payee:                    store.Deref(body.Payee, ""),
		Notes:                    store.Deref(body.Notes, ""),
		CheckNumber:              store.Deref(body.CheckNumber, ""),
		CategoryID:               categoryID,
		Source:                   domain.SourceManual,
		IsPending:                body.IsPending,
		IsReviewed:               body.IsReviewed || account.Kind.BornReviewed(),
		ExcludedFromReports:      body.ExcludedFromReports,
		ExcludedFromSpendingPlan: body.ExcludedFromSpendingPlan,
		IsBill:                   body.IsBill,
		IsSubscription:           body.IsSubscription,
		UserFlag:                 store.Deref(body.UserFlag, ""),
		UserFlagNote:             store.Deref(body.UserFlagNote, ""),
		TagIDs:                   tagIDs,
	}
	if len(body.Splits) > 0 {
		splits, err := buildSplits(r.Context(), env, sp, row.Amount, body.Splits)
		if err != nil {
			return err
		}
		row.Splits = splits
		row.CategoryID = uuid.Nil
	}

	// An effective date the client did not send is derived from the statement
	// cycle, so a hand-entered card charge files like a synced one.
	if body.EffectiveDate == nil {
		service.ApplyEffectiveDate(row, account, domain.Date{}, false)
	}

	if err := stampForeignAmount(r.Context(), env, sp, row); err != nil {
		return err
	}
	if err := env.DB.CreateTransaction(r.Context(), sp.ID(), row); err != nil {
		return err
	}
	if err := recomputeRunningBalances(r.Context(), env, sp, row.AccountID); err != nil {
		return err
	}
	return respondWithTransaction(env, w, r, sp, row.ID, http.StatusCreated)
}

func readTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveTransaction(r, env, sp)
	if err != nil {
		return err
	}
	return writeOneTransaction(env, w, r, sp, row, http.StatusOK)
}

func updateTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveTransaction(r, env, sp)
	if err != nil {
		return err
	}
	var body TransactionUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	// The allocations were written against the old figure; rescaling them
	// would invent a split, and keeping them would disagree with the amount.
	if body.Amount.Present() && len(row.Splits) > 0 && !body.Amount.Value.Equal(row.Amount) {
		return errConflict("re-split the transaction after changing its amount")
	}

	// Nothing revisits a transfer pair once the token is written, so neither
	// leg may move to the other's account or change amount alone.
	if row.TransferPairID != uuid.Nil {
		if body.AccountID.Present() && body.AccountID.Value != row.AccountID {
			return errConflict("unlink the transfer before moving this row to another account")
		}
		if body.Amount.Present() && !body.Amount.Value.Equal(row.Amount) {
			return errConflict("unlink the transfer before changing this row's amount")
		}
	}

	refiled := body.CategoryID.Set && valueOrNil(body.CategoryID) != row.CategoryID &&
		!dispatched(r.Context())
	subject := correctionFacts{
		transactionID: row.ID, statementName: row.StatementName, payee: row.Payee,
		amount: row.Amount, hasAmount: true,
	}
	previousAccount := row.AccountID
	previousAmount := row.Amount
	previousCurrency := row.Currency
	moved := body.AccountID.Present() && body.AccountID.Value != row.AccountID
	var newAccount, oldAccount store.Account
	if moved {
		if newAccount, err = requireAccount(r.Context(), env, sp, body.AccountID.Value); err != nil {
			return err
		}
		if oldAccount, err = env.DB.GetAccount(r.Context(), sp.ID(), previousAccount); err != nil {
			return err
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

	if body.AccountID.Cleared() {
		return errConflict("account_id cannot be cleared")
	}
	if body.CategoryID.Set {
		if err := checkCategory(r.Context(), env, sp, valueOrNil(body.CategoryID)); err != nil {
			return err
		}
	}
	if body.TagIDs != nil {
		tagIDs, err := resolveTags(r.Context(), env, sp, *body.TagIDs)
		if err != nil {
			return err
		}
		row.TagIDs = tagIDs
	}

	if body.AccountID.Present() {
		row.AccountID = body.AccountID.Value
	}
	if err := applyRequired("date", body.Date, (*Date)(&row.Date)); err != nil {
		return err
	}
	applyNullable(body.EffectiveDate, (*Date)(&row.EffectiveDate))
	if err := applyRequired("amount", body.Amount, &row.Amount); err != nil {
		return err
	}
	if err := applyRequired("currency", body.Currency, &row.Currency); err != nil {
		return err
	}
	if err := applyRequired("payee", body.Payee, &row.Payee); err != nil {
		return err
	}
	applyNullable(body.Notes, &row.Notes)
	applyNullable(body.CheckNumber, &row.CheckNumber)
	applyNullable(body.CategoryID, &row.CategoryID)
	// A split row's categories are its splits', and reports read only those:
	// one category for the row files every split under it, and the parent
	// keeps none.
	if len(row.Splits) > 0 && row.CategoryID != uuid.Nil {
		for i := range row.Splits {
			row.Splits[i].CategoryID = row.CategoryID
		}
		row.CategoryID = uuid.Nil
	}
	if err := applyRequired("is_pending", body.IsPending, &row.IsPending); err != nil {
		return err
	}
	if err := applyRequired("is_reviewed", body.IsReviewed, &row.IsReviewed); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_reports", body.ExcludedFromReports, &row.ExcludedFromReports); err != nil {
		return err
	}
	if err := applyRequired("excluded_from_spending_plan", body.ExcludedFromSpendingPlan, &row.ExcludedFromSpendingPlan); err != nil {
		return err
	}
	if err := applyRequired("is_bill", body.IsBill, &row.IsBill); err != nil {
		return err
	}
	if err := applyRequired("is_subscription", body.IsSubscription, &row.IsSubscription); err != nil {
		return err
	}
	applyNullable(body.UserFlag, &row.UserFlag)
	applyNullable(body.UserFlagNote, &row.UserFlagNote)
	if err := applyRequired("receipt_not_needed", body.ReceiptNotNeeded, &row.ReceiptNotNeeded); err != nil {
		return err
	}

	if moved {
		// The old account's cycle and currency no longer describe this row. A
		// value sent with the move wins over both.
		if !body.EffectiveDate.Set && derivedEffectiveDate {
			service.ApplyEffectiveDate(&row, newAccount, domain.Date{}, true)
		}
		if !body.Currency.Set && derivedCurrency {
			row.Currency = newAccount.Currency
		}
	}

	// A changed amount or currency invalidates the stored conversion.
	if !row.Amount.Equal(previousAmount) || row.Currency != previousCurrency {
		service.ClearPrimaryAmount(&row)
		// Re-derived now: an install with no sync or import has no next sweep,
		// and the foreign amount would count at face value.
		if err := stampForeignAmount(r.Context(), env, sp, &row); err != nil {
			return err
		}
	}

	if err := env.DB.UpdateTransaction(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	// Only these three move the stored running balance. A pending row already
	// counts, and a review or a rename does not.
	if body.Amount.Set || body.Date.Set || body.AccountID.Set {
		if err := recomputeRunningBalances(r.Context(), env, sp, row.AccountID); err != nil {
			return err
		}
		if row.AccountID != previousAccount {
			if err := recomputeRunningBalances(r.Context(), env, sp, previousAccount); err != nil {
				return err
			}
		}
	}
	if refiled {
		settleRowSuggestions(r.Context(), env, sp, subject, valueOrNil(body.CategoryID))
	}
	return respondWithTransaction(env, w, r, sp, row.ID, http.StatusOK)
}

// deleteTransaction soft-deletes a row. store.DeleteTransaction releases the
// transfer partner and deletes a purchase's padding income row in the same
// database transaction; the pad may be in another account, whose running
// balance is recomputed too.
func deleteTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveTransaction(r, env, sp)
	if err != nil {
		return err
	}
	pads, err := env.DB.PaddingOf(r.Context(), sp.ID(), []uuid.UUID{row.ID})
	if err != nil {
		return err
	}
	accounts := []uuid.UUID{row.AccountID}
	for _, padID := range pads {
		pad, err := env.DB.GetTransaction(r.Context(), sp.ID(), padID)
		if err != nil {
			return err
		}
		if pad.AccountID != row.AccountID {
			accounts = append(accounts, pad.AccountID)
		}
	}
	if err := purgeAttachments(env, r, sp, row.ID); err != nil {
		return err
	}
	if err := env.DB.DeleteTransaction(r.Context(), sp.ID(), row.ID); err != nil {
		return notFoundAs(err, "Transaction")
	}
	for _, accountID := range accounts {
		if err := recomputeRunningBalances(r.Context(), env, sp, accountID); err != nil {
			return err
		}
	}
	return writeNoContent(w)
}

// setSplits replaces a row's allocations. Splits must sum to the amount:
// reports read the splits when they exist and the parent otherwise.
func setSplits(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveTransaction(r, env, sp)
	if err != nil {
		return err
	}
	var body SplitsWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	splits, err := buildSplits(r.Context(), env, sp, row.Amount, body.Splits)
	if err != nil {
		return err
	}

	row.Splits = splits
	if len(splits) > 0 {
		// Reports read the splits when they exist and the parent otherwise;
		// leaving a category on the parent as well counts the row twice.
		row.CategoryID = uuid.Nil
	}
	if err := env.DB.UpdateTransaction(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	if len(splits) > 0 && !dispatched(r.Context()) {
		settleRowSuggestions(r.Context(), env, sp, correctionFacts{
			transactionID: row.ID, statementName: row.StatementName, payee: row.Payee,
			amount: row.Amount, hasAmount: true,
		}, uuid.Nil)
	}
	return respondWithTransaction(env, w, r, sp, row.ID, http.StatusOK)
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

func buildSplits(ctx context.Context, env *Env, sp auth.SpaceContext, amount domain.Money, writes []SplitWrite) ([]store.Split, error) {
	if len(writes) > 0 {
		allocated := domain.Sum(writes, func(s SplitWrite) domain.Money { return s.Amount })
		if !allocated.Equal(amount.Round()) {
			return nil, errConflict("splits total %s but the transaction is %s", allocated, amount)
		}
	}

	splits := make([]store.Split, 0, len(writes))
	for position, write := range writes {
		categoryID := store.Deref(write.CategoryID, uuid.Nil)
		if err := checkCategory(ctx, env, sp, categoryID); err != nil {
			return nil, err
		}
		tagIDs, err := resolveTags(ctx, env, sp, write.TagIDs)
		if err != nil {
			return nil, err
		}
		splits = append(splits, store.Split{
			Position:   position,
			Amount:     write.Amount,
			CategoryID: categoryID,
			Memo:       store.Deref(write.Memo, ""),
			TagIDs:     tagIDs,
		})
	}
	return splits, nil
}

// liveTransaction reads the row named in the path, treating a soft-deleted one
// and one in another space as the same 404.
func liveTransaction(r *http.Request, env *Env, sp auth.SpaceContext) (store.Transaction, error) {
	row, err := fromPath(r, sp, "transaction_id", "Transaction", env.DB.GetTransaction)
	if err != nil {
		return store.Transaction{}, err
	}
	if row.IsDeleted {
		return store.Transaction{}, errNotFound("Transaction")
	}
	return row, nil
}

// respondWithTransaction re-reads through the space-scoped query.
func respondWithTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, id uuid.UUID, status int) error {
	row, err := env.DB.GetTransaction(r.Context(), sp.ID(), id)
	if err != nil {
		return err
	}
	return writeOneTransaction(env, w, r, sp, row, status)
}

// writeOneTransaction serves a single row with its attachment count filled in,
// so the detail panel and the register agree about whether a file is there.
func writeOneTransaction(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext,
	row store.Transaction, status int,
) error {
	items := []TransactionResponse{transactionResponse(row)}
	if err := countAttachmentsInto(env, r, sp, items); err != nil {
		return err
	}
	if err := fillReceiptsInto(env, r, sp, items, map[uuid.UUID]store.Transaction{row.ID: row}); err != nil {
		return err
	}
	if err := fillSuggestionsInto(env, r, sp, items); err != nil {
		return err
	}
	if err := fillCategoryChecksInto(env, r, sp, items); err != nil {
		return err
	}
	if err := fillPaddingInto(env, r, sp, items); err != nil {
		return err
	}
	return writeJSON(w, status, items[0])
}

// fillSuggestionsInto hangs each row's waiting proposal on it, one query per
// page.
func fillSuggestionsInto(
	env *Env, r *http.Request, sp auth.SpaceContext, items []TransactionResponse,
) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	waiting, err := env.DB.PendingActionsForTransactions(r.Context(), sp.ID(), ids)
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

// CategoryCheckRow is one row's category check: the category, the suggestion
// and the check mark, which is everything a check can change, so the client
// can rewrite the row without refetching the page.
type CategoryCheckRow struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	// Checking is queued or running. Everything else is what the row says now.
	Checking           bool                   `json:"checking"`
	CategoryID         *uuid.UUID             `json:"category_id"`
	CategoryCheckedAt  *time.Time             `json:"category_checked_at"`
	CategoryCheckNote  string                 `json:"category_check_note"`
	CategoryCheckRunID *uuid.UUID             `json:"category_check_run_id"`
	Suggestion         *TransactionSuggestion `json:"suggestion"`
}

// CategoryCheckProgress is how far a batch of checks has got, counted from the
// runs so it survives a reload. A row not in this space, or deleted mid-batch,
// is absent from Rows and counted done.
type CategoryCheckProgress struct {
	Total int                `json:"total"`
	Done  int                `json:"done"`
	Rows  []CategoryCheckRow `json:"rows"`
}

// maxCheckedRows is the most rows one progress read may name. A long batch's
// progress is the batch's own read; this one patches the rows on screen.
const maxCheckedRows = 200

// categoryChecks answers where a named set of rows has got to.
func categoryChecks(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	ids, given, err := queryUUIDs(r, "id")
	if err != nil {
		return err
	}
	if !given || len(ids) == 0 {
		return errBadRequest("Name at least one transaction")
	}
	if len(ids) > maxCheckedRows {
		return errBadRequest("At most %d transactions at once", maxCheckedRows)
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

	rows, err := env.DB.ListTransactions(r.Context(), sp.ID(),
		store.TransactionQuery{IDs: ids, IncludeEstimates: true})
	if err != nil {
		return err
	}
	items := make([]TransactionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, transactionResponse(row))
	}
	if err := fillSuggestionsInto(env, r, sp, items); err != nil {
		return err
	}
	if err := fillCategoryChecksInto(env, r, sp, items); err != nil {
		return err
	}

	out := CategoryCheckProgress{Total: len(ids), Rows: make([]CategoryCheckRow, 0, len(items))}
	for _, item := range items {
		out.Rows = append(out.Rows, CategoryCheckRow{
			TransactionID: item.ID, Checking: item.CheckingCategory,
			CategoryID: item.CategoryID, CategoryCheckedAt: item.CategoryCheckedAt,
			CategoryCheckNote: item.CategoryCheckNote, CategoryCheckRunID: item.CategoryCheckRunID,
			Suggestion: item.Suggestion,
		})
	}
	for _, row := range out.Rows {
		if !row.Checking {
			out.Done++
		}
	}
	out.Done += len(ids) - len(out.Rows)
	return writeJSON(w, http.StatusOK, out)
}

// fillCategoryChecksInto marks the rows a check is working on, one query per
// page.
func fillCategoryChecksInto(
	env *Env, r *http.Request, sp auth.SpaceContext, items []TransactionResponse,
) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	checking, err := env.DB.PendingCategoryChecks(r.Context(), sp.ID(), ids)
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
func fillPaddingInto(
	env *Env, r *http.Request, sp auth.SpaceContext, items []TransactionResponse,
) error {
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	pads, err := env.DB.PaddingOf(r.Context(), sp.ID(), ids)
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
func countAttachmentsInto(
	env *Env, r *http.Request, sp auth.SpaceContext, items []TransactionResponse,
) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	counts, err := env.DB.CountDocumentsOnTransactions(r.Context(), sp.ID(), ids)
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
	env *Env, r *http.Request, sp auth.SpaceContext, items []TransactionResponse,
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
	statuses, err := service.ReceiptStatuses(r.Context(), env.DB, sp.ID(), page, documented)
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
			CategoryID: pgconv.NullUUID(split.CategoryID),
			Memo:       pgconv.NullText(split.Memo),
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
		Notes:                    pgconv.NullText(t.Notes),
		CheckNumber:              pgconv.NullText(t.CheckNumber),
		CategoryID:               pgconv.NullUUID(t.CategoryID),
		Source:                   t.Source,
		IsPending:                t.IsPending,
		IsReviewed:               t.IsReviewed,
		ExcludedFromReports:      t.ExcludedFromReports,
		ExcludedFromSpendingPlan: t.ExcludedFromSpendingPlan,
		IsBill:                   t.IsBill,
		IsSubscription:           t.IsSubscription,
		TransferPairID:           pgconv.NullUUID(t.TransferPairID),
		PaddedTxnID:              pgconv.NullUUID(t.PaddedTxnID),
		UserFlag:                 pgconv.NullText(t.UserFlag),
		UserFlagNote:             pgconv.NullText(t.UserFlagNote),
		ReceiptNotNeeded:         t.ReceiptNotNeeded,
		SeriesID:                 pgconv.NullUUID(t.SeriesID),
		SeriesDueOn:              nullableDate(t.SeriesDueOn),
		Balance:                  store.PtrIf(t.Balance, t.HasBalance),
		Splits:                   splits,
		TagIDs:                   store.NonNil(t.TagIDs),
		CategoryCheckedAt:        t.CategoryCheckedAt,
		CategoryCheckNote:        t.CategoryCheckNote,
		CategoryCheckRunID:       t.CategoryCheckRunID,
	}
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

// listPayees lists the distinct payees in the space. A payee is a column, not
// a row, so nothing else can.
func listPayees(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	limit, err := queryInt(r, "limit", maxPayees, 1, maxPayees)
	if err != nil {
		return err
	}
	names, err := env.DB.ListPayees(r.Context(), sp.ID(), limit)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, store.NonNil(names))
}
