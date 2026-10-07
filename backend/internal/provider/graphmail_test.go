package provider

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeGraph answers the token endpoint and one folder's delta query.
type fakeGraph struct {
	mu sync.Mutex
	// tokens counts refresh-token trades.
	tokens int
	// refused makes the next read of the delta link answer 401, once.
	refused bool
	// seen records the paths the reader asked for.
	seen   []string
	server *httptest.Server
}

func newFakeGraph(t *testing.T) *fakeGraph {
	t.Helper()
	graph := &fakeGraph{refused: true}
	graph.server = httptest.NewServer(http.HandlerFunc(graph.serve))
	t.Cleanup(graph.server.Close)
	return graph
}

func (g *fakeGraph) url(path string) string { return g.server.URL + path }

func (g *fakeGraph) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = append(g.seen, r.URL.Path)
	write := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	switch {
	case r.URL.Path == "/tenant-1/oauth2/v2.0/token":
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		g.tokens++
		// The refresh token rolls on use.
		write(map[string]any{
			"access_token":  fmt.Sprintf("access-%d", g.tokens),
			"refresh_token": "refresh-2",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})

	case strings.HasSuffix(r.URL.Path, "/messages/delta"):
		write(map[string]any{
			"value":           []any{graphFixture("first", "-2h", false)},
			"@odata.nextLink": g.url("/v1.0/delta-page-2"),
		})

	case r.URL.Path == "/v1.0/delta-page-2":
		write(map[string]any{
			"value": []any{
				graphFixture("second", "-1h", true),
				// Older than the lookback.
				graphFixture("ancient", "-9000h", false),
			},
			"@odata.deltaLink": g.url("/v1.0/delta-link-1"),
		})

	case r.URL.Path == "/v1.0/delta-link-1":
		if g.refused {
			g.refused = false
			http.Error(w, `{"error":{"code":"InvalidAuthenticationToken"}}`, http.StatusUnauthorized)
			return
		}
		write(map[string]any{
			"value":            []any{graphFixture("third", "-10m", false)},
			"@odata.deltaLink": g.url("/v1.0/delta-link-2"),
		})

	case strings.HasSuffix(r.URL.Path, "/attachments"):
		write(map[string]any{"value": []any{
			map[string]any{
				"@odata.type": "#microsoft.graph.fileAttachment",
				"name":        "statement.pdf", "contentType": "application/pdf",
				"size":         len(graphPDF),
				"contentBytes": base64.StdEncoding.EncodeToString([]byte(graphPDF)),
			},
			map[string]any{
				"@odata.type": "#microsoft.graph.fileAttachment",
				"name":        "logo.png", "contentType": "image/png", "size": 12,
				"contentBytes": base64.StdEncoding.EncodeToString([]byte("not a bill")),
			},
		}})

	default:
		http.Error(w, `{"error":{"code":"itemNotFound"}}`, http.StatusNotFound)
	}
}

const graphPDF = "%PDF-1.4 invented statement"

func graphFixture(id, ago string, attachments bool) map[string]any {
	offset, _ := time.ParseDuration(ago)
	return map[string]any{
		"id":                "graph-" + id,
		"internetMessageId": "<" + id + "@mail.example.invalid>",
		"receivedDateTime":  time.Now().UTC().Add(offset).Format(time.RFC3339),
		"subject":           "Your statement is ready",
		"hasAttachments":    attachments,
		"from": map[string]any{"emailAddress": map[string]any{
			"name": "Example Utility", "address": "Billing@Example.Invalid",
		}},
		"body": map[string]any{"contentType": "html", "content": "<p>Amount due</p>"},
	}
}

func TestGraphMailboxFollowsTheDeltaLinkAndRefreshes(t *testing.T) {
	graph := newFakeGraph(t)
	mailbox := &GraphMailbox{
		ClientID: "app-1", Tenant: "tenant-1", Address: "bills@example.invalid",
		Folder: "Inbox", RefreshToken: "refresh-1",
		AuthBaseURL: graph.server.URL, APIBaseURL: graph.server.URL + "/v1.0",
		Client: graph.server.Client(),
	}

	// The first read walks both pages and stops at the delta link, dropping
	// what is older than the lookback.
	first, cursor, err := mailbox.ListNew(t.Context(), nil, 30*24*time.Hour)
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Equal(t, "<first@mail.example.invalid>", first[0].ID)
	require.Equal(t, "<second@mail.example.invalid>", first[1].ID)
	require.True(t, first[0].ReceivedAt.Before(first[1].ReceivedAt), "oldest first")
	require.Equal(t, "billing@example.invalid", first[0].Sender, "the address alone, lower-cased")
	require.Equal(t, "<p>Amount due</p>", first[0].HTML)
	require.Empty(t, first[0].Text, "an HTML-only mail is stripped by the reader, not here")

	// Only the PDF is fetched.
	require.Len(t, first[1].Attachments, 1)
	require.Equal(t, "statement.pdf", first[1].Attachments[0].Filename)
	require.Equal(t, []byte(graphPDF), first[1].Attachments[0].Bytes)

	var stand graphCursor
	require.NoError(t, json.Unmarshal(cursor, &stand))
	require.Equal(t, "inbox", stand.FolderID)
	require.Equal(t, graph.url("/v1.0/delta-link-1"), stand.Delta)

	// The second read sends the delta link, is refused once, mints a token
	// afresh and carries on.
	graph.mu.Lock()
	tokensBefore := graph.tokens
	graph.mu.Unlock()

	second, cursor, err := mailbox.ListNew(t.Context(), cursor, 30*24*time.Hour)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "<third@mail.example.invalid>", second[0].ID)

	require.NoError(t, json.Unmarshal(cursor, &stand))
	require.Equal(t, graph.url("/v1.0/delta-link-2"), stand.Delta)

	graph.mu.Lock()
	defer graph.mu.Unlock()
	require.Equal(t, tokensBefore+2, graph.tokens,
		"one token for the read and one more after the refusal")
	require.Equal(t, "refresh-2", mailbox.RotatedSecret(),
		"the rolled refresh token comes back so the poll can re-seal it")
}

func TestGraphMailboxResolvesANamedFolderOnce(t *testing.T) {
	// A folder that is not the Inbox costs one lookup, and the id it answers
	// is what the cursor carries afterwards.
	var (
		asked int
		base  string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-1", "token_type": "Bearer", "expires_in": 3600,
			})
		case strings.HasSuffix(r.URL.Path, "/mailFolders"):
			asked++
			require.Equal(t, "displayName eq 'Bills'", r.URL.Query().Get("$filter"))
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{
				map[string]any{"id": "folder-9", "displayName": "Bills"},
			}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []any{}, "@odata.deltaLink": base + "/v1.0/delta-link-1",
			})
		}
	}))
	t.Cleanup(server.Close)
	base = server.URL

	mailbox := &GraphMailbox{
		ClientID: "app-1", Tenant: "tenant-1", Address: "bills@example.invalid",
		Folder: "Bills", RefreshToken: "refresh-1",
		AuthBaseURL: server.URL, APIBaseURL: server.URL + "/v1.0", Client: server.Client(),
	}
	_, cursor, err := mailbox.ListNew(t.Context(), nil, time.Hour)
	require.NoError(t, err)
	var stand graphCursor
	require.NoError(t, json.Unmarshal(cursor, &stand))
	require.Equal(t, "folder-9", stand.FolderID)

	_, _, err = mailbox.ListNew(t.Context(), cursor, time.Hour)
	require.NoError(t, err)
	require.Equal(t, 1, asked, "the folder id is resolved once and kept in the cursor")
}
