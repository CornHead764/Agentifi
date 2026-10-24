package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
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

const holdingColumns = `h.id, h.account_id, h.security_id, h.shares, h.cost_basis, h.average_cost,
	h.is_cost_basis_complete, h.market_value, s.currency`

const securityColumns = `id, symbol, name, kind, exchange, currency,
	last_price, prior_close, last_price_at`

// ListHoldings is the positions in this space, narrowed to accounts when given.
// An empty list of ids is every account, so a caller that means "none" must
// not call.
func (s *Store) ListHoldings(
	ctx context.Context, spaceID SpaceID, accountIDs []uuid.UUID,
) ([]Holding, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + holdingColumns + ` FROM holdings h JOIN securities s ON s.id=h.security_id`
	var args []any
	if len(accountIDs) > 0 {
		marks, values := inArgs(accountIDs)
		query += ` WHERE h.account_id IN ` + marks
		args = values
	}
	query += ` ORDER BY h.id`
	return sqliteQueryAll(ctx, db.Read(), "store: list holdings", scanHolding, query, args...)
}

func (s *Store) ListSecurities(ctx context.Context, spaceID SpaceID) ([]Security, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list securities", scanSecurity,
		`SELECT `+securityColumns+` FROM securities WHERE NOT is_deleted ORDER BY symbol`)
}

// GetSecurityBySymbol resolves a symbol this space already knows
// (`uq_security_space_symbol` makes it the identity a new holding attaches to).
func (s *Store) GetSecurityBySymbol(
	ctx context.Context, spaceID SpaceID, symbol string,
) (Security, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Security{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Security{}, err
	}
	row := db.Read().QueryRowContext(ctx, `SELECT `+securityColumns+` FROM securities WHERE symbol=? AND NOT is_deleted`, symbol)
	security, err := scanSecurity(row)
	return security, wrap("store: get security", err)
}

func (s *Store) CreateSecurity(ctx context.Context, spaceID SpaceID, sec *Security) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if sec.ID == uuid.Nil {
		sec.ID = uuid.New()
	}
	_, err = db.Write().ExecContext(ctx, `INSERT INTO securities (id,symbol,name,kind,currency,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`,
		sec.ID, sec.Symbol, sec.Name, sec.Kind, sec.Currency, db.Now(), db.Now())
	return wrap("store: create security", err)
}

// SetSecurityPrice stores a fresh quote. Only last_price and its timestamp
// move: this path carries no prior close, and guessing one would corrupt the
// day change.
func (s *Store) SetSecurityPrice(
	ctx context.Context, spaceID SpaceID, securityID uuid.UUID, price domain.Rate, at time.Time,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `UPDATE securities SET last_price=?,last_price_at=?,updated_at=? WHERE id=?`,
		price.String(), unixMillis(at), db.Now(), securityID)
	return wrap("store: set security price", err)
}

// CreateHolding attaches a position to an account, reporting false when that
// account already holds that security — refused rather than upserted, so no
// share count changes that nobody asked to change.
func (s *Store) CreateHolding(ctx context.Context, spaceID SpaceID, h *Holding) (bool, error) {
	scoped, err := s.scoped(ctx, spaceID)
	if err != nil {
		return false, err
	}
	db, err := scoped.household(ctx, spaceID.UUID())
	if err != nil {
		return false, err
	}
	var currency string
	if err := db.Read().QueryRowContext(ctx, `SELECT currency FROM securities WHERE id=?`, h.SecurityID).Scan(&currency); err != nil {
		return false, wrap("store: holding currency", err)
	}
	var basis, value any
	if h.HasCostBasis {
		basis, err = toMinor(h.CostBasis.Decimal(), currency)
		if err != nil {
			return false, err
		}
	}
	if h.HasMarketValue {
		value, err = toMinor(h.MarketValue.Decimal(), currency)
		if err != nil {
			return false, err
		}
	}
	if h.ID == uuid.Nil {
		h.ID = uuid.New()
	}
	now := db.Now()
	tag, err := db.Write().ExecContext(ctx, `INSERT INTO holdings (id,account_id,security_id,shares,cost_basis,is_cost_basis_complete,market_value,as_of,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT (account_id,security_id) DO NOTHING`,
		h.ID, h.AccountID, h.SecurityID, h.Shares.String(), basis, h.IsComplete, value, domain.DateOf(fromUnixMillis(now)).String(), now, now)
	if err != nil {
		return false, wrap("store: create holding", err)
	}
	n, err := tag.RowsAffected()
	return n > 0, wrap("store: create holding", err)
}

// DeleteHolding hard-deletes a position: a holding is a current share count;
// the history lives in the register's transactions.
func (s *Store) DeleteHolding(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete holding", `DELETE FROM holdings WHERE id=?`, id)
}

// HeldSymbols is the tickers this space holds, for the news carousel:
//
//   - live positions only — zero shares is a closed position;
//   - public quotes only — an unpriced security is a private fund or an import
//     artefact, and searching news for it returns noise that looks like news;
//   - largest first — the lookup is capped (each ticker is a request against a
//     throttled source), so the cap must drop the smallest positions.
func (s *Store) HeldSymbols(ctx context.Context, spaceID SpaceID) ([]string, error) {
	scoped, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := scoped.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	type position struct {
		symbol        string
		shares, price decimal.Decimal
	}
	rows, err := sqliteQueryAll(ctx, db.Read(), "store: held symbols", func(row scanner) (position, error) {
		var p position
		var shares, price string
		if err := row.Scan(&p.symbol, &shares, &price); err != nil {
			return p, err
		}
		var err error
		p.shares, err = decimal.NewFromString(shares)
		if err != nil {
			return p, err
		}
		p.price, err = decimal.NewFromString(price)
		return p, err
	}, `SELECT s.symbol,h.shares,s.last_price FROM holdings h JOIN accounts a ON a.id=h.account_id
		JOIN securities s ON s.id=h.security_id WHERE NOT a.is_deleted AND NOT s.is_deleted
		AND s.last_price IS NOT NULL AND s.symbol<>''`)
	if err != nil {
		return nil, err
	}
	totals := map[string]decimal.Decimal{}
	for _, p := range rows {
		if !p.shares.IsZero() {
			totals[p.symbol] = totals[p.symbol].Add(p.shares.Abs().Mul(p.price))
		}
	}
	out := make([]string, 0, len(totals))
	for symbol := range totals {
		out = append(out, symbol)
	}
	slices.SortFunc(out, func(a, b string) int {
		if cmp := totals[b].Cmp(totals[a]); cmp != 0 {
			return cmp
		}
		return strings.Compare(a, b)
	})
	return out, nil
}

func (s *Store) GetSecurity(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Security, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return Security{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return Security{}, err
	}
	row := db.Read().QueryRowContext(ctx, `SELECT `+securityColumns+` FROM securities WHERE id=? AND NOT is_deleted`, id)
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
	symbol, err := s.securityPriceSymbol(ctx, spaceID, securityID)
	if err != nil {
		return err
	}
	db, err := s.server()
	if err != nil {
		return err
	}
	for _, point := range points {
		_, err := db.Write().ExecContext(ctx, `
			INSERT INTO security_prices (id, symbol, on_date, close, created_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (symbol, on_date) DO UPDATE SET close = EXCLUDED.close`,
			uuid.New(), symbol, point.On.String(), point.Close.String(), db.Now())
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
	symbol, err := s.securityPriceSymbol(ctx, spaceID, securityID)
	if err != nil {
		return nil, err
	}
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	query := `SELECT on_date, close FROM security_prices WHERE symbol = ?`
	args := []any{symbol}
	if !from.IsZero() {
		query += ` AND on_date >= ?`
		args = append(args, from.String())
	}
	if !to.IsZero() {
		query += ` AND on_date <= ?`
		args = append(args, to.String())
	}
	query += ` ORDER BY on_date`
	return sqliteQueryAll(ctx, db.Read(), "store: list security prices", func(row scanner) (SecurityPrice, error) {
		var on, close string
		if err := row.Scan(&on, &close); err != nil {
			return SecurityPrice{}, err
		}
		date, err := time.Parse("2006-01-02", on)
		if err != nil {
			return SecurityPrice{}, err
		}
		value, err := decimal.NewFromString(close)
		return SecurityPrice{On: dateOf(date), Close: value}, err
	}, query, args...)
}

// Resolve the household reference before opening the global catalogue. Quotes
// must be materialized before a household transaction, never across files in one.
func (s *Store) securityPriceSymbol(ctx context.Context, spaceID SpaceID, id uuid.UUID) (string, error) {
	if s.sqliteInTx {
		return "", errors.New("store: security catalogue access inside transaction")
	}
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return "", err
	}
	var symbol string
	err = db.Read().QueryRowContext(ctx, `SELECT symbol FROM securities WHERE id = ? AND NOT is_deleted`, id).Scan(&symbol)
	return symbol, wrap("store: security price symbol", err)
}

func scanHolding(row scanner) (Holding, error) {
	var out Holding
	var shares, currency string
	var basis, value sql.Null[int64]
	var average sql.Null[string]
	if err := row.Scan(&out.ID, &out.AccountID, &out.SecurityID, &shares, &basis, &average, &out.IsComplete, &value, &currency); err != nil {
		return out, err
	}
	var err error
	out.Shares, err = decimal.NewFromString(shares)
	if err != nil {
		return out, err
	}
	if average.Valid {
		out.AverageCost, err = decimal.NewFromString(average.V)
		if err != nil {
			return out, err
		}
		out.HasAverage = true
	}
	if basis.Valid {
		d, err := fromMinor(basis.V, currency)
		if err != nil {
			return out, err
		}
		out.CostBasis, out.HasCostBasis = domain.FromDecimal(d), true
	}
	if value.Valid {
		d, err := fromMinor(value.V, currency)
		if err != nil {
			return out, err
		}
		out.MarketValue, out.HasMarketValue = domain.FromDecimal(d), true
	}
	return out, nil
}

func scanSecurity(row scanner) (Security, error) {
	var out Security
	var exchange, last, prior sql.Null[string]
	var at sql.Null[int64]
	if err := row.Scan(&out.ID, &out.Symbol, &out.Name, &out.Kind, &exchange, &out.Currency, &last, &prior, &at); err != nil {
		return out, err
	}
	out.Exchange, out.LastPriceAt = exchange.V, nullableTime(at)
	var err error
	if last.Valid {
		out.LastPrice, err = decimal.NewFromString(last.V)
		if err != nil {
			return out, err
		}
		out.HasLastPrice = true
	}
	if prior.Valid {
		out.PriorClose, err = decimal.NewFromString(prior.V)
		if err != nil {
			return out, err
		}
		out.HasPriorClose = true
	}
	return out, nil
}
