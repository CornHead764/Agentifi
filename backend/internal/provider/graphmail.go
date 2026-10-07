package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
	"golang.org/x/oauth2"
)

// Office 365 through Microsoft Graph, signed in by delegated device code:
// Exchange Online refuses IMAP basic auth, and client credentials would grant
// every mailbox in the tenant and need a secret held here. The sealed refresh
// token rolls on use, so the poll must store RotatedSecret after every call.

// Defaults for the AuthBaseURL and APIBaseURL fields.
const (
	graphAuthBaseURL = "https://login.microsoftonline.com"
	graphAPIBaseURL  = "https://graph.microsoft.com/v1.0"
)

// Mail.Read.Shared because the watched mailbox is usually a shared one;
// offline_access is what makes a refresh token exist.
var graphScopes = []string{
	"https://graph.microsoft.com/Mail.Read",
	"https://graph.microsoft.com/Mail.Read.Shared",
	"offline_access",
}

func graphOAuth(clientID, tenant, authBase string) *oauth2.Config {
	if authBase == "" {
		authBase = graphAuthBaseURL
	}
	if tenant == "" {
		tenant = "common"
	}
	return &oauth2.Config{
		ClientID: clientID,
		Scopes:   graphScopes,
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: authBase + "/" + tenant + "/oauth2/v2.0/devicecode",
			TokenURL:      authBase + "/" + tenant + "/oauth2/v2.0/token",
			// A public client has no secret for a Basic header.
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

type GraphDeviceSignIn struct {
	// AuthBaseURL and APIBaseURL default to Microsoft's own.
	AuthBaseURL string
	APIBaseURL  string
	Client      *http.Client
}

type GraphDeviceCode struct {
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	Interval        time.Duration
	// Poll blocks for minutes, until the person finishes or the code expires;
	// the caller runs it in a goroutine.
	Poll func(ctx context.Context) (refreshToken string, signedInAs string, err error)
}

func (g GraphDeviceSignIn) Start(ctx context.Context, clientID, tenant string) (GraphDeviceCode, error) {
	if strings.TrimSpace(clientID) == "" {
		return GraphDeviceCode{}, errors.New("provider: the mailbox has no app registration client id")
	}
	config := graphOAuth(clientID, tenant, g.AuthBaseURL)
	if g.Client != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, g.Client)
	}
	response, err := config.DeviceAuth(ctx)
	if err != nil {
		return GraphDeviceCode{}, fmt.Errorf("provider: asking Microsoft for a device code: %w", err)
	}
	interval := time.Duration(response.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return GraphDeviceCode{
		UserCode:        response.UserCode,
		VerificationURI: textutil.FirstNonBlank(response.VerificationURIComplete, response.VerificationURI),
		ExpiresAt:       response.Expiry,
		Interval:        interval,
		Poll: func(ctx context.Context) (string, string, error) {
			if g.Client != nil {
				ctx = context.WithValue(ctx, oauth2.HTTPClient, g.Client)
			}
			token, err := config.DeviceAccessToken(ctx, response)
			if err != nil {
				return "", "", err
			}
			if token.RefreshToken == "" {
				return "", "", errors.New(
					"provider: Microsoft answered without a refresh token; " +
						"the app registration needs the offline_access scope")
			}
			return token.RefreshToken, g.signedInAs(ctx, token.AccessToken), nil
		},
	}, nil
}

// signedInAs is best effort: a failed profile read does not undo the sign-in.
func (g GraphDeviceSignIn) signedInAs(ctx context.Context, accessToken string) string {
	base := g.APIBaseURL
	if base == "" {
		base = graphAPIBaseURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/me?$select=mail,userPrincipalName", nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	client := g.Client
	if client == nil {
		client = graphClient
	}
	var body struct {
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if _, err := httpx.DoJSON(client, request, 1<<16, &body); err != nil {
		return ""
	}
	return textutil.FirstNonBlank(body.Mail, body.UserPrincipalName)
}

// GraphMailbox reads one folder of one mailbox, always through
// /users/{address}, which serves the signed-in person's own mailbox as well as
// a shared one.
type GraphMailbox struct {
	ClientID     string
	Tenant       string
	Address      string
	Folder       string
	RefreshToken string

	AuthBaseURL string
	APIBaseURL  string
	Client      *http.Client

	rotated string
}

// RotatedSecret is the new refresh token, empty when it did not change.
func (m *GraphMailbox) RotatedSecret() string { return m.rotated }

type graphCursor struct {
	FolderID string `json:"folder_id,omitempty"`
	Delta    string `json:"delta,omitempty"`
}

func (m *GraphMailbox) api() string {
	if m.APIBaseURL != "" {
		return m.APIBaseURL
	}
	return graphAPIBaseURL
}

func (m *GraphMailbox) http() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return graphClient
}

// A stalled call would hold the whole mailbox pass.
var graphClient = &http.Client{Timeout: 60 * time.Second}

func (m *GraphMailbox) token(ctx context.Context) (string, error) {
	if m.Client != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, m.Client)
	}
	current := m.RefreshToken
	if m.rotated != "" {
		current = m.rotated
	}
	source := graphOAuth(m.ClientID, m.Tenant, m.AuthBaseURL).
		TokenSource(ctx, &oauth2.Token{RefreshToken: current})
	token, err := source.Token()
	if err != nil {
		return "", fmt.Errorf("provider: Microsoft refused the mailbox's refresh token: %w", err)
	}
	if token.RefreshToken != "" && token.RefreshToken != current {
		m.rotated = token.RefreshToken
	}
	return token.AccessToken, nil
}

// get retries a refusal once with a fresh token; a second is revoked consent
// and must surface.
func (m *GraphMailbox) get(ctx context.Context, endpoint string, out any) error {
	accessToken, err := m.token(ctx)
	if err != nil {
		return err
	}
	response, err := httpx.RetryOnceOnAuth(ctx,
		func(ctx context.Context) (httpx.Response, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return httpx.Response{}, err
			}
			request.Header.Set("Authorization", "Bearer "+accessToken)
			request.Header.Set("Accept", "application/json")
			return httpx.Read(m.http(), request, MaxMailAttachmentBytes+1<<20)
		},
		func(ctx context.Context) (err error) {
			m.rotated = ""
			accessToken, err = m.token(ctx)
			return err
		})
	if err != nil {
		return fmt.Errorf("provider: reading the mailbox: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("provider: the mailbox answered %w", response.Err())
	}
	return json.Unmarshal(response.Body, out)
}

type graphFolderList struct {
	Value []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"value"`
}

// folderID resolves the folder's name to Graph's id; "inbox" is a well-known
// name Graph accepts in place of one.
func (m *GraphMailbox) folderID(ctx context.Context) (string, error) {
	folder := strings.TrimSpace(m.Folder)
	if folder == "" || strings.EqualFold(folder, "inbox") {
		return "inbox", nil
	}
	var list graphFolderList
	endpoint := m.api() + "/users/" + url.PathEscape(m.Address) + "/mailFolders?$filter=" +
		url.QueryEscape("displayName eq '"+strings.ReplaceAll(folder, "'", "''")+"'")
	if err := m.get(ctx, endpoint, &list); err != nil {
		return "", err
	}
	for _, one := range list.Value {
		if strings.EqualFold(one.DisplayName, folder) {
			return one.ID, nil
		}
	}
	return "", fmt.Errorf("provider: the mailbox has no folder called %q", folder)
}

type graphMessage struct {
	ID                string `json:"id"`
	InternetMessageID string `json:"internetMessageId"`
	ReceivedDateTime  string `json:"receivedDateTime"`
	Subject           string `json:"subject"`
	HasAttachments    bool   `json:"hasAttachments"`
	From              struct {
		EmailAddress struct {
			Address string `json:"address"`
			Name    string `json:"name"`
		} `json:"emailAddress"`
	} `json:"from"`
	Body struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	// Removed marks a message the delta says is gone; it carries no headers.
	Removed *struct{} `json:"@removed"`
}

type graphDeltaPage struct {
	Value     []graphMessage `json:"value"`
	NextLink  string         `json:"@odata.nextLink"`
	DeltaLink string         `json:"@odata.deltaLink"`
}

const graphMessageSelect = "id,internetMessageId,receivedDateTime,from,subject,body,hasAttachments"

// ListNew walks the folder's delta query from the cursor. The delta query takes
// no filter, so a first read (the whole folder) is cut to the lookback here.
func (m *GraphMailbox) ListNew(
	ctx context.Context, cursor json.RawMessage, lookback time.Duration,
) ([]MailMessage, json.RawMessage, error) {
	var stand graphCursor
	if len(cursor) > 0 {
		if err := json.Unmarshal(cursor, &stand); err != nil {
			stand = graphCursor{}
		}
	}
	if stand.FolderID == "" {
		id, err := m.folderID(ctx)
		if err != nil {
			return nil, cursor, err
		}
		stand.FolderID = id
	}

	next := stand.Delta
	if next == "" {
		next = m.api() + "/users/" + url.PathEscape(m.Address) + "/mailFolders/" +
			url.PathEscape(stand.FolderID) + "/messages/delta?$select=" + graphMessageSelect
	}
	floor := time.Now().UTC().Add(-lookback)

	var out []MailMessage
	for page := 0; next != ""; page++ {
		if page >= graphMaxPages {
			break
		}
		var answer graphDeltaPage
		if err := m.get(ctx, next, &answer); err != nil {
			return out, cursorOf(stand), err
		}
		for _, one := range answer.Value {
			if one.Removed != nil || one.InternetMessageID == "" {
				continue
			}
			received, err := time.Parse(time.RFC3339, one.ReceivedDateTime)
			if err != nil {
				continue
			}
			if received.Before(floor) {
				continue
			}
			message, err := m.message(ctx, one, received)
			if err != nil {
				return out, cursorOf(stand), err
			}
			out = append(out, message)
		}
		if answer.DeltaLink != "" {
			stand.Delta = answer.DeltaLink
			break
		}
		next = answer.NextLink
	}

	sortByReceived(out)
	return out, cursorOf(stand), nil
}

func (m *GraphMailbox) message(
	ctx context.Context, one graphMessage, received time.Time,
) (MailMessage, error) {
	message := MailMessage{
		ID:         one.InternetMessageID,
		ProviderID: one.ID,
		Sender:     senderAddress(one.From.EmailAddress.Address),
		Subject:    one.Subject,
		ReceivedAt: received.UTC(),
	}
	if strings.EqualFold(one.Body.ContentType, "html") {
		message.HTML = one.Body.Content
	} else {
		message.Text = one.Body.Content
	}
	if !one.HasAttachments {
		return message, nil
	}
	attachments, err := m.attachments(ctx, one.ID)
	if err != nil {
		return message, err
	}
	message.Attachments = attachments
	return message, nil
}

// Fetch filters by message-id rather than using the delta query, which would
// move the cursor.
func (m *GraphMailbox) Fetch(ctx context.Context, messageID string) (MailMessage, bool, error) {
	folder, err := m.folderID(ctx)
	if err != nil {
		return MailMessage{}, false, err
	}
	endpoint := m.api() + "/users/" + url.PathEscape(m.Address) + "/mailFolders/" +
		url.PathEscape(folder) + "/messages?$top=1&$select=" + graphMessageSelect +
		"&$filter=" + url.QueryEscape(
		"internetMessageId eq '"+strings.ReplaceAll(messageID, "'", "''")+"'")

	var answer struct {
		Value []graphMessage `json:"value"`
	}
	if err := m.get(ctx, endpoint, &answer); err != nil {
		return MailMessage{}, false, err
	}
	for _, one := range answer.Value {
		if one.Removed != nil || one.InternetMessageID != messageID {
			continue
		}
		received, err := time.Parse(time.RFC3339, one.ReceivedDateTime)
		if err != nil {
			continue
		}
		message, err := m.message(ctx, one, received)
		if err != nil {
			return MailMessage{}, false, err
		}
		return message, true, nil
	}
	return MailMessage{}, false, nil
}

// graphMaxPages caps one poll's walk; the next poll carries on.
const graphMaxPages = 50

func cursorOf(stand graphCursor) json.RawMessage {
	raw, err := json.Marshal(stand)
	if err != nil {
		return nil
	}
	return raw
}

type graphAttachmentList struct {
	Value []struct {
		Type         string `json:"@odata.type"`
		Name         string `json:"name"`
		ContentType  string `json:"contentType"`
		Size         int    `json:"size"`
		ContentBytes string `json:"contentBytes"`
	} `json:"value"`
}

func (m *GraphMailbox) attachments(ctx context.Context, messageID string) ([]MailAttachment, error) {
	var list graphAttachmentList
	endpoint := m.api() + "/users/" + url.PathEscape(m.Address) + "/messages/" +
		url.PathEscape(messageID) + "/attachments"
	if err := m.get(ctx, endpoint, &list); err != nil {
		return nil, err
	}
	var out []MailAttachment
	for _, one := range list.Value {
		if one.ContentBytes == "" || !wantedAttachment(one.ContentType, one.Size) {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(one.ContentBytes)
		if err != nil {
			continue
		}
		out = append(out, MailAttachment{
			Filename: one.Name, ContentType: MailAttachmentContentType, Bytes: raw,
		})
	}
	return out, nil
}

// sortByReceived orders oldest first, so a poll that stops halfway has finished
// everything before where it stopped.
func sortByReceived(messages []MailMessage) {
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].ReceivedAt.Before(messages[j].ReceivedAt)
	})
}
