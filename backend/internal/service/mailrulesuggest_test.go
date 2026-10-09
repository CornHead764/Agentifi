package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A drafted rule is a candidate. The cafe, the figures and the names are
// invented.

func cafeReceipt() billmail.Message {
	return billmail.Message{
		ID: "<r1@mail.example.invalid>", Sender: "receipts@cafe.example.invalid",
		Subject: "Your receipt from Corner Cafe", ReceivedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		Text: "Thanks for stopping by.\nReceipt Total: $6.50\nReceipt #A-1142\n\n" +
			"Your order\nFlat white 1 4.00\nShortbread 1 2.50\nSubtotal 6.50",
	}
}

func TestADraftedRuleKeepsWhatIsValidAndSaysWhatItDropped(t *testing.T) {
	checking := ruleTarget{ID: uuid.New(), Name: "Everyday Checking"}
	dining := ruleTarget{ID: uuid.New(), Name: "Food > Dining Out"}

	out := checkDraftedRule(map[string]any{
		"name": "Corner Cafe", "sender": "receipts@cafe.example.invalid",
		"subject_contains": "Your receipt", "amount_label": "Receipt Total:",
		"reference_label": "Receipt #", "payee": "Corner Cafe",
		"notes_label": "Your order", "notes_end_label": "Subtotal",
		"date_pattern":  "(unclosed",
		"direction":     "sideways",
		"account":       "Everyday Checking",
		"category":      "Snacks",
		"body_contains": 42,
	}, cafeReceipt(), ruleTargets{accounts: []ruleTarget{checking}, categories: []ruleTarget{dining}})

	rule := out.Rule
	require.Equal(t, "Corner Cafe", rule.Name)
	require.Equal(t, "receipts@cafe.example.invalid", rule.Sender)
	require.Equal(t, "Receipt Total:", rule.AmountLabel)
	require.Equal(t, checking.ID, rule.AccountID, "a name the household has is its id")
	require.Equal(t, uuid.Nil, rule.CategoryID, "a name it does not have is not guessed at")
	require.Empty(t, rule.DatePattern, "a pattern that does not compile is dropped")
	require.Empty(t, rule.BodyContains)
	require.Equal(t, store.MailRuleExpense, rule.Direction)
	require.Equal(t, store.MailRuleTransaction, rule.Action)
	require.Equal(t, uuid.Nil, rule.ID, "nothing is saved")
	require.Len(t, out.Dropped, 4)

	// And the draft reads the mail it came from.
	tried := TryRule(rule, cafeReceipt())
	require.True(t, tried.Matched)
	require.Equal(t, "6.50", tried.Amount.String())
	require.Equal(t, "A-1142", tried.Reference, "a label ending in # reads what runs on from it")
	require.Equal(t, "A-1142\nFlat white 1 4.00\nShortbread 1 2.50", tried.Notes)
}

func TestADraftThatDoesNotMatchItsOwnMailSaysSo(t *testing.T) {
	out := checkDraftedRule(map[string]any{
		"name": "Cafe", "sender": "someone-else@example.invalid", "amount_label": "Receipt Total:",
	}, cafeReceipt(), ruleTargets{})
	require.Contains(t, out.Dropped[len(out.Dropped)-1], "does not match this email")
}

func TestADraftedBillRuleNamesOneOfTheHouseholdsProviders(t *testing.T) {
	water := ruleTarget{ID: uuid.New(), Name: "Example Water"}
	checking := ruleTarget{ID: uuid.New(), Name: "Everyday Checking"}
	bill := billmail.Message{
		ID: "<w1@mail.example.invalid>", Sender: "billing@water.example.invalid",
		Subject: "Your water bill is ready", ReceivedAt: time.Date(2026, 9, 30, 7, 0, 0, 0, time.UTC),
		Text: "Account number: 5550 0056 78\nStatement date: September 29, 2026\n" +
			"Amount due: $60.00\nDue date: October 20, 2026",
	}
	targets := ruleTargets{accounts: []ruleTarget{checking}, providers: []ruleTarget{water}}

	out := checkDraftedRule(map[string]any{
		"action": "bill", "bill_provider": "Example Water", "name": "Water bill",
		"sender": "billing@water.example.invalid", "amount_label": "Amount due",
		"date_label": "Due date", "issued_label": "Statement date",
		"reference_label": "Account number",
		// A bill rule posts nothing, so an account the model named anyway is
		// not carried into the draft.
		"account": "Everyday Checking",
	}, bill, targets)
	require.Empty(t, out.Dropped)
	require.Equal(t, store.MailRuleBill, out.Rule.Action)
	require.Equal(t, water.ID, out.Rule.BillConnectionID)
	require.Equal(t, "Statement date", out.Rule.IssuedLabel)
	require.Equal(t, uuid.Nil, out.Rule.AccountID)
	tried := TryRule(out.Rule, bill)
	require.True(t, tried.WouldFile)
	require.Equal(t, "60.00", tried.Amount.String())

	// A provider the household does not have is not guessed at, and neither
	// is an action no rule performs.
	unknown := checkDraftedRule(map[string]any{
		"action": "bill", "bill_provider": "City Gas", "name": "Gas bill",
		"sender": "billing@water.example.invalid", "amount_label": "Amount due",
	}, bill, targets)
	require.Equal(t, store.MailRuleBill, unknown.Rule.Action)
	require.Equal(t, uuid.Nil, unknown.Rule.BillConnectionID)
	require.Contains(t, unknown.Dropped[0], `"City Gas" is not one of this household's bill providers`)

	odd := checkDraftedRule(map[string]any{
		"action": "forward", "name": "Water", "amount_label": "Amount due",
	}, bill, targets)
	require.Equal(t, store.MailRuleTransaction, odd.Rule.Action)
	require.Contains(t, odd.Dropped[0], `"forward" is not something a rule does`)
}

// A card statement: the draft reads the minimum payment, and names the card
// the statement is of — from the household's cards and loans only, so a
// checking account is never offered as a statement's.
func TestADraftedCardStatementRuleReadsTheMinimumAndNamesTheCard(t *testing.T) {
	issuer := ruleTarget{ID: uuid.New(), Name: "Northwind Card"}
	card := ruleTarget{ID: uuid.New(), Name: "Northwind Rewards", Kind: domain.KindCreditCard}
	checking := ruleTarget{ID: uuid.New(), Name: "Everyday Checking", Kind: domain.KindCash}
	statement := billmail.Message{
		ID: "<nw1@mail.example.invalid>", Sender: "statements@alerts.northwind-card.example",
		Subject: "Your Northwind Card statement is ready", ReceivedAt: time.Date(2026, 9, 24, 6, 2, 0, 0, time.UTC),
		Text: "Account ending in 1234\nStatement Balance: $1,250.00\nMinimum Payment Due: $40.00\n" +
			"Payment Due Date: 10/21/2026",
	}
	targets := ruleTargets{accounts: []ruleTarget{checking, card}, providers: []ruleTarget{issuer}}
	draft := map[string]any{
		"action": "bill", "bill_provider": "Northwind Card", "name": "Northwind statement",
		"sender": "statements@alerts.northwind-card.example", "amount_label": "Statement Balance:",
		"minimum_label": "Minimum Payment Due:", "date_label": "Payment Due Date:",
		"reference_label": "Account ending in", "statement_account": "Northwind Rewards",
	}

	out := checkDraftedRule(draft, statement, targets)
	require.Empty(t, out.Dropped)
	require.Equal(t, "Minimum Payment Due:", out.Rule.MinimumLabel)
	require.Equal(t, card.ID, out.StatementAccountID)
	tried := TryRule(out.Rule, statement)
	require.True(t, tried.HasMinimum)
	require.Equal(t, "40.00", tried.MinimumDue.String())
	require.Equal(t, "1250.00", tried.Amount.String())

	draft["statement_account"] = "Everyday Checking"
	out = checkDraftedRule(draft, statement, targets)
	require.Equal(t, uuid.Nil, out.StatementAccountID)
	require.Contains(t, out.Dropped[0], "card or loan accounts")
}

// A drafted name is read the way every model-given name is: case, accents and
// punctuation aside, and by its words when no name is exactly it, but never
// guessed between two targets.
func TestADraftedTargetNameResolvesToOneTargetOrNone(t *testing.T) {
	dining := ruleTarget{ID: uuid.New(), Name: "Food & Dining › Restaurants"}
	coffee := ruleTarget{ID: uuid.New(), Name: "Food & Dining › Coffee Shops"}
	targets := []ruleTarget{dining, coffee}
	var dropped []string
	drop := func(format string, args ...any) { dropped = append(dropped, fmt.Sprintf(format, args...)) }

	require.Equal(t, dining.ID, pickTarget("food dining restaurants", targets, "category", drop))
	require.Equal(t, coffee.ID, pickTarget("Coffee", targets, "category", drop))
	require.Empty(t, dropped)

	require.Equal(t, uuid.Nil, pickTarget("Food", targets, "category", drop))
	require.Equal(t, uuid.Nil, pickTarget("Groceries", targets, "category", drop))
	require.Equal(t, []string{
		`more than one category is called "Food", so none was chosen`,
		`"Groceries" is not one of this household's categories names, so no category was chosen`,
	}, dropped)
}

func TestADraftedRuleIsNeverOfferedUncategorized(t *testing.T) {
	spaceID := newSpace(t)
	newCategory(t, spaceID, "Uncategorized", uuid.Nil)
	dining := newCategory(t, spaceID, "Dining", uuid.Nil)

	_, categories, err := (&Mailbox{store: db(t)}).ruleTargets(t.Context(), spaceID)
	require.NoError(t, err)
	var offered []uuid.UUID
	for _, one := range categories {
		offered = append(offered, one.ID)
		require.NotEqual(t, "Uncategorized", one.Name)
	}
	require.Contains(t, offered, dining.ID)
}
