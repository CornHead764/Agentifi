package store

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/migrations"
)

func chargeDays(t *testing.T, space SpaceID) []string {
	t.Helper()
	found, err := db(t).MerchantChargesBetween(t.Context(), space, domain.MerchantAmazon,
		domain.NewDate(2026, time.October, 1), domain.NewDate(2026, time.October, 31))
	require.NoError(t, err)
	var out []string
	for _, one := range found {
		out = append(out, one.ChargedOn.String()+" "+one.Amount.String())
	}
	return out
}

func TestTheSameChargeReadUnderAShiftedDateMovesInsteadOfRepeating(t *testing.T) {
	space := newSpace(t)
	account := MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, db(t).CreateMerchantAccount(t.Context(), space, &account))
	upsert := func(day int, amount string) bool {
		t.Helper()
		added, err := db(t).UpsertMerchantCharge(t.Context(), space, &MerchantCharge{
			Merchant: domain.MerchantAmazon, MerchantAccountID: account.ID, OrderNumber: "111-0000000-0000001",
			ChargedOn: domain.NewDate(2026, time.October, day), Amount: domain.MustFromString(amount),
		})
		require.NoError(t, err)
		return added
	}

	require.True(t, upsert(11, "-16.00"))
	require.False(t, upsert(11, "-16.00"), "the same day is the same charge")
	require.False(t, upsert(12, "-16.00"), "a day later is the same charge moved")
	require.Equal(t, []string{"2026-10-12 -16.00"}, chargeDays(t, space))

	require.True(t, upsert(12, "-12.00"), "another amount is another charge")
	require.True(t, upsert(20, "-16.00"), "a week later is another charge")
	require.Equal(t, []string{"2026-10-12 -16.00", "2026-10-12 -12.00", "2026-10-20 -16.00"}, chargeDays(t, space))
}

func TestMigrationTwelveKeepsTheLaterOfChargesReadUnderShiftedDates(t *testing.T) {
	ctx := t.Context()
	file, err := migrations.FS.ReadFile("00012_merchant_charge_date_twins.sql")
	require.NoError(t, err)
	up, _, ok := strings.Cut(string(file), "-- +goose Down")
	require.True(t, ok, "the migration has a Down")
	_, up, ok = strings.Cut(up, "-- +goose Up")
	require.True(t, ok, "the migration has an Up")

	space := newSpace(t)
	account := MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, db(t).CreateMerchantAccount(ctx, space, &account))

	tx, err := db(t).pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, row := range []struct{ order, day, amount string }{
		{"111-0000000-0000001", "2026-10-11", "-16.00"},
		{"111-0000000-0000001", "2026-10-12", "-16.00"},
		{"111-0000000-0000001", "2026-10-12", "-12.00"},
		{"111-0000000-0000001", "2026-10-25", "-16.00"},
		{"111-0000000-0000002", "2026-10-11", "-16.00"},
	} {
		_, err = tx.Exec(ctx,
			`INSERT INTO merchant_charges (id, space_id, merchant_account_id, order_number, charged_on, amount, instrument, merchant)
			 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, '', 'amazon')`,
			space.UUID(), account.ID, row.order, row.day, row.amount)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, up)
	require.NoError(t, err)

	rows, err := tx.Query(ctx,
		`SELECT order_number, charged_on::text, amount::text FROM merchant_charges
		  WHERE space_id = $1 ORDER BY order_number, charged_on, amount`, space.UUID())
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var order, day, amount string
		require.NoError(t, rows.Scan(&order, &day, &amount))
		got = append(got, order[len(order)-1:]+" "+day+" "+amount)
	}
	require.Equal(t, []string{
		"1 2026-10-12 -12.00", "1 2026-10-12 -16.00", "1 2026-10-25 -16.00", "2 2026-10-11 -16.00",
	}, got)
}
