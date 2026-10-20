// Package seed writes an entirely invented, deterministic development dataset
// through the same store and service entry points as the application.
package seed

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const DefaultSeed int64 = 1
const DefaultHouseholds = 3
const DefaultTransactions = 2000
const DefaultToday = "2026-06-30"

// Options describes business values, not database-generated IDs and audit times.
// Storage must persist document bytes; the CLI uses the application's local store.
type Options struct {
	Seed         int64
	Households   int
	Transactions int
	Today        time.Time
	Storage      provider.StorageProvider
}

func DefaultOptions() Options {
	today, _ := time.Parse(time.DateOnly, DefaultToday)
	return Options{Seed: DefaultSeed, Households: DefaultHouseholds, Transactions: DefaultTransactions, Today: today}
}

type household struct {
	st           *store.Store
	space        store.Space
	users        []store.User
	accounts     []store.Account
	categories   []store.Category
	tag          store.Tag
	filter       store.Filter
	txns         []store.Transaction
	today        time.Time
	rng          *rand.Rand
	storage      provider.StorageProvider
	transactions int
}

// Run does not migrate or erase existing data. The command performs the
// non-empty database safety check before calling Run.
func Run(ctx context.Context, st *store.Store, opts Options) error {
	if opts.Seed == 0 {
		opts.Seed = DefaultSeed
	}
	if opts.Households == 0 {
		opts.Households = DefaultHouseholds
	}
	if opts.Transactions == 0 {
		opts.Transactions = DefaultTransactions
	}
	if opts.Today.IsZero() {
		opts.Today, _ = time.Parse(time.DateOnly, DefaultToday)
	}
	if opts.Households < 1 || opts.Transactions < 32 {
		return fmt.Errorf("seed: households must be positive and transactions must be at least 32")
	}
	if opts.Storage == nil {
		return fmt.Errorf("seed: document storage is required")
	}
	rng := rand.New(rand.NewPCG(uint64(opts.Seed), uint64(opts.Seed)^0x9e3779b97f4a7c15))
	today := time.Date(opts.Today.Year(), opts.Today.Month(), opts.Today.Day(), 12, 0, 0, 0, time.UTC)
	users := make([]store.User, 4)
	for i, name := range []string{"Alex Morgan", "Sam Müller", "İpek Şahin", "Zoé Chen"} {
		email := fmt.Sprintf("seed.%d@example.invalid", i+1)
		existing, err := st.GetUserByEmail(ctx, email)
		if err == nil {
			users[i] = existing
			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		users[i] = store.User{Email: email, FullName: name, IsActive: true, IsVerified: true, IsSuperuser: i == 0, Locale: "en-US", Theme: "system"}
		if err := st.CreateUser(ctx, &users[i]); err != nil {
			return err
		}
	}
	for i := 0; i < opts.Households; i++ {
		names := []string{"Morgan shared household", "Sam's solo household", "Müller international household"}
		name := names[i%len(names)]
		if i >= len(names) {
			name = fmt.Sprintf("Household %03d", i+1)
		}
		currency := "USD"
		if i%3 == 2 {
			currency = "EUR"
		}
		h := household{st: st, space: store.Space{Name: name, PrimaryCurrency: currency, Timezone: "UTC"}, users: users, today: today, rng: rng, storage: opts.Storage, transactions: opts.Transactions}
		if err := st.InTx(ctx, func(tx *store.Store) error {
			h.st = tx
			if err := tx.CreateSpace(ctx, &h.space); err != nil {
				return err
			}
			if i == 0 {
				if err := seedServer(ctx, tx, users[0], h.space.ID.String(), today); err != nil {
					return fmt.Errorf("seed server: %w", err)
				}
			}
			roles := []store.Role{store.RoleOwner, store.RoleAdmin, store.RoleMember, store.RoleViewer}
			for j, role := range roles {
				if i%3 == 1 && j > 0 {
					break
				}
				member := store.Membership{UserID: users[j].ID, Role: role, InvitedAt: &today, AcceptedAt: &today}
				if err := tx.CreateMembership(ctx, h.space.ID, &member); err != nil {
					return err
				}
			}
			for _, step := range []struct {
				name string
				run  func(context.Context, *household) error
			}{
				{"core", seedCore}, {"bills", seedBills}, {"merchants", seedMerchants}, {"assistant", seedAssistant}, {"plan", seedPlan},
			} {
				if err := step.run(ctx, &h); err != nil {
					return fmt.Errorf("%s: %w", step.name, err)
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
	}
	return nil
}

func (h *household) date(months, days int) domain.Date {
	return domain.DateOf(h.today.AddDate(0, months, days))
}
func money(s string) domain.Money { return domain.MustFromString(s) }
func did(id uuid.UUID) domain.ID  { return domain.ID(id.String()) }

// Counts discovers tables rather than keeping an inventory that could silently
// miss a newly added migration. It also works in testdb's private search_path.
func Counts(ctx context.Context, st *store.Store) (map[string]int64, error) {
	rows, err := st.Conn().Query(ctx, `SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = current_schema() AND tablename <> 'goose_db_version' ORDER BY tablename`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(names))
	for _, name := range names {
		var n int64
		if err := st.Conn().QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{name}.Sanitize()).Scan(&n); err != nil {
			return nil, err
		}
		counts[name] = n
	}
	return counts, nil
}
