package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Transfer activity: what moved between the household's own accounts, and the
// two operations that fix a pairing the rules got wrong.
//
// Nothing here pairs anything by itself: internal/service/transfer.go owns the
// matching rules. This service reads what it produced, releases what it got
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewTransferServiceHandler(transferService{env}, opts...)
	})
}

type transferService struct{ env *Env }

// defaultTransferPageSize bounds the listing.
const defaultTransferPageSize = 200

func transfersService(env *Env) *service.Transfers { return service.NewTransfers(env.DB) }

// transferWindow is the request's date window, fixed to the posted date. Not
// windowOf, which would honour a date_field and split the legs.
func transferWindow(from, to string) (Window, error) {
	return windowBetween(from, to, domain.DatePosted)
}

func (s transferService) ListTransfers(
	ctx context.Context, req *agentifiv1.ListTransfersRequest,
) (*agentifiv1.ListTransfersResponse, error) {
	sp := spaceFrom(ctx)
	window, err := transferWindow(req.GetFrom(), req.GetTo())
	if err != nil {
		return nil, err
	}
	limit, err := limitField("limit", req.Limit, defaultTransferPageSize, 1, 1000)
	if err != nil {
		return nil, err
	}

	from, to, _ := window.storeQuery()
	pairIDs, err := s.env.DB.ListTransferPairIDs(ctx, sp.ID(), from, to, limit)
	if err != nil {
		return nil, err
	}

	transfers := []*agentifiv1.Transfer{}
	if len(pairIDs) > 0 {
		legs, err := s.env.DB.ListTransferLegs(ctx, sp.ID(), store.TransferLegQuery{PairIDs: pairIDs})
		if err != nil {
			return nil, err
		}
		transfers = buildTransfers(pairIDs, legs)
	}

	orphans, err := transfersService(s.env).FindOrphanPairs(ctx, sp.ID())
	if err != nil {
		return nil, err
	}

	return &agentifiv1.ListTransfersResponse{
		Transfers:   transfers,
		Window:      windowProto(window),
		OrphanCount: int32(len(orphans)),
	}, nil
}

// buildTransfers groups legs into movements, in the order the pairs were read.
// A token not carried by exactly two live rows is skipped: one leg is an
// orphan, which ListOrphanTransferLegs reports.
func buildTransfers(pairIDs []uuid.UUID, legs []store.TransferLeg) []*agentifiv1.Transfer {
	byPair := map[uuid.UUID][]store.TransferLeg{}
	for _, leg := range legs {
		byPair[leg.PairID] = append(byPair[leg.PairID], leg)
	}

	out := make([]*agentifiv1.Transfer, 0, len(pairIDs))
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
		out = append(out, &agentifiv1.Transfer{
			PairId:       pairID.String(),
			MovedOn:      movedOn.String(),
			Amount:       moneyProto(paying.Amount.Abs()),
			Currency:     paying.Currency,
			From:         transferLegProto(paying),
			To:           transferLegProto(receiving),
			PairedByHand: paying.PairedByHand,
		})
	}
	return out
}

func transferLegProto(leg store.TransferLeg) *agentifiv1.TransferLeg {
	return &agentifiv1.TransferLeg{
		TransactionId: leg.TransactionID.String(),
		AccountId:     leg.AccountID.String(),
		AccountName:   leg.AccountName,
		Date:          leg.Date.String(),
		Amount:        moneyProto(leg.Amount),
		Currency:      leg.Currency,
		Payee:         leg.Payee,
		Source:        string(leg.Source),
		PairId:        idProto(leg.PairID),
	}
}

// ListTransferCandidates is what a person picks from when pairing by hand. It
// includes hand-entered rows, the only listing that offers them.
func (s transferService) ListTransferCandidates(
	ctx context.Context, req *agentifiv1.ListTransferCandidatesRequest,
) (*agentifiv1.ListTransferCandidatesResponse, error) {
	sp := spaceFrom(ctx)
	window, err := transferWindow(req.GetFrom(), req.GetTo())
	if err != nil {
		return nil, err
	}
	limit, err := limitField("limit", req.Limit, defaultTransferPageSize, 1, 1000)
	if err != nil {
		return nil, err
	}

	from, to, _ := window.storeQuery()
	legs, err := s.env.DB.ListTransferLegs(ctx, sp.ID(), store.TransferLegQuery{
		From: from, To: to, Pairing: store.OnlyUnpaired, Limit: limit,
	})
	if err != nil {
		return nil, err
	}

	candidates := make([]*agentifiv1.TransferLeg, 0, len(legs))
	for _, leg := range legs {
		candidates = append(candidates, transferLegProto(leg))
	}
	return &agentifiv1.ListTransferCandidatesResponse{Candidates: candidates, Window: windowProto(window)}, nil
}

func (s transferService) ListOrphanTransferLegs(
	ctx context.Context, _ *agentifiv1.ListOrphanTransferLegsRequest,
) (*agentifiv1.ListOrphanTransferLegsResponse, error) {
	orphans, err := loadOrphanLegs(ctx, s.env, spaceFrom(ctx))
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListOrphanTransferLegsResponse{Orphans: transferLegProtos(orphans)}, nil
}

func transferLegProtos(legs []store.TransferLeg) []*agentifiv1.TransferLeg {
	out := make([]*agentifiv1.TransferLeg, 0, len(legs))
	for _, leg := range legs {
		out = append(out, transferLegProto(leg))
	}
	return out
}

// loadOrphanLegs names the damage without repairing it.
func loadOrphanLegs(ctx context.Context, env *Env, sp auth.SpaceContext) ([]store.TransferLeg, error) {
	ids, err := transfersService(env).FindOrphanPairs(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return env.DB.ListTransferLegs(ctx, sp.ID(), store.TransferLegQuery{IDs: ids})
}

// DetectTransfers runs the pairer over the whole ledger. The sync pairs only
// the rows it just wrote, so imported history is never a candidate otherwise.
// A nil candidate list means every leg (see PairOptions: nil and empty
// differ).
func (s transferService) DetectTransfers(
	ctx context.Context, _ *agentifiv1.DetectTransfersRequest,
) (*agentifiv1.DetectTransfersResponse, error) {
	paired, err := service.NewTransfers(s.env.DB).DetectPairs(ctx, spaceFrom(ctx).ID(), service.PairOptions{})
	if err != nil {
		return nil, err
	}
	return &agentifiv1.DetectTransfersResponse{Paired: int32(paired)}, nil
}

// RepairOrphanTransferLegs releases every leg whose partner is gone. The legs
// are read before the release, since afterwards nothing distinguishes them and
// the response must name them.
func (s transferService) RepairOrphanTransferLegs(
	ctx context.Context, _ *agentifiv1.RepairOrphanTransferLegsRequest,
) (*agentifiv1.RepairOrphanTransferLegsResponse, error) {
	sp := spaceFrom(ctx)
	before, err := loadOrphanLegs(ctx, s.env, sp)
	if err != nil {
		return nil, err
	}
	if _, err := transfersService(s.env).RepairOrphanPairs(ctx, sp.ID()); err != nil {
		return nil, err
	}
	// The manual marks go with the tokens they described.
	for _, leg := range before {
		if leg.PairID != uuid.Nil {
			if err := s.env.DB.ForgetTransferPair(ctx, sp.ID(), leg.PairID); err != nil {
				return nil, err
			}
		}
	}
	return &agentifiv1.RepairOrphanTransferLegsResponse{Orphans: transferLegProtos(before)}, nil
}

func (s transferService) PairTransfer(
	ctx context.Context, req *agentifiv1.PairTransferRequest,
) (*agentifiv1.PairTransferResponse, error) {
	sp := spaceFrom(ctx)
	payingID, err := uuidField(req.GetPayingTransactionId(), "body", "paying_transaction_id")
	if err != nil {
		return nil, err
	}
	receivingID, err := uuidField(req.GetReceivingTransactionId(), "body", "receiving_transaction_id")
	if err != nil {
		return nil, err
	}
	if payingID == uuid.Nil || receivingID == uuid.Nil {
		return nil, errInvalid("missing", []string{"body", "paying_transaction_id"},
			"both halves of the transfer must be named")
	}
	if payingID == receivingID {
		return nil, errConflict("A transaction cannot be both halves of one transfer")
	}

	// Read through the store, which is scoped to the space: a transaction id
	// from another household is a 404 here, exactly as it is everywhere else.
	paying, err := s.env.DB.GetTransaction(ctx, sp.ID(), payingID)
	if err != nil {
		return nil, err
	}
	receiving, err := s.env.DB.GetTransaction(ctx, sp.ID(), receivingID)
	if err != nil {
		return nil, err
	}
	if err := checkHandPair(paying, receiving); err != nil {
		return nil, err
	}

	pairID, paired, err := s.env.DB.PairTransactions(ctx, sp.ID(), paying.ID, receiving.ID, true)
	if err != nil {
		return nil, err
	}
	if !paired {
		// Both rows were read a moment ago, so something paired or deleted
		// one in between; the store refused rather than half-write the pair.
		return nil, errConflict("One of those transactions changed; reload and try again")
	}

	legs, err := s.env.DB.ListTransferLegs(ctx, sp.ID(), store.TransferLegQuery{PairIDs: []uuid.UUID{pairID}})
	if err != nil {
		return nil, err
	}
	built := buildTransfers([]uuid.UUID{pairID}, legs)
	if len(built) != 1 {
		return nil, errConflict("The pair could not be read back")
	}
	return &agentifiv1.PairTransferResponse{Transfer: built[0]}, nil
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

// UnpairTransfer releases a pair without deleting either transaction; both
// count as income and expense again.
func (s transferService) UnpairTransfer(
	ctx context.Context, req *agentifiv1.UnpairTransferRequest,
) (*agentifiv1.UnpairTransferResponse, error) {
	sp := spaceFrom(ctx)
	pairID, err := idFrom(req.GetPairId(), "Transfer")
	if err != nil {
		return nil, err
	}
	// Keyed on the shared token: matching on a leg id updates nothing and
	// leaves the orphan (see store.DeleteTransaction).
	released, err := transfersService(s.env).UnlinkPair(ctx, sp.ID(), pairID)
	if err != nil {
		return nil, err
	}
	if released == 0 {
		return nil, errNotFound("Transfer")
	}
	if err := s.env.DB.ForgetTransferPair(ctx, sp.ID(), pairID); err != nil {
		return nil, err
	}
	return &agentifiv1.UnpairTransferResponse{}, nil
}
