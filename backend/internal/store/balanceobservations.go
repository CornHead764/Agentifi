package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// What each sync saw of a connected account (domain.BalanceObservation),
// kept for the pending reading and the ledger drift check (calculations.md
// §3). Amounts are in the account's currency.

// RecordBalanceObservation adds one sync's observation of an account.
func (s *Store) RecordBalanceObservation(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, observation domain.BalanceObservation,
) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	var currency string
	if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id = ?`, accountID).Scan(&currency); err != nil {
		return wrap("store: observation account currency", err)
	}
	amounts := make([]any, 0, 5)
	for _, amount := range []domain.Money{
		observation.Reported, observation.Ledger, observation.Settled,
		observation.Movement, observation.SettledMovement,
	} {
		minor, err := toMinor(amount.Decimal(), currency)
		if err != nil {
			return wrap("store: observation amount", err)
		}
		amounts = append(amounts, minor)
	}
	return sqliteExecOne(ctx, db.Write(), "store: record balance observation", `
		INSERT INTO balance_observations
			(id, account_id, observed_at, reported, ledger_total, settled_total, movement, settled_movement)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		append([]any{uuid.New(), accountID, unixMillis(observation.At)}, amounts...)...)
}

// ListBalanceObservations is one account's observations, oldest first.
func (s *Store) ListBalanceObservations(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID,
) ([]domain.BalanceObservation, error) {
	byAccount, err := s.balanceObservations(ctx, spaceID, &accountID)
	if err != nil {
		return nil, err
	}
	return byAccount[accountID], nil
}

// BalanceObservationsByAccount is every account's observations, each oldest
// first.
func (s *Store) BalanceObservationsByAccount(
	ctx context.Context, spaceID SpaceID,
) (map[uuid.UUID][]domain.BalanceObservation, error) {
	return s.balanceObservations(ctx, spaceID, nil)
}

func (s *Store) balanceObservations(
	ctx context.Context, spaceID SpaceID, accountID *uuid.UUID,
) (map[uuid.UUID][]domain.BalanceObservation, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	var filter any
	if accountID != nil {
		filter = *accountID
	}
	type row struct {
		accountID   uuid.UUID
		observation domain.BalanceObservation
	}
	rows, err := sqliteQueryAll(ctx, db.Read(), "store: list balance observations", func(scan scanner) (row, error) {
		var out row
		var at int64
		var currency string
		minor := make([]int64, 5)
		if err := scan.Scan(&out.accountID, &at, &minor[0], &minor[1], &minor[2], &minor[3], &minor[4], &currency); err != nil {
			return row{}, err
		}
		amounts := make([]domain.Money, len(minor))
		for i, value := range minor {
			parsed, err := fromMinor(value, currency)
			if err != nil {
				return row{}, err
			}
			amounts[i] = domain.FromDecimal(parsed)
		}
		out.observation = domain.BalanceObservation{
			At: fromUnixMillis(at), Reported: amounts[0], Ledger: amounts[1], Settled: amounts[2],
			Movement: amounts[3], SettledMovement: amounts[4],
		}
		return out, nil
	}, `SELECT o.account_id, o.observed_at, o.reported, o.ledger_total, o.settled_total,
			o.movement, o.settled_movement, a.currency
		FROM balance_observations o JOIN accounts a ON a.id = o.account_id
		WHERE (? IS NULL OR o.account_id = ?) ORDER BY o.account_id, o.observed_at, o.id`, filter, filter)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]domain.BalanceObservation{}
	for _, one := range rows {
		out[one.accountID] = append(out[one.accountID], one.observation)
	}
	return out, nil
}

// SetAccountPendingReading writes the account's reading of its provider
// figure (domain.PendingReading), and nothing else.
func (s *Store) SetAccountPendingReading(
	ctx context.Context, spaceID SpaceID, accountID uuid.UUID, includesPending bool,
) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: set account pending reading",
		`UPDATE accounts SET bank_includes_pending = ?, updated_at = ? WHERE id = ?`,
		includesPending, db.Now(), accountID)
}
