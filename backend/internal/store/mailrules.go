package store

import (
	"context"
	"time"

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

const mailRuleColumns = `id, name, enabled, sender, subject_contains, body_contains,
	amount_label, amount_pattern, date_label, date_pattern, reference_label, reference_pattern,
	payee, payee_label, action, account_id, category_id, direction, pad_income,
	income_account_id, income_category_id, income_payee, bill_connection_id, bill_subaccount_id,
	issued_label, issued_pattern, minimum_label, minimum_pattern, notes_label, notes_end_label,
	sort_order, created_at, updated_at`

func scanMailRule(row scanner) (MailRule, error) {
	var (
		one              MailRule
		created, updated int64
		account          *uuid.UUID
		category         *uuid.UUID
		incomeAccount    *uuid.UUID
		incomeCategory   *uuid.UUID
		billConnection   *uuid.UUID
		billSubaccount   *uuid.UUID
	)
	err := row.Scan(&one.ID, &one.Name, &one.Enabled, &one.Sender,
		&one.SubjectContains, &one.BodyContains, &one.AmountLabel, &one.AmountPattern,
		&one.DateLabel, &one.DatePattern, &one.ReferenceLabel, &one.ReferencePattern,
		&one.Payee, &one.PayeeLabel, &one.Action, &account, &category, &one.Direction,
		&one.PadIncome, &incomeAccount, &incomeCategory, &one.IncomePayee,
		&billConnection, &billSubaccount, &one.IssuedLabel, &one.IssuedPattern,
		&one.MinimumLabel, &one.MinimumPattern, &one.NotesLabel, &one.NotesEndLabel, &one.SortOrder,
		&created, &updated)
	if err != nil {
		return one, err
	}
	one.CreatedAt = fromUnixMillis(created)
	one.UpdatedAt = fromUnixMillis(updated)
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
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	rows, err := sqliteQueryAll(ctx, db.Read(), "store: list mail rules", scanMailRule,
		`SELECT `+mailRuleColumns+` FROM mail_rules
		  ORDER BY sort_order, name`)
	for i := range rows {
		rows[i].SpaceID = spaceID
	}
	return rows, err
}

func (s *Store) GetMailRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) (MailRule, error) {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return MailRule{}, err
	}
	one, err := scanMailRule(db.Read().QueryRowContext(ctx,
		`SELECT `+mailRuleColumns+` FROM mail_rules WHERE id = ?`, id))
	one.SpaceID = spaceID
	return one, wrap("store: get mail rule", err)
}

func (s *Store) CreateMailRule(ctx context.Context, spaceID SpaceID, one *MailRule) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	now := db.Now()
	var created, updated int64
	err = db.Write().QueryRowContext(ctx,
		`INSERT INTO mail_rules
		     (id, name, enabled, sender, subject_contains, body_contains,
		      amount_label, amount_pattern, date_label, date_pattern, reference_label,
		      reference_pattern, payee, payee_label, action, account_id, category_id,
		      direction, pad_income, income_account_id, income_category_id, income_payee,
		      sort_order, bill_connection_id, bill_subaccount_id, issued_label, issued_pattern,
		      minimum_label, minimum_pattern, notes_label, notes_end_label, created_at, updated_at)
		 VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15,
		         ?16, ?17, ?18, ?19, ?20, ?21, ?22, ?23, ?24, ?25, ?26, ?27, ?28, ?29,
		         ?30, ?31, ?32, ?32)
		 RETURNING created_at, updated_at`,
		one.ID, one.Name, one.Enabled, one.Sender, one.SubjectContains,
		one.BodyContains, one.AmountLabel, one.AmountPattern, one.DateLabel, one.DatePattern,
		one.ReferenceLabel, one.ReferencePattern, one.Payee, one.PayeeLabel, one.Action,
		NullUUID(one.AccountID), NullUUID(one.CategoryID), one.Direction, one.PadIncome,
		NullUUID(one.IncomeAccountID), NullUUID(one.IncomeCategoryID), one.IncomePayee,
		one.SortOrder, NullUUID(one.BillConnectionID), NullUUID(one.BillSubaccountID),
		one.IssuedLabel, one.IssuedPattern, one.MinimumLabel, one.MinimumPattern,
		one.NotesLabel, one.NotesEndLabel, now).
		Scan(&created, &updated)
	if err == nil {
		one.CreatedAt = fromUnixMillis(created)
		one.UpdatedAt = fromUnixMillis(updated)
	}
	return wrap("store: create mail rule", err)
}

func (s *Store) UpdateMailRule(ctx context.Context, spaceID SpaceID, one *MailRule) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: update mail rule",
		`UPDATE mail_rules
		    SET name = ?2, enabled = ?3, sender = ?4, subject_contains = ?5,
		        body_contains = ?6, amount_label = ?7, amount_pattern = ?8, date_label = ?9,
		        date_pattern = ?10, reference_label = ?11, reference_pattern = ?12,
		        payee = ?13, payee_label = ?14, action = ?15, account_id = ?16,
		        category_id = ?17, direction = ?18, pad_income = ?19,
		        income_account_id = ?20, income_category_id = ?21, income_payee = ?22,
		        sort_order = ?23, bill_connection_id = ?24, bill_subaccount_id = ?25,
		        issued_label = ?26, issued_pattern = ?27, minimum_label = ?28,
		        minimum_pattern = ?29, notes_label = ?30, notes_end_label = ?31,
		        updated_at = ?32
		  WHERE id = ?1`,
		one.ID, one.Name, one.Enabled, one.Sender, one.SubjectContains,
		one.BodyContains, one.AmountLabel, one.AmountPattern, one.DateLabel, one.DatePattern,
		one.ReferenceLabel, one.ReferencePattern, one.Payee, one.PayeeLabel, one.Action,
		NullUUID(one.AccountID), NullUUID(one.CategoryID), one.Direction, one.PadIncome,
		NullUUID(one.IncomeAccountID), NullUUID(one.IncomeCategoryID), one.IncomePayee,
		one.SortOrder, NullUUID(one.BillConnectionID), NullUUID(one.BillSubaccountID),
		one.IssuedLabel, one.IssuedPattern, one.MinimumLabel, one.MinimumPattern,
		one.NotesLabel, one.NotesEndLabel, db.Now())
}

// DeleteMailRule keeps the log rows it wrote; their rule_id nulls on its key.
func (s *Store) DeleteMailRule(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	db, err := s.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: delete mail rule",
		`DELETE FROM mail_rules WHERE id = ?`, id)
}
