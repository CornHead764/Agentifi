package store

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// TestMoneyRoundTrip sends amounts to the database and compares what comes
// back as strings. pgx will scan a numeric into a float64 for anyone who
// offers one, and 1900.00 through a float is 1899.9999999999998.
func TestMoneyRoundTrip(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Round trip")

	amounts := []string{
		"1900.00",
		"0.01",
		"-0.01",
		"0.10",
		"0.20",
		"1234567890123.99", // the widest value numeric(15,2) holds
		"-1234567890123.99",
		"0.00",
	}

	for _, want := range amounts {
		txn := &Transaction{
			AccountID:     account.ID,
			Date:          domain.NewDate(2026, 3, 14),
			Amount:        domain.MustFromString(want),
			Currency:      "USD",
			StatementName: "ROUND TRIP " + want,
			Payee:         "Round trip",
			Source:        domain.SourceManual,
		}
		require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))

		read, err := db(t).GetTransaction(t.Context(), spaceID, txn.ID)
		require.NoError(t, err)
		require.Equal(t, want, read.Amount.String(), "amount changed on the way through Postgres")
		require.True(t, read.Amount.Equal(domain.MustFromString(want)))
	}
}

// TestMoneySumsExactly checks the property a float would break: a hundred
// dimes are ten dollars, not 9.999999999999998.
func TestMoneySumsExactly(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Dimes")

	for i := 0; i < 100; i++ {
		txn := &Transaction{
			AccountID:     account.ID,
			Date:          domain.NewDate(2026, 3, 14),
			Amount:        domain.MustFromString("0.10"),
			Currency:      "USD",
			StatementName: "DIME",
			Payee:         "Dime",
			Source:        domain.SourceManual,
		}
		require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))
	}

	txns, err := db(t).ListTransactions(t.Context(), spaceID, TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, txns, 100)

	total := domain.Sum(txns, func(txn Transaction) domain.Money { return txn.Amount })
	require.Equal(t, "10.00", total.String())
}

// TestRateRoundTrip covers the other numeric width: rates and share counts are
// numeric(20,10) and are not money. Quantizing one to the cent on the way in
// destroys the only copy of the figure.
func TestRateRoundTrip(t *testing.T) {
	spaceID := newSpace(t)

	account := &Account{
		Name:            "Mortgage",
		Kind:            domain.KindLoan,
		Type:            "mortgage",
		Currency:        "USD",
		InterestRate:    decimal.RequireFromString("0.0637500000"),
		HasInterestRate: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), spaceID, account))

	read, err := db(t).GetAccount(t.Context(), spaceID, account.ID)
	require.NoError(t, err)
	require.True(t, read.HasInterestRate)
	require.Equal(t, "0.06375", read.InterestRate.String())

	txn := &Transaction{
		AccountID:     account.ID,
		Date:          domain.NewDate(2026, 3, 14),
		Amount:        domain.MustFromString("-100.00"),
		Currency:      "EUR",
		AmountPrimary: domain.MustFromString("-112.35"),

		HasAmountPrimary: true,
		FxRateUsed:       decimal.RequireFromString("1.1234567891"),
		HasFxRateUsed:    true,
		StatementName:    "PARIS",
		Payee:            "Paris",
		Source:           domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))

	readTxn, err := db(t).GetTransaction(t.Context(), spaceID, txn.ID)
	require.NoError(t, err)
	require.Equal(t, "1.1234567891", readTxn.FxRateUsed.String())
	require.Equal(t, "-112.35", readTxn.AmountPrimary.String())
}

// TestNullAmountIsNotZero checks the distinction the Has flags exist for: a
// manual account has no provider balance, which is not a balance of zero.
func TestNullAmountIsNotZero(t *testing.T) {
	spaceID := newSpace(t)

	manual := newAccount(t, spaceID, "Cash box")
	read, err := db(t).GetAccount(t.Context(), spaceID, manual.ID)
	require.NoError(t, err)
	require.False(t, read.HasProviderBalance)
	require.True(t, read.ProviderBalance.IsZero())
	require.False(t, read.HasCreditLimit)

	connected := &Account{
		Name:               "Card",
		Kind:               domain.KindCreditCard,
		Type:               "credit_card",
		Currency:           "USD",
		ProviderBalance:    domain.MustFromString("-0.00"),
		HasProviderBalance: true,
		CreditLimit:        domain.MustFromString("5000.00"),
		HasCreditLimit:     true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), spaceID, connected))

	readCard, err := db(t).GetAccount(t.Context(), spaceID, connected.ID)
	require.NoError(t, err)
	require.True(t, readCard.HasProviderBalance, "a zero provider balance is still a provider balance")
	require.Equal(t, "0.00", readCard.ProviderBalance.String())
	require.Equal(t, "5000.00", readCard.CreditLimit.String())
}
