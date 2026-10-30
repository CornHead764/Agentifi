package seed

import (
	"context"
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func seedCore(ctx context.Context, h *household) error {
	st, sid := h.st, h.space.ID
	institution, err := st.EnsureInstitution(ctx, sid, "Seed Community Bank", "https://example.invalid/bank.svg")
	if err != nil {
		return err
	}
	conn := store.Connection{Name: "Invented bank feed", Status: store.ConnectionActive}
	if err := st.CreateConnection(ctx, sid, &conn, "https://seed:NOT-A-SECRET@bank.example.invalid/simplefin"); err != nil {
		return err
	}
	if err := st.IgnoreRemoteAccount(ctx, sid, &store.IgnoredRemoteAccount{ConnectionID: conn.ID, ExternalID: "brokerage-ignored-009", Name: "Courtage Société Générale", Institution: "Invented Brokerage", MaskedNumber: "••0009"}); err != nil {
		return err
	}
	kinds := []domain.AccountKind{domain.KindCash, domain.KindCash, domain.KindCreditCard, domain.KindLoan, domain.KindInvestment, domain.KindAsset, domain.KindCash, domain.KindCash, domain.KindCash, domain.KindCash, domain.KindCash}
	names := []string{"Everyday checking", "Emergency savings", "Travel credit card", "Home loan", "Long-term investments", "Family home", "Closed old checking", "Hidden reserve", "Tokyo wallet JPY", "Kuwait wallet KWD", "Archived account"}
	for i, kind := range kinds {
		a := store.Account{Name: names[i], Kind: kind, Type: string(kind), Currency: h.space.PrimaryCurrency, IncludeInNetWorth: true, InstitutionID: institution, OpeningBalance: money("1000"), OpeningBalanceOn: h.date(-23, 0), SortOrder: i, IsClosed: i == 6, ExcludedFromAccountBar: i == 7, IsDeleted: i == 10}
		if i == 0 {
			a.ConnectionID = conn.ID
			a.ExternalID = "checking-001"
		}
		if i == 6 {
			a.ClosedOn = h.date(-2, 0)
		}
		if i == 8 {
			a.Currency = "JPY"
		}
		if i == 9 {
			a.Currency = "KWD"
		}
		if i == 2 {
			a.CreditLimit = money("15000")
			a.HasCreditLimit = true
		}
		if kind == domain.KindLoan {
			a.OpeningBalance = money("-240000")
		}
		if kind == domain.KindAsset {
			a.OpeningBalance = money("350000")
		}
		if err := st.CreateAccount(ctx, sid, &a); err != nil {
			return err
		}
		h.accounts = append(h.accounts, a)
	}
	// Edge catalogue: parent/child categories, nullable tax/report grouping keys,
	// archived rows, and tags whose colors are NULL as well as explicitly set.
	for i, name := range []string{"Living expenses", "Groceries", "Restaurants & cafés", "Salary", "Archived hobbies"} {
		c := store.Category{Name: name, Kind: domain.CategoryExpense, IsEditable: true, IsUserAssignable: true, SortOrder: i, IsDeleted: i == 4}
		if i == 1 || i == 2 {
			c.ParentID = h.categories[0].ID
		}
		if i == 3 {
			c.Kind = domain.CategoryIncome
		}
		if err := st.CreateCategory(ctx, sid, &c); err != nil {
			return err
		}
		h.categories = append(h.categories, c)
	}
	h.tag = store.Tag{Name: "Shared 🏡", Color: "#507060"}
	if err := st.CreateTag(ctx, sid, &h.tag); err != nil {
		return err
	}
	archived := store.Tag{Name: "Archived travel", IsDeleted: true}
	if err := st.CreateTag(ctx, sid, &archived); err != nil {
		return err
	}
	h.filter = store.Filter{Name: "Groceries and cafés", Scope: "saved", Items: []store.FilterItem{{Field: "payee", Operator: "contains", Text: "café"}}}
	if err := st.CreateFilter(ctx, sid, &h.filter); err != nil {
		return err
	}
	rule := store.Rule{Name: "Recognize café statements", FilterID: h.filter.ID, IsActive: true, SetCategoryID: h.categories[2].ID, SetPayee: "Café du Marché", AddTagIDs: []uuid.UUID{h.tag.ID}}
	if err := st.CreateRule(ctx, sid, &rule); err != nil {
		return err
	}
	payees := []string{"Café du Marché", "CAFÉ DU MARCHÉ", "Straße Markt", "STRASSE MARKT", "İstanbul Bakery", "istanbul bakery", "Lunch 🍜", "City Water", "Payroll", "Amazon", "Rent", ""}
	h.txns = make([]store.Transaction, h.transactions)
	for i := range h.txns {
		// Edge catalogue: mixed case, accents, sharp-s, dotted-I, emoji, empty
		// display payees, NULL category/effective date/conversion/balance/series.
		t := store.Transaction{AccountID: h.accounts[i%3].ID, Date: domain.DateOf(h.today.AddDate(0, 0, -h.rng.IntN(730))), Amount: domain.FromDecimal(decimal.New(-100-h.rng.Int64N(35000), -2)), Currency: h.space.PrimaryCurrency, Payee: payees[h.rng.IntN(len(payees))], Source: domain.SourceSync, IsReviewed: i%4 == 0, IsPending: i%29 == 0, IsDeleted: i%197 == 196}
		t.StatementName = t.Payee + " / SEED"
		if i%5 != 0 {
			t.CategoryID = h.categories[1+i%2].ID
		}
		if i%13 == 0 {
			t.Amount = money("4200")
			t.Payee = "Payroll"
			t.CategoryID = h.categories[3].ID
		}
		if i%11 == 0 {
			t.EffectiveDate = domain.DateOf(t.Date.Time().AddDate(0, 0, 2))
		}
		switch i {
		case 0:
			t.Date = domain.DateOf(h.today.AddDate(0, -24, 0))
			t.Payee = "Before account opening"
			t.IsPending = false
		case 1:
			t.Date = domain.DateOf(h.today.AddDate(0, 0, -h.today.Day()+1))
			t.Payee = "Month boundary"
		case 2:
			t.Date = domain.DateOf(h.today.AddDate(-1, 0, 0))
			t.Date = domain.DateOf(t.Date.Time().AddDate(0, 1-int(t.Date.Time().Month()), 1-t.Date.Time().Day()))
			t.Payee = "Year boundary"
		case 3:
			t.Date = domain.DateOf(mustDate("2024-02-29"))
			t.Payee = "Leap day 2024"
		case 4, 6:
			t.AccountID = h.accounts[0].ID
			t.Amount = money("-125")
			t.Payee = "Transfer to savings"
			t.CategoryID = uuid.Nil
			t.IsPending = false
			t.Date = h.date(0, -10-i)
		case 5, 7:
			t.AccountID = h.accounts[1].ID
			t.Amount = money("125")
			t.Payee = "Transfer from checking"
			t.CategoryID = uuid.Nil
			t.IsPending = false
			t.Date = h.date(0, -9-i)
		case 8:
			t.Amount = money("-19.99")
			t.Payee = "Exact split groceries"
			t.Splits = []store.Split{{Position: 0, Amount: money("-12.34"), CategoryID: h.categories[1].ID, TagIDs: []uuid.UUID{h.tag.ID}}, {Position: 1, Amount: money("-7.65"), CategoryID: h.categories[2].ID}}
			t.TagIDs = []uuid.UUID{h.tag.ID}
		case 9:
			t.Amount = money("-49.95")
			t.Payee = "Refunded seed purchase"
			t.IsPending = false
		case 10:
			t.Amount = money("49.95")
			t.Payee = "Seed purchase refund"
			t.IsPending = false
		case 11, 12:
			t.Amount = money("-17.45")
			t.AccountID = h.accounts[0].ID
			t.Date = h.date(0, -7)
			t.Payee = "Duplicate candidate café"
			t.StatementName = "DUPLICATE CAFE"
			t.IsPending = false
		case 13:
			t.Amount = money("9999999999999.98")
			t.Payee = "Numeric limit windfall"
			t.ExcludedFromReports = true
			t.ExcludedFromSpendingPlan = true
		case 14:
			t.Amount = domain.Zero
			t.Payee = "Zero value verification"
		case 15:
			t.AccountID = h.accounts[8].ID
			t.Currency = "JPY"
			t.Amount = money("-2500")
			t.AmountPrimary = money("-15.75")
			t.HasAmountPrimary = true
			t.FxRateUsed = decimal.RequireFromString("0.0063")
			t.HasFxRateUsed = true
		case 16:
			t.AccountID = h.accounts[9].ID
			t.Currency = "KWD"
			t.Amount = money("-12.345")
			t.AmountPrimary = money("-40.11")
			t.HasAmountPrimary = true
			t.FxRateUsed = decimal.RequireFromString("3.2501234567")
			t.HasFxRateUsed = true
		}
		if err := st.CreateTransaction(ctx, sid, &t); err != nil {
			return fmt.Errorf("transaction %d: %w", i, err)
		}
		h.txns[i] = t
	}
	// Edge catalogue: automatic and manual transfer pairs, the pairing-owned
	// category, and a category explicitly changed after pairing.
	for i := 4; i <= 6; i += 2 {
		if _, ok, err := st.PairTransactions(ctx, sid, h.txns[i].ID, h.txns[i+1].ID, i == 6); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("transfer pair %d was refused", i)
		}
	}
	changed, err := st.GetTransaction(ctx, sid, h.txns[6].ID)
	if err != nil {
		return err
	}
	changed.CategoryID = h.categories[2].ID
	if err := st.UpdateTransaction(ctx, sid, &changed); err != nil {
		return err
	}
	if err := st.LinkRefund(ctx, sid, h.txns[10].ID, h.txns[9].ID); err != nil {
		return err
	}
	first, second := h.txns[11].ID, h.txns[12].ID
	if second.String() < first.String() { // duplicate_candidates_ordered: first_txn_id < second_txn_id
		first, second = second, first
	}
	pairs := []domain.DuplicateCandidate{{First: did(first), Second: did(second), Keep: did(first), DaysApart: 0}}
	if _, err := st.SaveDuplicateCandidates(ctx, sid, h.accounts[0].ID, pairs); err != nil {
		return err
	}
	if _, err := service.NewRules(st).RunRules(ctx, sid, []uuid.UUID{h.txns[17].ID}); err != nil {
		return err
	}
	if _, err := (service.Ingest{}).AfterIngest(ctx, st, sid, nil); err != nil {
		return err
	}
	goal := store.Goal{Name: "Family holiday 🏖", AccountID: h.accounts[1].ID, TargetAmount: money("8000"), TargetOn: h.date(6, 0), IsTakenFromPlan: true, TxnIDs: []uuid.UUID{h.txns[5].ID}, FundingAccountIDs: []uuid.UUID{h.accounts[0].ID, h.accounts[1].ID}}
	if err := st.CreateGoal(ctx, sid, &goal); err != nil {
		return err
	}
	if err := st.CreateWatchlist(ctx, sid, &store.Watchlist{Name: "Cafés", FilterID: h.filter.ID, Period: "monthly", HasTarget: true, TargetAmount: money("150")}); err != nil {
		return err
	}
	// Edge catalogue: 10-decimal fractional shares, huge share counts and
	// unknown market value/cost basis coexist with quoted securities.
	for i, shares := range []string{"0.0000000001", "9999999999.9999999999"} {
		// A symbol denotes one denomination globally, even across households.
		sec := store.Security{Symbol: fmt.Sprintf("SEED%d-%s", i+1, h.space.PrimaryCurrency), Name: fmt.Sprintf("Invented fund %d", i+1), Kind: "stock", Currency: h.space.PrimaryCurrency}
		if err := st.CreateSecurity(ctx, sid, &sec); err != nil {
			return err
		}
		if i == 0 {
			if err := st.SetSecurityPrice(ctx, sid, sec.ID, decimal.RequireFromString("123.4567890123"), h.today); err != nil {
				return err
			}
		}
		if _, err := st.CreateHolding(ctx, sid, &store.Holding{AccountID: h.accounts[4].ID, SecurityID: sec.ID, Shares: decimal.RequireFromString(shares)}); err != nil {
			return err
		}
		h.securities = append(h.securities, sec)
	}
	snapshots := make([]store.BalanceSnapshot, 0, 24*len(h.accounts))
	for m := -23; m <= 0; m++ {
		for _, a := range h.accounts {
			step := domain.FromDecimal(decimal.New(int64(m+23)*1371, -2))
			if a.Currency == "JPY" { // yen has no minor unit; 13.71 JPY would be refused by the import
				step = domain.FromDecimal(decimal.New(int64(m+23)*1300, -2))
			}
			snapshots = append(snapshots, store.BalanceSnapshot{AccountID: a.ID, AsOf: h.date(m, 0), Balance: a.OpeningBalance.Add(step)})
		}
	}
	return st.UpsertBalanceSnapshots(ctx, sid, snapshots)
}

// Global catalogues cannot be accessed from the household transaction.
func seedCatalog(ctx context.Context, h *household) error {
	st, sid := h.st, h.space.ID
	for _, sec := range h.securities {
		if err := st.RecordSecurityPrices(ctx, sid, sec.ID, []store.SecurityPrice{{On: h.date(0, -1), Close: decimal.RequireFromString("123.4567890123")}, {On: h.date(0, 0), Close: decimal.RequireFromString("124.0000000001")}}); err != nil {
			return err
		}
		server, err := st.ServerDB()
		if err != nil {
			return err
		}
		if _, err := server.Write().ExecContext(ctx, "UPDATE security_prices SET created_at = ? WHERE symbol = ?", h.today.UnixMilli(), sec.Symbol); err != nil {
			return err
		}
	}
	rates := []provider.FxRate{}
	for _, r := range []struct{ base, quote, value string }{{"USD", "EUR", "0.9234567891"}, {"USD", "JPY", "160.1234567890"}, {"USD", "KWD", "0.3061234567"}, {"IRR", "KWD", "0.0000000001"}} {
		rates = append(rates, provider.FxRate{SpaceID: domain.ID(sid.String()), BaseCurrency: r.base, QuoteCurrency: r.quote, On: h.date(0, 0), Rate: decimal.RequireFromString(r.value), Source: "seed"})
	}
	return store.NewFxRates(st).UpsertRates(ctx, rates)
}
