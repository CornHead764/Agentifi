package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Waiting for a code, and asking the model about one. While a sign-in is
// stopped on a code it is registered here, and mail from its provider or a
// household relay is read harder: the digit reader without the sender table,
// then the household's model with no tools. A model's answer must appear
// verbatim in the mail and look like a code, and is never logged or stored
// beyond mailboxCodes.

// MailModel is the model a mail is put to; provider.Assistant is one.
type MailModel interface {
	Complete(ctx context.Context, messages []provider.ChatMessage, tools []domain.AssistantTool) (provider.Reply, error)
}

// ErrAssistantUnavailable is a space with no model configured, or switched
// off. The API answers it with AssistantUnavailableCode so the page can offer
// setup.
var ErrAssistantUnavailable = errors.New("the assistant is not set up, or it is switched off")

const AssistantUnavailableCode = "assistant_unavailable"

// MailModelFor needs a store carrying the credential cipher: the API key is
// sealed.
func MailModelFor(st *store.Store) func(context.Context, store.SpaceID) (MailModel, error) {
	return func(ctx context.Context, spaceID store.SpaceID) (MailModel, error) {
		connection, err := st.GetAssistantConnection(ctx, spaceID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrAssistantUnavailable
		}
		if err != nil {
			return nil, err
		}
		if !connection.IsEnabled {
			return nil, ErrAssistantUnavailable
		}
		key, err := st.AssistantConnectionKey(ctx, spaceID)
		if err != nil {
			return nil, err
		}
		return &provider.Assistant{
			BaseURL: connection.BaseURL, APIKey: key, Model: connection.Model,
			ToolCallStyle: connection.ToolCallStyle,
		}, nil
	}
}

type codeOwner struct {
	biller   domain.BillerID
	merchant domain.MerchantID
}

func (o codeOwner) none() bool { return o.biller == "" && o.merchant == "" }

type codeWait struct {
	spaceID store.SpaceID
	owner   codeOwner
	// name is the provider as the household says it: what the model is asked
	// about and what a relayed text is matched on.
	name    string
	senders []string
	// domains are whole domains (subdomains included) a code may come from,
	// for a login whose codes come by e-mail from an unrecorded sender.
	domains []string
	since   time.Time
}

// codeWaits is in memory for the same reason mailboxCodes is.
var codeWaits expiring[string, codeWait]

// codeSlack is how long before a sign-in's own clock a code's mail may be
// stamped: the provider sends it before the sign-in reads the page, and the
// mail server's clock is not this one.
const codeSlack = time.Minute

// modelCodeTimeout: a slower model will not beat a person to the code.
const modelCodeTimeout = 45 * time.Second

// modelCodeTextLimit: a code sits near the top of its mail.
const modelCodeTextLimit = 6000

func openCodeWait(wait codeWait) func() {
	id := uuid.NewString()
	codeWaits.Put(id, wait, time.Time{})
	return func() { codeWaits.Delete(id) }
}

func waitsIn(spaceID store.SpaceID) []codeWait {
	var out []codeWait
	codeWaits.Each(func(_ string, wait codeWait) bool {
		if wait.spaceID == spaceID {
			out = append(out, wait)
		}
		return true
	})
	return out
}

// otherWaiters makes a relayed text naming nobody unanswerable.
func otherWaiters(spaceID store.SpaceID, self codeOwner) bool {
	for _, wait := range waitsIn(spaceID) {
		if wait.owner != self {
			return true
		}
	}
	return false
}

type foundCode struct {
	owner   codeOwner
	code    string
	relayed bool
}

func (m *Mailbox) readCode(
	ctx context.Context, spaceID store.SpaceID, message billmail.Message,
) (foundCode, bool) {
	if otp, found := findOTP(message, m.Relays); found {
		out := foundCode{owner: codeOwner{biller: otp.Biller}, code: otp.Code, relayed: otp.Relayed}
		if out.owner.none() {
			if wait, named := waiterNamedIn(spaceID, message); named {
				out.owner = wait.owner
			}
		}
		return out, true
	}

	wait, relayed, waiting := waiterFor(spaceID, message, m.Relays)
	if !waiting {
		return foundCode{}, false
	}
	text := message.Subject + "\n" + billmail.BodyText(message)
	if code := billmail.FindCode(text); code != "" {
		return foundCode{owner: wait.owner, code: code, relayed: relayed}, true
	}
	code, found := m.askModelForCode(ctx, spaceID, wait.name, message)
	if !found {
		return foundCode{}, false
	}
	return foundCode{owner: wait.owner, code: code, relayed: relayed}, true
}

// waiterFor is the waiting sign-in a message could be the code for: the one
// whose provider sent it, or for a relayed text the only one waiting or the
// one the text names.
func waiterFor(
	spaceID store.SpaceID, message billmail.Message, relays []string,
) (codeWait, bool, bool) {
	var fresh []codeWait
	for _, wait := range waitsIn(spaceID) {
		if message.ReceivedAt.Before(wait.since.Add(-codeSlack)) {
			continue
		}
		fresh = append(fresh, wait)
	}
	for _, wait := range fresh {
		if (len(wait.senders) > 0 && billmail.SentBy(message, wait.senders)) ||
			billmail.SentFromDomain(message, wait.domains) {
			return wait, false, true
		}
	}
	if !billmail.IsRelay(message, relays) {
		return codeWait{}, false, false
	}
	if len(fresh) == 1 {
		return fresh[0], true, true
	}
	if wait, named := waiterNamedIn(spaceID, message); named {
		return wait, true, true
	}
	return codeWait{}, false, false
}

func waiterNamedIn(spaceID store.SpaceID, message billmail.Message) (codeWait, bool) {
	text := strings.ToLower(message.Subject + "\n" + billmail.BodyText(message))
	var named []codeWait
	for _, wait := range waitsIn(spaceID) {
		if wait.name != "" && strings.Contains(text, strings.ToLower(wait.name)) {
			named = append(named, wait)
		}
	}
	if len(named) != 1 {
		return codeWait{}, false
	}
	return named[0], true
}

// askModelForCode asks the household's model one narrow question with no
// tools and the mail as delimited data. The mail can talk the model into
// anything, so the answer is only a candidate CheckCode must accept. No
// model, or a failed one, falls back to a person.
func (m *Mailbox) askModelForCode(
	ctx context.Context, spaceID store.SpaceID, providerName string, message billmail.Message,
) (string, bool) {
	if m.Model == nil {
		return "", false
	}
	model, err := m.Model(ctx, spaceID)
	if err != nil {
		return "", false
	}
	asking, cancel := context.WithTimeout(ctx, modelCodeTimeout)
	defer cancel()

	reply, err := model.Complete(asking, []provider.ChatMessage{
		{Role: "system", Content: "You extract one-time sign-in codes from email. " +
			UntrustedMailRule + " Answer with only a JSON object and nothing else: " +
			`{"code": "<the code exactly as it is written in the email>"}, or {"code": null} ` +
			"when the email carries no one-time sign-in code for the provider named."},
		{Role: "user", Content: fmt.Sprintf(
			"Extract the one-time sign-in code for %s from this message. Answer JSON {\"code\": string|null}.\n\n%s",
			providerName, MailAsData(message, modelCodeTextLimit))},
	}, nil)
	if err != nil {
		// The provider's error, never the mail or an answer.
		m.log().Warn("mailbox: the model could not be asked for a code", "error", err)
		return "", false
	}
	candidate, ok := codeFromReply(reply.Content)
	if !ok || !billmail.CheckCode(candidate, message, billmail.CodeDigits) {
		return "", false
	}
	return candidate, true
}

// codeFromReply reads {"code": "..."} from the first JSON object in a reply,
// fenced or not.
func codeFromReply(content string) (string, bool) {
	object, ok := firstJSONObject(content)
	if !ok {
		return "", false
	}
	var answer struct {
		Code *string `json:"code"`
	}
	if err := json.Unmarshal([]byte(object), &answer); err != nil || answer.Code == nil {
		return "", false
	}
	code := strings.TrimSpace(*answer.Code)
	return code, code != ""
}

func firstJSONObject(text string) (string, bool) {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return "", false
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}
	return "", false
}

// waitForCode registers the sign-in and polls every mailbox in the space
// every otpPollEvery, for at most the answerer's window. Its own loop because
// the scheduler's pass is too slow for a code's lifetime. sole says no other
// sign-in is waiting, which makes a relayed text naming nobody this one's.
func (m *Mailbox) waitForCode(
	ctx context.Context, wait codeWait, sole func() bool,
) (string, bool) {
	closeWait := openCodeWait(wait)
	defer closeWait()

	patience := m.OTPWait
	if patience <= 0 {
		patience = DefaultOTPWait
	}
	// The wall clock, not the injected one: a frozen test clock would never
	// reach the deadline.
	deadline := time.Now().Add(patience)
	for {
		alone := !otherWaiters(wait.spaceID, wait.owner) && sole()
		read := m.pollForACode(ctx, wait.spaceID)
		if code, found := takeOwnedCode(wait.spaceID, wait.owner, wait.since.Add(-codeSlack), alone); found {
			return code, true
		}
		// Nothing in the space can answer (usually no mailbox), so hand the
		// sign-in back to a person now.
		if read == 0 || !time.Now().Before(deadline) {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(min(otpPollEvery, time.Until(deadline))):
		}
	}
}

func (m *Mailbox) HasReadableMailbox(ctx context.Context, spaceID store.SpaceID) bool {
	connections, err := m.store.ListEmailConnections(ctx, spaceID)
	if err != nil {
		return false
	}
	for _, connection := range connections {
		if connection.Enabled && connection.HasSecret {
			return true
		}
	}
	return false
}

// WaitForMerchantCode finds the code a merchant sign-in was just sent, or
// nothing; since is when the sign-in reached the code page. The caller types
// it in.
func (m *Mailbox) WaitForMerchantCode(
	ctx context.Context, spaceID store.SpaceID, merchant domain.MerchantID, since time.Time,
) (string, bool) {
	name := string(merchant)
	if known, ok := domain.MerchantByID(merchant); ok {
		name = known.Name
	}
	return m.waitForCode(ctx, codeWait{
		spaceID: spaceID, owner: codeOwner{merchant: merchant}, name: name,
		senders: billmail.MerchantCodeSendersFor(merchant), since: since,
	}, func() bool { return m.noWaitingChallenges(ctx, spaceID) })
}

func (m *Mailbox) noWaitingChallenges(ctx context.Context, spaceID store.SpaceID) bool {
	waiting, err := m.store.ListBillChallenges(ctx, spaceID, store.BillChallengeWaiting)
	return err == nil && len(waiting) == 0
}
