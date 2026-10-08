package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
)

// The household's own rules for mail no parser knows. The match and
// extraction half is billmail.Rule, pure and testable against a string; the
// action half (account, category, padding, provider) stays here so billmail
// never becomes a second place transactions or bills are built.

type MailRule struct {
	ID      uuid.UUID
	SpaceID SpaceID
	// Name is unique per space and quoted in the message log's note.
	Name    string
	Enabled bool

	// Sender is an address in full, "@domain", or "" for any; the contains
	// fields are case-insensitive substrings.
	Sender          string
	SubjectContains string
	BodyContains    string

	// A label reads what follows it; a pattern's first capture group is the
	// value.
	AmountLabel      string
	AmountPattern    string
	DateLabel        string
	DatePattern      string
	ReferenceLabel   string
	ReferencePattern string
	IssuedLabel      string
	IssuedPattern    string
	MinimumLabel     string
	MinimumPattern   string
	Payee            string
	PayeeLabel       string
	// NotesLabel and NotesEndLabel bound the stretch of the mail kept in a
	// posted transaction's notes; see billmail.Rule.
	NotesLabel    string
	NotesEndLabel string

	Action     string
	AccountID  uuid.UUID
	CategoryID uuid.UUID
	Direction  string
	// BillConnectionID is the provider a bill rule files on; BillSubaccountID
	// pins the billed account, and when nil the account number read chooses.
	BillConnectionID uuid.UUID
	BillSubaccountID uuid.UUID

	// PadIncome posts an equal and opposite second row, for something paid by
	// deduction from a paycheck that arrives net of it.
	PadIncome bool
	// IncomeAccountID is nil-valued for the expense's own account.
	IncomeAccountID  uuid.UUID
	IncomeCategoryID uuid.UUID
	IncomePayee      string

	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// The mail_rules.action values.
const (
	MailRuleTransaction = "transaction"
	// MailRuleBill files a bill on a provider's billed account and posts
	// nothing.
	MailRuleBill = "bill"
)

// The mail_rules.direction values.
const (
	MailRuleExpense = "expense"
	MailRuleIncome  = "income"
)

func (r MailRule) Rule() billmail.Rule {
	return billmail.Rule{
		Name: r.Name, Sender: r.Sender, SubjectContains: r.SubjectContains,
		BodyContains: r.BodyContains, AmountLabel: r.AmountLabel,
		AmountPattern: r.AmountPattern, DateLabel: r.DateLabel, DatePattern: r.DatePattern,
		ReferenceLabel: r.ReferenceLabel, ReferencePattern: r.ReferencePattern,
		IssuedLabel: r.IssuedLabel, IssuedPattern: r.IssuedPattern,
		MinimumLabel: r.MinimumLabel, MinimumPattern: r.MinimumPattern,
		Payee: r.Payee, PayeeLabel: r.PayeeLabel,
		NotesLabel: r.NotesLabel, NotesEndLabel: r.NotesEndLabel,
	}
}

// PadAccountID is where the padded income row lands: the rule's own income
// account, else the expense's.
func (r MailRule) PadAccountID() uuid.UUID {
	if r.IncomeAccountID != uuid.Nil {
		return r.IncomeAccountID
	}
	return r.AccountID
}

const mailRuleColumns = `id, space_id, name, enabled, sender, subject_contains, body_contains,
	amount_label, amount_pattern, date_label, date_pattern, reference_label, reference_pattern,
	payee, payee_label, action, account_id, category_id, direction, pad_income,
	income_account_id, income_category_id, income_payee, bill_connection_id, bill_subaccount_id,
	issued_label, issued_pattern, minimum_label, minimum_pattern, notes_label, notes_end_label,
	sort_order, created_at, updated_at`

func scanMailRule(row scanner) (MailRule, error) {
	var (
		one            MailRule
		spaceID        uuid.UUID
		account        *uuid.UUID
		category       *uuid.UUID
		incomeAccount  *uuid.UUID
		incomeCategory *uuid.UUID
		billConnection *uuid.UUID
		billSubaccount *uuid.UUID
	)
	err := row.Scan(&one.ID, &spaceID, &one.Name, &one.Enabled, &one.Sender,
		&one.SubjectContains, &one.BodyContains, &one.AmountLabel, &one.AmountPattern,
		&one.DateLabel, &one.DatePattern, &one.ReferenceLabel, &one.ReferencePattern,
		&one.Payee, &one.PayeeLabel, &one.Action, &account, &category, &one.Direction,
		&one.PadIncome, &incomeAccount, &incomeCategory, &one.IncomePayee,
		&billConnection, &billSubaccount, &one.IssuedLabel, &one.IssuedPattern,
		&one.MinimumLabel, &one.MinimumPattern, &one.NotesLabel, &one.NotesEndLabel, &one.SortOrder,
		&one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return one, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	for _, pair := range []struct {
		from *uuid.UUID
		to   *uuid.UUID
	}{
		{account, &one.AccountID}, {category, &one.CategoryID},
		{incomeAccount, &one.IncomeAccountID}, {incomeCategory, &one.IncomeCategoryID},
		{billConnection, &one.BillConnectionID}, {billSubaccount, &one.BillSubaccountID},
	} {
		if pair.from != nil {
			*pair.to = *pair.from
		}
	}
	return one, nil
}

// ListMailRules is the space's rules in the order the reader tries them.
func (s *Store) ListMailRules(ctx context.Context, spaceID SpaceID) ([]MailRule, error) {
	return queryAll(ctx, s.db, "store: list mail rules", scanMailRule,
		`SELECT `+mailRuleColumns+` FROM mail_rules
		  WHERE space_id = $1 ORDER BY sort_order, name`, spaceID.UUID())
}

func (s *Store) GetMailRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MailRule, error) {
	one, err := scanMailRule(s.db.QueryRow(ctx,
		`SELECT `+mailRuleColumns+` FROM mail_rules WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id))
	return one, wrap("store: get mail rule", err)
}

func (s *Store) CreateMailRule(ctx context.Context, spaceID SpaceID, one *MailRule) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	err := s.db.QueryRow(ctx,
		`INSERT INTO mail_rules
		     (id, space_id, name, enabled, sender, subject_contains, body_contains,
		      amount_label, amount_pattern, date_label, date_pattern, reference_label,
		      reference_pattern, payee, payee_label, action, account_id, category_id,
		      direction, pad_income, income_account_id, income_category_id, income_payee,
		      sort_order, bill_connection_id, bill_subaccount_id, issued_label, issued_pattern,
		      minimum_label, minimum_pattern, notes_label, notes_end_label)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		         $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
		         $31, $32)
		 RETURNING created_at, updated_at`,
		one.ID, spaceID.UUID(), one.Name, one.Enabled, one.Sender, one.SubjectContains,
		one.BodyContains, one.AmountLabel, one.AmountPattern, one.DateLabel, one.DatePattern,
		one.ReferenceLabel, one.ReferencePattern, one.Payee, one.PayeeLabel, one.Action,
		dbconv.NullUUID(one.AccountID), dbconv.NullUUID(one.CategoryID), one.Direction, one.PadIncome,
		dbconv.NullUUID(one.IncomeAccountID), dbconv.NullUUID(one.IncomeCategoryID), one.IncomePayee,
		one.SortOrder, dbconv.NullUUID(one.BillConnectionID), dbconv.NullUUID(one.BillSubaccountID),
		one.IssuedLabel, one.IssuedPattern, one.MinimumLabel, one.MinimumPattern,
		one.NotesLabel, one.NotesEndLabel).
		Scan(&one.CreatedAt, &one.UpdatedAt)
	return wrap("store: create mail rule", err)
}

func (s *Store) UpdateMailRule(ctx context.Context, spaceID SpaceID, one *MailRule) error {
	return s.execOne(ctx, "store: update mail rule",
		`UPDATE mail_rules
		    SET name = $3, enabled = $4, sender = $5, subject_contains = $6,
		        body_contains = $7, amount_label = $8, amount_pattern = $9, date_label = $10,
		        date_pattern = $11, reference_label = $12, reference_pattern = $13,
		        payee = $14, payee_label = $15, action = $16, account_id = $17,
		        category_id = $18, direction = $19, pad_income = $20,
		        income_account_id = $21, income_category_id = $22, income_payee = $23,
		        sort_order = $24, bill_connection_id = $25, bill_subaccount_id = $26,
		        issued_label = $27, issued_pattern = $28, minimum_label = $29,
		        minimum_pattern = $30, notes_label = $31, notes_end_label = $32,
		        updated_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), one.ID, one.Name, one.Enabled, one.Sender, one.SubjectContains,
		one.BodyContains, one.AmountLabel, one.AmountPattern, one.DateLabel, one.DatePattern,
		one.ReferenceLabel, one.ReferencePattern, one.Payee, one.PayeeLabel, one.Action,
		dbconv.NullUUID(one.AccountID), dbconv.NullUUID(one.CategoryID), one.Direction, one.PadIncome,
		dbconv.NullUUID(one.IncomeAccountID), dbconv.NullUUID(one.IncomeCategoryID), one.IncomePayee,
		one.SortOrder, dbconv.NullUUID(one.BillConnectionID), dbconv.NullUUID(one.BillSubaccountID),
		one.IssuedLabel, one.IssuedPattern, one.MinimumLabel, one.MinimumPattern,
		one.NotesLabel, one.NotesEndLabel)
}

// DeleteMailRule keeps the log rows it wrote; their rule_id nulls on its key.
func (s *Store) DeleteMailRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: delete mail rule",
		`DELETE FROM mail_rules WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}
