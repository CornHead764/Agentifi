package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A loan and the asset it is secured on, end to end.
//
// The pairing is stored on the loan and read off the asset: a house carries
// its mortgage and its HELOC at once, and a loan is secured on one thing. What
// the listing has to get right is the sign — debt is stored negative, so
// equity is a sum and not a subtraction, and a version that subtracted would
// report a $420,000 house with a $284,000 mortgage as $704,000 of equity.

// newAccount creates an account of the given kind and returns its id.
func newAccount(l *ledger, name, kind, accountType, opening string) string {
	l.t.Helper()
	body := l.alex.post("/accounts", map[string]any{
		"name": name, "kind": kind, "type": accountType, "opening_balance": opening,
	}).requireStatus(http.StatusCreated).json()
	return body["id"].(string)
}

// listedAccount finds one account in `GET /accounts`.
func listedAccount(l *ledger, id string) map[string]any {
	l.t.Helper()
	for _, row := range l.alex.get("/accounts").requireStatus(http.StatusOK).list() {
		if row["id"] == id {
			return row
		}
	}
	l.t.Fatalf("account %s is not in the listing", id)
	return nil
}

func TestAnAssetReportsTheEquityLeftAfterTheLoansSecuredOnIt(t *testing.T) {
	l := buildLedger(t)
	house := newAccount(l, "Maple Street", "asset", "real_estate", "420000.00")
	mortgage := newAccount(l, "Maple Street Mortgage", "loan", "mortgage", "-284000.00")

	l.alex.patch("/accounts/"+mortgage, map[string]any{
		"secured_by_account_id": house,
	}).requireStatus(http.StatusOK)

	equity := listedAccount(l, house)["equity"].(map[string]any)
	require.Equal(t, "420000.00", equity["value"])
	require.Equal(t, "284000.00", equity["owed"])
	require.Equal(t, "136000.00", equity["equity"])
	require.Equal(t, []any{mortgage}, equity["loan_ids"])

	// The loan carries the pairing; the asset carries the arithmetic.
	loanRow := listedAccount(l, mortgage)
	require.Equal(t, house, loanRow["secured_by_account_id"])
	require.Nil(t, loanRow["equity"])
}

func TestAnAssetWithNothingSecuredOnItHasNoEquityBlock(t *testing.T) {
	// Null rather than "equity equals the whole value": an unfinanced car and
	// a mortgaged house are both assets, and only one has a second number
	// worth printing.
	l := buildLedger(t)
	car := newAccount(l, "Hatchback", "asset", "vehicle", "18400.00")
	require.Nil(t, listedAccount(l, car)["equity"])
}

func TestEverySecuredLoanCountsAgainstTheOneAsset(t *testing.T) {
	l := buildLedger(t)
	house := newAccount(l, "Maple Street", "asset", "real_estate", "420000.00")
	first := newAccount(l, "First Mortgage", "loan", "mortgage", "-284000.00")
	heloc := newAccount(l, "HELOC", "loan", "home_equity_loan", "-35000.00")

	for _, loan := range []string{first, heloc} {
		l.alex.patch("/accounts/"+loan, map[string]any{
			"secured_by_account_id": house,
		}).requireStatus(http.StatusOK)
	}

	equity := listedAccount(l, house)["equity"].(map[string]any)
	require.Equal(t, "319000.00", equity["owed"])
	require.Equal(t, "101000.00", equity["equity"])
	require.Len(t, equity["loan_ids"], 2)
}

func TestUnpairingALoanReturnsTheAssetToItsFullValue(t *testing.T) {
	l := buildLedger(t)
	house := newAccount(l, "Maple Street", "asset", "real_estate", "420000.00")
	mortgage := newAccount(l, "Maple Street Mortgage", "loan", "mortgage", "-284000.00")

	l.alex.patch("/accounts/"+mortgage, map[string]any{
		"secured_by_account_id": house,
	}).requireStatus(http.StatusOK)
	l.alex.patch("/accounts/"+mortgage, map[string]any{
		"secured_by_account_id": nil,
	}).requireStatus(http.StatusOK)

	require.Nil(t, listedAccount(l, house)["equity"])
	require.Nil(t, listedAccount(l, mortgage)["secured_by_account_id"])
}

func TestALoanCannotBeSecuredOnItself(t *testing.T) {
	l := buildLedger(t)
	mortgage := newAccount(l, "Maple Street Mortgage", "loan", "mortgage", "-284000.00")

	l.alex.patch("/accounts/"+mortgage, map[string]any{
		"secured_by_account_id": mortgage,
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestALoanCannotBeSecuredOnSomethingThatIsNotAnAsset(t *testing.T) {
	// "Equity in a checking account" is not a concept, and the listing would
	// print one.
	l := buildLedger(t)
	mortgage := newAccount(l, "Maple Street Mortgage", "loan", "mortgage", "-284000.00")

	l.alex.patch("/accounts/"+mortgage, map[string]any{
		"secured_by_account_id": l.str("checking"),
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestALoanCannotBeSecuredOnAnotherHouseholdsAsset(t *testing.T) {
	l := buildLedger(t)
	mortgage := newAccount(l, "Maple Street Mortgage", "loan", "mortgage", "-284000.00")

	l.alex.patch("/accounts/"+mortgage, map[string]any{
		"secured_by_account_id": l.str("stranger_account"),
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestReclassifyingAnAccountCorrectsTheBanksFigureWithIt(t *testing.T) {
	// SimpleFIN carries no account type, so a mortgage can arrive classified
	// as cash with its balance stored positive. Correcting the type has to
	// correct the sign in the same act: waiting for the next sync leaves a
	// mortgage counted as an asset in net worth until tomorrow morning, which
	// is the exact reading somebody reclassifying it is trying to fix.
	l := buildLedger(t)
	database := db(t)
	space := store.SpaceIDOf(l.id("space"))

	mislabelled := &store.Account{
		Name: "Home Mortgage", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		ProviderBalance: domain.MustFromString("284000.00"), HasProviderBalance: true,
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, mislabelled))

	fixed := l.alex.patch("/accounts/"+mislabelled.ID.String(), map[string]any{
		"kind": "loan", "type": "mortgage",
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, "loan", fixed["kind"])
	require.Equal(t, "-284000.00", fixed["provider_balance"])
}

func TestReclassifyingBackOutOfDebtUndoesTheNegation(t *testing.T) {
	l := buildLedger(t)
	database := db(t)
	space := store.SpaceIDOf(l.id("space"))

	account := &store.Account{
		Name: "Not Actually A Loan", Kind: domain.KindLoan, Type: "mortgage",
		Currency: "USD", IncludeInNetWorth: true,
		ProviderBalance: domain.MustFromString("-4000.00"), HasProviderBalance: true,
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, account))

	fixed := l.alex.patch("/accounts/"+account.ID.String(), map[string]any{
		"kind": "cash", "type": "savings",
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, "4000.00", fixed["provider_balance"])
}
