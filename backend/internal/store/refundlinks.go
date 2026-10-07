package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Refund links: which charge a credit gives back. One row joins two ids, so
// unlike a transfer pairing it cannot half-exist. Soft deletes are the half a
// foreign key cannot cover: DeleteTransaction drops a row's links in the same
// database transaction.

type RefundLink struct {
	RefundTxnID uuid.UUID
	ChargeTxnID uuid.UUID
}

// ListRefundLinks is every link in the space, with the charge's category
// resolved by the join because the charge is often outside the loaded window
// (a January purchase refunded in March goes back under January's category).
// Links whose charge is deleted are skipped: there is no spending left to
// come back out of.
func (s *Store) ListRefundLinks(ctx context.Context, spaceID SpaceID) ([]domain.RefundLink, error) {
	return queryAll(ctx, s.db, "store: list refund links", func(row scanner) (domain.RefundLink, error) {
		var refundID, chargeID uuid.UUID
		var categoryID string
		if err := row.Scan(&refundID, &chargeID, &categoryID); err != nil {
			return domain.RefundLink{}, err
		}
		return domain.RefundLink{
			RefundTxnID:      domainID(refundID),
			ChargeTxnID:      domainID(chargeID),
			ChargeCategoryID: domain.ID(categoryID),
		}, nil
	}, `
		SELECT l.refund_txn_id, l.charge_txn_id, coalesce(c.category_id::text, '')
		FROM transaction_refund_links l
		JOIN transactions c ON c.id = l.charge_txn_id AND c.space_id = l.space_id
		WHERE l.space_id = $1 AND NOT c.is_deleted
		ORDER BY l.refund_txn_id, l.charge_txn_id`, spaceID.UUID())
}

// ListRefundLinksFor is the links touching these transactions, both directions
// in one query so the dialog's two statements cannot disagree.
func (s *Store) ListRefundLinksFor(
	ctx context.Context, spaceID SpaceID, ids []uuid.UUID,
) ([]RefundLink, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.db, "store: list refund links for", func(row scanner) (RefundLink, error) {
		var one RefundLink
		err := row.Scan(&one.RefundTxnID, &one.ChargeTxnID)
		return one, err
	}, `
		SELECT refund_txn_id, charge_txn_id
		FROM transaction_refund_links
		WHERE space_id = $1 AND (refund_txn_id = ANY($2) OR charge_txn_id = ANY($2))
		ORDER BY refund_txn_id, charge_txn_id`, spaceID.UUID(), ids)
}

// LinkRefund records that the credit gives back part or all of the charge.
// Both ids are re-checked against the space inside the insert, so a
// cross-tenant id is a no-op. Idempotent.
func (s *Store) LinkRefund(
	ctx context.Context, spaceID SpaceID, refundID, chargeID uuid.UUID,
) error {
	result, err := s.db.Exec(ctx, `
		INSERT INTO transaction_refund_links (space_id, refund_txn_id, charge_txn_id)
		SELECT $1, r.id, c.id
		FROM transactions r, transactions c
		WHERE r.space_id = $1 AND r.id = $2 AND `+MoneyMovedOn("r")+`
		  AND c.space_id = $1 AND c.id = $3 AND `+MoneyMovedOn("c")+`
		ON CONFLICT DO NOTHING`, spaceID.UUID(), refundID, chargeID)
	if err != nil {
		return wrap("store: link refund", err)
	}
	// The credit takes the charge's category now; RefileLinkedRefunds keeps
	// them together afterwards.
	if _, err := s.db.Exec(ctx, `
		UPDATE transactions AS r
		SET category_id = c.category_id, updated_at = now()
		FROM transactions AS c
		WHERE r.space_id = $1 AND r.id = $2
		  AND c.space_id = $1 AND c.id = $3
		  AND r.category_id IS DISTINCT FROM c.category_id`,
		spaceID.UUID(), refundID, chargeID); err != nil {
		return wrap("store: link refund", err)
	}
	if result.RowsAffected() == 0 {
		// Nothing inserted: one row changed underneath, or the link exists.
		exists, err := s.refundLinkExists(ctx, spaceID, refundID, chargeID)
		if err != nil {
			return err
		}
		if !exists {
			return wrap("store: link refund", ErrNotFound)
		}
	}
	return nil
}

// RefileLinkedRefunds keeps a refund and its charge under one category, so
// recategorising either files the other there too.
//
// Direct partners only: the picker cannot build a credit against a credit
// (CanBeARefund wants a positive, CanBeRefunded a negative), so there is no
// chain. DISTINCT FROM makes it a no-op once they agree.
func (s *Store) RefileLinkedRefunds(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, categoryID *uuid.UUID,
) error {
	_, err := s.db.Exec(ctx, `
		UPDATE transactions SET category_id = $3, updated_at = now()
		WHERE space_id = $1 AND category_id IS DISTINCT FROM $3 AND id IN (
			SELECT charge_txn_id FROM transaction_refund_links
			WHERE space_id = $1 AND refund_txn_id = $2
			UNION
			SELECT refund_txn_id FROM transaction_refund_links
			WHERE space_id = $1 AND charge_txn_id = $2
		)`, spaceID.UUID(), id, categoryID)
	return wrap("store: refile linked refunds", err)
}

func (s *Store) refundLinkExists(
	ctx context.Context, spaceID SpaceID, refundID, chargeID uuid.UUID,
) (bool, error) {
	var found bool
	err := s.db.QueryRow(ctx, `
		SELECT true FROM transaction_refund_links
		WHERE space_id = $1 AND refund_txn_id = $2 AND charge_txn_id = $3`,
		spaceID.UUID(), refundID, chargeID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return found, wrap("store: refund link exists", err)
}

// UnlinkRefund drops one link, reporting how many rows went so a handler can
// tell a present link from an unknown id.
func (s *Store) UnlinkRefund(
	ctx context.Context, spaceID SpaceID, refundID, chargeID uuid.UUID,
) (int, error) {
	result, err := s.db.Exec(ctx, `
		DELETE FROM transaction_refund_links
		WHERE space_id = $1 AND refund_txn_id = $2 AND charge_txn_id = $3`,
		spaceID.UUID(), refundID, chargeID)
	if err != nil {
		return 0, wrap("store: unlink refund", err)
	}
	return int(result.RowsAffected()), nil
}

// releaseRefundLinks drops every link a transaction is either side of. Called
// inside DeleteTransaction's own database transaction so the release is never
// left to a caller (trap 2).
func (s *Store) releaseRefundLinks(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM transaction_refund_links
		WHERE space_id = $1 AND ($2 IN (refund_txn_id, charge_txn_id))`,
		spaceID.UUID(), id)
	return wrap("store: release refund links", err)
}

// RefileRefunds applies the space's refund links to a set of postings. The
// join lives here because domain.ApplyRefundLinks needs categories resolved,
// and a caller's wrong map would refile under another space's category.
func (s *Store) RefileRefunds(
	ctx context.Context, spaceID SpaceID, postings []domain.Posting, categories []Category,
) ([]domain.Posting, error) {
	links, err := s.ListRefundLinks(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return postings, nil
	}
	byID := make(map[domain.ID]domain.Category, len(categories))
	for _, one := range categories {
		byID[domainID(one.ID)] = DomainCategory(one)
	}
	return domain.ApplyRefundLinks(postings, links, byID), nil
}
