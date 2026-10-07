package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Bills against the bank rows that paid them: matching a provider's whole
// history on request, and which bill a payment settled.

func init() {
	Register(Resource{Prefix: "/bill-payments", Routes: func(rt *Routes) {
		rt.Write(http.MethodPost, "/connections/{connection_id}/match", matchConnectionHistory)
		rt.Write(http.MethodPost, "/subaccounts/{subaccount_id}/match", matchSubaccountHistory)
		rt.Read(http.MethodGet, "/transactions/{transaction_id}", billsSettledByTransaction)
	}})
}

// BillHistoryResponse is what matching a provider's history found, per billed
// account. StatementsOffered says whether the provider's bills can carry a
// statement at all, so a tally with none filed can say why.
type BillHistoryResponse struct {
	StatementsOffered bool                 `json:"statements_offered"`
	Accounts          []BillAccountHistory `json:"accounts"`
}

// BillAccountHistory is one billed account's result. SeriesID is null for an
// account linked to no reminder, which has nothing to match.
type BillAccountHistory struct {
	SubaccountID   uuid.UUID  `json:"subaccount_id"`
	Label          string     `json:"label"`
	SeriesID       *uuid.UUID `json:"series_id"`
	Matched        int        `json:"matched"`
	Settled        int        `json:"settled"`
	WithStatement  int        `json:"with_statement"`
	Unsettled      int        `json:"unsettled"`
	CadenceGapDays int        `json:"cadence_gap_days"`
}

// SettledBillResponse is a bill a bank row's reminder slot settled.
type SettledBillResponse struct {
	BillID       uuid.UUID    `json:"bill_id"`
	ConnectionID uuid.UUID    `json:"connection_id"`
	Provider     string       `json:"provider"`
	DueOn        Date         `json:"due_on"`
	AmountDue    domain.Money `json:"amount_due"`
	Status       string       `json:"status"`
	DocumentID   *uuid.UUID   `json:"document_id"`
}

func matchConnectionHistory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnection(r, env, sp)
	if err != nil {
		return err
	}
	rows, err := env.DB.ListBillSubaccounts(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return err
	}
	shown := make([]store.BillSubaccount, 0, len(rows))
	for _, row := range rows {
		if row.IsSelected {
			shown = append(shown, row)
		}
	}
	return matchBillHistory(env, w, r, sp, connection, shown)
}

func matchSubaccountHistory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	subaccount, err := billSubaccount(r, env, sp)
	if err != nil {
		return err
	}
	connection, err := env.DB.GetBillConnection(r.Context(), sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return err
	}
	return matchBillHistory(env, w, r, sp, connection, []store.BillSubaccount{subaccount})
}

func matchBillHistory(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext,
	connection store.BillConnection, subaccounts []store.BillSubaccount,
) error {
	found, err := billsService(env).MatchHistory(r.Context(), sp.ID(), subaccounts)
	if err != nil {
		return err
	}
	biller, _ := domain.BillerByID(connection.Biller)
	out := BillHistoryResponse{
		StatementsOffered: biller.HasDocuments || biller.Access == domain.AccessEmail,
		Accounts:          make([]BillAccountHistory, 0, len(found)),
	}
	for _, one := range found {
		row := BillAccountHistory{
			SubaccountID: one.Subaccount.ID, Label: one.Subaccount.Label,
			Matched: one.Matched, Settled: one.Settled, WithStatement: one.WithStatement,
			Unsettled: one.Unsettled, CadenceGapDays: one.CadenceGapDays,
		}
		if one.SeriesID != uuid.Nil {
			row.SeriesID = &one.SeriesID
		}
		out.Accounts = append(out.Accounts, row)
	}
	return writeJSON(w, http.StatusOK, out)
}

// billsSettledByTransaction answers which bill a payment settled, with or
// without a statement document behind it.
func billsSettledByTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	txnID, err := pathUUID(r, "transaction_id", "Transaction")
	if err != nil {
		return err
	}
	if _, err := attachmentTransaction(env, r, sp, txnID); err != nil {
		return err
	}
	settled, err := env.DB.BillsSettledByTransaction(r.Context(), sp.ID(), txnID)
	if err != nil {
		return err
	}
	out := make([]SettledBillResponse, 0, len(settled))
	for _, one := range settled {
		row := SettledBillResponse{
			BillID: one.Bill.ID, ConnectionID: one.Connection.ID, Provider: one.Connection.Title(),
			DueOn: Date(one.Bill.DueOn), AmountDue: one.Bill.AmountDue, Status: string(one.Bill.Status),
		}
		if one.Bill.DocumentID != uuid.Nil {
			id := one.Bill.DocumentID
			row.DocumentID = &id
		}
		out = append(out, row)
	}
	return writeJSON(w, http.StatusOK, out)
}
