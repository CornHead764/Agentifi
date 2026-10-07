package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A card statement e-mail, forwarded by the household to the billing mailbox,
// filed by a rule onto a billed account linked to the card: the bill is filed,
// and the card account's statement balance, minimum and due date are the
// statement's. The expected figures are the statement's own — copied, not
// computed. The issuer, its address and every figure are invented.

func forwardedStatement(id, due, balance, minimum string) provider.MailMessage {
	html := `<html><body><div>FYI</div><hr>
<div><b>From:</b> Northwind Card Services &lt;statements@alerts.northwind-card.example&gt;<br>
<b>Sent:</b> Thursday, September 24, 2026 6:02 AM<br>
<b>To:</b> Alex Example &lt;alex@example.invalid&gt;<br>
<b>Subject:</b> Your Northwind Card statement is ready</div>
<table>
<tr><td>Account ending in 1234</td></tr>
<tr><td>Statement Balance:</td><td><b>$` + balance + `</b></td></tr>
<tr><td>Minimum Payment Due:</td><td><b>$` + minimum + `</b></td></tr>
<tr><td>Payment Due Date:</td><td><b>` + due + `</b></td></tr>
</table></body></html>`
	return provider.MailMessage{
		ID: id, ProviderID: id, Sender: "alex@example.invalid",
		Subject:    "Fw: Your Northwind Card statement is ready",
		ReceivedAt: time.Date(2026, time.September, 26, 14, 30, 0, 0, time.UTC),
		HTML:       html,
	}
}

func (f mailRuleFixture) card(t *testing.T) store.Account {
	t.Helper()
	card := &store.Account{
		Name: "Northwind Card", Kind: domain.KindCreditCard, Type: "credit_card",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), f.space, card))
	return *card
}

func (f mailRuleFixture) statementRule(t *testing.T) store.MailRule {
	t.Helper()
	rule := store.MailRule{
		Name: "Northwind statement", Enabled: true, Action: store.MailRuleBill,
		Direction: store.MailRuleExpense, Sender: "@alerts.northwind-card.example",
		SubjectContains: "statement is ready", AmountLabel: "Statement Balance",
		MinimumLabel: "Minimum Payment Due", DateLabel: "Payment Due Date",
		ReferenceLabel:   "Account ending in",
		BillConnectionID: f.connection.ID, BillSubaccountID: f.subaccount.ID,
	}
	require.NoError(t, db(t).CreateMailRule(t.Context(), f.space, &rule))
	return rule
}

func TestAForwardedCardStatementFillsTheLinkedCard(t *testing.T) {
	fixture := newMailRuleFixture(t, []provider.MailMessage{
		forwardedStatement("<nw-sep@mail.example.invalid>", "10/21/2026", "1,250.00", "40.00"),
	})
	card := fixture.card(t)
	_, err := fixture.bills.LinkAccount(t.Context(), fixture.space, fixture.subaccount.ID, card.ID)
	require.NoError(t, err)
	fixture.statementRule(t)

	result := fixture.poll(t)
	require.Equal(t, 1, result.Bills)

	filed := fixture.filed(t)
	require.Len(t, filed, 1)
	require.Equal(t, "1250.00", filed[0].AmountDue.String())
	require.True(t, filed[0].HasMinimumDue)
	require.Equal(t, "40.00", filed[0].MinimumDue.String())

	got, err := db(t).GetAccount(t.Context(), fixture.space, card.ID)
	require.NoError(t, err)
	require.True(t, got.HasStatementBalance)
	require.Equal(t, "1250.00", got.StatementBalance.String())
	require.True(t, got.HasMinimumDue)
	require.Equal(t, "40.00", got.MinimumDue.String())
	require.Equal(t, domain.NewDate(2026, time.October, 21), got.DueDate)
	require.Equal(t, filed[0].ID, got.StatementBillID)

	log := fixture.log(t)
	require.Len(t, log, 1)
	require.Equal(t, "statements@alerts.northwind-card.example", log[0].Sender,
		"the forward is logged as the issuer's mail")
	require.True(t, strings.Contains(log[0].Note, "updated the statement on Northwind Card"), log[0].Note)

	sources, err := db(t).StatementSources(t.Context(), fixture.space, []uuid.UUID{filed[0].ID})
	require.NoError(t, err)
	require.Equal(t, store.BillSourceEmail, sources[filed[0].ID].Source)
}

func TestAStatementOnAnUnlinkedBilledAccountTouchesNoAccount(t *testing.T) {
	fixture := newMailRuleFixture(t, []provider.MailMessage{
		forwardedStatement("<nw-sep@mail.example.invalid>", "10/21/2026", "1,250.00", "40.00"),
	})
	card := fixture.card(t)
	fixture.statementRule(t)

	fixture.poll(t)

	got, err := db(t).GetAccount(t.Context(), fixture.space, card.ID)
	require.NoError(t, err)
	require.False(t, got.HasStatementBalance)
	require.True(t, got.DueDate.IsZero())
	require.Equal(t, uuid.Nil, got.StatementBillID)
}

// Linked after the statement arrived: the link fills the card from the bill
// already on file. A second billed account linked to the same card takes the
// link from the first.
func TestLinkingACardFillsItFromTheBillOnFileAndMovesTheLink(t *testing.T) {
	fixture := newMailRuleFixture(t, []provider.MailMessage{
		forwardedStatement("<nw-sep@mail.example.invalid>", "10/21/2026", "1,250.00", "40.00"),
	})
	card := fixture.card(t)
	fixture.statementRule(t)
	fixture.poll(t)

	linked, err := fixture.bills.LinkAccount(t.Context(), fixture.space, fixture.subaccount.ID, card.ID)
	require.NoError(t, err)
	require.Equal(t, card.ID, linked.AccountID)
	got, err := db(t).GetAccount(t.Context(), fixture.space, card.ID)
	require.NoError(t, err)
	require.Equal(t, "1250.00", got.StatementBalance.String())

	other := &store.BillSubaccount{
		ConnectionID: fixture.connection.ID, ExternalID: "second", Label: "Second card", IsSelected: true,
	}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), fixture.space, other))
	_, err = fixture.bills.LinkAccount(t.Context(), fixture.space, other.ID, card.ID)
	require.NoError(t, err)
	first, err := db(t).GetBillSubaccount(t.Context(), fixture.space, fixture.subaccount.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, first.AccountID, "one billed account per card")

	_, err = fixture.bills.LinkAccount(t.Context(), fixture.space, other.ID, uuid.Nil)
	require.NoError(t, err)
	second, err := db(t).GetBillSubaccount(t.Context(), fixture.space, other.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, second.AccountID)
}
