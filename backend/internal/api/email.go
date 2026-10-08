package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/billmail"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The watched mailbox: where a mailed-only bill comes from, and where a parked
// sign-in's code is read.
//
// A mailbox's secret is read access to all the household's mail, so no response
// has a field for it: store.EmailConnection carries only a boolean, and the
// cursor is left out too because a Graph delta link is a capability URL. The
// sign-in and secret methods are EmailSignInService's, and in
// dispatchDeniedRoutes.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewEmailServiceHandler(emailService{env}, opts...)
	})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewEmailSignInServiceHandler(emailSignInService{env}, opts...)
	})
}

type (
	emailService       struct{ env *Env }
	emailSignInService struct{ env *Env }
)

// EmailMessageResponse is one row of the per-message log as GET /email/messages
// writes it, which the assistant's list_mail reads.
type EmailMessageResponse struct {
	ID               uuid.UUID  `json:"id"`
	ConnectionID     uuid.UUID  `json:"connection_id"`
	MessageID        string     `json:"message_id"`
	ReceivedAt       time.Time  `json:"received_at"`
	Sender           string     `json:"sender"`
	Subject          string     `json:"subject"`
	Biller           string     `json:"biller"`
	Outcome          string     `json:"outcome"`
	Note             string     `json:"note"`
	BillID           *uuid.UUID `json:"bill_id"`
	DocumentID       *uuid.UUID `json:"document_id"`
	RuleID           *uuid.UUID `json:"rule_id"`
	TransactionID    *uuid.UUID `json:"transaction_id"`
	BillConnectionID *uuid.UUID `json:"bill_connection_id"`
}

func (s emailService) ListEmailConnections(
	ctx context.Context, _ *agentifiv1.ListEmailConnectionsRequest,
) (*agentifiv1.ListEmailConnectionsResponse, error) {
	rows, err := s.env.DB.ListEmailConnections(ctx, spaceFrom(ctx).ID())
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListEmailConnectionsResponse{
		Connections: make([]*agentifiv1.EmailConnection, 0, len(rows)),
	}
	for _, row := range rows {
		out.Connections = append(out.Connections, emailConnectionProto(row))
	}
	return out, nil
}

func (s emailService) CreateEmailConnection(
	ctx context.Context, req *agentifiv1.CreateEmailConnectionRequest,
) (*agentifiv1.CreateEmailConnectionResponse, error) {
	sp := spaceFrom(ctx)
	if req.GetLabel() == "" {
		return nil, errInvalid("missing", []string{"body", "label"}, "label is required")
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	connection := &store.EmailConnection{
		Label: req.GetLabel(), Kind: req.GetKind(), Address: req.GetAddress(),
		Folder: "Inbox", Enabled: true,
	}
	applyNullable(optOf(mask, "client_id", req.ClientId), &connection.ClientID)
	applyNullable(optOf(mask, "tenant", req.Tenant), &connection.Tenant)
	applyNullable(optOf(mask, "host", req.Host), &connection.Host)
	applyNullable(optOf(mask, "username", req.Username), &connection.Username)
	if err := applyRequired("port", intOpt(optOf(mask, "port", req.Port)), &connection.Port); err != nil {
		return nil, err
	}
	if folder := optOf(mask, "folder", req.Folder); folder.Present() && folder.Value != "" {
		connection.Folder = folder.Value
	}
	if err := checkMailbox(connection); err != nil {
		return nil, err
	}
	if err := checkMailboxLabelFree(ctx, s.env, sp, *connection, uuid.Nil); err != nil {
		return nil, err
	}
	if err := s.env.DB.CreateEmailConnection(ctx, sp.ID(), connection); err != nil {
		return nil, err
	}
	return &agentifiv1.CreateEmailConnectionResponse{Connection: emailConnectionProto(*connection)}, nil
}

func (s emailService) GetEmailConnection(
	ctx context.Context, req *agentifiv1.GetEmailConnectionRequest,
) (*agentifiv1.GetEmailConnectionResponse, error) {
	connection, err := emailConnection(ctx, s.env, spaceFrom(ctx), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetEmailConnectionResponse{Connection: emailConnectionProto(connection)}, nil
}

// UpdateEmailConnection writes the fields a person owns. Repointing the reader
// resets the cursor, since a delta link or IMAP UID belongs to one folder; the
// lookback re-read skips nothing.
func (s emailService) UpdateEmailConnection(
	ctx context.Context, req *agentifiv1.UpdateEmailConnectionRequest,
) (*agentifiv1.UpdateEmailConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	before := connection
	if err := applyRequired("label", optOf(mask, "label", req.Label), &connection.Label); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "address", req.Address), &connection.Address)
	applyNullable(optOf(mask, "client_id", req.ClientId), &connection.ClientID)
	applyNullable(optOf(mask, "tenant", req.Tenant), &connection.Tenant)
	applyNullable(optOf(mask, "host", req.Host), &connection.Host)
	applyNullable(optOf(mask, "username", req.Username), &connection.Username)
	if err := applyRequired("port", intOpt(optOf(mask, "port", req.Port)), &connection.Port); err != nil {
		return nil, err
	}
	if folder := optOf(mask, "folder", req.Folder); folder.Present() && folder.Value != "" {
		connection.Folder = folder.Value
	}
	if err := applyRequired("enabled", optOf(mask, "enabled", req.Enabled), &connection.Enabled); err != nil {
		return nil, err
	}
	if err := checkMailbox(&connection); err != nil {
		return nil, err
	}
	if err := checkMailboxLabelFree(ctx, s.env, sp, connection, connection.ID); err != nil {
		return nil, err
	}
	if err := s.env.DB.UpdateEmailConnection(ctx, sp.ID(), &connection); err != nil {
		return nil, err
	}
	if movedMailbox(before, connection) {
		if err := s.env.DB.SaveEmailCursor(ctx, sp.ID(), connection.ID, nil); err != nil {
			return nil, err
		}
		connection.Cursor = nil
	}
	return &agentifiv1.UpdateEmailConnectionResponse{Connection: emailConnectionProto(connection)}, nil
}

func (s emailService) DeleteEmailConnection(
	ctx context.Context, req *agentifiv1.DeleteEmailConnectionRequest,
) (*agentifiv1.DeleteEmailConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteEmailConnection(ctx, sp.ID(), connection.ID); err != nil {
		return nil, notFoundAs(err, "Email connection")
	}
	return &agentifiv1.DeleteEmailConnectionResponse{}, nil
}

// StartEmailSignIn connects the mailbox: a device code for Office 365, an app
// password for IMAP. The password is checked against the server before it is
// sealed, and is in no response and no log line.
func (s emailSignInService) StartEmailSignIn(
	ctx context.Context, req *agentifiv1.StartEmailSignInRequest,
) (*agentifiv1.StartEmailSignInResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	mailbox := NewMailbox(s.env)

	if connection.Kind == store.EmailKindGraph {
		started, err := mailbox.StartGraphSignIn(ctx, sp.ID(), connection.ID)
		if err != nil {
			return nil, errInvalid("sign_in_failed", []string{"body"}, "%s", err.Error())
		}
		return &agentifiv1.StartEmailSignInResponse{Result: &agentifiv1.StartEmailSignInResponse_DeviceCode{
			DeviceCode: &agentifiv1.EmailDeviceCode{
				SessionId: started.SessionID, UserCode: started.UserCode,
				VerificationUri: started.VerificationURI, ExpiresAt: timestamppb.New(started.ExpiresAt),
			},
		}}, nil
	}

	if err := mailbox.SetIMAPPassword(ctx, sp.ID(), connection.ID, req.GetPassword()); err != nil {
		// The server's own refusal, which is what the dialog shows. It names
		// the host and the reason and never the password.
		return nil, errInvalid("sign_in_failed", []string{"body", "password"}, "%s", err.Error())
	}
	connection.HasSecret = true
	return &agentifiv1.StartEmailSignInResponse{Result: &agentifiv1.StartEmailSignInResponse_Connection{
		Connection: emailConnectionProto(connection),
	}}, nil
}

func (s emailSignInService) GetEmailSignInState(
	ctx context.Context, req *agentifiv1.GetEmailSignInStateRequest,
) (*agentifiv1.GetEmailSignInStateResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	state, detail := NewMailbox(s.env).GraphSignInState(sp.ID(), connection.ID, req.GetSession())
	return &agentifiv1.GetEmailSignInStateResponse{State: state, Error: detail}, nil
}

// ForgetEmailSecret disconnects the mailbox and leaves everything else: the
// message log is what was read, whoever is signed in now.
func (s emailSignInService) ForgetEmailSecret(
	ctx context.Context, req *agentifiv1.ForgetEmailSecretRequest,
) (*agentifiv1.ForgetEmailSecretResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.ClearEmailSecret(ctx, sp.ID(), connection.ID); err != nil {
		return nil, err
	}
	connection.HasSecret = false
	return &agentifiv1.ForgetEmailSecretResponse{Connection: emailConnectionProto(connection)}, nil
}

func (s emailService) PollEmailConnection(
	ctx context.Context, req *agentifiv1.PollEmailConnectionRequest,
) (*agentifiv1.PollEmailConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	result, err := NewMailbox(s.env).Poll(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.PollEmailConnectionResponse{
		Read: int32(result.Read), Bills: int32(result.Bills), Rules: int32(result.Rules),
		Otps: int32(result.OTPs), Unrecognised: int32(result.Unrecognised),
		Proposed: int32(result.Proposed), Failed: int32(result.Failed), Error: result.Error,
	}, nil
}

func (s emailService) ListEmailConnectionMessages(
	ctx context.Context, req *agentifiv1.ListEmailConnectionMessagesRequest,
) (*agentifiv1.ListEmailConnectionMessagesResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	limit, err := mailLogLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.env.DB.ListBillEmails(ctx, sp.ID(), connection.ID, limit)
	if err != nil {
		return nil, err
	}
	out, err := emailMessageProtos(ctx, s.env, sp, rows)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListEmailConnectionMessagesResponse{Messages: out}, nil
}

// RereadEmailMessage reads one logged message again against today's rules and
// parsers. The poll skips logged messages, so this is how a new rule reaches
// old mail.
func (s emailService) RereadEmailMessage(
	ctx context.Context, req *agentifiv1.RereadEmailMessageRequest,
) (*agentifiv1.RereadEmailMessageResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := emailConnection(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	id, err := idFrom(req.GetMessageId(), "Message")
	if err != nil {
		return nil, err
	}
	row, err := NewMailbox(s.env).Reread(ctx, sp.ID(), connection.ID, id)
	switch {
	case errors.Is(err, service.ErrMailAlreadyRead):
		return nil, errConflict("%s", err.Error())
	case errors.Is(err, service.ErrMailGone):
		return nil, errNotFound("Message")
	case isNotFound(err):
		return nil, errNotFound("Message")
	case err != nil:
		return nil, err
	}
	out, err := emailMessageProtos(ctx, s.env, sp, []store.BillEmail{row})
	if err != nil {
		return nil, err
	}
	return &agentifiv1.RereadEmailMessageResponse{Message: out[0]}, nil
}

// mailLogLimit is a message log's page size: 50 when unset, refused outside
// 1..500 rather than clamped.
func mailLogLimit(limit *int32) (int, error) {
	if limit == nil {
		return 50, nil
	}
	if *limit < 1 || *limit > 500 {
		return 0, errInvalid("out_of_range", []string{"query", "limit"},
			"limit must be between 1 and 500")
	}
	return int(*limit), nil
}

// emailMessageProtos is the log's rows as the page reads them, each filed bill
// with the connection it is on.
func emailMessageProtos(
	ctx context.Context, env *Env, sp auth.SpaceContext, rows []store.BillEmail,
) ([]*agentifiv1.EmailMessage, error) {
	var bills []uuid.UUID
	for _, row := range rows {
		if row.BillID != uuid.Nil {
			bills = append(bills, row.BillID)
		}
	}
	filedOn, err := env.DB.BillConnectionsOf(ctx, sp.ID(), bills)
	if err != nil {
		return nil, err
	}
	out := make([]*agentifiv1.EmailMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, &agentifiv1.EmailMessage{
			Id: row.ID.String(), ConnectionId: row.ConnectionID.String(), MessageId: row.MessageID,
			ReceivedAt: timestamppb.New(row.ReceivedAt), Sender: row.Sender, Subject: row.Subject,
			Biller: string(row.Biller), Outcome: row.Outcome, Note: row.Note,
			BillId: optionalID(row.BillID), DocumentId: optionalID(row.DocumentID),
			RuleId: optionalID(row.RuleID), TransactionId: optionalID(row.TransactionID),
			BillConnectionId: optionalID(filedOn[row.BillID]),
		})
	}
	return out, nil
}

// NewMailbox builds the reader over this environment.
func NewMailbox(env *Env) *service.Mailbox {
	st := env.DB
	if sealed, err := sealedStore(env); err == nil {
		st = sealed
	}
	mailbox := service.NewMailbox(st)
	mailbox.Now = env.now
	mailbox.Open = openMailbox
	mailbox.Bills = NewBills(env)
	mailbox.Documents = env.documents()
	mailbox.Print = printMail
	if mailbox.Print == nil && env.Cfg != nil {
		mailbox.Print = env.browserEngine().PrintPDF
	}
	mailbox.Alerts = newAlerts(env)
	mailbox.Ingest = NewIngest(env)
	mailbox.Model = service.MailModelFor(st)
	if env.Cfg != nil {
		mailbox.Relays = env.Cfg.EmailOTPRelays
		mailbox.Forwarders = env.Cfg.EmailForwarders
		mailbox.PollEvery = env.Cfg.EmailPollEvery
		if env.Cfg.EmailLookbackDays > 0 {
			mailbox.Lookback = time.Duration(env.Cfg.EmailLookbackDays) * 24 * time.Hour
		}
	}
	return mailbox
}

// openMailbox builds the reader for a connection. Nil is the real one (Graph or
// IMAP); a test substitutes its own.
var openMailbox func(store.EmailConnection, string) (provider.Mailbox, error)

// printMail prints a mailed bill's statement. Nil is the browser in this
// process; a test substitutes its own.
var printMail func(page string) ([]byte, error)

// newMailboxAnswerer is the code answerer as the bridge takes it. The reader is
// built inside the call because NewBills builds this and NewMailbox calls
// NewBills.
func newMailboxAnswerer(env *Env) service.BillChallengeAnswerer {
	return func(
		ctx context.Context, spaceID store.SpaceID,
		connection store.BillConnection, challenge store.BillChallenge,
	) (string, string, bool) {
		return NewMailbox(env).AnswerChallenge(ctx, spaceID, connection, challenge)
	}
}

func emailConnection(
	ctx context.Context, env *Env, sp auth.SpaceContext, rawID string,
) (store.EmailConnection, error) {
	id, err := idFrom(rawID, "Email connection")
	if err != nil {
		return store.EmailConnection{}, err
	}
	connection, err := env.DB.GetEmailConnection(ctx, sp.ID(), id)
	if err != nil {
		return store.EmailConnection{}, notFoundAs(err, "Email connection")
	}
	return connection, nil
}

// checkMailbox refuses a connection that could never be read for lack of a
// fact its kind needs.
func checkMailbox(one *store.EmailConnection) error {
	if one.Address == "" {
		return errInvalid("missing", []string{"body", "address"}, "address is required")
	}
	switch one.Kind {
	case store.EmailKindGraph:
		if one.ClientID == "" {
			return errInvalid("missing", []string{"body", "client_id"},
				"an Office 365 mailbox needs the app registration's client id")
		}
		if one.Tenant == "" {
			return errInvalid("missing", []string{"body", "tenant"},
				"an Office 365 mailbox needs the tenant it is in")
		}
	case store.EmailKindIMAP:
		if one.Host == "" {
			return errInvalid("missing", []string{"body", "host"},
				"an IMAP mailbox needs the server to connect to")
		}
		if one.Username == "" {
			one.Username = one.Address
		}
		if one.Port == 0 {
			one.Port = 993
		}
	default:
		return errInvalid("invalid", []string{"body", "kind"}, "kind must be graph or imap")
	}
	return nil
}

func checkMailboxLabelFree(
	ctx context.Context, env *Env, sp auth.SpaceContext, one store.EmailConnection, self uuid.UUID,
) error {
	rows, err := env.DB.ListEmailConnections(ctx, sp.ID())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Label == one.Label && row.ID != self {
			return errConflict("a mailbox labelled %q already exists", one.Label)
		}
	}
	return nil
}

// movedMailbox says the reader is now pointed somewhere else, which is what
// makes the cursor meaningless.
func movedMailbox(before, after store.EmailConnection) bool {
	return before.Folder != after.Folder || before.Host != after.Host ||
		before.Address != after.Address || before.Username != after.Username
}

func emailConnectionProto(one store.EmailConnection) *agentifiv1.EmailConnection {
	out := &agentifiv1.EmailConnection{
		Id: one.ID.String(), Label: one.Label, Kind: one.Kind, Address: one.Address,
		ClientId: one.ClientID, Tenant: one.Tenant, Host: one.Host,
		Username: one.Username, Folder: one.Folder, Enabled: one.Enabled,
		Connected: one.HasSecret, LastPollError: one.LastPollError,
		CreatedAt: timestamppb.New(one.CreatedAt),
	}
	if one.Port != 0 {
		port := int32(one.Port)
		out.Port = &port
	}
	if one.LastPolledAt != nil {
		out.LastPolledAt = timestamppb.New(*one.LastPolledAt)
	}
	return out
}

// intOpt widens a proto int32 patch field to the int internal/store holds.
func intOpt(o Opt[int32]) Opt[int] {
	return Opt[int]{Set: o.Set, Null: o.Null, Value: int(o.Value)}
}

// --- The household's own mail rules ---------------------------------------------
//
// A rule is what somebody wrote about their own mail: which sender, which
// phrase, and the labels the figures sit behind. Nothing here is a credential,
// so none of it is denied to the in-process dispatch.

// MailRuleDraft is a rule as it is written: every field optional, so the same
// fields create one, patch one and are tried in the try box.
type MailRuleDraft struct {
	Name    Opt[string]
	Enabled Opt[bool]

	Sender          Opt[string]
	SubjectContains Opt[string]
	BodyContains    Opt[string]

	AmountLabel      Opt[string]
	AmountPattern    Opt[string]
	DateLabel        Opt[string]
	DatePattern      Opt[string]
	ReferenceLabel   Opt[string]
	ReferencePattern Opt[string]
	IssuedLabel      Opt[string]
	IssuedPattern    Opt[string]
	MinimumLabel     Opt[string]
	MinimumPattern   Opt[string]
	Payee            Opt[string]
	PayeeLabel       Opt[string]
	NotesLabel       Opt[string]
	NotesEndLabel    Opt[string]

	Action     Opt[string]
	AccountID  Opt[uuid.UUID]
	CategoryID Opt[uuid.UUID]
	Direction  Opt[string]

	BillConnectionID Opt[uuid.UUID]
	BillSubaccountID Opt[uuid.UUID]

	PadIncome        Opt[bool]
	IncomeAccountID  Opt[uuid.UUID]
	IncomeCategoryID Opt[uuid.UUID]
	IncomePayee      Opt[string]

	SortOrder Opt[int]
}

func (s emailService) ListMailRules(
	ctx context.Context, _ *agentifiv1.ListMailRulesRequest,
) (*agentifiv1.ListMailRulesResponse, error) {
	rows, err := s.env.DB.ListMailRules(ctx, spaceFrom(ctx).ID())
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListMailRulesResponse{Rules: make([]*agentifiv1.MailRule, 0, len(rows))}
	for _, row := range rows {
		out.Rules = append(out.Rules, mailRuleProto(row))
	}
	return out, nil
}

func (s emailService) CreateMailRule(
	ctx context.Context, req *agentifiv1.CreateMailRuleRequest,
) (*agentifiv1.CreateMailRuleResponse, error) {
	sp := spaceFrom(ctx)
	draft, err := mailRuleDraftOf(req)
	if err != nil {
		return nil, err
	}
	rule := store.MailRule{
		Enabled: true, Action: store.MailRuleTransaction, Direction: store.MailRuleExpense,
	}
	if err := applyMailRule(draft, &rule); err != nil {
		return nil, err
	}
	if err := checkMailRule(ctx, s.env, sp, rule, uuid.Nil); err != nil {
		return nil, err
	}
	if err := s.env.DB.CreateMailRule(ctx, sp.ID(), &rule); err != nil {
		return nil, err
	}
	return &agentifiv1.CreateMailRuleResponse{Rule: mailRuleProto(rule)}, nil
}

func (s emailService) UpdateMailRule(
	ctx context.Context, req *agentifiv1.UpdateMailRuleRequest,
) (*agentifiv1.UpdateMailRuleResponse, error) {
	sp := spaceFrom(ctx)
	rule, err := mailRule(ctx, s.env, sp, req.GetRuleId())
	if err != nil {
		return nil, err
	}
	draft, err := mailRuleDraftOf(req)
	if err != nil {
		return nil, err
	}
	if err := applyMailRule(draft, &rule); err != nil {
		return nil, err
	}
	if err := checkMailRule(ctx, s.env, sp, rule, rule.ID); err != nil {
		return nil, err
	}
	if err := s.env.DB.UpdateMailRule(ctx, sp.ID(), &rule); err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateMailRuleResponse{Rule: mailRuleProto(rule)}, nil
}

func (s emailService) DeleteMailRule(
	ctx context.Context, req *agentifiv1.DeleteMailRuleRequest,
) (*agentifiv1.DeleteMailRuleResponse, error) {
	sp := spaceFrom(ctx)
	rule, err := mailRule(ctx, s.env, sp, req.GetRuleId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteMailRule(ctx, sp.ID(), rule.ID); err != nil {
		return nil, notFoundAs(err, "Mail rule")
	}
	return &agentifiv1.DeleteMailRuleResponse{}, nil
}

// TryMailRule answers what a rule would make of a pasted mail, and writes
// nothing at all — not the rule, and above all not the sample.
func (s emailService) TryMailRule(
	ctx context.Context, req *agentifiv1.TryMailRuleRequest,
) (*agentifiv1.TryMailRuleResponse, error) {
	written := req.GetRule()
	if written == nil {
		written = &agentifiv1.MailRuleDraft{}
	}
	draft, err := mailRuleDraftOf(written)
	if err != nil {
		return nil, err
	}
	rule := store.MailRule{
		Enabled: true, Action: store.MailRuleTransaction, Direction: store.MailRuleExpense,
	}
	if err := applyMailRule(draft, &rule); err != nil {
		return nil, err
	}
	if err := checkMailRuleReadable(rule); err != nil {
		return nil, err
	}
	sample := req.GetSample()
	tried := service.TryRule(rule, billmail.Message{
		Sender:  strings.ToLower(strings.TrimSpace(sample.GetSender())),
		Subject: sample.GetSubject(), Text: sample.GetText(),
		ReceivedAt: s.env.now(),
	})

	out := &agentifiv1.TryMailRuleResponse{
		Matched: tried.Matched, Reference: tried.Reference, Payee: tried.Payee,
		Notes: tried.Notes, Error: tried.Error, WouldPost: []*agentifiv1.MailRulePosting{},
		WouldFile: tried.WouldFile, Account: tried.Account,
		Amount:     nullableMoneyProto(tried.Amount, tried.HasAmount),
		MinimumDue: nullableMoneyProto(tried.MinimumDue, tried.HasMinimum),
	}
	if tried.HasIssued {
		out.IssuedOn = proto.String(tried.IssuedOn.String())
	}
	if tried.HasDate {
		out.Date = proto.String(tried.Date.String())
	}
	for _, posting := range tried.WouldPost {
		out.WouldPost = append(out.WouldPost, &agentifiv1.MailRulePosting{
			AccountId: optionalID(posting.AccountID), Amount: moneyProto(posting.Amount),
			Payee: posting.Payee, CategoryId: optionalID(posting.CategoryID),
		})
	}
	return out, nil
}

func mailRule(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.MailRule, error) {
	id, err := idFrom(rawID, "Mail rule")
	if err != nil {
		return store.MailRule{}, err
	}
	rule, err := env.DB.GetMailRule(ctx, sp.ID(), id)
	if err != nil {
		return store.MailRule{}, notFoundAs(err, "Mail rule")
	}
	return rule, nil
}

// mailRuleDraftOf reads a request carrying MailRuleDraft's fields (a create,
// an update or the try box's rule) by name, so the three cannot drift apart.
func mailRuleDraftOf(req proto.Message) (MailRuleDraft, error) {
	mask, err := maskOf(req)
	if err != nil {
		return MailRuleDraft{}, err
	}
	var d agentifiv1.MailRuleDraft
	fields := d.ProtoReflect().Descriptor().Fields()
	req.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if into := fields.ByName(field.Name()); into != nil {
			d.ProtoReflect().Set(into, value)
		}
		return true
	})

	out := MailRuleDraft{
		Name: optOf(mask, "name", d.Name), Enabled: optOf(mask, "enabled", d.Enabled),
		Sender:           optOf(mask, "sender", d.Sender),
		SubjectContains:  optOf(mask, "subject_contains", d.SubjectContains),
		BodyContains:     optOf(mask, "body_contains", d.BodyContains),
		AmountLabel:      optOf(mask, "amount_label", d.AmountLabel),
		AmountPattern:    optOf(mask, "amount_pattern", d.AmountPattern),
		DateLabel:        optOf(mask, "date_label", d.DateLabel),
		DatePattern:      optOf(mask, "date_pattern", d.DatePattern),
		ReferenceLabel:   optOf(mask, "reference_label", d.ReferenceLabel),
		ReferencePattern: optOf(mask, "reference_pattern", d.ReferencePattern),
		IssuedLabel:      optOf(mask, "issued_label", d.IssuedLabel),
		IssuedPattern:    optOf(mask, "issued_pattern", d.IssuedPattern),
		MinimumLabel:     optOf(mask, "minimum_label", d.MinimumLabel),
		MinimumPattern:   optOf(mask, "minimum_pattern", d.MinimumPattern),
		Payee:            optOf(mask, "payee", d.Payee),
		PayeeLabel:       optOf(mask, "payee_label", d.PayeeLabel),
		NotesLabel:       optOf(mask, "notes_label", d.NotesLabel),
		NotesEndLabel:    optOf(mask, "notes_end_label", d.NotesEndLabel),
		Action:           optOf(mask, "action", d.Action),
		Direction:        optOf(mask, "direction", d.Direction),
		PadIncome:        optOf(mask, "pad_income", d.PadIncome),
		IncomePayee:      optOf(mask, "income_payee", d.IncomePayee),
		SortOrder:        intOpt(optOf(mask, "sort_order", d.SortOrder)),
	}
	for _, id := range []struct {
		name  string
		value *string
		dst   *Opt[uuid.UUID]
	}{
		{"account_id", d.AccountId, &out.AccountID},
		{"category_id", d.CategoryId, &out.CategoryID},
		{"bill_connection_id", d.BillConnectionId, &out.BillConnectionID},
		{"bill_subaccount_id", d.BillSubaccountId, &out.BillSubaccountID},
		{"income_account_id", d.IncomeAccountId, &out.IncomeAccountID},
		{"income_category_id", d.IncomeCategoryId, &out.IncomeCategoryID},
	} {
		raw := optOf(mask, id.name, id.value)
		*id.dst = Opt[uuid.UUID]{Set: raw.Set, Null: raw.Null}
		if !raw.Present() {
			continue
		}
		parsed, err := uuid.Parse(raw.Value)
		if err != nil {
			return MailRuleDraft{}, errInvalid("uuid_parsing", []string{"body", id.name},
				"%s must be a uuid", id.name)
		}
		id.dst.Value = parsed
	}
	return out, nil
}

// applyMailRule writes the fields the body carried and leaves the rest.
func applyMailRule(body MailRuleDraft, rule *store.MailRule) error {
	if err := applyRequired("name", body.Name, &rule.Name); err != nil {
		return err
	}
	if err := applyRequired("enabled", body.Enabled, &rule.Enabled); err != nil {
		return err
	}
	for _, field := range []struct {
		opt Opt[string]
		dst *string
	}{
		{body.Sender, &rule.Sender},
		{body.SubjectContains, &rule.SubjectContains},
		{body.BodyContains, &rule.BodyContains},
		{body.AmountLabel, &rule.AmountLabel},
		{body.AmountPattern, &rule.AmountPattern},
		{body.DateLabel, &rule.DateLabel},
		{body.DatePattern, &rule.DatePattern},
		{body.ReferenceLabel, &rule.ReferenceLabel},
		{body.ReferencePattern, &rule.ReferencePattern},
		{body.IssuedLabel, &rule.IssuedLabel},
		{body.IssuedPattern, &rule.IssuedPattern},
		{body.MinimumLabel, &rule.MinimumLabel},
		{body.MinimumPattern, &rule.MinimumPattern},
		{body.Payee, &rule.Payee},
		{body.PayeeLabel, &rule.PayeeLabel},
		{body.NotesLabel, &rule.NotesLabel},
		{body.NotesEndLabel, &rule.NotesEndLabel},
		{body.IncomePayee, &rule.IncomePayee},
	} {
		applyNullable(field.opt, field.dst)
	}
	if err := applyRequired("action", body.Action, &rule.Action); err != nil {
		return err
	}
	if err := applyRequired("direction", body.Direction, &rule.Direction); err != nil {
		return err
	}
	if err := applyRequired("pad_income", body.PadIncome, &rule.PadIncome); err != nil {
		return err
	}
	if err := applyRequired("sort_order", body.SortOrder, &rule.SortOrder); err != nil {
		return err
	}
	applyNullable(body.AccountID, &rule.AccountID)
	applyNullable(body.CategoryID, &rule.CategoryID)
	applyNullable(body.IncomeAccountID, &rule.IncomeAccountID)
	applyNullable(body.IncomeCategoryID, &rule.IncomeCategoryID)
	applyNullable(body.BillConnectionID, &rule.BillConnectionID)
	applyNullable(body.BillSubaccountID, &rule.BillSubaccountID)
	return nil
}

// checkMailRule refuses a rule that could not run, and one whose accounts are
// another space's, which would fail every message silently.
func checkMailRule(
	ctx context.Context, env *Env, sp auth.SpaceContext, rule store.MailRule, self uuid.UUID,
) error {
	if err := checkMailRuleShape(rule); err != nil {
		return err
	}
	for _, named := range []struct {
		field string
		id    uuid.UUID
	}{
		{"account_id", rule.AccountID}, {"income_account_id", rule.IncomeAccountID},
	} {
		if named.id == uuid.Nil {
			continue
		}
		account, err := env.DB.GetAccount(ctx, sp.ID(), named.id)
		if err != nil || account.IsDeleted {
			if err != nil && !isNotFound(err) {
				return err
			}
			return errInvalid("invalid", []string{"body", named.field},
				"account %s is not in this space", named.id)
		}
	}
	if err := checkMailRuleBill(ctx, env, sp, rule); err != nil {
		return err
	}
	for _, named := range []struct {
		field string
		id    uuid.UUID
	}{
		{"category_id", rule.CategoryID}, {"income_category_id", rule.IncomeCategoryID},
	} {
		if named.id == uuid.Nil {
			continue
		}
		if _, err := env.DB.GetCategory(ctx, sp.ID(), named.id); err != nil {
			if !isNotFound(err) {
				return err
			}
			return errInvalid("invalid", []string{"body", named.field},
				"category %s is not in this space", named.id)
		}
	}

	rows, err := env.DB.ListMailRules(ctx, sp.ID())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Name == rule.Name && row.ID != self {
			return errConflict("a mail rule called %q already exists", rule.Name)
		}
	}
	return nil
}

// checkMailRuleBill refuses a bill rule whose provider or billed account is
// not this household's, or whose billed account is at some other provider.
func checkMailRuleBill(ctx context.Context, env *Env, sp auth.SpaceContext, rule store.MailRule) error {
	if rule.Action != store.MailRuleBill {
		return nil
	}
	if _, err := env.DB.GetBillConnection(ctx, sp.ID(), rule.BillConnectionID); err != nil {
		if !isNotFound(err) {
			return err
		}
		return errInvalid("invalid", []string{"body", "bill_connection_id"},
			"bill provider %s is not in this space", rule.BillConnectionID)
	}
	if rule.BillSubaccountID == uuid.Nil {
		return nil
	}
	subaccount, err := env.DB.GetBillSubaccount(ctx, sp.ID(), rule.BillSubaccountID)
	if err != nil {
		if !isNotFound(err) {
			return err
		}
		return errInvalid("invalid", []string{"body", "bill_subaccount_id"},
			"billed account %s is not in this space", rule.BillSubaccountID)
	}
	if subaccount.ConnectionID != rule.BillConnectionID {
		return errInvalid("invalid", []string{"body", "bill_subaccount_id"},
			"billed account %s is not one of that provider's", rule.BillSubaccountID)
	}
	return nil
}

// checkMailRuleShape is a rule that could not be saved: no name, nothing to
// post or file with, or an amount nothing could read.
func checkMailRuleShape(rule store.MailRule) error {
	if strings.TrimSpace(rule.Name) == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := checkMailRuleReadable(rule); err != nil {
		return err
	}
	if rule.Action == store.MailRuleBill {
		if rule.BillConnectionID == uuid.Nil {
			return errInvalid("missing", []string{"body", "bill_connection_id"},
				"a rule that files a bill needs the bill provider to file it on")
		}
		return nil
	}
	if rule.AccountID == uuid.Nil {
		return errInvalid("missing", []string{"body", "account_id"},
			"a rule needs the account to post to")
	}
	if rule.CategoryID == uuid.Nil {
		return errInvalid("missing", []string{"body", "category_id"},
			"a rule needs the category to post under")
	}
	if rule.PadIncome && rule.IncomeCategoryID == uuid.Nil {
		return errInvalid("missing", []string{"body", "income_category_id"},
			"a rule that pads income needs the category the income lands under")
	}
	return nil
}

// checkMailRuleReadable is the half a rule being tried must pass: whether it
// can read anything. The try box runs before the account and category are
// chosen.
func checkMailRuleReadable(rule store.MailRule) error {
	if rule.Action != store.MailRuleTransaction && rule.Action != store.MailRuleBill {
		return errInvalid("enum", []string{"body", "action"}, "action must be transaction or bill")
	}
	if rule.Direction != store.MailRuleExpense && rule.Direction != store.MailRuleIncome {
		return errInvalid("enum", []string{"body", "direction"},
			"direction must be expense or income")
	}
	if rule.AmountLabel == "" && rule.AmountPattern == "" {
		return errInvalid("missing", []string{"body", "amount_label"},
			"a rule needs an amount label or an amount pattern")
	}
	for _, named := range []struct {
		field, pattern string
	}{
		{"amount_pattern", rule.AmountPattern},
		{"date_pattern", rule.DatePattern},
		{"reference_pattern", rule.ReferencePattern},
		{"issued_pattern", rule.IssuedPattern},
		{"minimum_pattern", rule.MinimumPattern},
	} {
		if named.pattern == "" {
			continue
		}
		if err := billmail.CheckPattern(named.pattern); err != nil {
			return errInvalid("invalid", []string{"body", named.field}, "%s", err.Error())
		}
	}
	return nil
}

func mailRuleProto(one store.MailRule) *agentifiv1.MailRule {
	return &agentifiv1.MailRule{
		Id: one.ID.String(), Name: one.Name, Enabled: one.Enabled, Sender: one.Sender,
		SubjectContains: one.SubjectContains, BodyContains: one.BodyContains,
		AmountLabel: one.AmountLabel, AmountPattern: one.AmountPattern,
		DateLabel: one.DateLabel, DatePattern: one.DatePattern,
		ReferenceLabel: one.ReferenceLabel, ReferencePattern: one.ReferencePattern,
		IssuedLabel: one.IssuedLabel, IssuedPattern: one.IssuedPattern,
		MinimumLabel: one.MinimumLabel, MinimumPattern: one.MinimumPattern,
		Payee: one.Payee, PayeeLabel: one.PayeeLabel, NotesLabel: one.NotesLabel,
		NotesEndLabel: one.NotesEndLabel, Action: one.Action,
		AccountId: optionalID(one.AccountID), CategoryId: optionalID(one.CategoryID),
		BillConnectionId: optionalID(one.BillConnectionID),
		BillSubaccountId: optionalID(one.BillSubaccountID),
		Direction:        one.Direction, PadIncome: one.PadIncome,
		IncomeAccountId:  optionalID(one.IncomeAccountID),
		IncomeCategoryId: optionalID(one.IncomeCategoryID),
		IncomePayee:      one.IncomePayee, SortOrder: int32(one.SortOrder),
		CreatedAt: timestamppb.New(one.CreatedAt),
	}
}
