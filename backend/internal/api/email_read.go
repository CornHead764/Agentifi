package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

// Reading the household's mail back, and asking the model about it. The body
// is never stored: each read fetches from the mailbox and caps the text. A
// message the folder has lost is a 404, and one carrying a sign-in code is
// refused to a person and the assistant alike.

// EmailMessageTextResponse is one message's text, fetched on demand.
type EmailMessageTextResponse struct {
	ID           uuid.UUID `json:"id"`
	ConnectionID uuid.UUID `json:"connection_id"`
	MessageID    string    `json:"message_id"`
	Sender       string    `json:"sender"`
	Subject      string    `json:"subject"`
	ReceivedAt   time.Time `json:"received_at"`
	Outcome      string    `json:"outcome"`
	Text         string    `json:"text"`
	// Truncated says the text was cut at the cap.
	Truncated bool `json:"truncated"`
}

// MailRuleSuggestionResponse is a drafted rule, unsaved.
type MailRuleSuggestionResponse struct {
	Rule MailRuleDraftResponse `json:"rule"`
	// Dropped is what of the model's answer was left out, and why.
	Dropped []string `json:"dropped"`
	// Sample is the mail the draft came from, for the editor's try box. It
	// is in this response and nowhere else.
	Sample MailSampleResponse `json:"sample"`
}

// MailRuleDraftResponse is the fields of a rule a person reviews before
// saving it.
type MailRuleDraftResponse struct {
	// Action is transaction or bill. A bill draft names BillConnectionID, the
	// provider the model chose from the household's own, and no account.
	Action           string     `json:"action"`
	BillConnectionID *uuid.UUID `json:"bill_connection_id"`
	IssuedLabel      string     `json:"issued_label"`
	IssuedPattern    string     `json:"issued_pattern"`
	MinimumLabel     string     `json:"minimum_label"`
	MinimumPattern   string     `json:"minimum_pattern"`
	// StatementAccountID is the household account the model says a card or
	// loan statement is of: not part of the rule, but the link the editor
	// offers to set on the billed account when the rule is saved.
	StatementAccountID *uuid.UUID `json:"statement_account_id"`
	Name               string     `json:"name"`
	Sender             string     `json:"sender"`
	SubjectContains    string     `json:"subject_contains"`
	BodyContains       string     `json:"body_contains"`
	AmountLabel        string     `json:"amount_label"`
	AmountPattern      string     `json:"amount_pattern"`
	DateLabel          string     `json:"date_label"`
	DatePattern        string     `json:"date_pattern"`
	ReferenceLabel     string     `json:"reference_label"`
	ReferencePattern   string     `json:"reference_pattern"`
	Payee              string     `json:"payee"`
	PayeeLabel         string     `json:"payee_label"`
	NotesLabel         string     `json:"notes_label"`
	NotesEndLabel      string     `json:"notes_end_label"`
	Direction          string     `json:"direction"`
	AccountID          *uuid.UUID `json:"account_id"`
	CategoryID         *uuid.UUID `json:"category_id"`
	PadIncome          bool       `json:"pad_income"`
	IncomeAccountID    *uuid.UUID `json:"income_account_id"`
	IncomeCategoryID   *uuid.UUID `json:"income_category_id"`
	IncomePayee        string     `json:"income_payee"`
}

type MailSampleResponse struct {
	Sender  string `json:"sender"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// listAllEmailMessages is the space's whole message log, every mailbox,
// newest first.
func listAllEmailMessages(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	limit, err := queryLimit(r, 50, 500)
	if err != nil {
		return err
	}
	rows, err := env.DB.ListBillEmails(r.Context(), sp.ID(), uuid.Nil, limit)
	if err != nil {
		return err
	}
	out, err := emailMessageResponses(env, r, sp, rows)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func readEmailMessageText(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	fetched, err := fetchLoggedMail(env, r, sp)
	if err != nil {
		return err
	}
	text, truncated := service.MailText(fetched.Message, service.MailTextLimit)
	return writeJSON(w, http.StatusOK, EmailMessageTextResponse{
		ID: fetched.Row.ID, ConnectionID: fetched.Row.ConnectionID, MessageID: fetched.Row.MessageID,
		Sender: fetched.Row.Sender, Subject: fetched.Row.Subject, ReceivedAt: fetched.Row.ReceivedAt,
		Outcome: fetched.Row.Outcome, Text: text, Truncated: truncated,
	})
}

// suggestMailRule asks the household's model to draft a rule from one mail.
// Nothing is saved: the draft is for the rule editor, and Save is a person's.
func suggestMailRule(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "message_id", "Message")
	if err != nil {
		return err
	}
	suggestion, err := NewMailbox(env).SuggestMailRule(r.Context(), sp.ID(), id)
	if err != nil {
		return mailReadError(err)
	}
	rule := suggestion.Rule
	text, _ := service.MailText(suggestion.Sample, service.MailTextLimit)
	dropped := suggestion.Dropped
	if dropped == nil {
		dropped = []string{}
	}
	return writeJSON(w, http.StatusOK, MailRuleSuggestionResponse{
		Rule: MailRuleDraftResponse{
			Action: rule.Action, BillConnectionID: dbconv.NullUUID(rule.BillConnectionID),
			IssuedLabel: rule.IssuedLabel, IssuedPattern: rule.IssuedPattern,
			MinimumLabel: rule.MinimumLabel, MinimumPattern: rule.MinimumPattern,
			StatementAccountID: dbconv.NullUUID(suggestion.StatementAccountID),
			Name:               rule.Name, Sender: rule.Sender,
			SubjectContains: rule.SubjectContains, BodyContains: rule.BodyContains,
			AmountLabel: rule.AmountLabel, AmountPattern: rule.AmountPattern,
			DateLabel: rule.DateLabel, DatePattern: rule.DatePattern,
			ReferenceLabel: rule.ReferenceLabel, ReferencePattern: rule.ReferencePattern,
			Payee: rule.Payee, PayeeLabel: rule.PayeeLabel, Direction: rule.Direction,
			NotesLabel: rule.NotesLabel, NotesEndLabel: rule.NotesEndLabel,
			AccountID: dbconv.NullUUID(rule.AccountID), CategoryID: dbconv.NullUUID(rule.CategoryID),
			PadIncome:        rule.PadIncome,
			IncomeAccountID:  dbconv.NullUUID(rule.IncomeAccountID),
			IncomeCategoryID: dbconv.NullUUID(rule.IncomeCategoryID),
			IncomePayee:      rule.IncomePayee,
		},
		Dropped: dropped,
		Sample: MailSampleResponse{
			Sender: suggestion.Sample.Sender, Subject: suggestion.Sample.Subject, Text: text,
		},
	})
}

// fetchLoggedMail is the message a path names, fetched from its mailbox.
func fetchLoggedMail(env *Env, r *http.Request, sp auth.SpaceContext) (service.FetchedMail, error) {
	id, err := pathUUID(r, "message_id", "Message")
	if err != nil {
		return service.FetchedMail{}, err
	}
	fetched, err := NewMailbox(env).FetchLogged(r.Context(), sp.ID(), id)
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
