package api

import (
	"context"
	"net/http"
	"sort"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// What happened in the investment accounts, named. There is no
// investment-transaction table: purchases, dividends and contributions are
// ordinary rows, and each one's action is derived by domain.ClassifyActivity
// rather than stored.
//
//   - A row nothing recognizes is "unknown" and gets no chip; guessing one of
//     withdrawal, purchase, fee or wire would invent a fact.
//   - The category is never trusted by name. The bank's wording is read
//     first and the category's kind last.
//
// The summary is computed over the same rows, so it always describes the list.
// Income and fees are computed here, since summing them off a windowed,
// paginated list would be wrong.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewInvestmentActivityServiceHandler(investmentActivityService{env}, opts...)
	})
}

type investmentActivityService struct{ env *Env }

func (s investmentActivityService) ListInvestmentActivity(
	ctx context.Context, req *agentifiv1.ListInvestmentActivityRequest,
) (*agentifiv1.ListInvestmentActivityResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	wanted, err := accountFilterOf(req.GetAccountId())
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.ListInvestmentActivityResponse{
		Window: windowProto(window),
		Income: moneyProto(domain.Zero),
		Fees:   moneyProto(domain.Zero),
	}
	if wanted.selectsNothing() {
		return out, nil
	}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(),
		wanted.narrow(store.AccountQuery{IncludeClosed: true}))
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(accounts))
	for _, account := range accounts {
		if account.Kind == domain.KindInvestment {
			ids = append(ids, account.ID)
		}
	}
	out.AccountIds = uuidStrings(ids)
	// No investment accounts is an empty answer: omitted AccountIDs below
	// would read as every account.
	if len(ids) == 0 {
		return out, nil
	}

	postings, _, err := service.LoadPostings(ctx, env.DB, sp.ID(), store.TransactionQuery{
		AccountIDs: ids,
		From:       window.From,
		To:         window.To,
		DateMode:   window.Mode,
	})
	if err != nil {
		return nil, err
	}

	rows := domain.ClassifyActivities(postings)
	type item struct {
		on  domain.Date
		row *agentifiv1.InvestmentActivityRow
	}
	items := make([]item, 0, len(rows))
	for _, row := range rows {
		txn := row.Posting.Txn
		one := &agentifiv1.InvestmentActivityRow{
			TransactionId: mustParseID(txn.ID).String(),
			AccountId:     mustParseID(txn.AccountID).String(),
			On:            txn.Date.String(),
			Payee:         txn.Payee,
			StatementName: txn.StatementName,
			Amount:        moneyProto(row.Posting.Amount()),
			Kind:          string(row.Kind),
			IsPending:     txn.IsPending,
		}
		if row.Posting.HasCategory {
			one.CategoryId = proto.String(mustParseID(row.Posting.Category.ID).String())
		}
		items = append(items, item{on: txn.Date, row: one})
	}
	// Newest first, the way the register reads, with a stable tie-break so two
	// rows on one day do not swap places between requests.
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.on != right.on {
			return left.on.After(right.on)
		}
		return left.row.GetTransactionId() < right.row.GetTransactionId()
	})
	for _, one := range items {
		out.Items = append(out.Items, one.row)
	}

	for _, summary := range domain.SummarizeActivity(rows) {
		out.Summary = append(out.Summary, &agentifiv1.InvestmentActivitySummary{
			Kind: string(summary.Kind), Count: int32(summary.Count), Total: moneyProto(summary.Total),
		})
	}
	out.Income = moneyProto(domain.InvestmentIncome(rows))
	out.Fees = moneyProto(domain.InvestmentFees(rows))
	return out, nil
}

// mustParseID turns a domain id back into its uuid. Every posting id came from
// the store, so a failure is a programming error, visible as the zero uuid.
func mustParseID(id domain.ID) uuid.UUID {
	parsed, err := store.ParseID(id)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}
