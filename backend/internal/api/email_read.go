package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

// Reading the household's mail back, and asking the model about it. The body
// is never stored: each read fetches from the mailbox and caps the text. A
// message the folder has lost is a 404, and one carrying a sign-in code is
// refused to a person and the assistant alike.

// EmailMessageTextResponse is one message's text as
// GET /email/messages/{message_id}/text writes it, which the assistant's
// read_mail reads.
type EmailMessageTextResponse struct {
	ID           uuid.UUID `json:"id"`
	ConnectionID uuid.UUID `json:"connection_id"`
	MessageID    string    `json:"message_id"`
	Sender       string    `json:"sender"`
	Subject      string    `json:"subject"`
	ReceivedAt   time.Time `json:"received_at"`
	Outcome      string    `json:"outcome"`
	Text         string    `json:"text"`
	Truncated    bool      `json:"truncated"`
}

// ListEmailMessages is the space's whole message log, every mailbox, newest
// first.
func (s emailService) ListEmailMessages(
	ctx context.Context, req *agentifiv1.ListEmailMessagesRequest,
) (*agentifiv1.ListEmailMessagesResponse, error) {
	sp := spaceFrom(ctx)
	limit, err := mailLogLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.env.DB.ListBillEmails(ctx, sp.ID(), uuid.Nil, limit)
	if err != nil {
		return nil, err
	}
	out, err := emailMessageProtos(ctx, s.env, sp, rows)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ListEmailMessagesResponse{Messages: out}, nil
}

func (s emailService) GetEmailMessageText(
	ctx context.Context, req *agentifiv1.GetEmailMessageTextRequest,
) (*agentifiv1.GetEmailMessageTextResponse, error) {
	fetched, err := fetchLoggedMail(ctx, s.env, spaceFrom(ctx), req.GetMessageId())
	if err != nil {
		return nil, err
	}
	text, truncated := service.MailText(fetched.Message, service.MailTextLimit)
	row := fetched.Row
	return &agentifiv1.GetEmailMessageTextResponse{
		Id: row.ID.String(), ConnectionId: row.ConnectionID.String(), MessageId: row.MessageID,
		Sender: row.Sender, Subject: row.Subject, ReceivedAt: timestamppb.New(row.ReceivedAt),
		Outcome: row.Outcome, Text: text, Truncated: truncated,
	}, nil
}

// SuggestMailRule asks the household's model to draft a rule from one mail.
// Nothing is saved: the draft is for the rule editor, and Save is a person's.
func (s emailService) SuggestMailRule(
	ctx context.Context, req *agentifiv1.SuggestMailRuleRequest,
) (*agentifiv1.SuggestMailRuleResponse, error) {
	id, err := idFrom(req.GetMessageId(), "Message")
	if err != nil {
		return nil, err
	}
	suggestion, err := NewMailbox(s.env).SuggestMailRule(ctx, spaceFrom(ctx).ID(), id)
	if err != nil {
		return nil, mailReadError(err)
	}
	rule := suggestion.Rule
	text, _ := service.MailText(suggestion.Sample, service.MailTextLimit)
	dropped := suggestion.Dropped
	if dropped == nil {
		dropped = []string{}
	}
	return &agentifiv1.SuggestMailRuleResponse{
		Rule: &agentifiv1.SuggestedMailRule{
			Action: rule.Action, BillConnectionId: optionalID(rule.BillConnectionID),
			IssuedLabel: rule.IssuedLabel, IssuedPattern: rule.IssuedPattern,
			MinimumLabel: rule.MinimumLabel, MinimumPattern: rule.MinimumPattern,
			StatementAccountId: optionalID(suggestion.StatementAccountID),
			Name:               rule.Name, Sender: rule.Sender,
			SubjectContains: rule.SubjectContains, BodyContains: rule.BodyContains,
			AmountLabel: rule.AmountLabel, AmountPattern: rule.AmountPattern,
			DateLabel: rule.DateLabel, DatePattern: rule.DatePattern,
			ReferenceLabel: rule.ReferenceLabel, ReferencePattern: rule.ReferencePattern,
			Payee: rule.Payee, PayeeLabel: rule.PayeeLabel, Direction: rule.Direction,
			NotesLabel: rule.NotesLabel, NotesEndLabel: rule.NotesEndLabel,
			AccountId: optionalID(rule.AccountID), CategoryId: optionalID(rule.CategoryID),
			PadIncome:        rule.PadIncome,
			IncomeAccountId:  optionalID(rule.IncomeAccountID),
			IncomeCategoryId: optionalID(rule.IncomeCategoryID),
			IncomePayee:      rule.IncomePayee,
		},
		Dropped: dropped,
		Sample: &agentifiv1.MailSample{
			Sender: suggestion.Sample.Sender, Subject: suggestion.Sample.Subject, Text: text,
		},
	}, nil
}

// fetchLoggedMail is the message a log row's id names, fetched from its
// mailbox.
func fetchLoggedMail(
	ctx context.Context, env *Env, sp auth.SpaceContext, rawID string,
) (service.FetchedMail, error) {
	id, err := idFrom(rawID, "Message")
	if err != nil {
		return service.FetchedMail{}, err
	}
	fetched, err := NewMailbox(env).FetchLogged(ctx, sp.ID(), id)
	if err != nil {
		return service.FetchedMail{}, mailReadError(err)
	}
	return fetched, nil
}

// mailReadError is the answer a read of the mail gives for each way it can
// not happen.
func mailReadError(err error) error {
	switch {
	case errors.Is(err, service.ErrMailGone), isNotFound(err):
		return errNotFound("Message")
	case errors.Is(err, service.ErrMailWithheld):
		return errConflict("%s", err.Error())
	}
	var upstream *service.ModelError
	if errors.As(err, &upstream) {
		return errBadGateway("%s", err.Error())
	}
	return err
}
