package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Two transactions proposed as one charge recorded twice (domain.FindDuplicateCandidates),
// and what the household said about them. A pair has one row, with the lower
// id first; a distinct verdict is what keeps it from being proposed again.

const (
	VerdictDuplicate = "duplicate"
	VerdictDistinct  = "distinct"
)

type DuplicateCandidate struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	// FirstID is the lower of the two ids.
	FirstID, SecondID uuid.UUID
	DaysApart         int
	// Verdict is empty while the pair is undecided.
	Verdict   string
	KeptID    uuid.UUID
	DecidedBy uuid.UUID
	DecidedAt *time.Time
	CreatedAt time.Time
}

const duplicateColumns = `d.id, d.account_id, d.first_txn_id, d.second_txn_id, d.days_apart,
	coalesce(d.verdict, ''), d.kept_txn_id, d.decided_by, d.decided_at, d.created_at`

func scanDuplicate(row scanner) (DuplicateCandidate, error) {
	var one DuplicateCandidate
	var kept, by *uuid.UUID
	err := row.Scan(&one.ID, &one.AccountID, &one.FirstID, &one.SecondID, &one.DaysApart,
		&one.Verdict, &kept, &by, &one.DecidedAt, &one.CreatedAt)
	if kept != nil {
		one.KeptID = *kept
	}
	if by != nil {
		one.DecidedBy = *by
	}
	return one, err
}

// SaveDuplicateCandidates records proposed pairs, leaving a pair already on
// file (decided or not) as it is, and returns how many were new.
func (s *Store) SaveDuplicateCandidates(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, pairs []domain.DuplicateCandidate,
) (int, error) {
	added := 0
	for _, pair := range pairs {
		first, err := uuid.Parse(string(pair.First))
		if err != nil {
			return added, wrap("store: save duplicate candidate", err)
		}
		second, err := uuid.Parse(string(pair.Second))
		if err != nil {
			return added, wrap("store: save duplicate candidate", err)
		}
		tag, err := s.db.Exec(ctx, `
			INSERT INTO duplicate_candidates (id, space_id, account_id, first_txn_id, second_txn_id, days_apart)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (first_txn_id, second_txn_id) DO NOTHING`,
			uuid.New(), spaceID.UUID(), accountID, first, second, pair.DaysApart)
		if err != nil {
			return added, wrap("store: save duplicate candidate", err)
		}
		added += int(tag.RowsAffected())
	}
	return added, nil
}

// RuledOutDuplicates is the pairs in these accounts the household has said are
// two real transactions, in the form domain.DuplicateOptions reads.
func (s *Store) RuledOutDuplicates(
	ctx context.Context, spaceID SpaceID, accountIDs []uuid.UUID,
) (map[[2]domain.ID]bool, error) {
	pairs, err := queryAll(ctx, s.db, "store: ruled out duplicates", scanPair[uuid.UUID, uuid.UUID], `
		SELECT first_txn_id, second_txn_id FROM duplicate_candidates
		WHERE space_id = $1 AND account_id = ANY($2) AND verdict = 'distinct'`,
		spaceID.UUID(), accountIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]domain.ID]bool, len(pairs))
	for _, p := range pairs {
		out[domain.DuplicateKey(domain.ID(p.first.String()), domain.ID(p.second.String()))] = true
	}
	return out, nil
}

// ListOpenDuplicates is the pairs still waiting on a verdict whose two rows
// are both live, oldest first.
func (s *Store) ListOpenDuplicates(ctx context.Context, spaceID SpaceID) ([]DuplicateCandidate, error) {
	return queryAll(ctx, s.db, "store: list open duplicates", scanDuplicate, `
		SELECT `+duplicateColumns+`
		FROM duplicate_candidates d
		JOIN transactions a ON a.id = d.first_txn_id AND NOT a.is_deleted
		JOIN transactions b ON b.id = d.second_txn_id AND NOT b.is_deleted
		WHERE d.space_id = $1 AND d.verdict IS NULL
		ORDER BY least(a.date, b.date), d.created_at, d.id`, spaceID.UUID())
}

// CountOpenDuplicates is len(ListOpenDuplicates) without the rows.
func (s *Store) CountOpenDuplicates(ctx context.Context, spaceID SpaceID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT count(*)
		FROM duplicate_candidates d
		JOIN transactions a ON a.id = d.first_txn_id AND NOT a.is_deleted
		JOIN transactions b ON b.id = d.second_txn_id AND NOT b.is_deleted
		WHERE d.space_id = $1 AND d.verdict IS NULL`, spaceID.UUID()).Scan(&count)
	return count, wrap("store: count open duplicates", err)
}

func (s *Store) GetDuplicate(ctx context.Context, spaceID SpaceID, id uuid.UUID) (DuplicateCandidate, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+duplicateColumns+` FROM duplicate_candidates d
		WHERE d.space_id = $1 AND d.id = $2`, spaceID.UUID(), id)
	one, err := scanDuplicate(row)
	return one, wrap("store: get duplicate", err)
}

// DecideDuplicate records the verdict on a pair still open. It reports
// ErrNotFound for a pair already decided, which a stale screen can ask for.
func (s *Store) DecideDuplicate(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, verdict string, keptID, decidedBy uuid.UUID,
) error {
	var kept, by *uuid.UUID
	if keptID != uuid.Nil {
		kept = &keptID
	}
	if decidedBy != uuid.Nil {
		by = &decidedBy
	}
	return s.execOne(ctx, "store: decide duplicate", `
		UPDATE duplicate_candidates
		SET verdict = $3, kept_txn_id = $4, decided_by = $5, decided_at = now()
		WHERE space_id = $1 AND id = $2 AND verdict IS NULL`,
		spaceID.UUID(), id, verdict, kept, by)
}

// AdoptExternalID gives a row the aggregator's id that RetireDuplicate cleared
// from its twin, so the next sync settles the survivor instead of writing the
// charge again. A row that already has an id keeps it.
func (s *Store) AdoptExternalID(ctx context.Context, spaceID SpaceID, id uuid.UUID, externalID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE transactions SET external_id = $3, updated_at = now()
		WHERE space_id = $1 AND id = $2 AND external_id IS NULL`,
		spaceID.UUID(), id, externalID)
	return wrap("store: adopt external id", err)
}
