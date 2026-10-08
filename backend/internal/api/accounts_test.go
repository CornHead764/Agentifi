package api

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// AccountService and the services beside it (balance history, held balances,
// institutions, ignored accounts) over their own protocol. The REST URLs are
// the bridge's, and the rest of this package still reaches them.

func TestListAccountsAnswersThisSpacesAccountsWithTheirBalances(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.ListAccountsRequest, agentifiv1.ListAccountsResponse](
		l.alex, agentifiv1connect.AccountServiceListAccountsProcedure, &agentifiv1.ListAccountsRequest{})
	require.Nil(t, err)

	byID := map[string]*agentifiv1.ListedAccount{}
	for _, account := range res.GetAccounts() {
		byID[account.GetId()] = account
	}
	require.Len(t, byID, 2)
	require.NotContains(t, byID, l.str("stranger_account"))
	checking := byID[l.str("checking")]
	require.Equal(t, "Everyday Checking", checking.GetName())
	// 500.00 opening, less 100.00 in July and 275.00 in August.
	require.Equal(t, "125.00", checking.GetBalances().GetBalance().GetAmount())
	require.Equal(t, "500.00", checking.GetOpeningBalance().GetAmount())
	require.Nil(t, checking.GetCreditLimit(), "no limit is absent, not zero")
	require.Equal(t, "5000.00", byID[l.str("card")].GetCreditLimit().GetAmount())
}

func TestAnotherSpacesAccountIsNotFound(t *testing.T) {
	l := buildLedger(t)
	for _, id := range []string{l.str("stranger_account"), "not-an-id"} {
		_, err := call[agentifiv1.GetAccountRequest, agentifiv1.GetAccountResponse](
			l.alex, agentifiv1connect.AccountServiceGetAccountProcedure, &agentifiv1.GetAccountRequest{AccountId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Account not found", err.Message())
	}
}

func TestAViewerIsRefusedAnAccountWrite(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.CreateAccountRequest, agentifiv1.CreateAccountResponse](
		l.as("vera"), agentifiv1connect.AccountServiceCreateAccountProcedure,
		&agentifiv1.CreateAccountRequest{Name: "Mine", Kind: "cash", Type: "checking"})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())
}

func TestACreatedAccountTakesOnlyTheFieldsItSets(t *testing.T) {
	l := buildLedger(t)
	created, err := call[agentifiv1.CreateAccountRequest, agentifiv1.CreateAccountResponse](
		l.alex, agentifiv1connect.AccountServiceCreateAccountProcedure,
		&agentifiv1.CreateAccountRequest{
			Name: "Rainy Day", Kind: "cash", Type: "savings",
			OpeningBalance: &agentifiv1.NullableMoney{Amount: "250.00"},
			Notes:          proto.String("for the roof"),
		})
	require.Nil(t, err)
	account := created.GetAccount()
	require.Equal(t, "250.00", account.GetOpeningBalance().GetAmount())
	require.Equal(t, "for the roof", account.GetNotes())
	require.Equal(t, "USD", account.GetCurrency(), "the space's currency")
	require.True(t, account.GetIncludeInNetWorth())
	require.Nil(t, account.Description)

	_, err = call[agentifiv1.CreateAccountRequest, agentifiv1.CreateAccountResponse](
		l.alex, agentifiv1connect.AccountServiceCreateAccountProcedure,
		&agentifiv1.CreateAccountRequest{Name: "Odd", Kind: "piggy_bank", Type: "jar"})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "kind"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestAnAccountUpdateLeavesAloneWhatTheMaskDoesNotName(t *testing.T) {
	l := buildLedger(t)
	update := func(req *agentifiv1.UpdateAccountRequest, paths ...string) *agentifiv1.Account {
		t.Helper()
		req.AccountId = l.str("card")
		req.UpdateMask = &fieldmaskpb.FieldMask{Paths: paths}
		res, err := call[agentifiv1.UpdateAccountRequest, agentifiv1.UpdateAccountResponse](
			l.alex, agentifiv1connect.AccountServiceUpdateAccountProcedure, req)
		require.Nil(t, err)
		return res.GetAccount()
	}

	// Set.
	account := update(&agentifiv1.UpdateAccountRequest{
		CreditLimit:     &agentifiv1.NullableMoney{Amount: "6000.00"},
		Notes:           proto.String("the travel card"),
		HistoryStartsOn: proto.String("2026-02-01"),
	}, "credit_limit", "notes", "history_starts_on")
	require.Equal(t, "6000.00", account.GetCreditLimit().GetAmount())
	require.Equal(t, "the travel card", account.GetNotes())
	require.Equal(t, "2026-02-01", account.GetHistoryStartsOn())

	// Absent: a rename leaves the rest as it was.
	account = update(&agentifiv1.UpdateAccountRequest{Name: proto.String("Travel Card")}, "name")
	require.Equal(t, "Travel Card", account.GetName())
	require.Equal(t, "6000.00", account.GetCreditLimit().GetAmount())
	require.Equal(t, "the travel card", account.GetNotes())
	require.Equal(t, "2026-02-01", account.GetHistoryStartsOn())

	// Cleared: named in the mask and unset.
	account = update(&agentifiv1.UpdateAccountRequest{}, "credit_limit", "notes", "history_starts_on")
	require.Nil(t, account.GetCreditLimit())
	require.Nil(t, account.Notes)
	require.Nil(t, account.HistoryStartsOn)
	require.Equal(t, "Travel Card", account.GetName())
}

func TestClearingAnAccountsNameIsAConflict(t *testing.T) {
	l := buildLedger(t)
	l.alex.rpc(agentifiv1connect.AccountServiceUpdateAccountProcedure, map[string]any{
		"account_id": l.str("checking"), "update_mask": "name",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)
}

func TestAMalformedDateInAnAccountUpdateNamesTheField(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.UpdateAccountRequest, agentifiv1.UpdateAccountResponse](
		l.alex, agentifiv1connect.AccountServiceUpdateAccountProcedure,
		&agentifiv1.UpdateAccountRequest{AccountId: l.str("checking"), DueDate: proto.String("next tuesday")})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "due_date"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestAnAccountSummaryEchoesItsWindow(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.GetAccountSummaryRequest, agentifiv1.GetAccountSummaryResponse](
		l.alex, agentifiv1connect.AccountServiceGetAccountSummaryProcedure,
		&agentifiv1.GetAccountSummaryRequest{AccountId: l.str("checking"), From: "2026-08-01", To: "2026-08-31"})
	require.Nil(t, err)
	require.Equal(t, "2026-08-01", res.GetWindow().GetFrom())
	require.Equal(t, "2026-08-31", res.GetWindow().GetTo())
	require.Equal(t, "posted", res.GetWindow().GetDateField())
	require.Equal(t, "400.00", res.GetOpeningBalance().GetAmount())
	require.Equal(t, "125.00", res.GetEndingBalance().GetAmount())
	require.Equal(t, int32(3), res.GetCount())
}

func TestABalanceHistoryNeedsBothEnds(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.GetAccountBalanceHistoryRequest, agentifiv1.GetAccountBalanceHistoryResponse](
		l.alex, agentifiv1connect.AccountBalanceHistoryServiceGetAccountBalanceHistoryProcedure,
		&agentifiv1.GetAccountBalanceHistoryRequest{AccountId: l.str("checking"), From: "2026-08-01"})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, int32(http.StatusBadRequest), problemIn(t, err).GetStatus())

	res, err := call[agentifiv1.GetAccountBalanceHistoryRequest, agentifiv1.GetAccountBalanceHistoryResponse](
		l.alex, agentifiv1connect.AccountBalanceHistoryServiceGetAccountBalanceHistoryProcedure,
		&agentifiv1.GetAccountBalanceHistoryRequest{AccountId: l.str("checking"), From: "2026-08-01", To: "2026-08-03"})
	require.Nil(t, err)
	require.Len(t, res.GetPoints(), 3)
	require.Equal(t, "2026-08-01", res.GetPoints()[0].GetOn())
	require.Equal(t, "400.00", res.GetPoints()[0].GetBalance().GetAmount())

	_, err = call[agentifiv1.GetAccountBalanceHistoryRequest, agentifiv1.GetAccountBalanceHistoryResponse](
		l.alex, agentifiv1connect.AccountBalanceHistoryServiceGetAccountBalanceHistoryProcedure,
		&agentifiv1.GetAccountBalanceHistoryRequest{
			AccountId: l.str("stranger_account"), From: "2026-08-01", To: "2026-08-03",
		})
	require.Equal(t, connect.CodeNotFound, err.Code())
}

func TestAnAccountWithNoHoldHasNothingToAccept(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.AcceptHeldBalanceRequest, agentifiv1.AcceptHeldBalanceResponse](
		l.alex, agentifiv1connect.HeldBalanceServiceAcceptHeldBalanceProcedure,
		&agentifiv1.AcceptHeldBalanceRequest{AccountId: l.str("card")})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
	require.Equal(t, int32(http.StatusConflict), problemIn(t, err).GetStatus())
}

func TestAnInstitutionsThresholdIsSetAndCleared(t *testing.T) {
	l := buildLedger(t)
	f := seedSmallBalances(t, l)
	update := func(req *agentifiv1.UpdateInstitutionRequest) (*agentifiv1.UpdateInstitutionResponse, *connect.Error) {
		req.InstitutionId = f.exchange.String()
		return call[agentifiv1.UpdateInstitutionRequest, agentifiv1.UpdateInstitutionResponse](
			l.alex, agentifiv1connect.InstitutionServiceUpdateInstitutionProcedure, req)
	}

	res, err := update(&agentifiv1.UpdateInstitutionRequest{
		HideBelowBalance: &agentifiv1.NullableMoney{Amount: "1.00"},
	})
	require.Nil(t, err)
	require.Equal(t, "1.00", res.GetInstitution().GetHideBelowBalance().GetAmount())

	// Absent is refused: the field is the whole request.
	_, err = update(&agentifiv1.UpdateInstitutionRequest{})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())

	res, err = update(&agentifiv1.UpdateInstitutionRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"hide_below_balance"}},
	})
	require.Nil(t, err)
	require.Nil(t, res.GetInstitution().GetHideBelowBalance())

	_, err = call[agentifiv1.UpdateInstitutionRequest, agentifiv1.UpdateInstitutionResponse](
		l.as("bob"), agentifiv1connect.InstitutionServiceUpdateInstitutionProcedure,
		&agentifiv1.UpdateInstitutionRequest{
			InstitutionId: f.exchange.String(), HideBelowBalance: &agentifiv1.NullableMoney{Amount: "1.00"},
		})
	require.Equal(t, connect.CodeNotFound, err.Code())
}

func TestAnIgnoredAccountComesBackOnce(t *testing.T) {
	l := buildLedger(t)
	ignored, err := call[agentifiv1.IgnoreAccountsRequest, agentifiv1.IgnoreAccountsResponse](
		l.alex, agentifiv1connect.IgnoredAccountServiceIgnoreAccountsProcedure,
		&agentifiv1.IgnoreAccountsRequest{AccountIds: []string{l.str("checking"), l.str("stranger_account")}})
	require.Nil(t, err)
	require.Equal(t, []string{l.str("checking")}, ignored.GetAccountIds(), "another space's account is not ignored")

	listed, err := call[agentifiv1.ListIgnoredAccountsRequest, agentifiv1.ListIgnoredAccountsResponse](
		l.alex, agentifiv1connect.IgnoredAccountServiceListIgnoredAccountsProcedure,
		&agentifiv1.ListIgnoredAccountsRequest{})
	require.Nil(t, err)
	require.Len(t, listed.GetAccounts(), 1)
	require.Equal(t, "125.00", listed.GetAccounts()[0].GetBalance().GetAmount())
	require.NotNil(t, listed.GetAccounts()[0].GetIgnoredAt())

	unignore := func() *connect.Error {
		_, err := call[agentifiv1.UnignoreAccountRequest, agentifiv1.UnignoreAccountResponse](
			l.alex, agentifiv1connect.IgnoredAccountServiceUnignoreAccountProcedure,
			&agentifiv1.UnignoreAccountRequest{AccountId: l.str("checking")})
		return err
	}
	require.Nil(t, unignore())
	require.Equal(t, connect.CodeNotFound, unignore().Code())
}

func TestAViewerIsRefusedIgnoringAnAccount(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.IgnoreAccountsRequest, agentifiv1.IgnoreAccountsResponse](
		l.as("vera"), agentifiv1connect.IgnoredAccountServiceIgnoreAccountsProcedure,
		&agentifiv1.IgnoreAccountsRequest{AccountIds: []string{l.str("checking")}})
	require.Equal(t, connect.CodePermissionDenied, err.Code())

	account, getErr := db(t).GetAccount(t.Context(), store.SpaceIDOf(l.id("space")), l.id("checking"))
	require.NoError(t, getErr)
	require.False(t, account.IsIgnored())
}
