package pgimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantChargesAndRefundsUsePrimaryScale(t *testing.T) {
	text := func(s string) *string { return &s }
	for _, table := range []string{"merchant_refunds", "merchant_charges"} {
		t.Run(table, func(t *testing.T) {
			s := &source{data: map[string][]row{"merchant_orders": {
				{"merchant_account_id": text("account"), "order_number": text("order"), "currency": text("JPY")},
			}}}
			r := row{"id": text("refund-id"), "merchant_account_id": text("account"), "order_number": text("order"), "amount": text("123")}
			got, err := s.convert(&target{primary: "USD"}, table, column{name: "amount", kind: "INTEGER"}, r)
			require.NoError(t, err)
			require.EqualValues(t, 12300, got, "the store reads this column at the household's primary scale")
			r["order_number"] = text("no-such-order")
			got, err = s.convert(&target{primary: "USD"}, table, column{name: "amount", kind: "INTEGER"}, r)
			require.NoError(t, err)
			require.EqualValues(t, 12300, got, "a charge or refund seen before its order still imports")
		})
	}
}

func TestImportKeepsUnmatchedRefundAndCharge(t *testing.T) {
	db, options := fixture(t)
	_, err := db.Exec(t.Context(), `
		INSERT INTO merchant_accounts (id, space_id, label)
		SELECT '10000000-0000-0000-0000-000000000001', id, 'Refund account'
		FROM spaces ORDER BY id LIMIT 1;
		INSERT INTO merchant_refunds (id, space_id, merchant, merchant_account_id, order_number, refunded_on, amount)
		SELECT '10000000-0000-0000-0000-000000000002', space_id, 'amazon', id, 'missing-order', '2026-01-01', 12.34
		FROM merchant_accounts WHERE id = '10000000-0000-0000-0000-000000000001';
		INSERT INTO merchant_charges (id, space_id, merchant, merchant_account_id, order_number, charged_on, amount)
		SELECT '10000000-0000-0000-0000-000000000003', space_id, 'amazon', id, 'missing-order', '2026-01-01', 56.78
		FROM merchant_accounts WHERE id = '10000000-0000-0000-0000-000000000001';
	`)
	require.NoError(t, err)
	_, err = Import(t.Context(), options)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(options.Data, "server.db"))
	require.NoError(t, err)
}

func TestMoneyColumnsUseTheStoresScale(t *testing.T) {
	text := func(s string) *string { return &s }
	s := &source{
		data: map[string][]row{},
		byID: map[string]map[string]row{
			"merchant_orders": {"order": {"currency": text("JPY")}},
			"accounts":        {"account": {"currency": text("USD")}},
		},
	}
	for _, tc := range []struct {
		table string
		r     row
		want  int64
	}{
		{"merchant_matches", row{"id": text("m"), "order_id": text("order"), "amount": text("5")}, 500},
		{"merchant_accounts", row{"id": text("a"), "gift_card_balance": text("5")}, 500},
		{"transactions", row{"id": text("t"), "account_id": text("account"), "currency": text("JPY"), "balance": text("5")}, 5},
	} {
		col := "amount"
		if tc.table == "merchant_accounts" {
			col = "gift_card_balance"
		} else if tc.table == "transactions" {
			col = "balance"
		}
		got, err := s.convert(&target{primary: "USD"}, tc.table, column{name: col, kind: "INTEGER"}, tc.r)
		require.NoError(t, err, tc.table)
		require.EqualValues(t, tc.want, got, tc.table+"."+col)
	}
}
