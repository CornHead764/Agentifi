package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func card(closeDay int, dueDate domain.Date) store.Account {
	account := store.Account{Name: "Card", Kind: domain.KindCreditCard, Type: "credit_card"}
	if closeDay != 0 {
		day := int16(closeDay)
		account.StatementCloseDay = &day
	}
	account.DueDate = dueDate
	return account
}

func TestPaymentDueDayIsReadOffTheBillFeed(t *testing.T) {
	require.Equal(t, 5, PaymentDueDay(card(0, on(2026, time.April, 5))))
	require.Equal(t, 0, PaymentDueDay(card(0, domain.Date{})))
}

func TestApplyEffectiveDateReportsWhetherItMoved(t *testing.T) {
	account := card(15, on(2026, time.April, 5))
	txn := &store.Transaction{Date: on(2026, time.March, 10), Amount: domain.MustFromString("-40.00")}

	require.True(t, ApplyEffectiveDate(txn, account, domain.Date{}, true))
	require.Equal(t, on(2026, time.April, 5), txn.EffectiveDate)
	// Idempotent: a second sync of the same row is not a change.
	require.False(t, ApplyEffectiveDate(txn, account, domain.Date{}, true))
}

func TestApplyEffectiveDateCanRefuseToOverwriteACorrection(t *testing.T) {
	account := card(15, on(2026, time.April, 5))
	txn := &store.Transaction{
		Date:          on(2026, time.March, 10),
		Amount:        domain.MustFromString("-40.00"),
		EffectiveDate: on(2026, time.March, 31),
	}

	require.False(t, ApplyEffectiveDate(txn, account, domain.Date{}, false))
	require.Equal(t, on(2026, time.March, 31), txn.EffectiveDate)
}

func TestRestampMovesEveryChargeOnACardWhoseCycleChanged(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Card", withKind(domain.KindCreditCard))
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 10), "-40.00")
	require.True(t, reload(t, spaceID, charge.ID).EffectiveDate.IsZero())

	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE accounts SET statement_close_day = 15, due_date = $2 WHERE id = $1`,
		account.ID, on(2026, time.April, 5))
	require.NoError(t, err)

	cards := NewCreditCards(db(t))
	moved, err := cards.Restamp(t.Context(), spaceID, account.ID, nil)
	require.NoError(t, err)
	require.Equal(t, 1, moved)
	require.Equal(t, on(2026, time.April, 5), reload(t, spaceID, charge.ID).EffectiveDate)

	// A re-stamp that changes nothing writes nothing.
	moved, err = cards.Restamp(t.Context(), spaceID, account.ID, nil)
	require.NoError(t, err)
	require.Equal(t, 0, moved)
}
