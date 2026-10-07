package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Drafting a mail rule from one mail. The model is shown names, not ids (an
// invented id would look like a real one). Every field is checked as the
// editor's save checks it, unknown names are dropped, and the draft is tried
// against its mail. Nothing is saved; a person reviews it in the editor.

type MailRuleSuggestion struct {
	// Rule is unsaved: no id, enabled, and the action the model proposed.
	Rule store.MailRule
	// Dropped explains, for the reviewer, each part of the answer left out.
	Dropped []string
	Sample  billmail.Message
	// StatementAccountID is the account a card or loan statement is of, for
	// the billed account's link. Not part of the rule.
	StatementAccountID uuid.UUID
}

// ModelError is the model failing or answering nothing usable: an upstream
// problem rather than the request's.
type ModelError struct{ Err error }

func (e *ModelError) Error() string { return e.Err.Error() }

func (e *ModelError) Unwrap() error { return e.Err }

const suggestTextLimit = 8000

const fieldLimit = 200

func (m *Mailbox) SuggestMailRule(
	ctx context.Context, spaceID store.SpaceID, logID uuid.UUID,
) (MailRuleSuggestion, error) {
	if m.Model == nil {
		return MailRuleSuggestion{}, ErrAssistantUnavailable
	}
	model, err := m.Model(ctx, spaceID)
	if err != nil {
		return MailRuleSuggestion{}, err
	}
	fetched, err := m.FetchLogged(ctx, spaceID, logID)
	if err != nil {
		return MailRuleSuggestion{}, err
	}
	accounts, categories, err := m.ruleTargets(ctx, spaceID)
	if err != nil {
		return MailRuleSuggestion{}, err
	}
	providers, err := m.billTargets(ctx, spaceID)
	if err != nil {
		return MailRuleSuggestion{}, err
	}

	reply, err := model.Complete(ctx, []provider.ChatMessage{
		{Role: "system", Content: suggestRulePrompt + "\n\n" + UntrustedMailRule},
		{Role: "user", Content: fmt.Sprintf(
			"Accounts (use one of these names exactly): %s\n"+
				"Card and loan accounts (a statement_account is one of these): %s\n"+
				"Categories (use one of these names exactly): %s\n"+
				"Bill providers (use one of these names exactly): %s\n\n"+
				"Draft a mail rule for emails like this one:\n\n%s",
			quotedList(names(accounts)), quotedList(names(statementTargets(accounts))),
			quotedList(names(categories)),
			quotedList(names(providers)), MailAsData(fetched.Message, suggestTextLimit))},
	}, nil)
	if err != nil {
		return MailRuleSuggestion{}, &ModelError{Err: fmt.Errorf("the model could not draft a rule: %w", err)}
	}
	object, ok := firstJSONObject(reply.Content)
	if !ok {
		return MailRuleSuggestion{}, &ModelError{Err: fmt.Errorf("the model did not answer with a rule")}
	}
	var drafted map[string]any
	if err := json.Unmarshal([]byte(object), &drafted); err != nil {
		return MailRuleSuggestion{}, &ModelError{Err: fmt.Errorf("the model's rule was not readable JSON")}
	}
	out := checkDraftedRule(drafted, fetched.Message, ruleTargets{
		accounts: accounts, categories: categories, providers: providers,
	})
	out.Sample = fetched.Message
	return out, nil
}

const suggestRulePrompt = `You draft a mail rule for a personal finance app. A mail rule turns
an email like a receipt into a transaction, or files an email that is a bill as a bill on one
of the household's bill providers. Answer with only one JSON object, no prose:

{
  "action": "transaction, or bill when the email is a bill from one of the bill providers listed",
  "bill_provider": "for a bill, one bill provider name from the list; otherwise null",
  "name": "short name for the rule",
  "sender": "the sender's full address, or @domain for any address there",
  "subject_contains": "text every such email's subject contains, or empty",
  "body_contains": "text every such email's body contains, or empty",
  "amount_label": "the words right before the amount, e.g. Total:",
  "amount_pattern": "a regular expression whose first group is the amount, only if no label works",
  "date_label": "the words right before the date, or empty",
  "date_pattern": "",
  "reference_label": "the words right before a receipt or order number, or empty",
  "reference_pattern": "",
  "issued_label": "for a bill, the words right before the statement date, or empty",
  "issued_pattern": "",
  "minimum_label": "for a card or loan statement, the words right before the minimum payment, or empty",
  "minimum_pattern": "",
  "statement_account": "for a card or loan statement, the card or loan account name from that list the statement is of, or null",
  "payee": "fixed payee name, or empty",
  "payee_label": "the words right before the merchant name, or empty",
  "notes_label": "for a receipt that lists what was bought, the words on the line just above the list, or empty",
  "notes_end_label": "the words on the line just below that list, e.g. Subtotal, or empty",
  "direction": "expense or income",
  "account": "one account name from the list, or null",
  "category": "one category name from the list, or null",
  "pad_income": false,
  "income_account": null,
  "income_category": null,
  "income_payee": ""
}

Labels must be copied exactly from the email. Leave a field empty rather than guess.
pad_income is only for something paid by deduction from a paycheck.
For a bill, amount_label is the words before the amount due, date_label the words before the due
date, and reference_label the words before the account number; leave account, category, direction
and the income fields out, because a bill rule posts no transaction.
For a credit card or loan statement, amount_label is the words before the statement balance (or
new balance), minimum_label the words before the minimum payment due, date_label the words before
the payment due date, and statement_account the card or loan it is the statement of.`

type ruleTarget struct {
	ID   uuid.UUID
	Name string
	// Kind is set only for an account.
	Kind domain.AccountKind
}

func statementTargets(accounts []ruleTarget) []ruleTarget {
	var out []ruleTarget
	for _, one := range accounts {
		if domain.HasStatement(one.Kind) {
			out = append(out, one)
		}
	}
	return out
}

type ruleTargets struct {
	accounts   []ruleTarget
	categories []ruleTarget
	providers  []ruleTarget
}

func names(targets []ruleTarget) []string {
	out := make([]string, 0, len(targets))
	for _, one := range targets {
		out = append(out, one.Name)
	}
	return out
}

func quotedList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// ruleTargets is the household's open accounts and assignable categories,
// named "Parent > Child" below the top level.
func (m *Mailbox) ruleTargets(
	ctx context.Context, spaceID store.SpaceID,
) ([]ruleTarget, []ruleTarget, error) {
	accounts, err := m.store.ListAccounts(ctx, spaceID, store.AccountQuery{})
	if err != nil {
		return nil, nil, err
	}
	var accountTargets []ruleTarget
	for _, one := range accounts {
		if one.IsClosed || one.IsDeleted {
			continue
		}
		accountTargets = append(accountTargets, ruleTarget{ID: one.ID, Name: one.Name, Kind: one.Kind})
	}
	categories, err := m.store.ListCategories(ctx, spaceID, false)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[uuid.UUID]store.Category, len(categories))
	for _, one := range categories {
		byID[one.ID] = one
	}
	var categoryTargets []ruleTarget
	for _, one := range categories {
		if !one.IsUserAssignable {
			continue
		}
		name := one.Name
		if parent, ok := byID[one.ParentID]; ok && one.ParentID != uuid.Nil {
			name = parent.Name + " > " + one.Name
		}
		categoryTargets = append(categoryTargets, ruleTarget{ID: one.ID, Name: name})
	}
	return accountTargets, categoryTargets, nil
}

// billTargets names each bill connection by company, plus the login's own
// name where it has one.
func (m *Mailbox) billTargets(ctx context.Context, spaceID store.SpaceID) ([]ruleTarget, error) {
	connections, err := m.store.ListBillConnections(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	out := make([]ruleTarget, 0, len(connections))
	for _, one := range connections {
		out = append(out, ruleTarget{ID: one.ID, Name: one.Title()})
	}
	return out, nil
}

// checkDraftedRule keeps what of the model's answer a rule could be saved
// with, and says what it left out.
func checkDraftedRule(
	drafted map[string]any, sample billmail.Message, targets ruleTargets,
) MailRuleSuggestion {
	out := MailRuleSuggestion{Rule: store.MailRule{
		Enabled: true, Action: store.MailRuleTransaction, Direction: store.MailRuleExpense,
	}}
	rule := &out.Rule
	drop := func(format string, args ...any) {
		out.Dropped = append(out.Dropped, fmt.Sprintf(format, args...))
	}
	field := func(key string) string {
		switch value := drafted[key].(type) {
		case nil:
			return ""
		case string:
			return value
		default:
			drop("%s was not text, so it was left out", key)
			return ""
		}
	}
	text := func(field, value string) string {
		value = strings.TrimSpace(value)
		if len([]rune(value)) > fieldLimit {
			drop("%s was longer than a rule keeps, so it was left out", field)
			return ""
		}
		return value
	}

	rule.Name = text("the name", field("name"))
	rule.Sender = strings.ToLower(text("the sender", field("sender")))
	if rule.Sender != "" && !strings.Contains(rule.Sender, "@") {
		drop("the sender %q is not an address or @domain, so it was left out", rule.Sender)
		rule.Sender = ""
	}
	rule.SubjectContains = text("the subject phrase", field("subject_contains"))
	rule.BodyContains = text("the body phrase", field("body_contains"))
	rule.AmountLabel = text("the amount label", field("amount_label"))
	rule.DateLabel = text("the date label", field("date_label"))
	rule.ReferenceLabel = text("the reference label", field("reference_label"))
	rule.Payee = text("the payee", field("payee"))
	rule.PayeeLabel = text("the payee label", field("payee_label"))
	rule.IncomePayee = text("the income payee", field("income_payee"))
	for _, pattern := range []struct {
		field string
		value string
		into  *string
	}{
		{"amount pattern", field("amount_pattern"), &rule.AmountPattern},
		{"date pattern", field("date_pattern"), &rule.DatePattern},
		{"reference pattern", field("reference_pattern"), &rule.ReferencePattern},
		{"statement date pattern", field("issued_pattern"), &rule.IssuedPattern},
		{"minimum payment pattern", field("minimum_pattern"), &rule.MinimumPattern},
	} {
		value := text("the "+pattern.field, pattern.value)
		if value == "" {
			continue
		}
		if err := billmail.CheckPattern(value); err != nil {
			drop("the %s was not a pattern a rule can use (%v), so it was left out", pattern.field, err)
			continue
		}
		*pattern.into = value
	}

	switch action := strings.ToLower(strings.TrimSpace(field("action"))); action {
	case "", store.MailRuleTransaction:
	case store.MailRuleBill:
		rule.Action = store.MailRuleBill
	default:
		drop("%q is not something a rule does; the draft posts a transaction", action)
	}

	if rule.Action == store.MailRuleBill {
		rule.IssuedLabel = text("the statement date label", field("issued_label"))
		rule.MinimumLabel = text("the minimum payment label", field("minimum_label"))
		out.StatementAccountID = pickTarget(
			field("statement_account"), statementTargets(targets.accounts), "card or loan account", drop)
		rule.BillConnectionID = pickTarget(field("bill_provider"), targets.providers, "bill provider", drop)
		if rule.BillConnectionID == uuid.Nil && strings.TrimSpace(field("bill_provider")) == "" {
			drop("the draft names no bill provider; choose the one this bill is from")
		}
	} else {
		switch direction := strings.ToLower(strings.TrimSpace(field("direction"))); direction {
		case "", store.MailRuleExpense:
		case store.MailRuleIncome:
			rule.Direction = store.MailRuleIncome
		default:
			drop("%q is not a direction; the draft posts an expense", direction)
		}

		rule.NotesLabel = text("the notes label", field("notes_label"))
		rule.NotesEndLabel = text("the notes end label", field("notes_end_label"))
		rule.AccountID = pickTarget(field("account"), targets.accounts, "account", drop)
		rule.CategoryID = pickTarget(field("category"), targets.categories, "category", drop)
		if pad, _ := drafted["pad_income"].(bool); pad {
			rule.PadIncome = true
			rule.IncomeAccountID = pickTarget(field("income_account"), targets.accounts, "income account", drop)
			rule.IncomeCategoryID = pickTarget(field("income_category"), targets.categories, "income category", drop)
		}
	}

	tried := TryRule(*rule, sample)
	switch {
	case !tried.Matched:
		drop("as drafted, the rule does not match this email; check the sender and phrases")
	case rule.AmountLabel == "" && rule.AmountPattern == "":
		drop("the draft names nowhere to read the amount from; add an amount label")
	case tried.Error != "":
		drop("as drafted, the rule cannot read this email: %s", tried.Error)
	}
	return out
}

// pickTarget is the target a drafted name means, read by domain.ResolveName
// like every other name a model gives. Only one match is used.
func pickTarget(named string, targets []ruleTarget, what string, drop func(string, ...any)) uuid.UUID {
	named = strings.TrimSpace(named)
	if named == "" || strings.EqualFold(named, "null") {
		return uuid.Nil
	}
	candidates := make([]domain.NameCandidate, 0, len(targets))
	for _, one := range targets {
		candidates = append(candidates, domain.NameCandidate{ID: one.ID.String(), Name: one.Name})
	}
	resolved := domain.ResolveName(named, candidates)
	switch {
	case resolved.Match != nil:
		return uuid.MustParse(resolved.Match.ID)
	case resolved.Ambiguous():
		drop("more than one %s is called %q, so none was chosen", what, named)
	default:
		drop("%q is not one of this household's %s names, so no %s was chosen",
			named, pluralOf(what), what)
	}
	return uuid.Nil
}

func pluralOf(what string) string {
	if strings.HasSuffix(what, "y") {
		return strings.TrimSuffix(what, "y") + "ies"
	}
	return what + "s"
}
