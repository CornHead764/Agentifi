package store_test

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteBills(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	c := store.BillConnection{Biller: domain.BillerID("test"), Label: "Home", CredentialSource: "typed", AutopayRule: "none", PullEnabled: true}
	require.NoError(t, st.CreateBillConnection(ctx, a, &c))
	got, err := st.GetBillConnection(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, a, got.SpaceID)
	require.Equal(t, c.CreatedAt, got.CreatedAt)
	_, err = st.GetBillConnection(ctx, b, c.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	got.Label = "House"
	require.NoError(t, st.UpdateBillConnection(ctx, a, &got))
	require.NoError(t, st.SetBillConnectionSecondFactor(ctx, a, c.ID, "totp"))
	require.NoError(t, st.MarkBillKeepalive(ctx, a, c.ID, time.Now()))
	due, err := st.ListBillConnectionsDue(ctx, time.Now())
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, a, due[0].SpaceID)
	sub := store.BillSubaccount{ConnectionID: c.ID, ExternalID: "home", Label: "House", IsSelected: true}
	require.NoError(t, st.UpsertBillSubaccount(ctx, a, &sub))
	original := sub.ID
	sub.ID = uuid.Nil
	sub.Label = "Renamed"
	require.NoError(t, st.UpsertBillSubaccount(ctx, a, &sub))
	require.Equal(t, original, sub.ID)
	subs, err := st.ListBillSubaccounts(ctx, a, uuid.Nil)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Equal(t, a, subs[0].SpaceID)
	require.NoError(t, st.SetBillSubaccountSelected(ctx, a, sub.ID, false))
	require.NoError(t, st.SetBillSubaccountAccount(ctx, a, sub.ID, uuid.Nil))
	on, err := domain.ParseDate("2026-10-20")
	require.NoError(t, err)
	bill := store.Bill{SubaccountID: sub.ID, DueOn: on, AmountDue: domain.MustFromString("1234"), Currency: "JPY", Status: domain.BillOpen, Source: "test", FetchedAt: time.Now(), HasMinimumDue: true, MinimumDue: domain.MustFromString("100")}
	require.NoError(t, st.UpsertBill(ctx, a, &bill, false))
	require.Equal(t, "1234.00", bill.AmountDue.String())
	require.Equal(t, a, bill.SpaceID)
	require.True(t, bill.HasMinimumDue)
	original = bill.ID
	bill.ID = uuid.Nil
	bill.AmountDue = domain.MustFromString("1250")
	require.NoError(t, st.UpsertBill(ctx, a, &bill, true))
	require.Equal(t, original, bill.ID)
	require.NotNil(t, bill.AmendedAt)
	require.NoError(t, st.MarkBillPaid(ctx, a, bill.ID))
	saved, err := st.GetBill(ctx, a, bill.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.MarkedPaidAt)
	require.NoError(t, st.MarkBillPaid(ctx, a, bill.ID))
	savedAgain, err := st.GetBill(ctx, a, bill.ID)
	require.NoError(t, err)
	require.Equal(t, saved.MarkedPaidAt, savedAgain.MarkedPaidAt)
	open, err := st.ListOpenBills(ctx, a, []uuid.UUID{sub.ID})
	require.NoError(t, err)
	require.Len(t, open, 1)
	require.NoError(t, st.SetBillStatus(ctx, a, bill.ID, domain.BillPaid))
	open, err = st.ListOpenBills(ctx, a, []uuid.UUID{sub.ID})
	require.NoError(t, err)
	require.Empty(t, open)
	standing, err := st.ListStandingBills(ctx, a, []uuid.UUID{sub.ID})
	require.NoError(t, err)
	require.Len(t, standing, 1)
	mapping, err := st.BillConnectionsOf(ctx, a, []uuid.UUID{bill.ID})
	require.NoError(t, err)
	require.Equal(t, c.ID, mapping[bill.ID])
	slots, err := st.SeriesHeldSlots(ctx, a, uuid.New())
	require.NoError(t, err)
	require.Empty(t, slots)
	candidates, err := st.BillPaymentCandidates(ctx, a, on, on, []domain.Money{domain.MustFromString("100")})
	require.NoError(t, err)
	require.Empty(t, candidates)
	require.NoError(t, st.DeleteBillConnection(ctx, a, c.ID))
	_, err = st.GetBill(ctx, a, bill.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}
