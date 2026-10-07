package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The email channel: only what is new, only where the rule asks, and never at
// the cost of the sweep itself.

type postbox struct {
	sent []provider.Email
	err  error
}

func (p *postbox) Send(_ context.Context, email provider.Email) error {
	p.sent = append(p.sent, email)
	return p.err
}

// alertFixture is a space, a person in it, and an account low enough to trip
// the low-balance alert on its catalog default.
func alertFixture(t *testing.T) (*Alerts, store.SpaceID, store.User, *postbox) {
	t.Helper()
	space := newSpace(t)
	database := db(t)

	// Unique per test: the schema holds one account per address.
	user := &store.User{
		Email:    fmt.Sprintf("alex-%s@example.test", uuid.NewString()),
		FullName: "Alex", IsActive: true,
	}
	require.NoError(t, database.CreateUser(t.Context(), user))
	require.NoError(t, database.CreateMembership(t.Context(), space, &store.Membership{
		UserID: user.ID, Role: store.RoleOwner,
	}))

	account := &store.Account{
		Name: "Everyday Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance: domain.MustFromString("40.00"),
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, account))

	post := &postbox{}
	alerts := NewAlerts(database)
	alerts.Mail = post
	return alerts, space, *user, post
}

func TestNoEmailGoesOutUntilTheRuleAsksForOne(t *testing.T) {
	// Email is off by default across the whole catalog.
	alerts, space, user, post := alertFixture(t)

	run, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Positive(t, run.Written, "the alert still fires")
	require.Empty(t, post.sent, "and nothing was emailed")
}

func TestAnAlertSwitchedToEmailIsEmailedOnce(t *testing.T) {
	alerts, space, user, post := alertFixture(t)
	rule := store.AlertRule{
		AlertType: domain.AlertLowBankBalance, IsEnabled: true, ChannelEmail: true,
		ThresholdAmount: domain.MustFromString("200.00"), HasThresholdAmount: true,
	}
	require.NoError(t, db(t).SaveAlertRule(t.Context(), space, user.ID, &rule))

	first, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Equal(t, 1, first.Mailed)
	require.Len(t, post.sent, 1)
	require.Equal(t, user.Email, post.sent[0].To)
	require.Contains(t, post.sent[0].Subject, "Everyday Checking is down to")

	// The second sweep writes nothing, so it must mail nothing.
	second, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Zero(t, second.Written)
	require.Zero(t, second.Mailed)
	require.Len(t, post.sent, 1)
}

func TestARelayThatIsDownDoesNotFailTheSweep(t *testing.T) {
	// The alert belongs in the feed whether or not it can also be mailed.
	alerts, space, user, post := alertFixture(t)
	post.err = provider.ErrEmailNotConfigured
	rule := store.AlertRule{
		AlertType: domain.AlertLowBankBalance, IsEnabled: true, ChannelEmail: true,
		ThresholdAmount: domain.MustFromString("200.00"), HasThresholdAmount: true,
	}
	require.NoError(t, db(t).SaveAlertRule(t.Context(), space, user.ID, &rule))

	run, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Positive(t, run.Written, "the alert reached the feed")
	require.Equal(t, 1, run.MailFailed)
	require.Zero(t, run.Mailed)
}

func TestAnInstallWithNoRelayJustDoesNotEmail(t *testing.T) {
	alerts, space, user, _ := alertFixture(t)
	alerts.Mail = nil
	rule := store.AlertRule{
		AlertType: domain.AlertLowBankBalance, IsEnabled: true, ChannelEmail: true,
		ThresholdAmount: domain.MustFromString("200.00"), HasThresholdAmount: true,
	}
	require.NoError(t, db(t).SaveAlertRule(t.Context(), space, user.ID, &rule))

	run, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Positive(t, run.Written)
	require.Zero(t, run.Mailed)
	require.Zero(t, run.MailFailed)
}

func TestAChannelTheAlertCannotUseIsNotUsedEvenIfStored(t *testing.T) {
	// A stored row asking for a channel the alert cannot reach must not
	// produce a message.
	alerts, space, user, post := alertFixture(t)
	channels, err := alerts.channels(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.False(t, channels[domain.AlertCreditScoreNewAlert].Push,
		"the credit-score alerts are email only")

	rule := store.AlertRule{
		AlertType: domain.AlertCreditScoreNewAlert, IsEnabled: true, ChannelPush: true,
	}
	require.NoError(t, db(t).SaveAlertRule(t.Context(), space, user.ID, &rule))
	channels, err = alerts.channels(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.False(t, channels[domain.AlertCreditScoreNewAlert].Push)

	_, err = alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Empty(t, post.sent)
}

func TestTheSweepMarksARevaluationAsBookkeeping(t *testing.T) {
	alerts, spaceID, user, _ := alertFixture(t)
	house := &store.Account{
		Name: "House", Kind: domain.KindAsset, Type: "real_estate",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), spaceID, house))
	card := newAccount(t, spaceID, "Card")
	today := domain.DateOf(alerts.now())

	revalued := newTransaction(t, spaceID, house, today, "-7500.00", func(txn *store.Transaction) {
		txn.Source = domain.SourceBalanceAdjustment
		txn.Payee = "Revaluation"
	})
	bought := newTransaction(t, spaceID, card, today, "-900.00")

	inputs, err := alerts.gather(t.Context(), spaceID, user.ID)
	require.NoError(t, err)
	marked := map[domain.ID]bool{}
	for _, one := range inputs.Transactions {
		marked[one.ID] = one.Bookkeeping
	}
	require.Contains(t, marked, domain.ID(revalued.ID.String()))
	require.True(t, marked[domain.ID(revalued.ID.String())])
	require.Contains(t, marked, domain.ID(bought.ID.String()))
	require.False(t, marked[domain.ID(bought.ID.String())])
}

// A split row's categories live on its splits; the parent is nulled when they
// are saved, so `CategoryID == ""` would count every split row as uncategorized.
func TestTheSweepCountsASplitRowByItsSplits(t *testing.T) {
	alerts, spaceID, user, _ := alertFixture(t)
	account := newAccount(t, spaceID, "Card")
	today := domain.DateOf(alerts.now())

	filed := newTransaction(t, spaceID, account, today, "-50.00", func(txn *store.Transaction) {
		txn.Splits = []store.Split{
			{Position: 0, Amount: domain.MustFromString("-30.00"), CategoryID: newCategory(t, spaceID, "Home Supplies", uuid.Nil).ID},
			{Position: 1, Amount: domain.MustFromString("-20.00"), CategoryID: newCategory(t, spaceID, "Office", uuid.Nil).ID},
		}
	})
	require.Equal(t, uuid.Nil, reload(t, spaceID, filed.ID).CategoryID,
		"the parent carries no category once splits are saved")

	inputs, err := alerts.gather(t.Context(), spaceID, user.ID)
	require.NoError(t, err)
	require.Equal(t, 0, inputs.UncategorizedCount, "every part is filed")

	// One part left unfiled is still uncategorized, as the register says.
	half := newTransaction(t, spaceID, account, today, "-40.00", func(txn *store.Transaction) {
		txn.Splits = []store.Split{
			{Position: 0, Amount: domain.MustFromString("-25.00"), CategoryID: newCategory(t, spaceID, "Travel", uuid.Nil).ID},
			{Position: 1, Amount: domain.MustFromString("-15.00")},
		}
	})
	require.NotNil(t, half)

	inputs, err = alerts.gather(t.Context(), spaceID, user.ID)
	require.NoError(t, err)
	require.Equal(t, 1, inputs.UncategorizedCount)
}

// The web push channel: a second sweep over the same facts writes nothing and
// must send nothing, however many browsers are subscribed.

type pushbox struct {
	sent []provider.PushSubscription
	// payloads is what each send carried.
	payloads []map[string]any
	err      error
}

func (p *pushbox) Send(_ context.Context, sub provider.PushSubscription, payload any) error {
	p.sent = append(p.sent, sub)
	if body, ok := payload.(map[string]any); ok {
		p.payloads = append(p.payloads, body)
	}
	return p.err
}

func TestAlertsDeliverPushesOnceAndDedupes(t *testing.T) {
	alerts, space, user, _ := alertFixture(t)
	push := &pushbox{}
	alerts.Push = push
	rule := store.AlertRule{
		AlertType: domain.AlertLowBankBalance, IsEnabled: true, ChannelPush: true,
		ThresholdAmount: domain.MustFromString("200.00"), HasThresholdAmount: true,
	}
	require.NoError(t, db(t).SaveAlertRule(t.Context(), space, user.ID, &rule))

	// Two browsers, a laptop and a phone.
	endpoints := []string{"https://push.example.test/laptop", "https://push.example.test/phone"}
	for _, endpoint := range endpoints {
		subscription := &store.PushSubscription{
			UserID: user.ID, Endpoint: endpoint, P256dh: "key-" + endpoint, Auth: "auth",
		}
		require.NoError(t, db(t).SavePushSubscription(t.Context(), space, user.ID, subscription))
	}

	first, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Positive(t, first.Written)
	require.Equal(t, 2, first.Pushed, "every browser the person allowed")
	require.Len(t, push.sent, 2)

	second, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Zero(t, second.Written)
	require.Zero(t, second.Pushed, "the dedupe refused the write, so nothing is sent")
	require.Len(t, push.sent, 2)
}

func TestASubscriptionThePushServiceSaysIsGoneIsForgotten(t *testing.T) {
	// The browser was uninstalled or permission revoked.
	alerts, space, user, _ := alertFixture(t)
	push := &pushbox{err: provider.ErrPushSubscriptionGone}
	alerts.Push = push
	rule := store.AlertRule{
		AlertType: domain.AlertLowBankBalance, IsEnabled: true, ChannelPush: true,
		ThresholdAmount: domain.MustFromString("200.00"), HasThresholdAmount: true,
	}
	require.NoError(t, db(t).SaveAlertRule(t.Context(), space, user.ID, &rule))
	subscription := &store.PushSubscription{
		UserID: user.ID, Endpoint: "https://push.example.test/gone", P256dh: "key", Auth: "auth",
	}
	require.NoError(t, db(t).SavePushSubscription(t.Context(), space, user.ID, subscription))

	run, err := alerts.Run(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Equal(t, 1, run.PushDropped)
	require.Zero(t, run.Pushed)

	left, err := db(t).ListPushSubscriptions(t.Context(), space, user.ID)
	require.NoError(t, err)
	require.Empty(t, left)
}

func watchOn(t *testing.T, spaceID store.SpaceID, account *store.Account, target string) {
	t.Helper()
	filter := &store.Filter{Name: "Card", Scope: "watchlist", Items: []store.FilterItem{{
		Field: string(domain.FieldAccount), Operator: string(domain.OpIn),
		ValueIDs: []uuid.UUID{account.ID},
	}}}
	require.NoError(t, db(t).CreateFilter(t.Context(), spaceID, filter))
	require.NoError(t, db(t).CreateWatchlist(t.Context(), spaceID, &store.Watchlist{
		FilterID: filter.ID, Name: "Card", Period: "monthly",
		TargetAmount: domain.MustFromString(target), HasTarget: true,
	}))
}

func excludedFromReports(t *testing.T, spaceID store.SpaceID, name string) *store.Category {
	t.Helper()
	category := newCategory(t, spaceID, name, uuid.Nil)
	category.ExcludedFromReports = true
	require.NoError(t, db(t).UpdateCategory(t.Context(), spaceID, category))
	return category
}

// A split filed under a category excluded from reports is not spending, on
// the watchlist screen or in the alert that links to it (calculations.md §13).
func TestTheWatchlistAlertSkipsASplitInAReportExcludedCategory(t *testing.T) {
	alerts, spaceID, user, _ := alertFixture(t)
	account := newAccount(t, spaceID, "Card")
	today := domain.DateOf(alerts.now())
	excluded := excludedFromReports(t, spaceID, "Reimbursable")

	newTransaction(t, spaceID, account, today, "-50.00", func(txn *store.Transaction) {
		txn.Splits = []store.Split{
			{Position: 0, Amount: domain.MustFromString("-30.00"), CategoryID: newCategory(t, spaceID, "Groceries", uuid.Nil).ID},
			{Position: 1, Amount: domain.MustFromString("-20.00"), CategoryID: excluded.ID},
		}
	})
	watchOn(t, spaceID, account, "100.00")

	inputs, err := alerts.gather(t.Context(), spaceID, user.ID)
	require.NoError(t, err)
	require.Len(t, inputs.Watchlists, 1)
	require.Equal(t, "30.00", inputs.Watchlists[0].Spent.String())
}

// A credit linked to a charge counts under the charge's category wherever a
// figure is read, even when the credit's own row says otherwise.
func TestTheWatchlistAlertCountsALinkedRefundWhereTheScreenDoes(t *testing.T) {
	alerts, spaceID, user, _ := alertFixture(t)
	account := newAccount(t, spaceID, "Card")
	today := domain.DateOf(alerts.now())
	groceries := newCategory(t, spaceID, "Groceries", uuid.Nil)
	excluded := excludedFromReports(t, spaceID, "Reimbursable")

	charge := newTransaction(t, spaceID, account, today, "-100.00", withCategory(groceries.ID))
	refund := newTransaction(t, spaceID, account, today, "30.00")
	require.NoError(t, db(t).LinkRefund(t.Context(), spaceID, refund.ID, charge.ID))
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET category_id = $2 WHERE id = $1`, refund.ID, excluded.ID)
	require.NoError(t, err)
	watchOn(t, spaceID, account, "200.00")

	inputs, err := alerts.gather(t.Context(), spaceID, user.ID)
	require.NoError(t, err)
	require.Len(t, inputs.Watchlists, 1)
	require.Equal(t, "70.00", inputs.Watchlists[0].Spent.String())
}

func TestTheFeeAlertFindsARenamedFeesAndCharges(t *testing.T) {
	fees := store.Category{ID: uuid.New(), Name: "Bank costs", KnownCategoryID: domain.KnownCategoryFeesAndCharges}
	atm := store.Category{ID: uuid.New(), ParentID: fees.ID, Name: "ATM Fee"}
	coffee := store.Category{ID: uuid.New(), Name: "Coffee Shops"}
	found := feeCategoryIDs([]store.Category{fees, atm, coffee})
	require.True(t, found[domain.ID(fees.ID.String())])
	require.True(t, found[domain.ID(atm.ID.String())])
	require.False(t, found[domain.ID(coffee.ID.String())])
}

// A provider's newest unpaid statement that no autopay takes is a bill due
// this week like any reminder, with no series behind it.
func TestAPayManuallyStatementDueThisWeekIsAnUpcomingBill(t *testing.T) {
	alerts, spaceID, user, _ := alertFixture(t)
	today := domain.DateOf(alerts.now())
	connection := &store.BillConnection{
		Biller: domain.BillerMyChart, Label: "Example Health",
		CredentialSource: store.BillCredentialSession, AutopayRule: domain.AutopayNone, PullEnabled: true,
	}
	require.NoError(t, db(t).CreateBillConnection(t.Context(), spaceID, connection))
	subaccount := &store.BillSubaccount{
		ConnectionID: connection.ID, ExternalID: "70005678", Label: "Guarantor account ****5678", IsSelected: true,
	}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), spaceID, subaccount))
	bill := &store.Bill{
		SubaccountID: subaccount.ID, IssuedOn: today.AddDays(-20), DueOn: today.AddDays(3),
		AmountDue: domain.MustFromString("90.00"), Currency: "USD", Status: domain.BillOpen,
		Source: store.BillSourceProvider, FetchedAt: alerts.now(),
	}
	require.NoError(t, db(t).UpsertBill(t.Context(), spaceID, bill, false))

	inputs, err := alerts.gather(t.Context(), spaceID, user.ID)
	require.NoError(t, err)
	require.Equal(t, []domain.AlertBill{{
		ID: domain.ID(bill.ID.String()), Name: "Example Health", DueOn: today.AddDays(3),
		Amount: domain.MustFromString("-90.00"),
	}}, inputs.Bills)
}
