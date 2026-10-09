package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Possible duplicates: the same charge written by two sources. domain.
// FindDuplicateCandidates proposes pairs; this persists them and applies the
// household's verdict. Nothing is retired without one.

var (
	// ErrDuplicateDecided is a verdict asked for a pair that already has one.
	ErrDuplicateDecided = errors.New("service: duplicate pair already decided")
	// ErrDuplicateGone is a pair one of whose rows has been deleted since it
	// was proposed.
	ErrDuplicateGone = errors.New("service: duplicate pair has a deleted row")
	// ErrNotInPair is a keep id that names neither row of the pair.
	ErrNotInPair = errors.New("service: kept row is not in the pair")
)

type Duplicates struct{ base }

func NewDuplicates(st *store.Store) *Duplicates { return &Duplicates{newBase(st)} }

// DetectForRows proposes pairs that involve any of the rows just written,
// looking only at the days around them in each row's account.
func (d *Duplicates) DetectForRows(
	ctx context.Context, spaceID store.SpaceID, ids []uuid.UUID,
) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	fresh, err := d.store.ListTransactions(ctx, spaceID, store.TransactionQuery{IDs: ids})
	if err != nil {
		return 0, err
	}
	type span struct {
		from, to domain.Date
		ids      []domain.ID
	}
	byAccount := map[uuid.UUID]*span{}
	var order []uuid.UUID
	for _, row := range fresh {
		if !store.DomainTransaction(row).CanBeDuplicated() {
			continue
		}
		one, seen := byAccount[row.AccountID]
		if !seen {
			one = &span{from: row.Date, to: row.Date}
			byAccount[row.AccountID] = one
			order = append(order, row.AccountID)
		}
		if row.Date.Before(one.from) {
			one.from = row.Date
		}
		if row.Date.After(one.to) {
			one.to = row.Date
		}
		one.ids = append(one.ids, domain.ID(row.ID.String()))
	}

	found := 0
	for _, accountID := range order {
		one := byAccount[accountID]
		added, err := d.detectAccount(ctx, spaceID, accountID,
			one.from.AddDays(-domain.DuplicateToleranceDays),
			one.to.AddDays(domain.DuplicateToleranceDays), one.ids)
		if err != nil {
			return found, err
		}
		found += added
	}
	return found, nil
}

// DetectSpace looks over the whole history of every account that is not
// ignored. It is what runs after a Simplifi import, which writes no rows
// through the ingest, and what a person asks for to check history that was
// there before the check existed.
func (d *Duplicates) DetectSpace(ctx context.Context, spaceID store.SpaceID) (int, error) {
	accounts, err := d.store.ListAccounts(ctx, spaceID, store.AccountQuery{IncludeClosed: true})
	if err != nil {
		return 0, err
	}
	found := 0
	for _, account := range accounts {
		added, err := d.detectAccount(ctx, spaceID, account.ID, domain.Date{}, domain.Date{}, nil)
		if err != nil {
			return found, err
		}
		found += added
	}
	return found, nil
}

// detectAccount saves the pairs found among one account's rows between from
// and to (the whole account when from is zero), returning how many are new.
// candidates narrows the pairs to those touching one of those rows; nil is
// every row.
func (d *Duplicates) detectAccount(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID,
	from, to domain.Date, candidates []domain.ID,
) (int, error) {
	rows, err := d.store.ListTransactions(ctx, spaceID, store.TransactionQuery{
		AccountIDs: []uuid.UUID{accountID}, From: from, To: to,
	})
	if err != nil {
		return 0, err
	}
	distinct, err := d.store.RuledOutDuplicates(ctx, spaceID, []uuid.UUID{accountID})
	if err != nil {
		return 0, err
	}
	pairs := domain.FindDuplicateCandidates(store.DomainTransactions(rows), domain.DuplicateOptions{
		CandidateIDs: candidates,
		Distinct:     distinct,
	})
	return d.store.SaveDuplicateCandidates(ctx, spaceID, accountID, pairs)
}

// Retired is what a duplicate verdict did.
type Retired struct {
	KeptID, RetiredID uuid.UUID
	AccountID         uuid.UUID
}

// MarkDuplicate retires one copy of a pair and records the verdict. keepID is
// the copy to keep; the zero value takes domain.SuggestedDuplicateKeep.
//
// The copy goes through the store's retire path, which releases its transfer
// pair first (trap 2) and its pad, so no leg is left holding a token for a
// partner that is gone. When the retired copy came from the bank, the kept
// copy takes over its aggregator id, so the next sync settles it instead of
// writing the charge again.
func (d *Duplicates) MarkDuplicate(
	ctx context.Context, spaceID store.SpaceID, userID, id, keepID uuid.UUID,
) (Retired, error) {
	pair, first, second, err := d.openPair(ctx, spaceID, id)
	if err != nil {
		return Retired{}, err
	}
	if keepID == uuid.Nil {
		keepID, err = uuid.Parse(string(domain.SuggestedDuplicateKeep(
			store.DomainTransaction(first), store.DomainTransaction(second))))
		if err != nil {
			return Retired{}, err
		}
	}
	keep, discard := first, second
	switch keepID {
	case first.ID:
	case second.ID:
		keep, discard = second, first
	default:
		return Retired{}, ErrNotInPair
	}

	partners, err := d.transferPartners(ctx, spaceID, discard)
	if err != nil {
		return Retired{}, err
	}
	err = d.inTx(ctx, func(tx *store.Store) error {
		if discard.ExternalID != "" && keep.ExternalID == "" {
			if err := tx.RetireDuplicate(ctx, spaceID, discard.ID); err != nil {
				return err
			}
			if err := tx.AdoptExternalID(ctx, spaceID, keep.ID, discard.ExternalID); err != nil {
				return err
			}
		} else if err := tx.DeleteTransaction(ctx, spaceID, discard.ID); err != nil {
			return err
		}
		return tx.DecideDuplicate(ctx, spaceID, pair.ID, store.VerdictDuplicate, keep.ID, userID)
	})
	if err != nil {
		return Retired{}, fmt.Errorf("service: retire duplicate: %w", err)
	}

	// The retired copy's partner leg was released; it may pair with the kept
	// copy's own partner or the kept copy itself.
	if len(partners) > 0 {
		if _, err := NewTransfers(d.store).DetectPairs(ctx, spaceID,
			PairOptions{CandidateIDs: append(partners, keep.ID)}); err != nil {
			return Retired{}, err
		}
	}
	if err := RecomputeRunningBalances(ctx, d.store, spaceID, keep.AccountID); err != nil {
		return Retired{}, err
	}
	return Retired{KeptID: keep.ID, RetiredID: discard.ID, AccountID: keep.AccountID}, nil
}

// MarkDistinct records that both rows are real, so the pair is never proposed
// again.
func (d *Duplicates) MarkDistinct(
	ctx context.Context, spaceID store.SpaceID, userID, id uuid.UUID,
) error {
	if _, err := d.store.GetDuplicate(ctx, spaceID, id); err != nil {
		return err
	}
	if err := d.store.DecideDuplicate(ctx, spaceID, id, store.VerdictDistinct, uuid.Nil, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrDuplicateDecided
		}
		return err
	}
	return nil
}

// openPair loads an undecided pair and its two live rows.
func (d *Duplicates) openPair(
	ctx context.Context, spaceID store.SpaceID, id uuid.UUID,
) (store.DuplicateCandidate, store.Transaction, store.Transaction, error) {
	pair, err := d.store.GetDuplicate(ctx, spaceID, id)
	if err != nil {
		return pair, store.Transaction{}, store.Transaction{}, err
	}
	if pair.Verdict != "" {
		return pair, store.Transaction{}, store.Transaction{}, ErrDuplicateDecided
	}
	rows, err := d.store.ListTransactions(ctx, spaceID, store.TransactionQuery{
		IDs: []uuid.UUID{pair.FirstID, pair.SecondID},
	})
	if err != nil {
		return pair, store.Transaction{}, store.Transaction{}, err
	}
	if len(rows) != 2 {
		return pair, store.Transaction{}, store.Transaction{}, ErrDuplicateGone
	}
	if rows[0].ID == pair.SecondID {
		rows[0], rows[1] = rows[1], rows[0]
	}
	return pair, rows[0], rows[1], nil
}

// transferPartners is the other legs of the transfer a row is half of.
func (d *Duplicates) transferPartners(
	ctx context.Context, spaceID store.SpaceID, row store.Transaction,
) ([]uuid.UUID, error) {
	if row.TransferPairID == uuid.Nil {
		return nil, nil
	}
	rows, err := d.conn().Query(ctx, `
		SELECT id FROM transactions
		WHERE space_id = $1 AND transfer_pair_id = $2 AND id <> $3 AND NOT is_deleted`,
		spaceID.UUID(), row.TransferPairID, row.ID)
	if err != nil {
		return nil, fmt.Errorf("service: transfer partners: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("service: transfer partners: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
