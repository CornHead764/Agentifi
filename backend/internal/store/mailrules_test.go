package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Mail rules against a real Postgres. Every address, label and figure here is
// invented.

func newMailRule(t *testing.T, spaceID SpaceID, name string, sort int) *MailRule {
	t.Helper()
	account := newAccount(t, spaceID, "Everyday Checking "+name)
	category := newCategory(t, spaceID, "Dining "+name, domain.CategoryExpense)
	rule := &MailRule{
		Name: name, Enabled: true, Sender: "@example.invalid",
		SubjectContains: "Cafe Receipt", AmountLabel: "Receipt Total",
		DateLabel: "Receipt Date", ReferenceLabel: "ReceiptID",
		PayeeLabel: "Your receipt from", NotesLabel: "Receipt Total",
		Action: MailRuleTransaction, AccountID: account.ID, CategoryID: category.ID,
		Direction: MailRuleExpense, SortOrder: sort,
	}
	require.NoError(t, db(t).CreateMailRule(t.Context(), spaceID, rule))
	return rule
}

func TestAMailRuleRoundTripsWhole(t *testing.T) {
	space := newSpace(t)
	rule := newMailRule(t, space, "Lunch", 0)
	income := newCategory(t, space, "Payroll padding", domain.CategoryIncome)

	stored, err := db(t).GetMailRule(t.Context(), space, rule.ID)
	require.NoError(t, err)
	require.Equal(t, "Receipt Total", stored.AmountLabel)
	require.Equal(t, "Your receipt from", stored.PayeeLabel)
	require.Equal(t, "Receipt Total", stored.NotesLabel)
	require.Empty(t, stored.NotesEndLabel)
	require.Equal(t, rule.AccountID, stored.AccountID)
	require.Equal(t, MailRuleExpense, stored.Direction)
	require.False(t, stored.PadIncome)
	require.Equal(t, rule.AccountID, stored.PadAccountID(),
		"a rule with no income account pads in the expense's own")

	// The match and extraction half is what billmail reads, and nothing about
	// accounts reaches it.
	require.Equal(t, "Cafe Receipt", stored.Rule().SubjectContains)

	stored.PadIncome = true
	stored.IncomeCategoryID = income.ID
	stored.IncomePayee = "Lunch deduction"
	stored.NotesEndLabel = "ReceiptID"
	stored.Enabled = false
	require.NoError(t, db(t).UpdateMailRule(t.Context(), space, &stored))

	again, err := db(t).GetMailRule(t.Context(), space, rule.ID)
	require.NoError(t, err)
	require.True(t, again.PadIncome)
	require.Equal(t, income.ID, again.IncomeCategoryID)
	require.Equal(t, "Lunch deduction", again.IncomePayee)
	require.Equal(t, "ReceiptID", again.NotesEndLabel)
	require.Equal(t, "ReceiptID", again.Rule().NotesEndLabel)
	require.False(t, again.Enabled)

	require.NoError(t, db(t).DeleteMailRule(t.Context(), space, rule.ID))
	_, err = db(t).GetMailRule(t.Context(), space, rule.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, db(t).DeleteMailRule(t.Context(), space, rule.ID), ErrNotFound)
}

func TestMailRulesAreListedInTheOrderTheReaderTriesThem(t *testing.T) {
	space := newSpace(t)
	newMailRule(t, space, "Lunch", 2)
	newMailRule(t, space, "Parking", 1)
	newMailRule(t, space, "Bookshop", 1)

	rules, err := db(t).ListMailRules(t.Context(), space)
	require.NoError(t, err)
	names := make([]string, 0, len(rules))
	for _, rule := range rules {
		names = append(names, rule.Name)
	}
	require.Equal(t, []string{"Bookshop", "Parking", "Lunch"}, names)
}

func TestOneMailRuleNameIsOneRule(t *testing.T) {
	// The name is what the message log quotes, so it must identify one rule.
	space, stranger := newSpace(t), newSpace(t)
	first := newMailRule(t, space, "Lunch", 0)

	second := *first
	second.ID = uuid.Nil
	require.Error(t, db(t).CreateMailRule(t.Context(), space, &second))

	// The same name in another household is another household's business.
	elsewhere := newMailRule(t, stranger, "Lunch", 0)
	_, err := db(t).GetMailRule(t.Context(), space, elsewhere.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, db(t).DeleteMailRule(t.Context(), space, elsewhere.ID), ErrNotFound)
}
