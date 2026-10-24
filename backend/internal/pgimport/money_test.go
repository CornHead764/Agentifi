package pgimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRefundRequiresMatchingOrderCurrency(t *testing.T) {
	text := func(s string) *string { return &s }
	for _, table := range []string{"merchant_refunds", "merchant_charges"} {
		t.Run(table, func(t *testing.T) {
			s := &source{data: map[string][]row{"merchant_orders": {
				{"merchant_account_id": text("account"), "order_number": text("order"), "currency": text("JPY")},
				{"merchant_account_id": text("another-account"), "order_number": text("other"), "currency": text("USD")},
			}}}
			r := row{"id": text("refund-id"), "merchant_account_id": text("account"), "order_number": text("order"), "amount": text("123")}
			got, err := s.convert(&target{primary: "USD"}, table, column{name: "amount", kind: "INTEGER"}, r)
			require.NoError(t, err)
			require.EqualValues(t, 123, got)
			// Same order number in another account must not supply a scale.
			r["order_number"] = text("other")
			got, err = s.convert(&target{primary: "USD"}, table, column{name: "amount", kind: "INTEGER"}, r)
			require.Nil(t, got)
			require.EqualError(t, err, table+" refund-id has an amount without a matching order currency")
			// A nullable amount does not require a denomination.
			r["amount"] = nil
			got, err = s.convert(&target{primary: "USD"}, table, column{name: "amount", kind: "INTEGER"}, r)
			require.NoError(t, err)
			require.Nil(t, got)
		})
	}
}

func TestImportRefusesUnmatchedRefund(t *testing.T) {
	db, options := fixture(t)
	_, err := db.Exec(t.Context(), `
		INSERT INTO merchant_accounts (id, space_id, label)
		SELECT '10000000-0000-0000-0000-000000000001', id, 'Refund account'
		FROM spaces ORDER BY id LIMIT 1;
		INSERT INTO merchant_refunds (id, space_id, merchant, merchant_account_id, order_number, refunded_on, amount)
		SELECT '10000000-0000-0000-0000-000000000002', space_id, 'amazon', id, 'missing-order', '2026-01-01', 12.34
		FROM merchant_accounts WHERE id = '10000000-0000-0000-0000-000000000001';
	`)
	require.NoError(t, err)
	_, err = Import(t.Context(), options)
	require.ErrorContains(t, err, "merchant_refunds 10000000-0000-0000-0000-000000000002 has an amount without a matching order currency")
	_, err = os.Stat(filepath.Join(options.Data, "server.db"))
	require.True(t, os.IsNotExist(err), "refused import published server.db")
}
