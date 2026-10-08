package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Transfer activity: what moved between the household's own accounts, and the
// two operations that fix a pairing the rules got wrong.
//
// Nothing here pairs anything by itself: internal/service/transfer.go owns the
// matching rules. This resource reads what it produced, releases what it got
// wrong, and lets a person state a pair it could not see.
//
// A hand-made pair is exempt from trap 3 (the automatic pairer never touches
// hand-entered rows) and from equal magnitude (a wire that lands 5.00 short
// after a fee is the case hand pairing is for). Everything else still holds:
// both rows live and unpaired, two accounts of one space, one currency, one
// paying and one receiving.
//
// This screen reports on the posted date, not effective_date (trap 4): the two
// legs of one movement can be a statement cycle apart in effective date, which
// would split the pair across windows.
//
// Agentifi moves no money, so unlike Simplifi's screen this initiates nothing.

func init() {
	Register(Resource{Prefix: "/transfers", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listTransfers)
		rt.Read(http.MethodGet, "/candidates", listTransferCandidates)
		rt.Read(http.MethodGet, "/orphans", listOrphanTransferLegs)
		rt.Write(http.MethodPost, "/", pairTransferByHand)
		rt.Write(http.MethodPost, "/detect", detectTransfers)
		rt.Write(http.MethodPost, "/orphans/repair", repairOrphanTransferLegs)
		rt.Write(http.MethodDelete, "/{pair_id}", unpairTransfer)
	}})
}

// defaultTransferPageSize bounds the listing.
const defaultTransferPageSize = 200

// TransferLegResponse is one side of a movement.
type TransferLegResponse struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	AccountName   string    `json:"account_name"`
	// Date is the posted day. See the note at the top of this file for why
	// this screen does not read the effective date.
	Date     Date         `json:"date"`
	Amount   domain.Money `json:"amount"`
	Currency string       `json:"currency"`
	Payee    string       `json:"payee"`
	Source   string       `json:"source"`
	// PairID is set on an orphan leg, and null on a candidate for pairing.
	PairID *uuid.UUID `json:"pair_id"`
}

// TransferResponse is one paired movement: money out of one account and into
// another, under a token both legs carry.
type TransferResponse struct {
	PairID uuid.UUID `json:"pair_id"`
	// MovedOn is the later of the two posted dates, when the money had
	// finished moving.
	MovedOn Date `json:"moved_on"`
	// Amount is the magnitude of the movement, taken from the paying leg.
	Amount   domain.Money        `json:"amount"`
	Currency string              `json:"currency"`
	From     TransferLegResponse `json:"from"`
	To       TransferLegResponse `json:"to"`
	// PairedByHand distinguishes a person's decision from the rules' guess.
	PairedByHand bool `json:"paired_by_hand"`
}

// TransferListResponse is the activity, plus the window it was read over.
type TransferListResponse struct {
	Transfers []TransferResponse `json:"transfers"`
	Window    WindowResponse     `json:"window"`
	// OrphanCount is the repair job's finding over the whole space, not the
	// window: an orphaned leg is wrong wherever it sits.
	OrphanCount int `json:"orphan_count"`
}

// TransferCandidateListResponse is the unpaired rows a person can join.
type TransferCandidateListResponse struct {
	Candidates []TransferLegResponse `json:"candidates"`
	Window     WindowResponse        `json:"window"`
}

// OrphanListResponse is trap 2 made visible: legs still carrying a pair token
// whose partner is gone.
type OrphanListResponse struct {
	Orphans []TransferLegResponse `json:"orphans"`
}

// TransferPairRequest names the two halves of one movement.
type TransferPairRequest struct {
	PayingTransactionID    uuid.UUID `json:"paying_transaction_id"`
	ReceivingTransactionID uuid.UUID `json:"receiving_transaction_id"`
}

func transfersService(env *Env) *service.Transfers { return service.NewTransfers(env.DB) }

// transferWindow is the request's date window, fixed to the posted date. Not
// WindowFromRequest, which would honour a `date_field` and split the legs.
func transferWindow(r *http.Request) (Window, error) {
	return windowOn(r, domain.DatePosted)
}

func listTransfers(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := transferWindow(r)
	if err != nil {
		return err
	}
	limit, err := queryInt(r, "limit", defaultTransferPageSize, 1, 1000)
	if err != nil {
		return err
	}

	from, to, _ := window.storeQuery()
	pairIDs, err := env.DB.ListTransferPairIDs(r.Context(), sp.ID(), from, to, limit)
	if err != nil {
		return err
	}

	transfers := []TransferResponse{}
	if len(pairIDs) > 0 {
		legs, err := env.DB.ListTransferLegs(r.Context(), sp.ID(),
			store.TransferLegQuery{PairIDs: pairIDs})
		if err != nil {
			return err
		}
		transfers = buildTransfers(pairIDs, legs)
	}

	orphans, err := transfersService(env).FindOrphanPairs(r.Context(), sp.ID())
	if err != nil {
		return err
	}

	return writeJSON(w, http.StatusOK, TransferListResponse{
		Transfers:   transfers,
		Window:      windowResponse(window),
		OrphanCount: len(orphans),
	})
}

// buildTransfers groups legs into movements, in the order the pairs were read.
// A token not carried by exactly two live rows is skipped: one leg is an
// orphan, which /orphans reports.
func buildTransfers(pairIDs []uuid.UUID, legs []store.TransferLeg) []TransferResponse {
	byPair := map[uuid.UUID][]store.TransferLeg{}
	for _, leg := range legs {
		byPair[leg.PairID] = append(byPair[leg.PairID], leg)
	}

	out := make([]TransferResponse, 0, len(pairIDs))
	for _, pairID := range pairIDs {
		pair := byPair[pairID]
		if len(pair) != 2 {
			continue
		}
		paying, receiving := pair[0], pair[1]
		if paying.Amount.IsPositive() {
			paying, receiving = receiving, paying
		}
		movedOn := paying.Date
		if receiving.Date.After(movedOn) {
			movedOn = receiving.Date
		}
		out = append(out, TransferResponse{
			PairID:       pairID,
			MovedOn:      Date(movedOn),
			Amount:       paying.Amount.Abs(),
			Currency:     paying.Currency,
			From:         transferLegResponse(paying),
			To:           transferLegResponse(receiving),
			PairedByHand: paying.PairedByHand,
		})
	}
	return out
}

func transferLegResponse(leg store.TransferLeg) TransferLegResponse {
	return TransferLegResponse{
		TransactionID: leg.TransactionID,
		AccountID:     leg.AccountID,
		AccountName:   leg.AccountName,
		Date:          Date(leg.Date),
		Amount:        leg.Amount,
		Currency:      leg.Currency,
		Payee:         leg.Payee,
		Source:        string(leg.Source),
		PairID:        dbconv.NullUUID(leg.PairID),
	}
}

// listTransferCandidates is what a person picks from when pairing by hand. It
// includes hand-entered rows, the only listing that offers them.
func listTransferCandidates(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := transferWindow(r)
	if err != nil {
		return err
	}
	limit, err := queryInt(r, "limit", defaultTransferPageSize, 1, 1000)
	if err != nil {
		return err
	}

	from, to, _ := window.storeQuery()
	legs, err := env.DB.ListTransferLegs(r.Context(), sp.ID(), store.TransferLegQuery{
		From: from, To: to, Pairing: store.OnlyUnpaired, Limit: limit,
	})
	if err != nil {
		return err
	}

	candidates := make([]TransferLegResponse, 0, len(legs))
	for _, leg := range legs {
		candidates = append(candidates, transferLegResponse(leg))
	}
	return writeJSON(w, http.StatusOK, TransferCandidateListResponse{
		Candidates: candidates, Window: windowResponse(window),
	})
}

func listOrphanTransferLegs(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	orphans, err := loadOrphanLegs(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, OrphanListResponse{Orphans: orphans})
}

// loadOrphanLegs names the damage without repairing it.
func loadOrphanLegs(r *http.Request, env *Env, sp auth.SpaceContext) ([]TransferLegResponse, error) {
	ids, err := transfersService(env).FindOrphanPairs(r.Context(), sp.ID())
	if err != nil {
		return nil, err
	}
	out := []TransferLegResponse{}
	if len(ids) == 0 {
		return out, nil
	}
	legs, err := env.DB.ListTransferLegs(r.Context(), sp.ID(), store.TransferLegQuery{IDs: ids})
	if err != nil {
		return nil, err
	}
	for _, leg := range legs {
		out = append(out, transferLegResponse(leg))
	}
	return out, nil
}

// DetectResponse is what a sweep found.
type DetectResponse struct {
	Paired int `json:"paired"`
}

// detectTransfers runs the pairer over the whole ledger. The sync pairs only
// the rows it just wrote, so imported history is never a candidate otherwise.
// A nil candidate list means every leg (see PairOptions: nil and empty
// differ).
func detectTransfers(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	paired, err := service.NewTransfers(env.DB).DetectPairs(
		r.Context(), sp.ID(), service.PairOptions{})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, DetectResponse{Paired: paired})
}

// repairOrphanTransferLegs releases every leg whose partner is gone. The legs
// are read before the release, since afterwards nothing distinguishes them and
// the response must name them.
func repairOrphanTransferLegs(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	before, err := loadOrphanLegs(r, env, sp)
	if err != nil {
		return err
	}
	if _, err := transfersService(env).RepairOrphanPairs(r.Context(), sp.ID()); err != nil {
		return err
	}
	// The manual marks go with the tokens they described.
	for _, leg := range before {
		if leg.PairID != nil {
			if err := env.DB.ForgetTransferPair(r.Context(), sp.ID(), *leg.PairID); err != nil {
				return err
			}
		}
	}
	return writeJSON(w, http.StatusOK, OrphanListResponse{Orphans: before})
}

func pairTransferByHand(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body TransferPairRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.PayingTransactionID == uuid.Nil || body.ReceivingTransactionID == uuid.Nil {
		return errInvalid("missing", []string{"body", "paying_transaction_id"},
			"both halves of the transfer must be named")
	}
	if body.PayingTransactionID == body.ReceivingTransactionID {
		return errConflict("A transaction cannot be both halves of one transfer")
	}

	// Read through the store, which is scoped to the space: a transaction id
	// from another household is a 404 here, exactly as it is everywhere else.
	paying, err := env.DB.GetTransaction(r.Context(), sp.ID(), body.PayingTransactionID)
	if err != nil {
		return err
	}
	receiving, err := env.DB.GetTransaction(r.Context(), sp.ID(), body.ReceivingTransactionID)
	if err != nil {
		return err
	}
	if err := checkHandPair(paying, receiving); err != nil {
		return err
	}

	pairID, paired, err := env.DB.PairTransactions(r.Context(), sp.ID(), paying.ID, receiving.ID, true)
	if err != nil {
		return err
	}
	if !paired {
		// Both rows were read a moment ago, so something paired or deleted
		// one in between; the store refused rather than half-write the pair.
		return errConflict("One of those transactions changed; reload and try again")
	}

	legs, err := env.DB.ListTransferLegs(r.Context(), sp.ID(),
		store.TransferLegQuery{PairIDs: []uuid.UUID{pairID}})
	if err != nil {
		return err
	}
	built := buildTransfers([]uuid.UUID{pairID}, legs)
	if len(built) != 1 {
		return errConflict("The pair could not be read back")
	}
	return writeJSON(w, http.StatusCreated, built[0])
}

// checkHandPair is what still holds when the source rule is waived: each
// refusal is a pair that would remove two rows from profit and loss without
// money having moved between two accounts.
func checkHandPair(paying, receiving store.Transaction) error {
	// store.MoneyMoved's rule in row form: a forecast is not deleted, and must
	// not be hand-picked as a leg.
	if !store.CountsTowardBalance(paying) || !store.CountsTowardBalance(receiving) {
		// The flags only choose the wording; the refusal above is the rule.
		if paying.IsDeleted || receiving.IsDeleted {
			return errConflict("A deleted transaction cannot be paired")
		}
		return errConflict(
			"A forecast has not moved any money yet, so it cannot be half of a transfer")
	}
	if paying.TransferPairID != uuid.Nil || receiving.TransferPairID != uuid.Nil {
		// Re-pairing a paired row would strand its current partner holding a
		// token nothing else carries. Unpair that transfer first.
		return errConflict("One of those transactions is already part of a transfer")
	}
	if paying.AccountID == receiving.AccountID {
		return errConflict("Both halves are in the same account, so no money moved between accounts")
	}
	if paying.Currency != receiving.Currency {
		// The same figure in two currencies is not the same money, and pairing
		// them drops both from profit and loss permanently.
		return errConflict("The two halves are in different currencies")
	}
	if !paying.Amount.IsNegative() || !receiving.Amount.IsPositive() {
		return errConflict("A transfer needs one transaction leaving an account and one arriving")
	}
	return nil
}

// unpairTransfer releases a pair without deleting either transaction; both
// count as income and expense again.
func unpairTransfer(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	pairID, err := pathUUID(r, "pair_id", "Transfer")
	if err != nil {
		return err
	}
	// Keyed on the shared token: matching on a leg id updates nothing and
	// leaves the orphan (see store.DeleteTransaction).
	released, err := transfersService(env).UnlinkPair(r.Context(), sp.ID(), pairID)
	if err != nil {
		return err
	}
	if released == 0 {
		return errNotFound("Transfer")
	}
	if err := env.DB.ForgetTransferPair(r.Context(), sp.ID(), pairID); err != nil {
		return err
	}
	return writeNoContent(w)
}
