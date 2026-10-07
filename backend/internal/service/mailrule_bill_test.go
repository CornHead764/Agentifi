package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// A household's rule that files a bill: it lands on the row a pull of the same
// cycle lands on, whichever arrives first, a linked reminder follows it,
// nothing is posted to the register, and a mail with no statement is printed
// as one. Every address, message-id, account number and figure here is
// invented.

// printer is a stand-in for the browser's print path: it keeps each page it
// was handed and answers a PDF, or the failure it was told to.
type printer struct {
	pages []string
	fail  error
}

func (p *printer) print(page string) ([]byte, error) {
	p.pages = append(p.pages, page)
	if p.fail != nil {
		return nil, p.fail
	}
	return []byte("%PDF-1.7\n% the mail, printed\n"), nil
}

// mailRuleFixture is a bridged provider with one billed account, a mailbox, and
// the reader over both.
type mailRuleFixture struct {
	bridgeFixture
	mailbox *Mailbox
	inbox   *store.EmailConnection
	reader  *stubMailbox
	printer *printer
}

func newMailRuleFixture(t *testing.T, batches ...[]provider.MailMessage) mailRuleFixture {
	t.Helper()
	bridge := newBridgeFixture(t, domain.BillerAlliant)
	cipher, err := store.NewCipher("the-credential-key")
	require.NoError(t, err)
	sealed := db(t).WithCipher(cipher)

	inbox := &store.EmailConnection{
		Label: "the bills mailbox", Kind: store.EmailKindIMAP,
		Address: "bills@example.invalid", Host: "imap.example.invalid", Port: 993,
		Username: "bills@example.invalid", Folder: "Inbox", Enabled: true,
	}
	require.NoError(t, sealed.CreateEmailConnection(t.Context(), bridge.space, inbox))
	require.NoError(t, sealed.SaveEmailSecret(t.Context(), bridge.space, inbox.ID, "abcd efgh ijkl mnop"))

	reader := &stubMailbox{batches: batches}
	printed := &printer{}
	mailbox := NewMailbox(sealed)
	mailbox.Bills = bridge.bills
	mailbox.Documents = bridge.bills.Documents
	mailbox.Print = printed.print
	mailbox.Now = func() time.Time { return time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC) }
	mailbox.Open = func(store.EmailConnection, string) (provider.Mailbox, error) { return reader, nil }
	return mailRuleFixture{
		bridgeFixture: bridge, mailbox: mailbox, inbox: inbox, reader: reader, printer: printed,
	}
}

func (f mailRuleFixture) poll(t *testing.T) MailPollResult {
	t.Helper()
	result, err := f.mailbox.Poll(t.Context(), f.space, f.inbox.ID)
	require.NoError(t, err)
	require.Empty(t, result.Error)
	return result
}

func (f mailRuleFixture) log(t *testing.T) []store.BillEmail {
	t.Helper()
	rows, err := db(t).ListBillEmails(t.Context(), f.space, f.inbox.ID, 50)
	require.NoError(t, err)
	return rows
}

func (f mailRuleFixture) filed(t *testing.T) []store.Bill {
	t.Helper()
	rows, err := db(t).ListBills(t.Context(), f.space, f.subaccount.ID)
	require.NoError(t, err)
	return rows
}

// billRule is the water-bill rule, filing on the fixture's connection.
func (f mailRuleFixture) billRule(t *testing.T, edit ...func(*store.MailRule)) store.MailRule {
	t.Helper()
	rule := store.MailRule{
		Name: "Utility bill", Enabled: true, Action: store.MailRuleBill,
		Direction: store.MailRuleExpense, Sender: "@utility.example.invalid",
		SubjectContains: "bill is ready", AmountLabel: "Amount due", DateLabel: "Due date",
		IssuedLabel: "Statement date", ReferenceLabel: "Account number",
		BillConnectionID: f.connection.ID,
	}
	for _, one := range edit {
		one(&rule)
	}
	require.NoError(t, db(t).CreateMailRule(t.Context(), f.space, &rule))
	return rule
}

// utilityBill is the provider's bill-ready mail, HTML only, carrying the
// figures and no attachment.
func utilityBill(id, due, amount string) provider.MailMessage {
	return provider.MailMessage{
		ID: id, ProviderID: "1", Sender: "billing@utility.example.invalid",
		Subject:    "Your bill is ready",
		ReceivedAt: time.Date(2026, time.October, 2, 14, 0, 0, 0, time.UTC),
		HTML: `<html><body><img src="https://pixel.tracker.example.invalid/open.gif">
<table><tr><td>Account number:</td><td>5550 0056 78</td></tr>
<tr><td>Statement date:</td><td>October 2, 2026</td></tr>
<tr><td>Amount due:</td><td>$` + amount + `</td></tr>
<tr><td>Due date:</td><td>` + due + `</td></tr></table></body></html>`,
	}
}

func TestABillRuleFilesTheBillAndPostsNothing(t *testing.T) {
	fixture := newMailRuleFixture(t,
		[]provider.MailMessage{utilityBill("<u1@mail.example.invalid>", "October 26, 2026", "120.00")})
	rule := fixture.billRule(t)

	result := fixture.poll(t)
	require.Equal(t, 1, result.Bills)
	require.Zero(t, result.Rules, "a bill rule files a bill, it does not post")

	// On the provider's one billed account, as the mail's own bill.
	bills := fixture.filed(t)
	require.Len(t, bills, 1)
	require.Equal(t, domain.MustFromString("120.00"), bills[0].AmountDue)
	require.Equal(t, on(2026, time.October, 26), bills[0].DueOn)
	require.Equal(t, on(2026, time.October, 2), bills[0].IssuedOn)
	require.Equal(t, store.BillSourceEmail, bills[0].Source)
	require.Equal(t, "<u1@mail.example.invalid>", bills[0].ExternalID)

	log := fixture.log(t)
	require.Len(t, log, 1)
	require.Equal(t, store.EmailOutcomeBill, log[0].Outcome)
	require.Equal(t, rule.ID, log[0].RuleID)
	require.Equal(t, bills[0].ID, log[0].BillID)
	require.Equal(t, domain.BillerAlliant, log[0].Biller)
	require.Equal(t, uuid.Nil, log[0].TransactionID)
	require.Contains(t, log[0].Note, `Rule "Utility bill": filed a bill of 120.00 due 2026-10-26 on Main account`)

	posted, err := db(t).ListTransactions(t.Context(), fixture.space, store.TransactionQuery{})
	require.NoError(t, err)
	require.Empty(t, posted, "the payment arrives on the bank sync, not from the mail")

	// No second connection and no second billed account were made for it.
	connections, err := db(t).ListBillConnections(t.Context(), fixture.space)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	subaccounts, err := db(t).ListBillSubaccounts(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Len(t, subaccounts, 1)
}

func TestAMailedBillAndAPulledBillForOneCycleAreOneBill(t *testing.T) {
	// October's bill is mailed first and pulled after; November's is pulled
	// first and mailed after. Either way round, one cycle is one row.
	fixture := newMailRuleFixture(t,
		[]provider.MailMessage{utilityBill("<oct@mail.example.invalid>", "October 26, 2026", "120.00")},
		[]provider.MailMessage{utilityBill("<nov@mail.example.invalid>", "November 26, 2026", "145.00")},
	)
	fixture.billRule(t)
	november := pulledStatement("stmt-nov", "2026-11-26", "140.00")
	november["minimum_due"] = "35.00"
	fixture.agent.answers = []map[string]any{
		okPull(fakeBillSession, pulledStatement("stmt-oct", "2026-10-26", "120.00")),
		okPull(fakeBillSession, pulledStatement("stmt-oct", "2026-10-26", "120.00"), november),
	}

	fixture.poll(t)
	_, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	bills := fixture.filed(t)
	require.Len(t, bills, 1, "the pull of the mailed cycle updated the mailed bill")
	require.Equal(t, store.BillSourceProvider, bills[0].Source)
	require.Equal(t, "stmt-oct", bills[0].ExternalID)
	require.Equal(t, on(2026, time.October, 2), bills[0].IssuedOn,
		"the pull states no issue date, so the mail's stands")

	_, err = fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Len(t, fixture.filed(t), 2)
	fixture.poll(t)

	byDue := map[domain.Date]store.Bill{}
	for _, one := range fixture.filed(t) {
		byDue[one.DueOn] = one
	}
	require.Len(t, byDue, 2, "the mail of the pulled cycle landed on the pulled bill")
	pulled := byDue[on(2026, time.November, 26)]
	require.Equal(t, store.BillSourceProvider, pulled.Source, "the provider's bill stays the provider's")
	require.Equal(t, domain.MustFromString("140.00"), pulled.AmountDue, "the provider's amount wins")
	require.Equal(t, "stmt-nov", pulled.ExternalID)
	require.True(t, pulled.HasMinimumDue)
	require.Equal(t, domain.MustFromString("35.00"), pulled.MinimumDue)
	require.Equal(t, on(2026, time.October, 2), pulled.IssuedOn, "the mail fills what the pull left empty")
	require.Equal(t, domain.BillSuperseded, byDue[on(2026, time.October, 26)].Status)
}

func TestAMailNeverReopensAPaidBill(t *testing.T) {
	for _, source := range []string{store.BillSourceProvider, store.BillSourceEmail} {
		t.Run(source, func(t *testing.T) {
			fixture := newMailRuleFixture(t,
				[]provider.MailMessage{utilityBill("<again@mail.example.invalid>", "October 26, 2026", "120.00")})
			fixture.billRule(t)
			paid := store.Bill{
				SubaccountID: fixture.subaccount.ID, DueOn: on(2026, time.October, 26),
				AmountDue: domain.MustFromString("120.00"), Currency: "USD",
				Status: domain.BillPaid, Source: source, ExternalID: "first-statement",
				FetchedAt: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC),
			}
			require.NoError(t, db(t).UpsertBill(t.Context(), fixture.space, &paid, false))

			fixture.poll(t)
			bills := fixture.filed(t)
			require.Len(t, bills, 1)
			require.Equal(t, domain.BillPaid, bills[0].Status)
		})
	}
}

func TestARuleFiledBillKeepsItsReminderCurrentAsAPulledOneWould(t *testing.T) {
	fixture := newMailRuleFixture(t,
		[]provider.MailMessage{utilityBill("<u1@mail.example.invalid>", "October 26, 2026", "120.00")})
	fixture.billRule(t)
	series := newSeries(t, fixture.space, newAccount(t, fixture.space, "Everyday Checking"),
		seriesNextDueOn(on(2026, time.October, 24)))
	_, err := fixture.bills.Link(t.Context(), fixture.space, series.ID, fixture.subaccount.ID)
	require.NoError(t, err)

	fixture.poll(t)

	connects, err := fixture.bills.BillConnectFor(t.Context(), fixture.space, nil)
	require.NoError(t, err)
	linked := connects[domain.ID(series.ID.String())]
	require.Len(t, linked, 1, "the linked reminder reads the mailed bill")
	connect := linked[0]
	require.Equal(t, on(2026, time.October, 26), connect.DueOn)
	require.Equal(t, "-120.00", connect.Amount.String())
}

func TestABillRuleFilesOnTheBilledAccountItPinsAndRefusesAnotherProvidersAccount(t *testing.T) {
	fixture := newMailRuleFixture(t,
		[]provider.MailMessage{utilityBill("<u1@mail.example.invalid>", "October 26, 2026", "120.00")},
		[]provider.MailMessage{utilityBill("<u2@mail.example.invalid>", "November 26, 2026", "99.00")},
	)
	second := &store.BillSubaccount{
		ConnectionID: fixture.connection.ID, ExternalID: "premise-8", Label: "the shed",
		MaskedNumber: "••••9999", IsSelected: true,
	}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), fixture.space, second))
	rule := fixture.billRule(t, func(rule *store.MailRule) { rule.BillSubaccountID = second.ID })

	fixture.poll(t)
	pinned, err := db(t).ListBills(t.Context(), fixture.space, second.ID)
	require.NoError(t, err)
	require.Len(t, pinned, 1, "the pinned account, whatever number the mail carried")
	require.Empty(t, fixture.filed(t))

	// A billed account at some other provider is not the rule's to file on.
	elsewhere := &store.BillConnection{
		Biller: domain.BillerSpectrum, Label: "the office", CredentialSource: store.BillCredentialSession,
		AutopayRule: domain.AutopayNone,
	}
	require.NoError(t, db(t).CreateBillConnection(t.Context(), fixture.space, elsewhere))
	theirs := &store.BillSubaccount{ConnectionID: elsewhere.ID, ExternalID: "line-1", Label: "Internet"}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), fixture.space, theirs))
	rule.BillSubaccountID = theirs.ID
	require.NoError(t, db(t).UpdateMailRule(t.Context(), fixture.space, &rule))

	result := fixture.poll(t)
	require.Equal(t, 1, result.Failed)
	log := fixture.log(t)
	require.Equal(t, store.EmailOutcomeFailed, log[0].Outcome)
	require.Contains(t, log[0].Note, "a billed account that is not Main account's")
}

func TestAMailedBillWithNoAttachmentIsFiledWithTheMailPrinted(t *testing.T) {
	fixture := newMailRuleFixture(t,
		[]provider.MailMessage{utilityBill("<u1@mail.example.invalid>", "October 26, 2026", "120.00")})
	fixture.billRule(t)
	fixture.poll(t)

	require.Len(t, fixture.printer.pages, 1)
	page := fixture.printer.pages[0]
	require.Contains(t, page, "Amount due:")
	require.Contains(t, page, "billing@utility.example.invalid", "the header says who sent it")
	require.NotContains(t, page, "tracker.example.invalid", "the tracking pixel is not on the page")

	log := fixture.log(t)
	require.NotEqual(t, uuid.Nil, log[0].DocumentID)
	bills := fixture.filed(t)
	require.Equal(t, log[0].DocumentID, bills[0].DocumentID, "the printed mail is the bill's statement")
	require.NotContains(t, log[0].Note, "PDF")
}

func TestAMailedBillWithAPDFIsFiledWithThePDFAndNotPrinted(t *testing.T) {
	mail := utilityBill("<u1@mail.example.invalid>", "October 26, 2026", "120.00")
	mail.Attachments = []provider.MailAttachment{{
		Filename: "statement.pdf", ContentType: provider.MailAttachmentContentType, Bytes: storetest.PDF(),
	}}
	fixture := newMailRuleFixture(t, []provider.MailMessage{mail})
	fixture.billRule(t)
	fixture.poll(t)

	require.Empty(t, fixture.printer.pages, "a mail that brought its statement is not printed")
	log := fixture.log(t)
	require.NotEqual(t, uuid.Nil, log[0].DocumentID)
	require.Equal(t, log[0].DocumentID, fixture.filed(t)[0].DocumentID)
}

func TestAMailThatWillNotPrintIsStillFiledAsABill(t *testing.T) {
	fixture := newMailRuleFixture(t,
		[]provider.MailMessage{utilityBill("<u1@mail.example.invalid>", "October 26, 2026", "120.00")})
	fixture.printer.fail = errors.New("browser: Chrome would not launch")
	fixture.billRule(t)

	result := fixture.poll(t)
	require.Equal(t, 1, result.Bills)
	require.Zero(t, result.Failed)
	require.Len(t, fixture.filed(t), 1)

	log := fixture.log(t)
	require.Equal(t, store.EmailOutcomeBill, log[0].Outcome)
	require.Equal(t, uuid.Nil, log[0].DocumentID)
	require.Contains(t, log[0].Note, "the mail could not be saved as a PDF: browser: Chrome would not launch")
}

func TestABuiltInParsersBillIsFiledWithTheMailPrintedToo(t *testing.T) {
	fixture := newMailRuleFixture(t, []provider.MailMessage{
		alliantMail("<a1@mail.example.invalid>", time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC)),
	})
	fixture.poll(t)

	require.Len(t, fixture.printer.pages, 1)
	log := fixture.log(t)
	require.Equal(t, store.EmailOutcomeBill, log[0].Outcome)
	require.Equal(t, uuid.Nil, log[0].RuleID)
	require.NotEqual(t, uuid.Nil, log[0].DocumentID)
}

func TestAMailThatMayCarryACodeIsNeverPrinted(t *testing.T) {
	fixture := newMailRuleFixture(t)
	_, err := fixture.mailbox.printMail(billmail.Message{
		ID: "<c1@mail.example.invalid>", Sender: "noreply@myaccount.alliantenergy.com",
		Subject: "Your security code", ReceivedAt: time.Date(2026, time.October, 2, 7, 0, 0, 0, time.UTC),
		Text: "Your security code is 481902. Your bill of $120.00 is ready.",
	})
	require.ErrorIs(t, err, errMailNotPrinted)
	require.Empty(t, fixture.printer.pages)
}

func TestATransactionRuleStillPostsAndPadsIncome(t *testing.T) {
	fixture := newMailRuleFixture(t, []provider.MailMessage{{
		ID: "<r1@mail.example.invalid>", Sender: "receipts@cafe.example.invalid",
		Subject: "Cafe Receipt", ReceivedAt: time.Date(2026, time.September, 28, 13, 0, 0, 0, time.UTC),
		Text: "Your receipt from Some Cafe\nReceipt Date: 9/27/26\nReceipt Total: $4.50\nReceiptID: AB1234567\n",
	}})
	checking := newAccount(t, fixture.space, "Everyday Checking")
	dining := newCategory(t, fixture.space, "Dining Out", uuid.Nil)
	wages := newCategory(t, fixture.space, "Wages", uuid.Nil)
	rule := store.MailRule{
		Name: "Lunch", Enabled: true, Action: store.MailRuleTransaction,
		Direction: store.MailRuleExpense, Sender: "@cafe.example.invalid",
		AmountLabel: "Receipt Total", DateLabel: "Receipt Date", ReferenceLabel: "ReceiptID",
		PayeeLabel: "Your receipt from", AccountID: checking.ID, CategoryID: dining.ID,
		PadIncome: true, IncomeCategoryID: wages.ID,
	}
	require.NoError(t, db(t).CreateMailRule(t.Context(), fixture.space, &rule))

	result := fixture.poll(t)
	require.Equal(t, 1, result.Rules)
	require.Zero(t, result.Bills)
	require.Empty(t, fixture.printer.pages, "a receipt posted as a transaction is not a bill's statement")

	posted, err := db(t).ListTransactions(t.Context(), fixture.space, store.TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, posted, 2)
	amounts := map[string]string{}
	for _, one := range posted {
		amounts[one.Amount.String()] = one.Payee
		require.False(t, one.NeedsSettle, "a mailed row is settled like a synced one")
		require.True(t, one.HasBalance, "a mailed row has its running balance")
	}
	require.Equal(t, map[string]string{
		"-4.50": "Some Cafe", "4.50": "Some Cafe (paycheck deduction)",
	}, amounts)

	log := fixture.log(t)
	require.Equal(t, store.EmailOutcomeRule, log[0].Outcome)
	require.NotEqual(t, uuid.Nil, log[0].TransactionID)
	for _, one := range posted {
		if one.Amount.IsPositive() {
			require.Equal(t, log[0].TransactionID, one.PaddedTxnID, "the pad names the purchase it pads")
		} else {
			require.Equal(t, uuid.Nil, one.PaddedTxnID)
		}
	}
	require.Equal(t, uuid.Nil, log[0].BillID)
}

// The editor's preview is the write: every row the poll posts, field for
// field, is one the try box listed, including the day a mail that states no
// date is posted on and the notes the rule keeps.
func TestARulesPreviewIsWhatItPosts(t *testing.T) {
	mail := provider.MailMessage{
		ID: "<r2@mail.example.invalid>", Sender: "receipts@cafe.example.invalid",
		Subject: "Cafe Receipt", ReceivedAt: time.Date(2026, time.September, 28, 13, 0, 0, 0, time.UTC),
		Text: "Your receipt from Some Cafe\nReceipt Total: $4.50\nReceiptID: AB7654321\n\n" +
			"Items\nToast   1   2.50\nTea     1   2.00\n\nThank you\n",
	}
	fixture := newMailRuleFixture(t, []provider.MailMessage{mail})
	checking := newAccount(t, fixture.space, "Everyday Checking")
	payroll := newAccount(t, fixture.space, "Payroll")
	dining := newCategory(t, fixture.space, "Dining Out", uuid.Nil)
	wages := newCategory(t, fixture.space, "Wages", uuid.Nil)
	rule := store.MailRule{
		Name: "Lunch", Enabled: true, Action: store.MailRuleTransaction,
		Direction: store.MailRuleExpense, Sender: "@cafe.example.invalid",
		AmountLabel: "Receipt Total", ReferenceLabel: "ReceiptID",
		PayeeLabel: "Your receipt from", AccountID: checking.ID, CategoryID: dining.ID,
		PadIncome: true, IncomeAccountID: payroll.ID, IncomeCategoryID: wages.ID,
		NotesLabel: "Items",
	}
	require.NoError(t, db(t).CreateMailRule(t.Context(), fixture.space, &rule))

	tried := TryRule(rule, mailMessage(mail))
	require.Empty(t, tried.Error)
	require.Equal(t, "AB7654321\nToast 1 2.50\nTea 1 2.00", tried.Notes)
	require.Equal(t, []TryPosting{
		{AccountID: checking.ID, Date: on(2026, time.September, 28), Amount: domain.MustFromString("-4.50"),
			Payee: "Some Cafe", CategoryID: dining.ID},
		{AccountID: payroll.ID, Date: on(2026, time.September, 28), Amount: domain.MustFromString("4.50"),
			Payee: "Some Cafe (paycheck deduction)", CategoryID: wages.ID},
	}, tried.WouldPost)

	require.Equal(t, 1, fixture.poll(t).Rules)
	posted, err := db(t).ListTransactions(t.Context(), fixture.space, store.TransactionQuery{})
	require.NoError(t, err)
	written := make([]TryPosting, 0, len(posted))
	for _, one := range posted {
		require.Equal(t, tried.Notes, one.Notes)
		written = append(written, TryPosting{
			AccountID: one.AccountID, Date: one.Date, Amount: one.Amount,
			Payee: one.Payee, CategoryID: one.CategoryID,
		})
	}
	require.ElementsMatch(t, tried.WouldPost, written)
}

func TestTryingABillRuleReadsTheBillAndPostsNothing(t *testing.T) {
	rule := store.MailRule{
		Name: "Utility bill", Action: store.MailRuleBill, Sender: "@utility.example.invalid",
		AmountLabel: "Amount due", DateLabel: "Due date", IssuedLabel: "Statement date",
		ReferenceLabel: "Account number", BillConnectionID: uuid.New(),
	}
	mail := mailMessage(utilityBill("<u1@mail.example.invalid>", "October 26, 2026", "120.00"))
	tried := TryRule(rule, mail)
	require.True(t, tried.Matched)
	require.True(t, tried.WouldFile)
	require.Empty(t, tried.Error)
	require.Equal(t, "120.00", tried.Amount.String())
	require.Equal(t, on(2026, time.October, 26), tried.Date)
	require.Equal(t, on(2026, time.October, 2), tried.IssuedOn)
	require.Equal(t, "5550005678", tried.Account)
	require.Empty(t, tried.WouldPost)
}

func TestTheEmailOnlyProviderIsNeverPulled(t *testing.T) {
	// Even with the switch on and a credential source that would make any
	// other connection due, there is nothing to pull it with.
	bridge := newBridgeFixture(t, domain.BillerAlliant)
	emailed := &store.BillConnection{
		Biller: domain.BillerEmailOnly, Label: "Example Water",
		CredentialSource: store.BillCredentialTyped, AutopayRule: domain.AutopayNone, PullEnabled: true,
	}
	require.NoError(t, db(t).CreateBillConnection(t.Context(), bridge.space, emailed))
	bridge.agent.answers = []map[string]any{okPull(fakeBillSession)}

	bridge.bills.PullDue(t.Context(), time.Date(2026, time.October, 3, 0, 0, 0, 0, time.UTC))
	stored, err := db(t).GetBillConnection(t.Context(), bridge.space, emailed.ID)
	require.NoError(t, err)
	require.Nil(t, stored.LastPulledAt, "the scheduler passed it by")
}
