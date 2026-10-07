package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Where one account's balance has been: a point per day, derived on read by
// domain.BalanceHistory, the same balance-as-of-a-day the projection opens on,
// so the line ends where the projection begins.
//
// Both ends are required (trap 5), and `to` is held at today.

func init() {
	Register(Resource{Prefix: "/account-balance-history", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", readAccountBalanceHistory)
	}})
}

// maxBalanceHistoryDays bounds one request: two years of daily points.
const maxBalanceHistoryDays = 731

type AccountBalanceHistoryResponse struct {
	AccountID uuid.UUID `json:"account_id"`
	From      Date      `json:"from"`
	To        Date      `json:"to"`
	// Balance is the account's current balance as the header shows it, so a
	// page can see whether the line's last point agrees with it.
	Balance domain.Money            `json:"balance"`
	Points  []CashFlowPointResponse `json:"points"`
}

func readAccountBalanceHistory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, given, err := queryUUID(r, "account_id")
	if err != nil {
		return err
	}
	if !given {
		return errInvalid("missing", []string{"query", "account_id"}, "account_id is required")
	}
	window, err := windowOn(r, domain.DatePosted)
	if err != nil {
		return err
	}
	if !window.HasFrom || !window.HasTo {
		return errBadRequest("from and to are both required: a balance history has no " +
			"default window")
	}
	from, to := window.From, window.To
	if span := domain.DaysBetween(from, to) + 1; span > maxBalanceHistoryDays {
		return errBadRequest("a balance history covers at most %d days", maxBalanceHistoryDays)
	}
	if today := domain.DateOf(env.now()); to.After(today) {
		to = today
	}

	account, err := env.DB.GetAccount(r.Context(), sp.ID(), id)
	if err != nil {
		return notFoundAs(err, "Account")
	}
	if account.IsDeleted {
		return errNotFound("Account")
	}
	postings, err := postingsByAccount(r.Context(), env, sp, []store.Account{account})
	if err != nil {
		return err
	}
	domainAccount := store.DomainAccount(account)
	history := domain.BalanceHistory(domainAccount, postings[account.ID], from, to)

	points := make([]CashFlowPointResponse, 0, len(history))
	for _, point := range history {
		points = append(points, CashFlowPointResponse{On: Date(point.On), Balance: point.Balance})
	}
	return writeJSON(w, http.StatusOK, AccountBalanceHistoryResponse{
		AccountID: account.ID, From: Date(from), To: Date(to),
		Balance: domain.AccountBalance(domainAccount, postings[account.ID]),
		Points:  points,
	})
}
