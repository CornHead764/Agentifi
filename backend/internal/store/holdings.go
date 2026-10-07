package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
)

// Holdings and the securities that price them.
//
// Every figure that can be unknown is a value plus a Has flag rather than a
// zero: an unquoted security is not priced at nothing, and missing lots are an
// unknown basis, not zero. calculations.md §4 turns on that distinction.

type Security struct {
	ID       uuid.UUID
	Symbol   string
	Name     string
	Kind     string
	Exchange string
	Currency string

	LastPrice     domain.Rate
	HasLastPrice  bool
	PriorClose    domain.Rate
	HasPriorClose bool
	LastPriceAt   *time.Time
}

type Holding struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	SecurityID uuid.UUID

	Shares domain.Rate

	CostBasis    domain.Money
	HasCostBasis bool
	AverageCost  domain.Rate
	HasAverage   bool
	// IsComplete is the stored verdict. A row that claims completeness with no
	// price at all is still incomplete: the claim is not the figure.
	IsComplete bool

	// MarketValue is the provider's own valuation, all there is for a security
	// with no public quote.
	MarketValue    domain.Money
	HasMarketValue bool
}

// DomainHolding builds the holding the calculations read. The schema stores an
// average cost, not lots, so the position is one lot covering every share; an
// unknown basis leaves the lot unpriced so domain.CostBasis reports false
// rather than a plausible zero.
func DomainHolding(h Holding) domain.Holding {
	lot := domain.Lot{Shares: h.Shares}
	switch {
	case !h.IsComplete:
	case h.HasAverage:
		lot.CostPerShare, lot.HasCostPerShare = h.AverageCost, true
	case h.HasCostBasis && !h.Shares.IsZero():
		lot.CostPerShare = h.CostBasis.Decimal().Div(h.Shares)
		lot.HasCostPerShare = true
	}
	return domain.Holding{
		ID:             domainID(h.ID),
		AccountID:      domainID(h.AccountID),
		SecurityID:     domainID(h.SecurityID),
		Shares:         h.Shares,
		Lots:           []domain.Lot{lot},
		MarketValue:    h.MarketValue,
		HasMarketValue: h.HasMarketValue,
	}
}

const holdingColumns = `id, account_id, security_id, shares, cost_basis, average_cost,
	is_cost_basis_complete, market_value`

const securityColumns = `id, symbol, name, kind, exchange, currency,
	last_price, prior_close, last_price_at`

// ListHoldings is the positions in this space, narrowed to accounts when given.
// An empty list of ids is every account, so a caller that means "none" must
// not call.
func (s *Store) ListHoldings(
	ctx context.Context, spaceID SpaceID, accountIDs []uuid.UUID,
) ([]Holding, error) {
	sql := `SELECT ` + holdingColumns + ` FROM holdings WHERE space_id = $1`
	args := []any{spaceID.UUID()}
	if len(accountIDs) > 0 {
		sql += ` AND account_id = ANY($2)`
		args = append(args, accountIDs)
	}
	sql += ` ORDER BY id`

	return queryAll(ctx, s.db, "store: list holdings", scanHolding, sql, args...)
}

func (s *Store) ListSecurities(ctx context.Context, spaceID SpaceID) ([]Security, error) {
	return queryAll(ctx, s.db, "store: list securities", scanSecurity,
		`SELECT `+securityColumns+` FROM securities
		 WHERE space_id = $1 AND NOT is_deleted
		 ORDER BY symbol`, spaceID.UUID())
}

// GetSecurityBySymbol resolves a symbol this space already knows
// (`uq_security_space_symbol` makes it the identity a new holding attaches to).
func (s *Store) GetSecurityBySymbol(
	ctx context.Context, spaceID SpaceID, symbol string,
) (Security, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+securityColumns+` FROM securities
		 WHERE space_id = $1 AND symbol = $2 AND NOT is_deleted`,
		spaceID.UUID(), symbol)
	security, err := scanSecurity(row)
	return security, wrap("store: get security", err)
}

func (s *Store) CreateSecurity(ctx context.Context, spaceID SpaceID, sec *Security) error {
	if sec.ID == uuid.Nil {
		sec.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO securities (id, space_id, symbol, name, kind, currency)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		sec.ID, spaceID.UUID(), sec.Symbol, sec.Name, sec.Kind, sec.Currency)
	return wrap("store: create security", err)
}

// SetSecurityPrice stores a fresh quote. Only last_price and its timestamp
// move: this path carries no prior close, and guessing one would corrupt the
// day change.
func (s *Store) SetSecurityPrice(
	ctx context.Context, spaceID SpaceID, securityID uuid.UUID, price domain.Rate, at time.Time,
) error {
	_, err := s.db.Exec(ctx, `
		UPDATE securities SET last_price = $1, last_price_at = $2
		WHERE id = $3 AND space_id = $4`,
		pgconv.Numeric(price), at, securityID, spaceID.UUID())
	return wrap("store: set security price", err)
}

// CreateHolding attaches a position to an account, reporting false when that
// account already holds that security — refused rather than upserted, so no
// share count changes that nobody asked to change.
func (s *Store) CreateHolding(ctx context.Context, spaceID SpaceID, h *Holding) (bool, error) {
	if h.ID == uuid.Nil {
		h.ID = uuid.New()
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO holdings (id, space_id, account_id, security_id, shares, cost_basis,
			is_cost_basis_complete, market_value, as_of)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CURRENT_DATE)
		ON CONFLICT (account_id, security_id) DO NOTHING`,
		h.ID, spaceID.UUID(), h.AccountID, h.SecurityID, pgconv.Numeric(h.Shares),
		pgconv.NullMoney(h.CostBasis, h.HasCostBasis), h.IsComplete,
		pgconv.NullMoney(h.MarketValue, h.HasMarketValue))
	if err != nil {
		return false, wrap("store: create holding", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteHolding hard-deletes a position: a holding is a current share count;
// the history lives in the register's transactions.
func (s *Store) DeleteHolding(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete holding",
		`DELETE FROM holdings WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}

// HeldSymbols is the tickers this space holds, for the news carousel:
//
//   - live positions only — zero shares is a closed position;
//   - public quotes only — an unpriced security is a private fund or an import
//     artefact, and searching news for it returns noise that looks like news;
//   - largest first — the lookup is capped (each ticker is a request against a
//     throttled source), so the cap must drop the smallest positions.
func (s *Store) HeldSymbols(ctx context.Context, spaceID SpaceID) ([]string, error) {
	return queryAll(ctx, s.db, "store: held symbols", scanValue[string], `
		SELECT s.symbol
		  FROM holdings h
		  JOIN accounts a ON a.id = h.account_id
		  JOIN securities s ON s.id = h.security_id
		 WHERE h.space_id = $1 AND NOT a.is_deleted AND NOT s.is_deleted
		   AND h.shares <> 0 AND s.last_price IS NOT NULL AND s.symbol <> ''
		 GROUP BY s.symbol
		 ORDER BY sum(abs(h.shares) * s.last_price) DESC, s.symbol`,
		spaceID.UUID())
}

func (s *Store) GetSecurity(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Security, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+securityColumns+` FROM securities
		 WHERE space_id = $1 AND id = $2 AND NOT is_deleted`, spaceID.UUID(), id)
	security, err := scanSecurity(row)
	return security, wrap("store: get security", err)
}

type SecurityPrice struct {
	On    domain.Date
	Close domain.Rate
}

// RecordSecurityPrices upserts closes, one row per security per day: a later
// reading of the same day wins, so an overlapping backfill is safe to rerun.
func (s *Store) RecordSecurityPrices(
	ctx context.Context, spaceID SpaceID, securityID uuid.UUID, points []SecurityPrice,
) error {
	for _, point := range points {
		_, err := s.db.Exec(ctx, `
			INSERT INTO security_prices (id, space_id, security_id, on_date, close)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (space_id, security_id, on_date) DO UPDATE SET close = EXCLUDED.close`,
			uuid.New(), spaceID.UUID(), securityID, point.On.Time(), pgconv.Numeric(point.Close))
		if err != nil {
			return wrap("store: record security prices", err)
		}
	}
	return nil
}

// ListSecurityPrices is one security's closes over a window, oldest first. A
// zero bound is open-ended on that side.
func (s *Store) ListSecurityPrices(
	ctx context.Context, spaceID SpaceID, securityID uuid.UUID, from, to domain.Date,
) ([]SecurityPrice, error) {
	args := &argList{}
	sql := `SELECT on_date, close FROM security_prices
	         WHERE space_id = ` + args.add(spaceID.UUID()) +
		` AND security_id = ` + args.add(securityID)
	if !from.IsZero() {
		sql += ` AND on_date >= ` + args.add(from.Time())
	}
	if !to.IsZero() {
		sql += ` AND on_date <= ` + args.add(to.Time())
	}
	sql += ` ORDER BY on_date`

	return queryAll(ctx, s.db, "store: list security prices", func(row scanner) (SecurityPrice, error) {
		var (
			out   SecurityPrice
			on    time.Time
			close pgtype.Numeric
		)
		if err := row.Scan(&on, &close); err != nil {
			return SecurityPrice{}, err
		}
		out.On = dateOf(on)
		var err error
		if out.Close, _, err = pgconv.ReadNullDecimal(close, "security_prices.close"); err != nil {
			return SecurityPrice{}, err
		}
		return out, nil
	}, sql, args.values...)
}

func scanHolding(row scanner) (Holding, error) {
	var (
		out                      Holding
		shares, costBasis        pgtype.Numeric
		averageCost, marketValue pgtype.Numeric
		err                      error
	)
	if err = row.Scan(&out.ID, &out.AccountID, &out.SecurityID, &shares, &costBasis,
		&averageCost, &out.IsComplete, &marketValue); err != nil {
		return Holding{}, err
	}
	if out.Shares, _, err = pgconv.ReadNullDecimal(shares, "holdings.shares"); err != nil {
		return Holding{}, err
	}
	if out.CostBasis, out.HasCostBasis, err = pgconv.ReadNullMoney(costBasis, "holdings.cost_basis"); err != nil {
		return Holding{}, err
	}
	if out.AverageCost, out.HasAverage, err = pgconv.ReadNullDecimal(averageCost, "holdings.average_cost"); err != nil {
		return Holding{}, err
	}
	if out.MarketValue, out.HasMarketValue, err = pgconv.ReadNullMoney(marketValue, "holdings.market_value"); err != nil {
		return Holding{}, err
	}
	return out, nil
}

func scanSecurity(row scanner) (Security, error) {
	var (
		out                   Security
		exchange              *string
		lastPrice, priorClose pgtype.Numeric
		err                   error
	)
	if err = row.Scan(&out.ID, &out.Symbol, &out.Name, &out.Kind, &exchange, &out.Currency,
		&lastPrice, &priorClose, &out.LastPriceAt); err != nil {
		return Security{}, err
	}
	out.Exchange = Deref(exchange)
	if out.LastPrice, out.HasLastPrice, err = pgconv.ReadNullDecimal(lastPrice, "securities.last_price"); err != nil {
		return Security{}, err
	}
	if out.PriorClose, out.HasPriorClose, err = pgconv.ReadNullDecimal(priorClose, "securities.prior_close"); err != nil {
		return Security{}, err
	}
	return out, nil
}
