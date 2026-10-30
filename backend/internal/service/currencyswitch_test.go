package service

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

// A merchant's charges, refunds, matches and gift-card balance are written at
// the primary currency's scale, so a switch to another scale rescales them or
// is refused, like every other primary column.
func TestAPrimarySwitchRescalesTheMerchantFigures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		amount, want int64
		refused      bool
	}{
		{"whole dollars", 1200, 12, false},
		{"cents", 1250, 1250, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.New(t)
			id := storetest.NewHousehold(t, st)
			db, err := st.HouseholdDB(t.Context(), id)
			require.NoError(t, err)
			_, err = db.Write().ExecContext(t.Context(), `INSERT INTO merchant_accounts (id, label, created_at, updated_at, gift_card_balance) VALUES ('shop', 'Example Shop', 1, 1, ?)`, tc.amount)
			require.NoError(t, err)
			_, err = db.Write().ExecContext(t.Context(), `INSERT INTO merchant_charges (id, merchant_account_id, order_number, charged_on, amount, created_at) VALUES ('charge', 'shop', 'A-1', '2026-01-01', ?, 1)`, tc.amount)
			require.NoError(t, err)
			err = NewCurrency(st, &provider.FxRates{Store: store.NewFxRates(st)}).SwitchPrimaryCurrency(t.Context(), id, "JPY")
			if tc.refused {
				var refusal *store.CurrencyRescaleError
				require.ErrorAs(t, err, &refusal)
			} else {
				require.NoError(t, err)
			}
			var charge, balance int64
			require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT amount FROM merchant_charges WHERE id = 'charge'`).Scan(&charge))
			require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT gift_card_balance FROM merchant_accounts WHERE id = 'shop'`).Scan(&balance))
			require.Equal(t, tc.want, charge)
			require.Equal(t, tc.want, balance)
		})
	}
}

func TestSQLitePrimaryCurrencySwitch(t *testing.T) {
	for _, tc := range []struct {
		name, next   string
		amount, want int64
		refused      bool
	}{
		{"whole USD to JPY", "JPY", 1200, 12, false},
		{"fractional USD to JPY", "JPY", 1234, 1234, true},
		{"USD to KWD", "KWD", 1234, 12340, false},
		{"same exponent", "EUR", 1234, 1234, false},
		{"overflow", "KWD", math.MaxInt64, math.MaxInt64, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.New(t)
			id := storetest.NewHousehold(t, st)
			db, err := st.HouseholdDB(t.Context(), id)
			require.NoError(t, err)
			_, err = db.Write().ExecContext(t.Context(), `INSERT INTO spending_plan_months (id, month, projection_type, projection_window_months, calculated_income_amount, overwritten_spent_amount, created_at, updated_at) VALUES ('plan', '2026-01-01', 'none', 1, ?, ?, 1, 1)`, tc.amount, -1200)
			require.NoError(t, err)
			account := &store.Account{Name: "Cash", Kind: domain.KindCash, Type: "checking", Currency: "USD"}
			require.NoError(t, st.CreateAccount(t.Context(), id, account))
			on, err := domain.ParseDate("2026-01-01")
			require.NoError(t, err)
			txn := &store.Transaction{AccountID: account.ID, Date: on, EffectiveDate: on, Amount: domain.MustFromString("12"), Currency: "USD", Source: domain.SourceManual}
			require.NoError(t, st.CreateTransaction(t.Context(), id, txn))
			rates := &provider.FxRates{Store: store.NewFxRates(st)}
			require.NoError(t, store.NewFxRates(st).UpsertRates(t.Context(), []provider.FxRate{{SpaceID: domain.ID(id.String()), BaseCurrency: "USD", QuoteCurrency: tc.next, On: on, Rate: rate("1"), Source: "test"}}))
			err = NewCurrency(st, rates).SwitchPrimaryCurrency(t.Context(), id, tc.next)
			wantCurrency := tc.next
			if tc.refused {
				var refusal *store.CurrencyRescaleError
				require.ErrorAs(t, err, &refusal)
				require.Equal(t, "spending_plan_months", refusal.Table)
				require.Equal(t, "calculated_income_amount", refusal.Column)
				require.EqualValues(t, 1, refusal.Count)
				wantCurrency = "USD"
			} else {
				require.NoError(t, err)
			}
			var amount int64
			require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT calculated_income_amount FROM spending_plan_months WHERE id = 'plan'`).Scan(&amount))
			require.Equal(t, tc.want, amount)
			var primary string
			require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT primary_currency FROM household_metadata WHERE id = 1`).Scan(&primary))
			require.Equal(t, wantCurrency, primary)
			mirror, err := st.GetSpace(t.Context(), id)
			require.NoError(t, err)
			require.Equal(t, wantCurrency, mirror.PrimaryCurrency)
			got, err := st.GetTransaction(t.Context(), id, txn.ID)
			require.NoError(t, err)
			if tc.refused {
				require.False(t, got.HasAmountPrimary)
			} else {
				require.True(t, got.HasAmountPrimary)
				require.True(t, got.AmountPrimary.Equal(domain.MustFromString("12")))
			}
		})
	}
}
