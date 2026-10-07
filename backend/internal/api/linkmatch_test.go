package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The match screen chooses a pairing for the household only when the
// candidates rank one account above every other on its number or its name.

func localAccount(name string, kind domain.AccountKind, mask string) store.Account {
	return store.Account{ID: uuid.New(), Name: name, Kind: kind, MaskedNumber: mask}
}

func bankAccount(id, name string, kind domain.AccountKind, mask string) provider.Account {
	return provider.Account{ExternalID: id, Name: name, Kind: kind, MaskedNumber: mask}
}

func onlyRemote(t *testing.T, remote provider.Account, local ...store.Account) RemoteAccountResponse {
	t.Helper()
	out := buildLinkCandidates([]provider.Account{remote}, local, nil)
	require.Len(t, out.Remote, 1)
	return out.Remote[0]
}

func TestAnEqualAccountNumberIsALikelyMatch(t *testing.T) {
	checking := localAccount("Everyday", domain.KindCash, "5521")
	savings := localAccount("Rainy Day", domain.KindCash, "7710")
	row := onlyRemote(t, bankAccount("b-1", "CHECKING", domain.KindCash, "5521"), checking, savings)
	require.Equal(t, MatchNumber, row.Match)
	require.Equal(t, &checking.ID, row.Likely)
}

func TestAnEqualNameIsALikelyMatch(t *testing.T) {
	card := localAccount("Travel Rewards", domain.KindCreditCard, "")
	row := onlyRemote(t, bankAccount("b-1", "travel rewards", domain.KindCreditCard, ""), card)
	require.Equal(t, MatchName, row.Match)
	require.Equal(t, &card.ID, row.Likely)
}

func TestTheSameKindAloneChoosesNothing(t *testing.T) {
	cash := localAccount("Everyday", domain.KindCash, "")
	row := onlyRemote(t, bankAccount("b-1", "Second Checking", domain.KindCash, ""), cash)
	require.Equal(t, MatchWeak, row.Match)
	require.Nil(t, row.Likely)
	require.Equal(t, []uuid.UUID{cash.ID}, row.Suggested, "a weak guess is still listed")
}

func TestTwoAccountsScoringAlikeChooseNeither(t *testing.T) {
	first := localAccount("Brokerage", domain.KindInvestment, "")
	second := localAccount("Brokerage", domain.KindInvestment, "")
	row := onlyRemote(t, bankAccount("b-1", "Brokerage", domain.KindInvestment, ""), first, second)
	require.Equal(t, MatchTie, row.Match)
	require.Nil(t, row.Likely)
}

func TestNothingAlikeIsNoMatch(t *testing.T) {
	loan := localAccount("Car Loan", domain.KindLoan, "")
	row := onlyRemote(t, bankAccount("b-1", "Checking", domain.KindCash, ""), loan)
	require.Equal(t, MatchNone, row.Match)
	require.Nil(t, row.Likely)
	require.Empty(t, row.Suggested)
}

func TestALikelyMatchTwoBankAccountsShareChoosesNeither(t *testing.T) {
	wallet := localAccount("Wallet", domain.KindInvestment, "")
	out := buildLinkCandidates([]provider.Account{
		bankAccount("b-1", "Wallet", domain.KindInvestment, ""),
		bankAccount("b-2", "Wallet", domain.KindInvestment, ""),
	}, []store.Account{wallet}, nil)
	for _, row := range out.Remote {
		require.Equal(t, MatchTie, row.Match, row.ExternalID)
		require.Nil(t, row.Likely, row.ExternalID)
	}
}
