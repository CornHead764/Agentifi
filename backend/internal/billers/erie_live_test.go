package billers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The invoice's own link pressed on a documents page in Erie's shape, against
// a real Chromium. The page is invented: one row per document, the invoice's
// link carrying its handle, or only its row carrying the print date.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/ -run AgainstARealBrowser

const erieLiveStatement = "%PDF-1.4\n% an invented statement\n%%EOF\n"

func erieLiveDocuments(link string) string {
	return `<!doctype html><title>My Documents</title><body><table>
<tr><td>Declarations</td><td>07/01/2026</td><td><a href="/files/other" data-handle="100200299">View</a></td></tr>
<tr><td>Invoice</td><td>08/25/2026</td><td>Q00-0001234</td><td>` + link + `</td></tr>
</table></body>`
}

func erieLiveSite(t *testing.T, link string) (Call, func()) {
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/documents":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, erieLiveDocuments(link))
		case "/files/invoice":
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `attachment; filename="invoice.pdf"`)
			_, _ = io.WriteString(w, erieLiveStatement)
		case "/api/pdf/download":
			_ = r.ParseForm()
			if r.PostForm.Get("origin") == "Gateway" {
				if _, err := r.Cookie("MRHSession"); err != nil {
					http.Redirect(w, r, "/vdesk/hold", http.StatusFound)
					return
				}
			}
			if r.Method == http.MethodPost && r.PostForm.Get("documentHandle") == "100200300" &&
				r.PostForm.Get("startDate") != "" {
				http.Redirect(w, r, "/files/invoice", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/Login/Login", http.StatusFound)
		case "/vdesk/hold":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><title>Please wait</title><h1>Checking your access</h1>
<form method="post" action="/my.policy"><input type="hidden" name="state" value="invented"></form>`)
		case "/my.policy":
			http.SetCookie(w, &http.Cookie{Name: "MRHSession", Value: "invented", Path: "/"})
			http.Redirect(w, r, "/vdesk/done", http.StatusFound)
		case "/vdesk/done":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><title>Access granted</title><h1>You may close this window</h1>`)
		case "/viewer/invoice":
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", "inline")
			_, _ = io.WriteString(w, erieLiveStatement)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<!doctype html><title>elsewhere</title>")
		}
	})
	require.NoError(t, page.Goto(base+"documents"))
	return Call{Ctx: context.Background(), Page: page, Notes: &Notes{}}, done
}

func TestTheInvoicesOwnLinkIsPressedAndItsFileReadAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	ref := erieStatementRef{
		Handle: "100200300", ID: "abcdef01", Policy: "Q00-0001234", Printed: "2026-08-25",
		Account: "00000000-0000-4000-8000-000000000001", From: "2025-08-20", To: "2026-09-19",
		Row: map[string]any{"documentHandle": "100200300", "policyNumber": "Q00-0001234"},
	}
	for name, link := range map[string]string{
		"an attachment by its handle": `<a href="/files/invoice" data-handle="100200300">View</a>`,
		"a popup by its print date": `<button type="button" ` +
			`onclick="window.open('/viewer/invoice', '_blank')">View</button>`,
		"a script's download by its id": `<span role="button" data-id="abcdef01">View</span>
<script>document.querySelector('[data-id]').addEventListener('click', () => { location.href = '/files/invoice'; });</script>`,
		"an accordion's inner control": `<div class="doc-header" ng-click="vm.forms(doc)" data-toggle="collapse" aria-expanded="false"
  aria-controls="panel-1" data-handle="100200300">Invoice</div>
<div id="panel-1" style="display:none"><span>Pages: 2</span>
  <a href="#" class="icon" title="Download PDF" ng-click="vm.download(doc)">&#8595;</a></div>
<script>
  const header = document.querySelector('.doc-header');
  header.addEventListener('click', () => {
    document.getElementById('panel-1').style.display = 'block';
    header.setAttribute('aria-expanded', 'true');
  });
  document.querySelector('#panel-1 a').addEventListener('click', (event) => {
    event.preventDefault();
    location.href = '/files/invoice';
  });
</script>`,
		"the page's own hidden form": `<span>no link</span>
<form method="post" action="/api/pdf/download" style="display:none">
  <input type="hidden" name="documentHandle"><input type="hidden" name="policyNumber">
  <input type="hidden" name="startDate"><input type="hidden" name="origin" value="DocumentsPage"></form>
<form method="post" action="/api/pdf/download" style="display:none">
  <input type="hidden" name="documentHandle"><input type="hidden" name="policySourceSystem">
  <input type="hidden" name="startDate"><input type="hidden" name="origin"><input type="hidden" name="extra"></form>`,
		"the controller's own function": `<span>no link</span>
<script>
  const scope = {
    $parent: null,
    $apply(fn) { fn(); },
    vm: {
      documents: [{ documentHandle: '100200299' }, { documentHandle: '100200300', documentId: 'abcdef01' }],
      downloadDocument(doc) { location.href = '/files/invoice?h=' + doc.documentHandle; },
    },
  };
  window.angular = { element: () => ({ scope: () => scope }) };
</script>`,
		"the row's own ng-click past a list reset": `<a href="#" ng-click="vm.downloadInvoice(doc, $event)">View</a>
<script>
  const scope = {
    $parent: null,
    $apply(expr) {
      if (typeof expr === 'function') return expr();
      if (expr.startsWith('vm.downloadInvoice(')) location.href = '/files/invoice';
    },
    vm: {
      documents: [{ documentHandle: '100200300', documentId: 'abcdef01' }],
      resetAndGetDocuments() { location.href = '/elsewhere'; },
      getPolicyDocuments() { location.href = '/elsewhere'; },
    },
  };
  window.angular = { element: () => ({ scope: () => scope }) };
</script>`,
		"the page's own form through the gateway": `<span>no link</span>
<form method="post" action="/api/pdf/download" target="_blank" style="display:none">
  <input type="hidden" name="documentHandle"><input type="hidden" name="startDate">
  <input type="hidden" name="origin" value="Gateway"></form>`,
	} {
		t.Run(name, func(t *testing.T) {
			call, done := erieLiveSite(t, link)
			defer done()

			body, said := NewErie().askThePage(call, ref)

			require.Equalf(t, erieLiveStatement, string(body), "said: %s", said)
			require.False(t, strings.Contains(said, "100200300"))
		})
	}
}

func TestAPageWithNoLinkForTheInvoiceIsDescribedAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	call, done := erieLiveSite(t, `<form method="post" action="/api/pdf/download/77777?x=1">
<input type="hidden" name="documentHandle" value="100200300"><button type="submit">Download</button></form>`)
	defer done()

	body, said := NewErie().askThePage(call, erieStatementRef{Handle: "999999999", Printed: "2026-01-01"})

	require.Nil(t, body)
	require.Regexp(t, `the documents page shows no link or button for the invoice; submitted the page's own form `+
		`to 127\.0\.0\.1:\d+/api/pdf/download \(one of 1, target none\) with documentHandle from the page's own value: `+
		`the browser saw HTTP 200 at 127\.0\.0\.1:\d+/api/pdf/download \(not a PDF\); no popup opened `+
		`\(the documents page has 2 links and buttons; those naming a document: <a>, `+
		`attributes href data-handle, href 127\.0\.0\.1`, said)
	require.Regexp(t, `; a post form to 127\.0\.0\.1:\d+/api/pdf/download with inputs documentHandle\)$`, said)
	require.NotContains(t, said, "100200300")
	require.NotContains(t, said, "77777")
}
