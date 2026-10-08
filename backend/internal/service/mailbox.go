package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The mailbox reader: the poll, what it makes of each message, and the code
// answerer a parked sign-in is finished by.
//
// A mailed bill goes through Bills.Ingest (source 'email', external_id the
// message-id), so it lands on the same row a pull would. A one-time code never
// reaches the database, and message bodies are not stored; a bill that is only its
// message is printed to a PDF and filed as its statement.

type Mailbox struct {
	base
	store *store.Store
	// Bills is also whose challenges the code answerer finishes.
	Bills *Bills
	// Documents is where a mailed statement is filed. Nil skips the files.
	Documents *Documents
	// Print turns HTML into a PDF with nothing fetched or run, for a bill whose
	// only statement is the mail itself. Nil files no printed statement.
	Print  func(page string) ([]byte, error)
	Alerts *Alerts
	// Ingest is what a row a mail rule posts goes through once it is written.
	Ingest Ingest
	// Relays are the household's SMS-to-email forwarders. A code from anywhere
	// not in this list and not a provider's own sender is not a code.
	Relays []string
	// Forwarders are addresses whose forwarded mail is read as the mail they
	// forwarded. Any address at the watched mailbox's own domain counts without
	// being listed; nobody else's "From:" line is believed.
	Forwarders []string
	// Lookback is how far back a first read goes.
	Lookback time.Duration
	// PollEvery is the scheduler's interval, which is also what "due" means.
	PollEvery time.Duration
	// OTPWait is how long the answerer watches for a code before giving the
	// challenge back to a person.
	OTPWait time.Duration
	Log     *slog.Logger
	Now     func() time.Time

	// Open builds the reader for one connection; a field so tests can supply a
	// mailbox without a mail server.
	Open   func(connection store.EmailConnection, secret string) (provider.Mailbox, error)
	SignIn provider.GraphDeviceSignIn
	// Model is asked for a code the deterministic reader could not find while a
	// sign-in waits on one. Nil, or no model configured, is the reader alone.
	Model func(ctx context.Context, spaceID store.SpaceID) (MailModel, error)
}

func NewMailbox(st *store.Store) *Mailbox {
	return &Mailbox{base: newBase(st), store: st}
}

const DefaultMailLookback = 30 * 24 * time.Hour

// DefaultOTPWait is shorter than the engine's twenty-minute sign-in reaper,
// so an unanswered challenge reaches a person while its sign-in is alive.
const DefaultOTPWait = 3 * time.Minute

const otpPollEvery = 15 * time.Second

func (m *Mailbox) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now().UTC()
}

func (m *Mailbox) log() *slog.Logger {
	if m.Log == nil {
		return slog.Default()
	}
	return m.Log
}

func (m *Mailbox) lookback() time.Duration {
	if m.Lookback > 0 {
		return m.Lookback
	}
	return DefaultMailLookback
}

type MailboxSignIn struct {
	SessionID       string
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
}

const (
	MailboxSignInPending  = "pending"
	MailboxSignInSignedIn = "signed_in"
	MailboxSignInFailed   = "failed"
	MailboxSignInExpired  = "expired"
)

// mailboxSignIns is every device-code sign-in this process is waiting on.
// Package-level because the request that starts one and the one that polls it
// use different Mailbox values; a restart just means signing in again.
var mailboxSignIns expiring[string, *mailboxSignInState]

// mailboxSignInState is bound to the space and connection that started it, so
// a poll from another space or connection is told the sign-in does not exist.
type mailboxSignInState struct {
	spaceID      store.SpaceID
	connectionID uuid.UUID

	mu        sync.Mutex
	state     string
	err       string
	expiresAt time.Time
}

// mailboxSignInGrace keeps a finished sign-in readable after its code runs
// out, so the dialog's last poll still gets the answer.
const mailboxSignInGrace = 10 * time.Minute

func (s *mailboxSignInState) read() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == MailboxSignInPending && !s.expiresAt.IsZero() && time.Now().After(s.expiresAt) {
		s.state = MailboxSignInExpired
	}
	return s.state, s.err
}

func (s *mailboxSignInState) set(state, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.err = state, err
}

// StartGraphSignIn asks Microsoft for a device code and waits in a goroutine
// with its own context: the starting request is gone long before the person
// finishes, and cancelling with it would make the sign-in unfinishable.
func (m *Mailbox) StartGraphSignIn(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (MailboxSignIn, error) {
	connection, err := m.store.GetEmailConnection(ctx, spaceID, connectionID)
	if err != nil {
		return MailboxSignIn{}, err
	}
	if connection.Kind != store.EmailKindGraph {
		return MailboxSignIn{}, errors.New(
			"mailbox: only an Office 365 mailbox signs in with a device code")
	}
	code, err := m.SignIn.Start(ctx, connection.ClientID, connection.Tenant)
	if err != nil {
		return MailboxSignIn{}, err
	}

	session := uuid.NewString()
	state := &mailboxSignInState{
		spaceID: spaceID, connectionID: connectionID,
		state: MailboxSignInPending, expiresAt: code.ExpiresAt,
	}
	mailboxSignIns.Put(session, state, code.ExpiresAt.Add(mailboxSignInGrace))

	go func() {
		background, cancel := context.WithDeadline(context.Background(),
			code.ExpiresAt.Add(time.Minute))
		defer cancel()
		refreshToken, _, err := code.Poll(background)
		if err != nil {
			// Microsoft's own wording, shown on the card; it is never a credential.
			state.set(MailboxSignInFailed, err.Error())
			return
		}
		if err := m.store.SaveEmailSecret(background, spaceID, connectionID, refreshToken); err != nil {
			state.set(MailboxSignInFailed, "the mailbox signed in and the token could not be stored")
			m.log().Error("mailbox: sealing a refresh token", "error", err)
			return
		}
		state.set(MailboxSignInSignedIn, "")
	}()

	return MailboxSignIn{
		SessionID: session, UserCode: code.UserCode,
		VerificationURI: code.VerificationURI, ExpiresAt: code.ExpiresAt,
	}, nil
}

func (m *Mailbox) GraphSignInState(
	spaceID store.SpaceID, connectionID uuid.UUID, session string,
) (string, string) {
	held, ok := mailboxSignIns.Get(session)
	if !ok || held.spaceID != spaceID || held.connectionID != connectionID {
		return MailboxSignInExpired, "that sign-in is no longer in progress"
	}
	state, err := held.read()
	if state != MailboxSignInPending {
		mailboxSignIns.Delete(session)
	}
	return state, err
}

// SetIMAPPassword seals an app password only after the server accepts it, so
// a wrong password never sits in settings looking connected.
func (m *Mailbox) SetIMAPPassword(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, password string,
) error {
	connection, err := m.store.GetEmailConnection(ctx, spaceID, connectionID)
	if err != nil {
		return err
	}
	if connection.Kind != store.EmailKindIMAP {
		return errors.New("mailbox: an Office 365 mailbox signs in with a device code, not a password")
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("mailbox: the app password is empty")
	}
	if err := m.verify(connection, password); err != nil {
		return err
	}
	return m.store.SaveEmailSecret(ctx, spaceID, connectionID, password)
}

func (m *Mailbox) verify(connection store.EmailConnection, password string) error {
	if m.Open != nil {
		mailbox, err := m.Open(connection, password)
		if err != nil {
			return err
		}
		if checker, ok := mailbox.(interface{ Verify(context.Context) error }); ok {
			return checker.Verify(context.Background())
		}
		return nil
	}
	reader := &provider.IMAPMailbox{
		Host: connection.Host, Port: connection.Port,
		Username: textutil.FirstNonBlank(connection.Username, connection.Address),
		Password: password, Folder: connection.Folder,
	}
	return reader.Verify(context.Background())
}

func (m *Mailbox) open(connection store.EmailConnection, secret string) (provider.Mailbox, error) {
	if m.Open != nil {
		return m.Open(connection, secret)
	}
	switch connection.Kind {
	case store.EmailKindGraph:
		return &provider.GraphMailbox{
			ClientID: connection.ClientID, Tenant: connection.Tenant,
			Address: connection.Address, Folder: connection.Folder, RefreshToken: secret,
		}, nil
	case store.EmailKindIMAP:
		return &provider.IMAPMailbox{
			Host: connection.Host, Port: connection.Port,
			Username: textutil.FirstNonBlank(connection.Username, connection.Address),
			Password: secret, Folder: connection.Folder,
		}, nil
	default:
		return nil, fmt.Errorf("mailbox: %q is not a kind of mailbox this build reads", connection.Kind)
	}
}

type MailPollResult struct {
	// Read counts every message handed over, seen or not.
	Read         int
	Bills        int
	Rules        int
	OTPs         int
	Unrecognised int
	Proposed     int
	Failed       int
	Error        string
}

// mailCursorEvery is how often a long batch saves its cursor, so a poll that
// dies halfway does not read everything again.
const mailCursorEvery = 50

// Poll reads one mailbox and acts on everything new. A message the log already
// holds is skipped: reading a code twice would answer it twice.
func (m *Mailbox) Poll(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (MailPollResult, error) {
	connection, err := m.store.GetEmailConnection(ctx, spaceID, connectionID)
	if err != nil {
		return MailPollResult{}, err
	}
	secret, err := m.store.EmailSecret(ctx, spaceID, connectionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return m.stopped(ctx, connection, "this mailbox has not been signed in to yet"), nil
		}
		return m.stopped(ctx, connection,
			"the stored mailbox credential could not be opened"), nil
	}
	reader, err := m.open(connection, secret)
	if err != nil {
		return m.stopped(ctx, connection, err.Error()), nil
	}

	messages, cursor, err := reader.ListNew(ctx, connection.Cursor, m.lookback())
	if rotated, ok := reader.(provider.RotatedSecret); ok && rotated.RotatedSecret() != "" {
		// The refresh token rolled during the poll. Seal it before anything else:
		// the old one will not work again.
		if sealErr := m.store.SaveEmailSecret(
			ctx, spaceID, connectionID, rotated.RotatedSecret()); sealErr != nil {
			m.log().Error("mailbox: re-sealing a rolled refresh token", "error", sealErr)
		}
	}
	if err != nil {
		return m.stopped(ctx, connection, err.Error()), nil
	}

	out := MailPollResult{Read: len(messages)}
	for index, message := range messages {
		if ctx.Err() != nil {
			break
		}
		seen, err := m.store.HasBillEmail(ctx, spaceID, connectionID, message.ID)
		if err != nil {
			return out, err
		}
		if seen {
			continue
		}
		m.handle(ctx, spaceID, connection, message, &out, false)
		if (index+1)%mailCursorEvery == 0 {
			if err := m.store.SaveEmailCursor(ctx, spaceID, connectionID, cursor); err != nil {
				return out, err
			}
		}
	}

	if err := m.store.SaveEmailCursor(ctx, spaceID, connectionID, cursor); err != nil {
		return out, err
	}
	if _, err := m.store.MarkEmailPoll(ctx, spaceID, connectionID, "", m.now()); err != nil {
		return out, err
	}
	if m.Alerts != nil {
		if err := m.Alerts.ResolveCondition(ctx, spaceID, mailboxCondition(connectionID)); err != nil {
			m.log().Error("mailbox: withdrawing a stopped-mailbox alert", "error", err)
		}
	}
	return out, nil
}

func mailboxCondition(connectionID uuid.UUID) string {
	return "email-connection-failed:" + connectionID.String()
}

// handle is what one new message becomes. The order matters: codes first,
// because a provider's code mail and bill mail share a sender and a parser
// would swallow the code; then the household's rules, which deliberately beat
// the built-in parsers; then the parsers. Anything unclaimed is logged as
// unrecognised. replace overwrites the row of a message already logged.
func (m *Mailbox) handle(
	ctx context.Context, spaceID store.SpaceID, connection store.EmailConnection,
	message provider.MailMessage, out *MailPollResult, replace bool,
) {
	parsed := mailMessage(message)
	if m.forwardedByTheHousehold(parsed.Sender, connection.Address) {
		if inner, ok := billmail.UnwrapForward(parsed); ok {
			parsed = inner
		}
	}

	if code, found := m.readCode(ctx, spaceID, parsed); found {
		rememberOwnedCode(spaceID, code, parsed.ReceivedAt, parsed.ID)
		note := "a one-time code, kept in memory for the sign-in waiting on it"
		if merchant, known := domain.MerchantByID(code.owner.merchant); known {
			note = "a one-time code for " + merchant.Name + ", kept in memory for the sign-in waiting on it"
		}
		m.record(ctx, spaceID, connection, parsed, store.BillEmail{
			Biller: code.owner.biller, Outcome: store.EmailOutcomeOTP, Note: note,
		}, replace)
		out.OTPs++
		return
	}

	if m.applyRules(ctx, spaceID, connection, parsed, out, replace) {
		return
	}

	claim, claimed := billmail.Match(parsed)
	if !claimed {
		m.record(ctx, spaceID, connection, parsed, store.BillEmail{
			Outcome: store.EmailOutcomeUnrecognised,
		}, replace)
		out.Unrecognised++
		return
	}

	if len(claim.Bills) == 0 || claim.Proposed {
		document := m.fileLooseDocument(ctx, spaceID, parsed)
		m.record(ctx, spaceID, connection, parsed, store.BillEmail{
			Biller: claim.Biller, Outcome: store.EmailOutcomeProposed,
			Note:       textutil.FirstNonBlank(claim.Note, "the mail is a bill whose figures nothing here could read"),
			DocumentID: document,
		}, replace)
		out.Proposed++
		return
	}

	recorded, document, printNote, err := m.ingest(ctx, spaceID, claim, parsed)
	if err != nil {
		m.record(ctx, spaceID, connection, parsed, store.BillEmail{
			Biller: claim.Biller, Outcome: store.EmailOutcomeFailed, Note: err.Error(),
		}, replace)
		out.Failed++
		return
	}
	m.record(ctx, spaceID, connection, parsed, store.BillEmail{
		Biller: claim.Biller, Outcome: store.EmailOutcomeBill, Note: joinNotes(claim.Note, printNote),
		BillID: recorded, DocumentID: document,
	}, replace)
	out.Bills++
}

// ingest writes every bill one mail carried and files its statement,
// answering the first bill's id, the document's, and a note when the statement
// could not be kept.
func (m *Mailbox) ingest(
	ctx context.Context, spaceID store.SpaceID, claim billmail.Claim, message billmail.Message,
) (uuid.UUID, uuid.UUID, string, error) {
	var first, document uuid.UUID
	var note string
	for _, read := range claim.Bills {
		subaccount, err := m.subaccountFor(ctx, spaceID, claim.Biller, read)
		if err != nil {
			return first, document, note, err
		}
		filed, err := m.fileBill(ctx, spaceID, subaccount, read, message)
		if err != nil {
			return first, document, note, err
		}
		if filed.bill == uuid.Nil {
			continue
		}
		if first == uuid.Nil {
			first = filed.bill
		}
		if filed.document != uuid.Nil {
			document = filed.document
		}
		note = textutil.FirstNonBlank(note, filed.note)
	}
	if first == uuid.Nil {
		return first, document, note, errors.New("the mail named no account this household bills")
	}
	return first, document, note, nil
}

type filedBill struct {
	bill     uuid.UUID
	document uuid.UUID
	note     string
}

// fileBill is the one way a mailed bill is written, by parser or rule: through
// Bills.Ingest onto the row a pull of the same account and due date lands on.
func (m *Mailbox) fileBill(
	ctx context.Context, spaceID store.SpaceID, subaccount store.BillSubaccount,
	read billmail.Bill, message billmail.Message,
) (filedBill, error) {
	result, err := m.Bills.Ingest(ctx, spaceID, subaccount.ID, []store.Bill{{
		DueOn: read.DueOn, AmountDue: read.AmountDue, IssuedOn: read.IssuedOn,
		MinimumDue: read.MinimumDue, HasMinimumDue: read.HasMinimumDue,
		PeriodStart: read.PeriodStart, PeriodEnd: read.PeriodEnd, AutopayOn: read.AutopayOn,
		Status: domain.BillOpen, Source: store.BillSourceEmail, ExternalID: message.ID,
		FetchedAt: m.now(),
	}})
	if err != nil {
		return filedBill{}, err
	}
	stored, held := BillOfCycle(result.Bills, read.DueOn, "")
	if !held {
		return filedBill{}, nil
	}
	document, note := m.fileStatement(ctx, spaceID, message, read, stored)
	return filedBill{bill: stored.ID, document: document, note: joinNotes(note, m.statementNote(ctx, spaceID, result))}, nil
}

// statementNote is the log's line for the statement figures the bill wrote,
// or "" when it wrote none.
func (m *Mailbox) statementNote(ctx context.Context, spaceID store.SpaceID, result IngestResult) string {
	if result.Statement == uuid.Nil {
		return ""
	}
	account, err := m.store.GetAccount(ctx, spaceID, result.Statement)
	if err != nil {
		return "updated the linked account's statement"
	}
	return "updated the statement on " + account.Name
}

// subaccountFor is which billed account a read bill belongs to. A provider the
// household has no connection for gets one made here.
func (m *Mailbox) subaccountFor(
	ctx context.Context, spaceID store.SpaceID, biller domain.BillerID, read billmail.Bill,
) (store.BillSubaccount, error) {
	connections, err := m.store.ListBillConnections(ctx, spaceID)
	if err != nil {
		return store.BillSubaccount{}, err
	}
	var theirs []store.BillConnection
	for _, one := range connections {
		if one.Biller == biller {
			theirs = append(theirs, one)
		}
	}
	if len(theirs) == 0 {
		connection, err := m.connectionFor(ctx, spaceID, biller)
		if err != nil {
			return store.BillSubaccount{}, err
		}
		theirs = []store.BillConnection{connection}
	}
	return m.subaccountAmong(ctx, spaceID, theirs, read)
}

// subaccountAmong matches on the masked number first (one mail can bill
// several premises), then falls back to the only billed account there is.
func (m *Mailbox) subaccountAmong(
	ctx context.Context, spaceID store.SpaceID, theirs []store.BillConnection, read billmail.Bill,
) (store.BillSubaccount, error) {
	last := domain.LastFour(read.MaskedNumber)
	var only store.BillSubaccount
	onlyOne := 0
	for _, connection := range theirs {
		rows, err := m.store.ListBillSubaccounts(ctx, spaceID, connection.ID)
		if err != nil {
			return store.BillSubaccount{}, err
		}
		for _, row := range rows {
			if read.ExternalID != "" && row.ExternalID == read.ExternalID {
				return row, nil
			}
			if last != "" && strings.HasSuffix(row.MaskedNumber, last) {
				return row, nil
			}
			only = row
			onlyOne++
		}
	}
	// The only billed account, but only when nothing contradicts the mail: a
	// different last four is a second premise, not this one.
	if onlyOne == 1 && (last == "" || only.MaskedNumber == "") {
		return only, nil
	}
	if read.ExternalID == "" {
		return store.BillSubaccount{}, fmt.Errorf(
			"the mail names an account this household does not bill (%s)",
			textutil.FirstNonBlank(read.MaskedNumber, "no account number"))
	}
	// An account the connection has never pulled, keyed on the provider's id so a
	// later pull updates this row rather than adding a second.
	row := &store.BillSubaccount{
		ConnectionID: theirs[0].ID, ExternalID: read.ExternalID,
		Label:        textutil.FirstNonBlank(read.Label, read.ExternalID),
		MaskedNumber: read.MaskedNumber, IsSelected: true,
	}
	if err := m.store.UpsertBillSubaccount(ctx, spaceID, row); err != nil {
		return store.BillSubaccount{}, err
	}
	return *row, nil
}

// connectionFor makes the connection a mailed bill arrived without.
// pull_enabled only for a mail-only provider: a browser provider with no
// session would fail every nightly pull.
func (m *Mailbox) connectionFor(
	ctx context.Context, spaceID store.SpaceID, biller domain.BillerID,
) (store.BillConnection, error) {
	known, ok := domain.BillerByID(biller)
	if !ok {
		return store.BillConnection{}, fmt.Errorf("the mail names %q, which this build does not know", biller)
	}
	connection := &store.BillConnection{
		Biller: biller, Label: "Mailbox", CredentialSource: store.BillCredentialSession,
		AutopayRule: domain.AutopayNone, PullEnabled: known.Access == domain.AccessEmail,
	}
	if err := m.store.CreateBillConnection(ctx, spaceID, connection); err != nil {
		return store.BillConnection{}, err
	}
	return *connection, nil
}

// fileStatement puts the mail's own PDF against the bill, or the mail printed
// to one. A document that will not store is a note on the row, not a failed
// message: the figures still matter more than the PDF.
func (m *Mailbox) fileStatement(
	ctx context.Context, spaceID store.SpaceID, message billmail.Message,
	read billmail.Bill, bill store.Bill,
) (uuid.UUID, string) {
	if m.Documents == nil {
		return uuid.Nil, ""
	}
	attachment := read.Document
	if attachment == nil {
		attachment = firstPDF(message)
	}
	if attachment == nil {
		if m.Print == nil {
			return uuid.Nil, ""
		}
		printed, err := m.printMail(message)
		if err != nil {
			m.log().Warn("mailbox: a mailed bill could not be printed", "error", err)
			return uuid.Nil, "the mail could not be saved as a PDF: " + err.Error()
		}
		attachment = printed
	}
	document, err := m.Documents.AttachStatement(ctx, spaceID, bill.ID, DocumentUpload{
		Bytes: attachment.Bytes, Filename: attachment.Filename,
		Source: store.DocumentSourceEmail, SourceRef: message.ID,
	})
	if err != nil {
		m.log().Warn("mailbox: a mailed statement was refused", "error", err)
		return uuid.Nil, ""
	}
	return document.ID, ""
}

// errMailNotPrinted: a mail that may carry a sign-in code is never printed.
var errMailNotPrinted = errors.New("it may carry a sign-in code, so it is not kept")

func (m *Mailbox) printMail(message billmail.Message) (*billmail.Attachment, error) {
	if m.withholds(message) {
		return nil, errMailNotPrinted
	}
	printed, err := m.Print(billmail.Printable(message))
	if err != nil {
		return nil, err
	}
	return &billmail.Attachment{
		Filename:    "email-" + domain.DateOf(message.ReceivedAt).String() + ".pdf",
		ContentType: provider.MailAttachmentContentType, Bytes: printed,
	}, nil
}

// withholds says a message's text is never read back, shown to the model or
// printed, because it may carry a one-time code.
func (m *Mailbox) withholds(message billmail.Message) bool {
	_, code := findOTP(message, m.Relays)
	return code || billmail.IsRelay(message, m.Relays) || billmail.MayCarryCode(message)
}

func joinNotes(parts ...string) string {
	var kept []string
	for _, one := range parts {
		if one = strings.TrimSpace(one); one != "" {
			kept = append(kept, one)
		}
	}
	return strings.Join(kept, "; ")
}

// fileLooseDocument keeps the PDF of a message nothing could read a figure
// from. The bill_emails row is its only link, which the purge excepts.
func (m *Mailbox) fileLooseDocument(
	ctx context.Context, spaceID store.SpaceID, message billmail.Message,
) uuid.UUID {
	attachment := firstPDF(message)
	if attachment == nil || m.Documents == nil {
		return uuid.Nil
	}
	document, _, err := m.Documents.Store(ctx, spaceID, DocumentUpload{
		Bytes: attachment.Bytes, Filename: attachment.Filename,
		Source: store.DocumentSourceEmail, SourceRef: message.ID,
	})
	if err != nil {
		m.log().Warn("mailbox: a mailed document was refused", "error", err)
		return uuid.Nil
	}
	return document.ID
}

// record writes the log row for one message; replace overwrites a previous
// reading's row.
func (m *Mailbox) record(
	ctx context.Context, spaceID store.SpaceID, connection store.EmailConnection,
	message billmail.Message, row store.BillEmail, replace bool,
) {
	row.ConnectionID = connection.ID
	row.MessageID = message.ID
	row.ReceivedAt = message.ReceivedAt
	row.Sender = message.Sender
	row.Subject = message.Subject
	var err error
	if replace {
		err = m.store.ReplaceBillEmail(ctx, spaceID, &row)
	} else {
		_, err = m.store.RecordBillEmail(ctx, spaceID, &row)
	}
	if err != nil {
		m.log().Error("mailbox: recording a message", "connection", connection.Label, "error", err)
	}
}

// stopped records a mailbox that could not be read, and alerts once the
// failing streak is an outage rather than a blip
// (domain.MailboxOutageWorthTelling). The alert is a condition: one per
// streak, withdrawn by the next successful poll.
func (m *Mailbox) stopped(
	ctx context.Context, connection store.EmailConnection, detail string,
) MailPollResult {
	now := m.now()
	streak, err := m.store.MarkEmailPoll(ctx, connection.SpaceID, connection.ID, detail, now)
	if err != nil {
		m.log().Error("mailbox: recording a stopped poll", "error", err)
		return MailPollResult{Error: detail}
	}
	var failingFor time.Duration
	if streak.FailingSince != nil {
		failingFor = now.Sub(*streak.FailingSince)
	}
	if m.Alerts != nil && domain.MailboxOutageWorthTelling(streak.Failures, failingFor) {
		hit := domain.AlertHit{
			Type:  domain.AlertEmailConnectionFailed,
			Title: fmt.Sprintf("%s stopped reading mail", connection.Label),
			Body: fmt.Sprintf("Mailbox read failed: %s. Mailed bills and sign-in codes are "+
				"missed until it works.", alertDetail(detail)),
			URL:       "/settings/email",
			DedupeKey: mailboxCondition(connection.ID), Ongoing: true,
		}
		if _, err := m.Alerts.DeliverToSpace(ctx, connection.SpaceID, hit); err != nil {
			m.log().Error("mailbox: delivering a stopped-mailbox alert", "error", err)
		}
	}
	return MailPollResult{Error: detail}
}

var urlInText = regexp.MustCompile(`https?://([^/\s"']+)[^\s"']*`)

// alertDetail trims a poll error for a notification: a provider error can
// quote a whole request URL, sync token and all. The full error stays on the
// connection.
func alertDetail(detail string) string {
	short := urlInText.ReplaceAllString(detail, "$1")
	if clipped := textutil.Clip(short, 159); len(clipped) < len(short) {
		short = strings.TrimSpace(clipped) + "…"
	}
	return short
}

// PollDue reads every mailbox whose interval has passed, across every space.
func (m *Mailbox) PollDue(ctx context.Context, since time.Time) int {
	return runDue(ctx, m.store, m.log(), dueRun[store.EmailConnection]{
		kind: "mailbox",
		list: func(ctx context.Context) ([]store.EmailConnection, error) {
			return m.store.ListEmailConnectionsDue(ctx, since)
		},
		run: m.pollDue,
	})
}

func (m *Mailbox) pollDue(ctx context.Context, connection store.EmailConnection) bool {
	result, err := m.Poll(ctx, connection.SpaceID, connection.ID)
	switch {
	case err != nil:
		m.log().Error("mailbox: poll failed", "mailbox", connection.Label, "error", err)
	case result.Error != "":
		m.log().Warn("mailbox: poll stopped",
			"mailbox", connection.Label, "detail", result.Error)
	default:
		m.log().Info("mailbox: polled", "mailbox", connection.Label,
			"read", result.Read, "bills", result.Bills, "codes", result.OTPs)
	}
	return true
}

// ErrMailAlreadyRead is a re-read of a message that already produced a bill,
// a rule's transactions or a code; reading it again would duplicate or unpick
// that. The API answers it as a conflict.
var ErrMailAlreadyRead = errors.New("mailbox: this message has already been acted on")

var ErrMailGone = errors.New("mailbox: the mailbox no longer holds that message")

// applyRules hands the message to the first of the household's rules, in
// stored order, that claims it. First match only: two rules over one mail
// would post one receipt twice.
func (m *Mailbox) applyRules(
	ctx context.Context, spaceID store.SpaceID, connection store.EmailConnection,
	message billmail.Message, out *MailPollResult, replace bool,
) bool {
	rules, err := m.store.ListMailRules(ctx, spaceID)
	if err != nil {
		m.log().Error("mailbox: reading the household's mail rules", "error", err)
		return false
	}
	for _, rule := range rules {
		if !rule.Enabled || !rule.Rule().Match(message) {
			continue
		}
		if rule.Action == store.MailRuleBill {
			filed, err := m.fileRuleBill(ctx, spaceID, rule, message)
			if err != nil {
				m.record(ctx, spaceID, connection, message, store.BillEmail{
					Outcome: store.EmailOutcomeFailed, RuleID: rule.ID,
					Note: fmt.Sprintf("Rule %q: %s", rule.Name, err.Error()),
				}, replace)
				out.Failed++
				return true
			}
			m.record(ctx, spaceID, connection, message, store.BillEmail{
				Biller: filed.biller, Outcome: store.EmailOutcomeBill, RuleID: rule.ID,
				BillID: filed.bill, DocumentID: filed.document, Note: filed.note,
			}, replace)
			out.Bills++
			return true
		}
		if rule.Action != store.MailRuleTransaction {
			continue
		}
		note, posted, err := m.postRule(ctx, spaceID, rule, message)
		if err != nil {
			// Not passed on to the parsers: a rule that could not finish is something
			// to fix, not a message to guess at.
			m.record(ctx, spaceID, connection, message, store.BillEmail{
				Outcome: store.EmailOutcomeFailed, RuleID: rule.ID,
				Note: fmt.Sprintf("Rule %q: %s", rule.Name, err.Error()),
			}, replace)
			out.Failed++
			return true
		}
		m.record(ctx, spaceID, connection, message, store.BillEmail{
			Outcome: store.EmailOutcomeRule, RuleID: rule.ID,
			TransactionID: posted, Note: note,
		}, replace)
		out.Rules++
		return true
	}
	return false
}

// postRule writes the transaction a rule reads, plus the padding income row
// for a deduction linked to it, and answers the log note and the expense row's
// id.
func (m *Mailbox) postRule(
	ctx context.Context, spaceID store.SpaceID, rule store.MailRule, message billmail.Message,
) (string, uuid.UUID, error) {
	read, err := rule.Rule().Extract(message)
	if err != nil {
		return "", uuid.Nil, err
	}
	legs := rulePostings(rule, read, message.ReceivedAt)
	account, err := m.ruleAccount(ctx, spaceID, legs[0].AccountID)
	if err != nil {
		return "", uuid.Nil, err
	}
	if err := m.ruleCategory(ctx, spaceID, legs[0].CategoryID); err != nil {
		return "", uuid.Nil, err
	}
	posted, err := m.postOnce(ctx, spaceID, mailRuleRow(legs[0], account, message, read, ""))
	if err != nil {
		return "", uuid.Nil, err
	}

	note := fmt.Sprintf("Rule %q: posted %s to %s as %s",
		rule.Name, read.Amount.Abs(), account.Name, legs[0].Payee)
	if len(legs) == 1 {
		return note, posted, nil
	}

	pad := legs[1]
	padAccount, err := m.ruleAccount(ctx, spaceID, pad.AccountID)
	if err != nil {
		return "", posted, err
	}
	if pad.CategoryID == uuid.Nil {
		return "", posted, errors.New("the rule pads income and names no income category")
	}
	if err := m.ruleCategory(ctx, spaceID, pad.CategoryID); err != nil {
		return "", posted, err
	}
	padded, err := m.postOnce(ctx, spaceID, mailRuleRow(pad, padAccount, message, read, "income"))
	if err != nil {
		return "", posted, err
	}
	if err := m.store.LinkPadding(ctx, spaceID, padded, posted); err != nil {
		return "", posted, err
	}
	return note + "; income padded", posted, nil
}

// rulePostings is shared by postRule and TryRule so the preview cannot say
// something the write does not do.
func rulePostings(rule store.MailRule, read billmail.Extraction, receivedAt time.Time) []TryPosting {
	day := domain.DateOf(receivedAt)
	if read.HasDate {
		day = read.Date
	}
	payee := textutil.FirstNonBlank(read.Payee, rule.Name)
	amount := read.Amount.Abs()
	if rule.Direction != store.MailRuleIncome {
		amount = amount.Neg()
	}
	out := []TryPosting{{
		AccountID: rule.AccountID, Date: day, Amount: amount,
		Payee: payee, CategoryID: rule.CategoryID,
	}}
	if rule.PadIncome {
		// The paycheck is already net of this, so the expense alone would make
		// the month look cheaper than it was.
		out = append(out, TryPosting{
			AccountID: rule.PadAccountID(), Date: day, Amount: read.Amount.Abs(),
			Payee:      textutil.FirstNonBlank(rule.IncomePayee, payee+" (paycheck deduction)"),
			CategoryID: rule.IncomeCategoryID,
		})
	}
	return out
}

// mailRuleRow uses the message's external id, so the unique index on
// (account, external id) makes a re-read land on the row already there.
func mailRuleRow(
	leg TryPosting, account store.Account, message billmail.Message, read billmail.Extraction, suffix string,
) *store.Transaction {
	row := &store.Transaction{
		AccountID: account.ID, ExternalID: mailExternalID(message.ID, suffix),
		Date: leg.Date, Amount: leg.Amount, Currency: account.Currency,
		StatementName: leg.Payee, Payee: leg.Payee, Memo: message.Subject,
		Notes: ruleNotes(read), CategoryID: leg.CategoryID,
		Source: domain.SourceEmail, IsReviewed: account.Kind.BornReviewed(),
		NeedsSettle: true,
	}
	ApplyEffectiveDate(row, account, domain.Date{}, false)
	return row
}

// ruleNotes is the reference, then on the lines below it the stretch of the
// mail the rule keeps.
func ruleNotes(read billmail.Extraction) string {
	var kept []string
	for _, one := range []string{read.Reference, read.Section} {
		if one != "" {
			kept = append(kept, one)
		}
	}
	return strings.Join(kept, "\n")
}

type ruleFiledBill struct {
	biller   domain.BillerID
	bill     uuid.UUID
	document uuid.UUID
	note     string
}

// fileRuleBill files a bill on the rule's provider and nothing else: the
// payment is the bank's to report, and pairs with this bill like a pulled one.
func (m *Mailbox) fileRuleBill(
	ctx context.Context, spaceID store.SpaceID, rule store.MailRule, message billmail.Message,
) (ruleFiledBill, error) {
	read, err := rule.Rule().ExtractBill(message)
	if err != nil {
		return ruleFiledBill{}, err
	}
	connection, err := m.ruleConnection(ctx, spaceID, rule.BillConnectionID)
	if err != nil {
		return ruleFiledBill{}, err
	}
	subaccount, err := m.ruleSubaccount(ctx, spaceID, connection, rule.BillSubaccountID, read)
	if err != nil {
		return ruleFiledBill{}, err
	}
	filed, err := m.fileBill(ctx, spaceID, subaccount, read, message)
	if err != nil {
		return ruleFiledBill{}, err
	}
	if filed.bill == uuid.Nil {
		return ruleFiledBill{}, errors.New("the bill was not kept")
	}
	note := fmt.Sprintf("Rule %q: filed a bill of %s due %s on %s",
		rule.Name, read.AmountDue, read.DueOn, connection.DisplayName())
	return ruleFiledBill{
		biller: connection.Biller, bill: filed.bill, document: filed.document,
		note: joinNotes(note, filed.note),
	}, nil
}

// ruleConnection refuses a provider that is not this household's.
func (m *Mailbox) ruleConnection(
	ctx context.Context, spaceID store.SpaceID, id uuid.UUID,
) (store.BillConnection, error) {
	if id == uuid.Nil {
		return store.BillConnection{}, errors.New("the rule names no bill provider to file on")
	}
	connection, err := m.store.GetBillConnection(ctx, spaceID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.BillConnection{}, errors.New("the rule names a bill provider this household does not have")
		}
		return store.BillConnection{}, err
	}
	return connection, nil
}

// ruleSubaccount is the rule's pinned billed account, or the one its account
// number picks, exactly as a parser's bill is placed.
func (m *Mailbox) ruleSubaccount(
	ctx context.Context, spaceID store.SpaceID, connection store.BillConnection,
	pinned uuid.UUID, read billmail.Bill,
) (store.BillSubaccount, error) {
	if pinned == uuid.Nil {
		return m.subaccountAmong(ctx, spaceID, []store.BillConnection{connection}, read)
	}
	subaccount, err := m.store.GetBillSubaccount(ctx, spaceID, pinned)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.BillSubaccount{}, errors.New("the rule names a billed account this household does not have")
		}
		return store.BillSubaccount{}, err
	}
	if subaccount.ConnectionID != connection.ID {
		return store.BillSubaccount{}, fmt.Errorf(
			"the rule names a billed account that is not %s's", connection.DisplayName())
	}
	return subaccount, nil
}

// postOnce writes the row unless the account already holds one under the same
// external id, and answers the row's id either way, so a duplicate delivery is
// an unchanged ledger rather than a failure. A new row goes through the ingest.
func (m *Mailbox) postOnce(
	ctx context.Context, spaceID store.SpaceID, row *store.Transaction,
) (uuid.UUID, error) {
	held, err := m.store.TransactionByExternalID(ctx, spaceID, row.AccountID, row.ExternalID)
	switch {
	case err == nil:
		return held.ID, nil
	case !errors.Is(err, store.ErrNotFound):
		return uuid.Nil, err
	}
	if err := m.store.CreateTransaction(ctx, spaceID, row); err != nil {
		return uuid.Nil, err
	}
	if _, err := m.Ingest.AfterIngest(ctx, m.store, spaceID, []uuid.UUID{row.ID}); err != nil {
		return row.ID, err
	}
	return row.ID, nil
}

func mailExternalID(messageID, part string) string {
	if part == "" {
		return "mail:" + messageID
	}
	return "mail:" + messageID + ":" + part
}

// ruleAccount refuses an account that is not this household's.
func (m *Mailbox) ruleAccount(
	ctx context.Context, spaceID store.SpaceID, id uuid.UUID,
) (store.Account, error) {
	if id == uuid.Nil {
		return store.Account{}, errors.New("the rule names no account to post to")
	}
	account, err := m.store.GetAccount(ctx, spaceID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Account{}, errors.New("the rule names an account this household does not have")
		}
		return store.Account{}, err
	}
	if account.IsDeleted {
		return store.Account{}, errors.New("the rule names an account that has been deleted")
	}
	return account, nil
}

// ruleCategory refuses a category that is not this household's. The nil id is
// uncategorized, which is a real answer.
func (m *Mailbox) ruleCategory(ctx context.Context, spaceID store.SpaceID, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	if _, err := m.store.GetCategory(ctx, spaceID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("the rule names a category this household does not have")
		}
		return err
	}
	return nil
}

// Reread reads one already-logged message again against the current rules and
// parsers; the poll skips logged messages, so this is how a new rule reaches
// the mail that prompted it.
func (m *Mailbox) Reread(
	ctx context.Context, spaceID store.SpaceID, connectionID, billEmailID uuid.UUID,
) (store.BillEmail, error) {
	row, err := m.store.GetBillEmail(ctx, spaceID, billEmailID)
	if err != nil {
		return store.BillEmail{}, err
	}
	if row.ConnectionID != connectionID {
		return store.BillEmail{}, store.ErrNotFound
	}
	switch row.Outcome {
	case store.EmailOutcomeBill, store.EmailOutcomeRule, store.EmailOutcomeOTP:
		return store.BillEmail{}, ErrMailAlreadyRead
	}

	connection, err := m.store.GetEmailConnection(ctx, spaceID, connectionID)
	if err != nil {
		return store.BillEmail{}, err
	}
	secret, err := m.store.EmailSecret(ctx, spaceID, connectionID)
	if err != nil {
		return store.BillEmail{}, err
	}
	reader, err := m.open(connection, secret)
	if err != nil {
		return store.BillEmail{}, err
	}
	message, found, err := reader.Fetch(ctx, row.MessageID)
	if err != nil {
		return store.BillEmail{}, err
	}
	if !found {
		return store.BillEmail{}, ErrMailGone
	}

	var out MailPollResult
	m.handle(ctx, spaceID, connection, message, &out, true)
	return m.store.GetBillEmail(ctx, spaceID, billEmailID)
}

type TryResult struct {
	Matched bool
	Amount  domain.Money
	// HasAmount and HasDate distinguish an unread figure from zero or January 1.
	HasAmount bool
	Date      domain.Date
	HasDate   bool
	Reference string
	Payee     string
	// Notes is what the posted rows' notes would say.
	Notes     string
	Error     string
	WouldPost []TryPosting
	// WouldFile says the rule files a bill; Date is then its due date and Account
	// the account number as read.
	WouldFile  bool
	IssuedOn   domain.Date
	HasIssued  bool
	MinimumDue domain.Money
	HasMinimum bool
	Account    string
}

type TryPosting struct {
	AccountID  uuid.UUID
	Date       domain.Date
	Amount     domain.Money
	Payee      string
	CategoryID uuid.UUID
}

// TryRule answers what a rule would do with a sample, and writes nothing.
// Whether the accounts it names are this household's is checked on save.
func TryRule(rule store.MailRule, sample billmail.Message) TryResult {
	out := TryResult{Matched: rule.Rule().Match(sample)}
	if !out.Matched {
		return out
	}
	if rule.Action == store.MailRuleBill {
		return tryBillRule(rule, sample, out)
	}
	read, err := rule.Rule().Extract(sample)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Amount, out.HasAmount = read.Amount, true
	out.Date, out.HasDate = read.Date, read.HasDate
	out.Reference = read.Reference
	out.Payee = textutil.FirstNonBlank(read.Payee, rule.Name)
	out.Notes = ruleNotes(read)

	if rule.AccountID == uuid.Nil {
		// The try box runs before the account is chosen.
		return out
	}
	out.WouldPost = rulePostings(rule, read, sample.ReceivedAt)
	return out
}

func tryBillRule(rule store.MailRule, sample billmail.Message, out TryResult) TryResult {
	out.WouldFile = true
	bill, err := rule.Rule().ExtractBill(sample)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Amount, out.HasAmount = bill.AmountDue, true
	out.Date, out.HasDate = bill.DueOn, true
	out.IssuedOn, out.HasIssued = bill.IssuedOn, !bill.IssuedOn.IsZero()
	out.MinimumDue, out.HasMinimum = bill.MinimumDue, bill.HasMinimumDue
	out.Account = textutil.FirstNonBlank(bill.ExternalID, bill.MaskedNumber)
	return out
}

type mailboxCode struct {
	spaceID   store.SpaceID
	owner     codeOwner
	code      string
	relayed   bool
	messageID string
	at        time.Time
}

// mailboxCodes is every code read recently. Package-level because the poll
// and the answerer are different Mailbox values; never the database, since a
// stored passcode is a credential with no reason to be kept.
var mailboxCodes expiring[string, mailboxCode]

// codeTTL: providers give a code five to ten minutes.
const codeTTL = 10 * time.Minute

func rememberOwnedCode(spaceID store.SpaceID, found foundCode, at time.Time, messageID string) {
	mailboxCodes.Put(messageID, mailboxCode{
		spaceID: spaceID, owner: found.owner, code: found.code,
		relayed: found.relayed, messageID: messageID, at: at,
	}, at.Add(codeTTL))
}

// takeOwnedCode consumes the newest code that could answer this challenge. A
// code from the provider's own sender answers that provider; a relayed text
// naming no provider answers only when exactly one sign-in is waiting.
func takeOwnedCode(
	spaceID store.SpaceID, owner codeOwner, after time.Time, soleChallenge bool,
) (string, bool) {
	var best mailboxCode
	mailboxCodes.Each(func(_ string, held mailboxCode) bool {
		if held.spaceID != spaceID || !held.at.After(after) {
			return true
		}
		matches := held.owner == owner || (held.owner.none() && held.relayed && soleChallenge)
		if !matches {
			return true
		}
		if best.code == "" || held.at.After(best.at) {
			best = held
		}
		return true
	})
	if best.code == "" {
		return "", false
	}
	mailboxCodes.Delete(best.messageID)
	return best.code, true
}

// findOTP is a variable so a test can substitute its own reader.
var findOTP = billmail.FindOTP

// AnswerChallenge is a BillChallengeAnswerer.
func (m *Mailbox) AnswerChallenge(
	ctx context.Context, spaceID store.SpaceID,
	connection store.BillConnection, challenge store.BillChallenge,
) (string, string, bool) {
	biller, known := domain.BillerByID(connection.Biller)
	if known && !biller.Raises(domain.ChallengeEmail) && !biller.Raises(domain.ChallengeSMS) &&
		connection.SecondFactor != domain.SecondFactorEmail {
		// Nothing to watch for, unless the household said this login's codes come
		// by e-mail.
		return "", "", false
	}

	code, found := m.waitForCode(ctx, billCodeWait(spaceID, connection, challenge.CreatedAt),
		func() bool { return m.soleWaitingChallenge(ctx, spaceID, challenge.ID) })
	if !found {
		return "", "", false
	}
	return code, store.BillChallengeByMailbox, true
}

func billCodeWait(spaceID store.SpaceID, connection store.BillConnection, since time.Time) codeWait {
	biller, known := domain.BillerByID(connection.Biller)
	wait := codeWait{
		spaceID: spaceID, owner: codeOwner{biller: connection.Biller}, name: connection.ProviderName(),
		senders: billmail.CodeSendersFor(connection.Biller), since: since,
	}
	if connection.SecondFactor == domain.SecondFactorEmail && known && len(wait.senders) == 0 {
		// Codes by e-mail with no known sender: the provider's own domain is the
		// likeliest. A portal deployed per customer mails from that customer's
		// domain, which its portal's host is usually one subdomain of.
		wait.domains = []string{homeDomain(biller.Home)}
		if biller.SiteAddress && connection.Site != "" {
			wait.domains = []string{parentDomain(homeDomain(connection.Site))}
		}
	}
	return wait
}

// parentDomain drops a host's first label when two or more remain after it:
// "portal.example.org" is mailed from "example.org".
func parentDomain(host string) string {
	if _, rest, cut := strings.Cut(host, "."); cut && strings.Contains(rest, ".") {
		return rest
	}
	return host
}

// WaitForBillCode watches for the code a typed sign-in was just sent and
// answers it or nothing; the caller types it in.
func (m *Mailbox) WaitForBillCode(
	ctx context.Context, spaceID store.SpaceID, connection store.BillConnection, since time.Time,
) (string, bool) {
	return m.waitForCode(ctx, billCodeWait(spaceID, connection, since),
		func() bool { return m.noWaitingChallenges(ctx, spaceID) })
}

func homeDomain(home string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(home, "https://"), "http://")
	if cut := strings.IndexAny(host, "/?#"); cut >= 0 {
		host = host[:cut]
	}
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

// pollForACode reads every mailbox in the space and answers how many it read;
// codes land in mailboxCodes on the way past.
func (m *Mailbox) pollForACode(ctx context.Context, spaceID store.SpaceID) int {
	connections, err := m.store.ListEmailConnections(ctx, spaceID)
	if err != nil {
		m.log().Error("mailbox: listing mailboxes for a code", "error", err)
		return 0
	}
	read := 0
	for _, connection := range connections {
		if !connection.Enabled || !connection.HasSecret || ctx.Err() != nil {
			continue
		}
		result, err := m.Poll(ctx, spaceID, connection.ID)
		if err != nil {
			m.log().Error("mailbox: polling for a code",
				"mailbox", connection.Label, "error", err)
			continue
		}
		if result.Error != "" {
			continue
		}
		read++
	}
	return read
}

// soleWaitingChallenge makes a relayed text that names no provider answerable.
func (m *Mailbox) soleWaitingChallenge(
	ctx context.Context, spaceID store.SpaceID, self uuid.UUID,
) bool {
	waiting, err := m.store.ListBillChallenges(ctx, spaceID, store.BillChallengeWaiting)
	if err != nil {
		return false
	}
	for _, one := range waiting {
		if one.ID != self {
			return false
		}
	}
	return true
}

func (m *Mailbox) forwardedByTheHousehold(sender, watched string) bool {
	sender = strings.ToLower(strings.TrimSpace(sender))
	for _, one := range m.Forwarders {
		if strings.EqualFold(strings.TrimSpace(one), sender) {
			return true
		}
	}
	_, senderDomain, ok := strings.Cut(sender, "@")
	_, watchedDomain, watchedOK := strings.Cut(strings.ToLower(watched), "@")
	return ok && watchedOK && senderDomain != "" && senderDomain == watchedDomain
}

// mailMessage strips here rather than in either provider, so an HTML-only mail
// reads the same from Graph or IMAP.
func mailMessage(message provider.MailMessage) billmail.Message {
	out := billmail.Message{
		ID: message.ID, Sender: strings.ToLower(message.Sender), Subject: message.Subject,
		ReceivedAt: message.ReceivedAt, Text: message.Text, HTML: message.HTML,
	}
	if strings.TrimSpace(out.Text) == "" && out.HTML != "" {
		out.Text = billmail.StripHTML(out.HTML)
	}
	for _, attachment := range message.Attachments {
		out.Attachments = append(out.Attachments, billmail.Attachment{
			Filename: attachment.Filename, ContentType: attachment.ContentType,
			Bytes: attachment.Bytes,
		})
	}
	return out
}

func firstPDF(message billmail.Message) *billmail.Attachment {
	for i, one := range message.Attachments {
		if strings.EqualFold(one.ContentType, provider.MailAttachmentContentType) {
			return &message.Attachments[i]
		}
	}
	return nil
}
