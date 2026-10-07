package billers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// MyChart's billing pages walked end to end by the module itself, against a
// real Chromium: a summary of three account cards behind the portal's frame
// (one printing no number), an account page whose latest statement is on a
// "Billing Documents" tab with the rest behind "View past statements" in a
// dialog that loads more and whose Payments tab lists only the payments since
// the last statement until a hidden "Viewing options" radio and its Apply
// swap in a wider period, an account whose statements and letters are paged,
// and statements whose files are a viewer framing the PDF, the PDF itself, or
// a page with no PDF at all. Every name, number and figure is invented.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/ -run MyChart.*AgainstARealBrowser

const myChartLiveSummary = `<!doctype html><title>Billing Summary</title><body>
<a class="skip" href="#main">Skip navigation to main content</a>
<header><nav><a href="/MyChart/Home/">Home</a><a href="/MyChart/Messaging">Messages</a>
  <a href="/MyChart/Billing/Summary">Billing Summary</a></nav></header>
<div id="wrap"><main id="main"><h1>Billing Account Summary</h1>
<p>Need help with a bill? Call us on weekdays from 8 to 5.</p>
<div class="cards">
  <div class="card">
    <div class="cardHeader"><h2>Example Medical Group</h2><div>Physician Services</div>
      <div>Guarantor #00001234 (Sam Example)</div></div>
    <div>Your Balance <strong>$45.00</strong></div>
    <a class="button" href="/MyChart/Billing/Payment?id=1">Pay now</a>
    <div>Last paid: $30.00 on 02/20/2026</div>
    <a href="/MyChart/Billing/Details?ID=a1">View account</a>
    <a href="/MyChart/Billing/Details?ID=a1&amp;show=last">View last statement (03/12/2026)</a>
  </div>
  <div class="card">
    <div class="cardHeader"><h2>Example Hospital</h2><span>Account #00005678</span></div>
    <div>Your Balance <strong>$0.00</strong></div>
    <a href="/MyChart/Billing/Details?ID=a2">View account</a>
  </div>
  <div class="card">
    <div class="cardHeader"><h2>Example Clinic</h2></div>
    <div>Your Balance <strong>$12.00</strong></div>
    <a href="/MyChart/Billing/Details?ID=a3">View balance details</a>
  </div>
</div></main></div></body>`

// myChartLiveTabs shows a tab's panel and hides the rest.
const myChartLiveTabs = `<script>
  for (const tab of document.querySelectorAll('[data-panel]')) {
    tab.addEventListener('click', (event) => {
      event.preventDefault();
      for (const panel of document.querySelectorAll('.panel')) panel.style.display = 'none';
      document.getElementById(tab.dataset.panel).style.display = '';
    });
  }
</script>`

const myChartLiveGroup = `<!doctype html><title>Billing Account Details</title><body>
<header><nav><a href="/MyChart/Home/">Home</a></nav></header>
<main><h1>Billing Account Details</h1><p>Guarantor #00001234</p>
  <div role="tablist">
    <button role="tab" data-panel="overview">Overview</button>
    <button role="tab" data-panel="visits">Account Details</button>
    <a id="tab_topic_Payments" role="tab" href="#" data-panel="payments">Payments</a>
    <button role="tab" data-panel="documents">Billing Documents</button>
  </div>
  <section id="overview" class="panel">
    <div>Your Balance $45.00</div>
    <div>Last paid: $30.00 on 02/20/2026</div>
    <a href="/MyChart/Billing/Details/StatementViewer?id=s5">View last statement (03/12/2026)</a>
  </section>
  <section id="visits" class="panel" style="display:none">
    <div class="visit"><span>Jan 6, 2026</span> <span>Office visit</span> <span>$140.00</span></div>
  </section>
  <section id="payments" class="panel" style="display:none">
    <a class="backToLink" href="/MyChart/Billing/Summary">Billing Summary</a>
    <button type="button">Get itemized bill</button>
    <img id="ba_details_printerfriendlylink" role="button" alt="Printer-friendly" src="data:,">
    <div id="ba_details_visits_filters" role="button" tabindex="0">Currently viewing: Payments since last statement</div>
    <div id="viewingOptions" style="display:none"><h3>Viewing options</h3>
      <input type="radio" id="filterOption_0" name="paymentSectionFilter" class="clearradio togglebutton" value="0" checked>
      <label for="filterOption_0">Since last statement</label>
      <input type="radio" id="filterOption_1" name="paymentSectionFilter" class="clearradio togglebutton" value="1">
      <label for="filterOption_1">Year to date</label>
      <input type="radio" id="filterOption_2" name="paymentSectionFilter" class="clearradio togglebutton" value="2">
      <label for="filterOption_2">Last year</label>
      <input type="radio" id="filterOption_3" name="paymentSectionFilter" class="clearradio togglebutton" value="3">
      <label for="filterOption_3">Date range</label>
      <input type="date" aria-label="From"><input type="date" aria-label="To">
      <button type="button" id="applyFilter">Apply</button>
    </div>
    <p id="showing"></p>
    <div id="paymentList"></div>
    <p>Don't see the payment you are looking for? <a href="#" id="moreOptions">Click here for more viewing options.</a></p>
  </section>
  <section id="documents" class="panel" style="display:none">
    <h2>Statements</h2>
    <p>Only your most recent statement is shown here.</p>
    <div class="statement">
      <div class="cal"><span>Mar</span><span>12</span><span>2026</span></div>
      <a href="/MyChart/Billing/Details/StatementViewer?id=s5"><img src="data:," alt="View"></a>
      <span>Sent electronically</span> <span class="amount">$45.00</span>
    </div>
    <button type="button" id="past">View past statements</button>
    <h2>Detailed Bills</h2>
    <div class="detailed"><div class="cal"><span>Oct</span><span>28</span><span>2025</span></div>
      <a href="/MyChart/Billing/Details/DetailedBill?id=d1">View (PDF)</a></div>
  </section>
</main>
<div id="pastDialog" role="dialog" aria-modal="true" aria-label="Past statements" style="display:none">
  <h2>Past statements</h2>
  <ul id="pastList"></ul>
  <button type="button" id="loadMore">Load more</button>
  <button type="button" id="closePast">Close</button>
</div>
` + myChartLiveTabs + `
<script>
  const past = [['Feb', '12', '2026', '$30.00', 's4'], ['Jan', '12', '2026', '$25.00', 's3'],
    ['Dec', '12', '2025', '$60.00', 's2'], ['Nov', '12', '2025', '$15.50', 's1']];
  let shown = 0;
  const more = () => {
    for (const [month, day, year, amount, id] of past.slice(shown, shown + 2)) {
      const row = document.createElement('li');
      row.innerHTML = '<div class="cal"><span>' + month + '</span><span>' + day + '</span><span>' + year +
        '</span></div><span>' + amount + '</span>';
      const view = document.createElement('button');
      view.type = 'button';
      view.textContent = 'View';
      view.onclick = () => window.open('/MyChart/Billing/Details/StatementViewer?id=' + id);
      row.appendChild(view);
      document.getElementById('pastList').appendChild(row);
    }
    shown += 2;
    if (shown >= past.length) document.getElementById('loadMore').remove();
  };
  const dialog = document.getElementById('pastDialog');
  document.getElementById('past').onclick = () => { dialog.style.display = ''; if (shown === 0) more(); };
  document.getElementById('loadMore').onclick = () => setTimeout(more, 300);
  document.getElementById('closePast').onclick = () => { dialog.style.display = 'none'; };
  document.addEventListener('keydown', (event) => { if (event.key === 'Escape') dialog.style.display = 'none'; });

  const periods = [
    ['since last statement', 'since 03/12/2026', [['March', '19', '2026', 'Mar 19 2026', 'MasterCard x0000', '$45.00', 'r5']]],
    ['year to date', 'since 01/01/2026', [['March', '19', '2026', 'Mar 19 2026', 'MasterCard x0000', '$45.00', 'r5'],
      ['February', '20', '2026', 'Feb 20 2026', 'Visa x0000', '$30.00', 'r4'],
      ['January', '20', '2026', 'Jan 20 2026', 'MasterCard x0000', '$25.00', 'r3']]],
    ['last year', 'from 01/01/2025 to 12/31/2025', [['December', '18', '2025', 'Dec 18 2025', 'Visa x0000', '$60.00', 'r2'],
      ['November', '20', '2025', 'Nov 20 2025', 'MasterCard x0000', '$15.50', 'r1']]],
  ];
  const drawPayments = (at) => {
    const [named, since, rows] = periods[at];
    document.getElementById('ba_details_visits_filters').textContent = 'Currently viewing: Payments ' + named;
    document.getElementById('showing').textContent =
      'Showing ' + rows.length + ' out of ' + rows.length + ' payments ' + since;
    document.getElementById('paymentList').innerHTML = rows.map(([month, day, year, short, method, amount, id]) =>
      '<div class="paymentRow"><div class="sr-only"><span>' + month + '</span><span>' + day + '</span><span>' + year +
      '</span></div><span aria-hidden="true">' + short + '</span> <span>Patient Payment</span> <span>' + method +
      '</span> <span>' + amount + '</span> <button type="button" data-receipt="' + id + '">View receipt</button></div>').join('');
  };
  document.getElementById('paymentList').addEventListener('click', (event) => {
    const receipt = event.target.closest('[data-receipt]');
    if (receipt) window.open('/MyChart/Billing/Receipt?id=' + receipt.dataset.receipt);
  });
  const options = document.getElementById('viewingOptions');
  document.getElementById('ba_details_visits_filters').onclick = () => {
    options.style.display = options.style.display === 'none' ? '' : 'none';
  };
  document.getElementById('applyFilter').onclick = () => {
    const ticked = document.querySelector('input[name="paymentSectionFilter"]:checked').value;
    document.getElementById('paymentList').innerHTML = '';
    if (ticked !== '3') setTimeout(() => drawPayments(Number(ticked)), 300);
  };
  drawPayments(0);
</script>
<style>.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
  .sr-only span { display: block; }</style></body>`

const myChartLiveHospital = `<!doctype html><title>Billing Account Details</title><body>
<main><h1>Billing Account Details</h1><p>Account #00005678</p>
  <div class="tabs">
    <a href="#overview" data-panel="overview">Overview</a>
    <a href="#letters" data-panel="letters">Statements/Letters</a>
  </div>
  <section id="overview" class="panel"><div>Your Balance $0.00</div></section>
  <section id="letters" class="panel" style="display:none"><h2>Statements and letters</h2>
    <table><tbody id="rows"></tbody></table>
    <nav aria-label="Pages"><button type="button" id="prev">Previous</button>
      <span id="at"></span><button type="button" id="next" aria-label="Next page">›</button></nav>
  </section>
</main>
` + myChartLiveTabs + `
<script>
  const pages = [
    [['10/05/2025', 'Statement', '$80.00', 'h4'], ['09/05/2025', 'Statement', '$20.00', 'h3']],
    [['08/05/2025', 'Statement', '$10.00', 'h2'], ['07/07/2025', 'Letter: financial assistance', '', 'l1']],
  ];
  let at = 0;
  const draw = () => {
    document.getElementById('rows').innerHTML = pages[at].map(([day, what, amount, id]) =>
      '<tr><td>' + day + '</td><td>' + what + '</td><td>' + amount + '</td><td>' +
      (amount === '' ? '<a href="/MyChart/Billing/Details/Letter?id=' + id + '">View letter</a>' :
        '<a href="/MyChart/Billing/Details/StatementPDF?id=' + id + '">View statement</a>') + '</td></tr>').join('');
    document.getElementById('at').textContent = 'Page ' + (at + 1) + ' of ' + pages.length;
    document.getElementById('next').disabled = at === pages.length - 1;
    document.getElementById('prev').disabled = at === 0;
  };
  document.getElementById('next').onclick = () => { at += 1; draw(); };
  document.getElementById('prev').onclick = () => { at -= 1; draw(); };
  draw();
</script></body>`

const myChartLiveClinic = `<!doctype html><title>Billing Account Details</title><body>
<main><h1>Example Clinic</h1><p>Guarantor #: 99009999</p>
  <h2>Statements</h2>
  <div class="row"><div>Statement date: 04/02/2026</div><div>Amount due $12.00</div><div>Due date: 04/30/2026</div>
    <a href="/MyChart/Billing/Details/StatementPage?id=c1">View statement</a></div>
</main></body>`

const myChartLiveViewer = `<!doctype html><title>Statement</title><body>
<main><h1>Your statement</h1>
  <iframe title="Statement" src="/MyChart/Billing/Details/StatementPDF?id=%s" width="600" height="400"></iframe>
  <button type="button" onclick="window.print()">Print</button>
</main></body>`

const myChartLiveStatementPage = `<!doctype html><title>Statement</title><body>
<main><h1>Example Clinic statement</h1><p>Statement date: 04/02/2026</p><p>Amount due $12.00</p></main></body>`

// myChartLivePDF is a statement's file, named by its id so a test can tell
// which one was fetched.
func myChartLivePDF(id string) string {
	return "%PDF-1.4\n% invented statement " + id + "\n%%EOF\n"
}

func myChartLiveSite(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	html := func(body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}
	switch r.URL.Path {
	case "/MyChart/Billing/Summary":
		html(myChartLiveSummary)
	case "/MyChart/Billing/Details":
		switch r.URL.Query().Get("ID") {
		case "a1":
			html(myChartLiveGroup)
		case "a2":
			html(myChartLiveHospital)
		case "a3":
			html(myChartLiveClinic)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	case "/MyChart/Billing/Details/StatementViewer":
		html(strings.Replace(myChartLiveViewer, "%s", id, 1))
	case "/MyChart/Billing/Details/StatementPDF":
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, myChartLivePDF(id))
	case "/MyChart/Billing/Details/StatementPage":
		html(myChartLiveStatementPage)
	default:
		html(`<!doctype html><title>MyChart</title><main><h1>Elsewhere</h1></main>`)
	}
}

// liveTLSPage is a real page in a context that takes the test server's own
// certificate: MyChart's account area is https only.
func liveTLSPage(t *testing.T, answer http.HandlerFunc) (browser.Page, string, func()) {
	t.Helper()
	site := httptest.NewTLSServer(answer)
	engine := browser.NewEngine(liveBrowserSettings(t))
	chromium, err := engine.Browser()
	require.NoError(t, err)
	context, err := chromium.NewContext(playwright.BrowserNewContextOptions{
		IgnoreHttpsErrors: playwright.Bool(true),
		Viewport:          &playwright.Size{Width: 1280, Height: 900},
	})
	require.NoError(t, err)
	opened, err := context.NewPage()
	require.NoError(t, err)
	return browser.Wrap(opened), site.URL, func() {
		_ = context.Close()
		_ = engine.Close()
		site.Close()
	}
}

func TestMyChartsBillingPagesAreWalkedEndToEndAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	page, base, done := liveTLSPage(t, myChartLiveSite)
	defer done()
	module := &MyChart{Draft: NewMyChart().Draft, site: base + "/MyChart"}
	notes := &Notes{}
	var trail []string
	call := Call{Ctx: t.Context(), Page: page, Notes: notes, Trail: func(note string, look bool) {
		if look {
			note += "\n" + ReadingSnapshot(page)
		}
		trail = append(trail, note)
	}}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the walk's trail:\n%s\nits notes:\n%s", strings.Join(trail, "\n"), strings.Join(notes.List(), "\n"))
		}
	})

	accounts, err := module.Subaccounts(call)
	require.NoError(t, err)
	require.Equal(t, []Subaccount{
		{ExternalID: "00001234", Label: "Billing account ****1234 · Example Medical Group",
			MaskedNumber: domain.MaskAccount("00001234")},
		{ExternalID: "00005678", Label: "Billing account ****5678 · Example Hospital",
			MaskedNumber: domain.MaskAccount("00005678")},
		{ExternalID: "99009999", Label: "Billing account ****9999 · Example Clinic",
			MaskedNumber: domain.MaskAccount("99009999")},
	}, accounts, "the card that printed no number is known by its page's")

	pulled, err := module.FetchBills(call)
	require.NoError(t, err)
	require.False(t, pulled.NeedsSignIn)
	issued := map[string][]string{}
	for _, bill := range pulled.Bills {
		issued[bill.Subaccount] = append(issued[bill.Subaccount], bill.IssuedOn+" "+bill.AmountDue.String())
	}
	require.Equal(t, map[string][]string{
		"00001234": {"2025-11-12 15.50", "2025-12-12 60.00", "2026-01-12 25.00", "2026-02-12 30.00", "2026-03-12 45.00"},
		"00005678": {"2025-08-05 10.00", "2025-09-05 20.00", "2025-10-05 80.00"},
		"99009999": {"2026-04-02 12.00"},
	}, issued, "the latest statement, every past one behind Load more, every page, and no detailed bill or letter")
	var paid []string
	for _, payment := range pulled.Payments {
		paid = append(paid, payment.ExternalID+" "+payment.Method)
	}
	slices.Sort(paid)
	require.Equal(t, []string{
		"00001234:2025-11-20:15.50 MasterCard x0000",
		"00001234:2025-12-18:60.00 Visa x0000",
		"00001234:2026-01-20:25.00 MasterCard x0000",
		"00001234:2026-02-20:30.00 Visa x0000",
		"00001234:2026-03-19:45.00 MasterCard x0000",
	}, paid, "every period's payments, each once, though each prints its day twice and two periods list one of them")
	require.Len(t, pulled.Subaccounts, 3)

	ways := map[string]string{}
	for _, bill := range pulled.Bills {
		document, err := module.FetchDocument(call, bill)
		require.NoError(t, err)
		require.NotNilf(t, document, "the statement of %s", bill.IssuedOn)
		require.True(t, isPDF(document.Bytes))
		ways[bill.IssuedOn] = string(document.Bytes)
	}
	for day, id := range map[string]string{
		"2026-03-12": "s5", "2026-02-12": "s4", "2026-01-12": "s3", "2025-12-12": "s2", "2025-11-12": "s1",
		"2025-10-05": "h4", "2025-09-05": "h3", "2025-08-05": "h2",
	} {
		require.Equalf(t, myChartLivePDF(id), ways[day], "the statement of %s is its own file", day)
	}
	require.NotContains(t, ways["2026-04-02"], "invented statement", "a page with no PDF is printed")

	all := strings.Join(trail, "\n")
	require.Contains(t, all, "MyChart's billing summary")
	require.Contains(t, all, "after showing the tab “Billing Documents”")
	require.Contains(t, all, "pressed “View past statements” (more): 4 new rows", "two statements, each a dated row too")
	require.Contains(t, all, "pressed “Load more” (more): 4 new rows")
	require.Contains(t, all, "pressed “Next page” (next): 3 new rows", "a statement and a letter, dated, and the statement")
	require.Contains(t, all, "opened a window at /MyChart/Billing/Details/StatementViewer")
	require.Contains(t, all, "followed its page's iframe at /MyChart/Billing/Details/StatementPDF to the PDF")
	require.Contains(t, all, "printed its page, which offered no PDF of its own")
	require.Contains(t, all, "Payments can be shown for Year to date · Last year", "never the date range, which asks for days")
	require.Contains(t, all, "Payments shown for “Year to date”")
	require.Contains(t, all, "chose “Year to date” (applied by “Apply”)")
	require.Contains(t, all, "chose “Last year” (applied by “Apply”)")
	require.NotContains(t, all, "opened nothing this reader can fetch again",
		"neither the period filter nor a receipt is a statement control")
	require.NotContains(t, all, "/MyChart/Billing/Receipt")
	require.True(t, slices.ContainsFunc(trail, func(line string) bool {
		return strings.Contains(line, "href=/MyChart/Billing/Details/StatementViewer")
	}), "a page's snapshot names its links' paths")
	require.NotContains(t, all, "Skip navigation")
	for _, note := range notes.List() {
		require.Truef(t, strings.HasPrefix(note, "MyChart lists ") || strings.HasPrefix(note, "MyChart billing account "),
			"nothing but the accounts and their counts: %q", note)
	}
}
