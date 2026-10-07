package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestABillInCreditIsStoredAsNothingOwedAndNeverAsItsAbsoluteValue(t *testing.T) {
	bills, notes := CoerceBills([]WireBill{{
		Subaccount: "acct-1", DueOn: "2026-09-26",
		AmountDue: domain.MustFromString("-42.00"), Status: "Open",
	}})

	require.Len(t, bills, 1)
	require.True(t, bills[0].AmountDue.IsZero(), "a credit is not money owed")
	require.Equal(t, string(domain.BillPaid), bills[0].Status)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "in credit by 42.00")
}

func TestABillOwedKeepsItsAmountAndStatus(t *testing.T) {
	bills, notes := CoerceBills([]WireBill{{
		Subaccount: "acct-1", DueOn: "2026-09-26",
		AmountDue: domain.MustFromString("42.00"), Status: "Open",
	}})

	require.Len(t, bills, 1)
	require.Equal(t, "42.00", bills[0].AmountDue.String())
	require.Equal(t, "Open", bills[0].Status)
	require.Empty(t, notes)
}

func TestABillWithNoDueDateIsDroppedWithANote(t *testing.T) {
	bills, notes := CoerceBills([]WireBill{{Subaccount: "acct-1", AmountDue: domain.MustFromString("1.00")}})
	require.Empty(t, bills)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "is not a due date")
}
