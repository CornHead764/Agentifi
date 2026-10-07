package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Notifications about a standing state (a mailbox that cannot be read, a
// balance under its line) are told once while it lasts and leave the feed
// when it ends.

func feedOf(t *testing.T, space store.SpaceID, userID uuid.UUID, kind domain.AlertType) []store.Notification {
	t.Helper()
	all, err := db(t).ListNotifications(t.Context(), space, userID, store.NotificationQuery{})
	require.NoError(t, err)
	var out []store.Notification
	for _, one := range all {
		if one.AlertType == kind {
			out = append(out, one)
		}
	}
	return out
}

func unreadIn(t *testing.T, space store.SpaceID, userID uuid.UUID) int {
	t.Helper()
	count, err := db(t).UnreadNotificationCount(t.Context(), space, userID)
	require.NoError(t, err)
	return count
}

// member adds a writer who has accepted, the audience a connector's alert
// goes to.
func member(t *testing.T, space store.SpaceID) uuid.UUID {
	t.Helper()
	user := &store.User{
		Email:    fmt.Sprintf("sam-%s@example.test", uuid.NewString()),
		FullName: "Sam", IsActive: true,
	}
	require.NoError(t, db(t).CreateUser(t.Context(), user))
	accepted := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db(t).CreateMembership(t.Context(), space, &store.Membership{
		UserID: user.ID, Role: store.RoleOwner, AcceptedAt: &accepted,
	}))
	return user.ID
}

func TestAMailboxThatStopsIsToldOnceAndWithdrawnByTheNextRead(t *testing.T) {
	fixture := newMailFixture(t)
	userID := member(t, fixture.space)
	fixture.mailbox.Alerts = NewAlerts(db(t))
	start := time.Date(2026, time.October, 1, 1, 0, 0, 0, time.UTC)
	at := func(later time.Duration) {
		fixture.mailbox.Now = func() time.Time { return start.Add(later) }
	}
	poll := func() MailPollResult {
		result, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
		require.NoError(t, err)
		return result
	}

	// One dropped connection overnight is the network, not an outage.
	fixture.reader.err = errors.New("dial tcp: lookup imap.example.invalid: no such host")
	at(0)
	require.NotEmpty(t, poll().Error)
	require.Empty(t, feedOf(t, fixture.space, userID, domain.AlertEmailConnectionFailed),
		"a single failure is not news")

	// The next read works, so the streak is over before it said anything.
	fixture.reader.err = nil
	at(30 * time.Minute)
	require.Empty(t, poll().Error)

	// Failing for an hour and more is.
	fixture.reader.err = errors.New("imap.example.invalid refused the sign-in")
	at(2 * time.Hour)
	poll()
	at(2*time.Hour + 30*time.Minute)
	poll()
	require.Empty(t, feedOf(t, fixture.space, userID, domain.AlertEmailConnectionFailed),
		"two failures half an hour apart are still not an hour")
	at(3 * time.Hour)
	poll()
	cards := feedOf(t, fixture.space, userID, domain.AlertEmailConnectionFailed)
	require.Len(t, cards, 1)
	require.Contains(t, cards[0].Title, "the bills mailbox stopped reading mail")

	// Every later failure is the same outage.
	at(27 * time.Hour)
	poll()
	require.Len(t, feedOf(t, fixture.space, userID, domain.AlertEmailConnectionFailed), 1,
		"a mailbox down for a day is one notification, not one a day")
	require.Equal(t, 1, unreadIn(t, fixture.space, userID))

	// The mailbox reads again: the card is wrong now, and goes.
	fixture.reader.err = nil
	at(28 * time.Hour)
	require.Empty(t, poll().Error)
	require.Empty(t, feedOf(t, fixture.space, userID, domain.AlertEmailConnectionFailed))
	require.Zero(t, unreadIn(t, fixture.space, userID))

	// And the next outage is news again.
	fixture.reader.err = errors.New("imap.example.invalid refused the sign-in")
	at(40 * time.Hour)
	poll()
	at(42 * time.Hour)
	poll()
	require.Len(t, feedOf(t, fixture.space, userID, domain.AlertEmailConnectionFailed), 1)
}

func TestALowBalanceIsOneCardWhileItLastsAndGoesWhenItRecovers(t *testing.T) {
	alerts, space, user, _ := alertFixture(t)
	line := func(amount string) {
		rule := store.AlertRule{
			AlertType: domain.AlertLowBankBalance, IsEnabled: true, ChannelInApp: true,
			ThresholdAmount: domain.MustFromString(amount), HasThresholdAmount: true,
		}
		require.NoError(t, db(t).SaveAlertRule(t.Context(), space, user.ID, &rule))
	}
	sweep := func(day int) {
		alerts.Now = func() time.Time { return time.Date(2026, time.October, day, 8, 0, 0, 0, time.UTC) }
		_, err := alerts.Run(t.Context(), space, user.ID)
		require.NoError(t, err)
	}

	// 40.00 against a line of 200.00, on three mornings running.
	line("200.00")
	sweep(1)
	sweep(2)
	sweep(3)
	require.Len(t, feedOf(t, space, user.ID, domain.AlertLowBankBalance), 1,
		"a balance that stays low is one card, not one a morning")

	// The line moves below the balance: the state is over and the card goes.
	line("10.00")
	sweep(4)
	require.Empty(t, feedOf(t, space, user.ID, domain.AlertLowBankBalance))

	// Under the line again is a new dip, and is told.
	line("200.00")
	sweep(5)
	require.Len(t, feedOf(t, space, user.ID, domain.AlertLowBankBalance), 1)
}

func TestABillPullThatGetsInWithdrawsTheStoppedCard(t *testing.T) {
	fixture := newBridgeFixture(t, domain.BillerAlliant)
	fixture.agent.answers = []map[string]any{{
		"needs_sign_in": true, "reason": "Alliant Energy refused the kept session",
	}}
	_, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	stopped := feedOf(t, fixture.space, fixture.user.ID, domain.AlertBillPullFailed)
	require.Len(t, stopped, 1)

	fixture.agent.answers = []map[string]any{okPull(`{"token":"rolled-two"}`)}
	ok, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, ok.Status)
	require.Empty(t, feedOf(t, fixture.space, fixture.user.ID, domain.AlertBillPullFailed),
		"a pull that got in leaves no card saying it cannot")
}

func TestAMerchantPullStoppedAtASignInIsPushedOnceAndWithdrawnByTheNextPull(t *testing.T) {
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
	merchants, _, space, account := merchantsWithAgent(t, agent)
	userID := member(t, space)
	require.NoError(t, db(t).SavePushSubscription(t.Context(), space, userID,
		&store.PushSubscription{
			Endpoint: "https://push.example.test/" + uuid.NewString(),
			P256dh:   "key", Auth: "auth", UserAgent: "a laptop",
		}))
	push := &pushbox{}
	merchants.Alerts = NewAlerts(db(t))
	merchants.Alerts.Push = push
	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept", "", "")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))

	agent.lapsed, agent.paused = true, provider.SignInPausedPasswordRefused
	for range 2 {
		_, err = merchants.Pull(t.Context(), space, account.ID, 30)
		require.ErrorIs(t, err, ErrMerchantNeedsSignIn)
	}
	stopped := feedOf(t, space, userID, domain.AlertAmazonSignIn)
	require.Len(t, stopped, 1, "a pull that stays stopped is one card")
	require.True(t, strings.HasPrefix(stopped[0].DedupeKey,
		space.UUID().String()+":amazon-sign-in:"+account.ID.String()+":"),
		"the condition matches the rows already in the feed: %s", stopped[0].DedupeKey)
	require.Len(t, push.payloads, 1, "the card reaches the phone once")
	require.Equal(t, "/settings/merchants/amazon?sign-in="+account.ID.String(), push.payloads[0]["url"])

	agent.lapsed, agent.paused = false, ""
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Empty(t, feedOf(t, space, userID, domain.AlertAmazonSignIn),
		"a pull that got in leaves no card saying it cannot")
}
