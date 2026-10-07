package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

// The assistant's two mail tools, both through dispatch to the /email routes
// the settings page reads, so the deny list and viewer rules apply: list_mail
// is the message log, and read_mail the on-demand fetch, which refuses a
// message that carried a sign-in code. Neither can reach a credential.

// mailToolLimit is the most rows list_mail returns.
const mailToolLimit = 100

func (a assistantTools) listMail(ctx context.Context, arguments map[string]any) (any, error) {
	limit, err := toolLimit(arguments, 20, mailToolLimit)
	if err != nil {
		return nil, err
	}
	search := strings.ToLower(toolString(arguments, "search"))
	fetch := limit
	if search != "" {
		// Filtered here, so the page read is the widest one allowed.
		fetch = 500
	}
	response, err := a.env.dispatch(ctx, a.sp, http.MethodGet, "/email/messages",
		url.Values{"limit": {strconv.Itoa(fetch)}}, nil)
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, fmt.Errorf("the mail log answered %d: %s", response.Status,
			strings.TrimSpace(response.Body))
	}
	var rows []EmailMessageResponse
	if err := json.Unmarshal([]byte(response.Body), &rows); err != nil {
		return nil, fmt.Errorf("the mail log could not be read")
	}

	type row struct {
		ID         string `json:"id"`
		From       string `json:"from"`
		Subject    string `json:"subject"`
		ReceivedAt string `json:"received_at"`
		// Outcome is what the app did with it: bill, rule, otp, proposed,
		// unrecognised or failed.
		Outcome string `json:"outcome"`
		Note    string `json:"note,omitempty"`
	}
	out := make([]row, 0, limit)
	for _, one := range rows {
		if search != "" && !strings.Contains(strings.ToLower(one.Sender+" "+one.Subject), search) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, row{
			ID: one.ID.String(), From: one.Sender, Subject: one.Subject,
			ReceivedAt: one.ReceivedAt.UTC().Format("2006-01-02 15:04 MST"), Outcome: one.Outcome, Note: one.Note,
		})
	}
	return map[string]any{"messages": out, "returned": len(out)}, nil
}

func (a assistantTools) readMail(ctx context.Context, arguments map[string]any) (any, error) {
	raw := toolString(arguments, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%q is not a message id; take one from list_mail", raw)
	}
	response, err := a.env.dispatch(ctx, a.sp, http.MethodGet,
		"/email/messages/"+id.String()+"/text", nil, nil)
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, fmt.Errorf("that message could not be read (%d): %s", response.Status,
			strings.TrimSpace(response.Body))
	}
	var read EmailMessageTextResponse
	if err := json.Unmarshal([]byte(response.Body), &read); err != nil {
		return nil, fmt.Errorf("that message could not be read")
	}
	return map[string]any{
		"id":   read.ID.String(),
		"rule": service.UntrustedMailRule,
		"email": service.MailAsData(billmail.Message{
			Sender: read.Sender, Subject: read.Subject, ReceivedAt: read.ReceivedAt, Text: read.Text,
		}, service.MailTextLimit),
	}, nil
}

// mailTextPath says a path is a message's text, which read_endpoint leaves to
// read_mail: that tool's result is not kept in the conversation's record, and
// read_endpoint's is.
func mailTextPath(path string) bool {
	return underRoutePattern("/email/messages/{message_id}/text", normalizeDispatchPath(path))
}
