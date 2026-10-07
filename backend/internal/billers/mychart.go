package billers

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// MyChart is a patient portal, one product every health system deploys
// at its own address, so the connection's site is that address
// (`NeedsSite`, `SiteAddress`; domain.SiteAddressOf) and `WithSite` aims a
// copy at it. See docs/connectors/providers.md.
//
// Its statements are what a health savings account asks for as receipts:
// each is filed on the bill it is, and the payments the portal lists are
// paired with the bank rows that carried them (domain.MatchBillPayments), so
// the statement a payment settled shows on that row.
//
// The billing pages are read as rendered text rather than as the portal's
// own calls: the readers are pure functions over what the page shows, and
// every selector and word they rely on is in myChartPage, so a live run that
// finds them wrong changes one block. Each page a pull reads is kept on its
// trail (Call.Saw), so a pull that read less than the portal holds says what
// the portal showed. What is confirmed and what is not is in providers.md.
//
// The sign-in is the shared draft's: a username and password form, then, on
// a device it does not trust, a page offering a code by e-mail or text and a
// "trust this device" box, which the shared reading ticks.

type MyChart struct {
	Draft
	site string
}

// myChartPage is everything the billing pages are read by.
var myChartPage = struct {
	// Summary and Details are the billing pages under the portal's root.
	Summary, Details string
	// DetailsLink is the link from the summary to one billing account, and
	// OpenWords the words of the one a card opens its account with.
	DetailsLink, OpenWords string
	// AccountNumber is how a card or page prints a billing account's number,
	// as a JavaScript pattern too; its group is the number.
	AccountNumber string
	// Tabs are the controls that show another part of an account's page,
	// TabWords the ones worth showing, and NotATab the ones that carry the
	// words but start something.
	Tabs, TabWords, NotATab string
	// StatementControl is a link or button for one statement, and
	// NotAStatement the controls that carry the word, or sit among
	// statements, but are settings, itemised bills, letters, receipts or the
	// controls that choose which period a list shows.
	StatementControl, StatementWords, NotAStatement string
	// Filters are the controls that choose what a list shows (a period's
	// radios and their labels, a "Currently viewing" toggle), never a
	// statement's.
	Filters string
	// ViewingWords name a period a list can be shown for that reaches further
	// back than the one it opens on, ApplyWords the control that shows it, and
	// ViewingToggle the control that opens a collapsed panel of periods.
	ViewingWords, ApplyWords, ViewingToggle string
	// ViewWords are a control that opens its row without naming it: a
	// statement's under a heading or tab that names statements, as is one
	// with no words at all (an envelope icon).
	ViewWords string
	// MoreWords are a control that shows more of a list, and NextWords one
	// that turns to its next page.
	MoreWords, NextWords string
	// Headings name the section a row sits in where no tab does.
	Headings string
	// Dates is every spelling of a day the pages print, as a JavaScript
	// pattern too.
	Dates string
	// Chrome is a line of the portal's frame, never a billing account's name.
	Chrome string
}{
	Summary:       "/Billing/Summary",
	Details:       "/Billing/Details",
	DetailsLink:   `a[href*="/Billing/Details" i]`,
	OpenWords:     `view account|account details|balance details|details`,
	AccountNumber: `\b(?:guarantor|account|acct)\b\s*(?:#|no\.?|number|id)?\s*:?\s*#?\s*(\d[\d-]{3,19}\d)\b`,
	Tabs: `[role="tab"], [role="tablist"] a, [role="tablist"] button, .tabs a, .tabs button, ` +
		`nav a, nav button`,
	TabWords:         `statement|letter|document|communication|payment|activity|history`,
	NotATab:          `pay now|pay as guest|make a payment|payment plan|set up|paperless|estimate|sign up|setting|help`,
	StatementControl: `a, button, [role="button"]`,
	StatementWords:   `statement`,
	NotAStatement: `paperless|sign up|enrol|enroll|setting|preference|deliver|e-?mail me|learn more|help|` +
		`detailed bill|itemi[sz]ed|\bletter\b|receipt|since last statement|currently viewing|viewing options`,
	Filters: `label, input, select, option, summary, [role="radio"], [role="radiogroup"] *, ` +
		`[id*="filter" i], [class*="filter" i]`,
	ViewingWords: `year to date|this year|last year|previous year|prior year|past year|` +
		`last \d+ (?:days|months)|all (?:time|dates|payments)`,
	ApplyWords:    `^(?:apply|go|update|search|show)$`,
	ViewingToggle: `currently viewing|viewing options|filter|options`,
	ViewWords:     `^(?:view|open|download|print|see|show|pdf)\b[^\n]{0,30}$`,
	MoreWords: `show (?:all|more|older|previous|past)|load more|view (?:all|more|older|previous|past)|` +
		`see (?:all|more|older|previous|past)|(?:all|past|older|previous|more) statements`,
	NextWords: `^(?:next|next page|older|›|»|>)$`,
	Headings:  `h1, h2, h3, h4, [role="heading"], legend, caption`,
	// The year has no boundary after it: a page's inline spans run a day into
	// the next word ("2026Office visit"). A calendar drawn as an icon prints
	// its month, day and year on lines of their own, or as inline spans with
	// nothing between them ("Feb122026").
	Dates: `\b\d{1,2}/\d{1,2}/\d{4}|` +
		`\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)[a-z]*\.?\s*\d{1,2},?\s*\d{4}`,
	Chrome: `skip|main content|^menu$|^your menu$|log ?out|sign ?out|back to|^home$|` +
		`billing (?:account )?summary|^billing$`,
}

// How long a page is given to draw what a press showed, and how far one list
// is followed: presses of its "more" or "next", and statements whose control
// is opened to learn where its file is.
const (
	myChartWait    = 1200 * time.Millisecond
	myChartPresses = 25
	myChartOpens   = 30
)

var (
	myChartDay     = regexp.MustCompile(`(?i)` + myChartPage.Dates)
	myChartMoney   = regexp.MustCompile(`\(?-?\$\s?\d[\d,]*(?:\.\d{2})?\)?(?:\s?CR\b)?`)
	myChartAccount = regexp.MustCompile(`(?i)` + myChartPage.AccountNumber)
	myChartChrome  = regexp.MustCompile(`(?i)` + myChartPage.Chrome)

	// What labels a figure on a statement row, and on a payment row.
	myChartIssuedWords  = regexp.MustCompile(`(?i)statement|dated|issued|bill(?:ing)? date|sent`)
	myChartDueWords     = regexp.MustCompile(`(?i)\bdue\b|pay by|payment due`)
	myChartServiceWords = regexp.MustCompile(`(?i)service|visit|encounter`)
	myChartOwedWords    = regexp.MustCompile(
		`(?i)amount due|balance due|total due|new balance|statement balance|you owe|your (?:portion|balance)|` +
			`patient (?:balance|responsibility|portion)|\bbalance\b|\bdue\b`)
	myChartChargeWords    = regexp.MustCompile(`(?i)\bcharges?\b|billed|total cost`)
	myChartInsuranceWords = regexp.MustCompile(`(?i)insurance|insurer|plan paid|covered`)
	myChartAdjustWords    = regexp.MustCompile(`(?i)adjust|discount|write.?off|savings`)

	// A payment the household made, and what one not yet settled says. An
	// insurer's payment is not one: it never left a household account.
	myChartPaymentWords = regexp.MustCompile(
		`(?i)payment (?:received|-\s*thank|thank)|(?:patient|guarantor|online|mychart|web|card|self[- ]pay) payment|` +
			`paid by (?:you|patient|guarantor|card|visa|mastercard|discover|amex|american express)|\bpayment\b`)
	myChartUnsettledWords = regexp.MustCompile(
		`(?i)scheduled|pending|processing|declined|failed|reversed|returned|refund|void|cancel`)
	myChartPaymentSection        = regexp.MustCompile(`(?i)payment`)
	myChartPaidWords             = regexp.MustCompile(`(?i)payment|paid|amount`)
	myChartStatementControlWords = regexp.MustCompile(`(?i)` + myChartPage.StatementWords)
	myChartNotAStatement         = regexp.MustCompile(`(?i)` + myChartPage.NotAStatement)
	myChartLinkWords             = regexp.MustCompile(
		`(?i)` + myChartPage.StatementWords + `|pdf|view|download|open|print|viewer`)
	myChartMethodWords = regexp.MustCompile(
		`(?i)(?:visa|mastercard|discover|amex|american express|hsa|fsa|debit|credit card|card|check|bank account)` +
			`[^\n$(]{0,40}`)
)

// NewMyChart is unaimed and has no addresses: the engine refuses a NeedsSite
// connection with no site before it opens a browser.
func NewMyChart() *MyChart {
	return &MyChart{Draft: Draft{
		BillerID: domain.BillerMyChart,
		Home:     "https://www.mychart.org",
		Prompt: "Sign in to your health system's MyChart. If it offers to send a code, " +
			"e-mail lets a nightly update read it from your mailbox; tick “trust this device” if it is offered.",
	}}
}

// WithSite answers a copy aimed at one portal; a site that is not a portal
// address leaves it unaimed.
func (m *MyChart) WithSite(site string) Module {
	root, ok := domain.SiteAddressOf(site)
	if !ok {
		return m
	}
	aimed := &MyChart{Draft: m.Draft, site: root}
	// The billing summary is only an account holder's: signed out, it sends the
	// browser to the sign-in form, and signed in it is where a pull starts.
	aimed.SignIn = root + myChartPage.Summary
	aimed.Landing = root + myChartPage.Summary
	aimed.AccountPage = root + myChartPage.Summary
	aimed.Home = root
	aimed.AccountArea = func(address string) bool { return MyChartInside(root, address) }
	return aimed
}

func (m *MyChart) SiteHome() string { return m.site }

// myChartPublic is the first path segment under the portal's root of the
// pages a signed-out visitor sees there: the sign-in and its code pages, the
// sign-out, sign-up, recovery and the public forms.
var myChartPublic = regexp.MustCompile(
	`(?i)^(?:authentication|login|logout|signup|sign-?up|passwordreset|recover|accesscheck|publicforms|openscheduling|` +
		`selfsignup|activation|default\.asp|$)`)

// MyChartInside is the portal's account area: its own host, under its root,
// and not one of the pages a signed-out visitor sees.
func MyChartInside(root, address string) bool {
	base, err := url.Parse(root)
	if err != nil || base.Host == "" {
		return false
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), base.Hostname()) {
		return false
	}
	path := parsed.Path
	if prefix := base.Path; prefix != "" {
		rest, under := strings.CutPrefix(strings.ToLower(path), strings.ToLower(prefix)+"/")
		if !under {
			return false
		}
		path = rest
	} else {
		path = strings.TrimPrefix(path, "/")
	}
	first, _, _ := strings.Cut(path, "/")
	return !myChartPublic.MatchString(first)
}

// MyChartCard is one billing account as the summary shows it: its words and
// the link to its page. Account is the number its page printed, for a card
// that printed none.
type MyChartCard struct {
	Text    string `json:"text"`
	Href    string `json:"href"`
	Account string `json:"account,omitempty"`
}

// MyChartControl is a link or button on a row: its words and, for a link,
// where it goes.
type MyChartControl struct {
	Words string `json:"words"`
	Href  string `json:"href"`
}

// MyChartRow is one entry of an account's page: the tab or heading it sits
// under, its words, and its controls. Opens names the statement control of a
// row whose control is no link, for the walk to press. View is the tab and
// period the walk was showing when it read the row.
type MyChartRow struct {
	Section  string           `json:"section"`
	Text     string           `json:"text"`
	Controls []MyChartControl `json:"controls"`
	Opens    string           `json:"opens,omitempty"`
	View     string           `json:"view,omitempty"`
}

// MyChartAccountPage is what one account's page showed: its statements
// (rows read around each statement control), every dated row, and the
// account number it printed.
type MyChartAccountPage struct {
	Statements []MyChartRow `json:"statements"`
	Dated      []MyChartRow `json:"dated"`
	Account    string       `json:"account"`
}

// myChartRaw is what a statement keeps for FetchDocument and for anybody
// reading the bill later: where its file is and the figures beside the one
// owed.
type myChartRaw struct {
	Account    string `json:"account"`
	Statement  string `json:"statement,omitempty"`
	Charges    string `json:"charges,omitempty"`
	Insurance  string `json:"insurance,omitempty"`
	Adjustment string `json:"adjustments,omitempty"`
}

// myChartArgs is what every page script is handed.
func myChartArgs(more map[string]any) map[string]any {
	args := map[string]any{
		"dates": myChartPage.Dates, "account": myChartPage.AccountNumber,
		"statementWords": myChartPage.StatementWords, "notAStatement": myChartPage.NotAStatement,
		"viewWords": myChartPage.ViewWords, "moreWords": myChartPage.MoreWords, "nextWords": myChartPage.NextWords,
		"tabWords": myChartPage.TabWords, "notATab": myChartPage.NotATab, "tabs": myChartPage.Tabs,
		"headings": myChartPage.Headings, "controls": myChartPage.StatementControl, "filters": myChartPage.Filters,
		"viewingWords": myChartPage.ViewingWords, "applyWords": myChartPage.ApplyWords,
		"viewingToggle": myChartPage.ViewingToggle,
		"link":          myChartPage.DetailsLink, "open": myChartPage.OpenWords,
	}
	for name, value := range more {
		args[name] = value
	}
	return args
}

// myChartWordsJS declares words(el), a control's own words: its label, its
// text, its title, or an image's alt for an icon; and linkOf(el), where a
// control goes, or "" for one that goes nowhere a fetch can follow (a
// fragment, a script).
const myChartWordsJS = `
const imageWords = (el) => { const img = el.querySelector && el.querySelector('img[alt]'); return img ? img.getAttribute('alt') : ''; };
const words = (el) => (el.getAttribute('aria-label') || el.innerText || el.getAttribute('title') || imageWords(el) || '').trim().slice(0, 200);
const linkOf = (el) => {
  const raw = (el.getAttribute('href') || el.getAttribute('data-href') || el.getAttribute('data-url') || '').trim();
  if (raw === '' || raw.startsWith('#') || /^javascript:/i.test(raw)) return '';
  try { return new URL(raw, location.href).href; } catch (e) { return ''; }
};
`

// myChartSummaryScript is the summary's cards. A card is the widest element
// around a printed account number that prints no other, or, for a link to an
// account's page outside every such card, the widest element holding only
// that link. Neither ever reaches the page's frame: the main region, a
// header, a menu, the page's title or a "skip to content" link.
const myChartSummaryScript = `(v) => {` + agent.VisibleJS + myChartWordsJS + `
  const root = document.querySelector('main, [role="main"]') || document.body;
  const one = new RegExp(v.account, 'i');
  const open = new RegExp(v.open, 'i');
  const numbersIn = (el) => {
    const found = new Set();
    for (const m of (el.innerText || '').matchAll(new RegExp(v.account, 'gi'))) found.add(m[1].replace(/-/g, ''));
    return found.size;
  };
  const frame = (el) => el === root || el === document.body || !root.contains(el) ||
    el.matches('header, nav, footer, [role="navigation"], [role="banner"]') ||
    !!el.querySelector('h1, header, nav, footer, [role="navigation"], [role="banner"]') ||
    [...el.querySelectorAll('a[href^="#"]')].some((a) => /skip/i.test(a.textContent || ''));
  const climb = (from, holds) => {
    let card = from;
    while (card.parentElement && !frame(card.parentElement) && holds(card.parentElement)) card = card.parentElement;
    return card;
  };
  const opener = (card) => {
    const links = [...card.querySelectorAll(v.link)];
    const chosen = links.find((a) => open.test(words(a))) || links[0];
    return chosen ? chosen.href : '';
  };
  const cards = [];
  const seen = new Set();
  for (const el of root.querySelectorAll('*')) {
    if (!visible(el) || !one.test(el.innerText || '')) continue;
    if ([...el.children].some((child) => one.test(child.innerText || ''))) continue;
    const card = climb(el, (parent) => numbersIn(parent) === 1);
    if (seen.has(card)) continue;
    seen.add(card);
    cards.push({ text: (card.innerText || '').trim().slice(0, 4000), href: opener(card) });
  }
  const found = [...seen];
  for (const link of root.querySelectorAll(v.link)) {
    if (!visible(link) || found.some((card) => card.contains(link))) continue;
    const card = climb(link, (parent) => parent.querySelectorAll(v.link).length === 1 && numbersIn(parent) === 0);
    if (seen.has(card)) continue;
    seen.add(card);
    cards.push({ text: (card.innerText || '').trim().slice(0, 4000), href: link.href || '' });
  }
  return cards;
}`

// myChartRowsScript reads the rows the page shows, in the dialog in front
// or else the page's main region: around every statement control, the widest
// element holding only that one; and around every printed day, the widest
// element printing only that one day, once in each of its spellings (a row
// prints its day as a calendar for a screen reader and again as the eye sees
// it, but two rows of one day print each spelling twice). A row's section is
// the tab it was shown under, else the nearest heading before it. A statement control that is no
// link is marked for the walk to press, and the page's account number is read
// beside the rows.
const myChartRowsScript = `(v) => {` + agent.DialogScopeJS + agent.VisibleJS + myChartWordsJS + `
  const day = new RegExp(v.dates, 'gi');
  const oneDay = new RegExp(v.dates, 'i');
  const statementWords = new RegExp(v.statementWords, 'i');
  const notAStatement = new RegExp(v.notAStatement, 'i');
  const viewWords = new RegExp(v.viewWords, 'i');
  const moreWords = new RegExp(v.moreWords, 'i');
  const nextWords = new RegExp(v.nextWords, 'i');
  const main = document.querySelector('main, [role="main"]') || document.body;
  const root = agentifiScope !== document ? agentifiScope : main;
  const months = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec'];
  const dayOf = (said) => {
    const numeric = said.match(/(\d{1,2})\/(\d{1,2})\/(\d{4})/);
    if (numeric) return numeric[3] + '-' + Number(numeric[1]) + '-' + Number(numeric[2]);
    const spelled = said.match(/^([a-z]{3})[\s\S]*?(\d{1,2}),?\s*(\d{4})$/i);
    return spelled ? spelled[3] + '-' + (months.indexOf(spelled[1].toLowerCase()) + 1) + '-' + Number(spelled[2]) : said;
  };
  // days is 0 for an element printing no day, 1 for one printing one day once
  // in each spelling, and 2 for any other.
  const days = (el) => {
    const found = (el.innerText || '').match(day) || [];
    if (found.length === 0) return 0;
    const spellings = new Set(found.map((said) => said.replace(/\s+/g, ' ').toLowerCase()));
    return spellings.size === found.length && new Set(found.map(dayOf)).size === 1 ? 1 : 2;
  };
  const headingBefore = (el) => {
    let found = '';
    for (const h of document.querySelectorAll(v.headings)) {
      if (h.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING) found = (h.innerText || '').trim();
    }
    return found.slice(0, 120);
  };
  const isTab = (c) => c.matches(v.tabs) || !!c.closest('[role="tablist"]');
  const isMore = (c) => moreWords.test(words(c)) || nextWords.test(words(c)) || c.getAttribute('rel') === 'next';
  const isFilter = (c) => c.matches(v.filters) || !!c.querySelector('input, select');
  const isStatement = (c) => {
    if (!visible(c) || isTab(c) || isMore(c) || isFilter(c)) return false;
    const said = words(c);
    if (notAStatement.test(said)) return false;
    if (statementWords.test(said) || statementWords.test(c.getAttribute('href') || '')) return true;
    const opens = said === '' || viewWords.test(said) || oneDay.test(said);
    return opens && (statementWords.test(v.section || '') || statementWords.test(headingBefore(c)));
  };
  let marked = 0;
  const markOf = (c) => {
    if (linkOf(c) !== '') return '';
    if (!c.hasAttribute('data-agentifi-statement')) {
      marked += 1;
      c.setAttribute('data-agentifi-statement', v.mark + '-' + marked);
    }
    return c.getAttribute('data-agentifi-statement');
  };
  const controlsOf = (el) => [...el.querySelectorAll(v.controls)].filter(visible).slice(0, 12)
    .map((c) => ({ words: words(c), href: linkOf(c) }));
  const out = { statements: [], dated: [], account: '' };
  const keys = new Set();
  const keep = (list, el, opens) => {
    const text = (el.innerText || '').trim().slice(0, 2000);
    const key = list + '\u0000' + text;
    if (!text || keys.has(key)) return;
    keys.add(key);
    out[list].push({ section: v.section || headingBefore(el), text, controls: controlsOf(el), opens: opens || '' });
  };
  const controls = [...root.querySelectorAll(v.controls)].filter(isStatement);
  const holds = (el) => controls.filter((c) => el.contains(c)).length;
  for (const control of controls) {
    let row = control;
    while (row.parentElement && row.parentElement !== root && holds(row.parentElement) === 1) row = row.parentElement;
    keep('statements', row, markOf(control));
  }
  const monthAlone = /^\s*(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)[a-z]*\.?\s*$/i;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const rows = new Set();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const said = node.textContent || '';
    if (!oneDay.test(said) && !monthAlone.test(said)) continue;
    const parent = node.parentElement;
    if (!parent || !visible(parent)) continue;
    // A calendar icon prints its month, day and year as nodes of their own,
    // so the day is looked for in the element around the node.
    let row = parent;
    while (row && row !== root && days(row) === 0) row = row.parentElement;
    if (!row || row === root || days(row) !== 1) continue;
    while (row.parentElement && row.parentElement !== root && days(row.parentElement) === 1) row = row.parentElement;
    rows.add(row);
  }
  for (const row of rows) keep('dated', row, '');
  const printed = (main.innerText || '').match(new RegExp(v.account, 'i'));
  out.account = printed ? printed[1].replace(/-/g, '') : '';
  return out;
}`

// myChartTabsScript names the tabs worth showing, in the page's main region,
// or presses the one at v.press and names it.
const myChartTabsScript = `(v) => {` + agent.VisibleJS + myChartWordsJS + `
  const tabWords = new RegExp(v.tabWords, 'i');
  const notATab = new RegExp(v.notATab, 'i');
  const root = document.querySelector('main, [role="main"]') || document.body;
  let tabs = [...root.querySelectorAll(v.tabs)].filter((t) => visible(t) && tabWords.test(words(t)) && !notATab.test(words(t)));
  tabs = tabs.filter((t) => !tabs.some((other) => other !== t && other.contains(t))).slice(0, 8);
  if (v.press < 0) return tabs.map(words);
  const tab = tabs[v.press];
  if (!tab) return [];
  tab.click();
  return [words(tab)];
}`

// myChartMoreScript presses the control that shows more of a list (one that
// names statements first), else the one that turns to its next page, in the
// dialog in front or the page's main region, and names it; null when there is
// none. A link off the portal's own host is never pressed, nor one marked
// spent.
const myChartMoreScript = `(v) => {` + agent.DialogScopeJS + agent.VisibleJS + myChartWordsJS + `
  const moreWords = new RegExp(v.moreWords, 'i');
  const nextWords = new RegExp(v.nextWords, 'i');
  const notAStatement = new RegExp(v.notAStatement, 'i');
  const root = agentifiScope !== document ? agentifiScope : (document.querySelector('main, [role="main"]') || document.body);
  const pressable = (c) => visible(c) && !c.disabled && c.getAttribute('aria-disabled') !== 'true' &&
    !/(^|\s)disabled(\s|$)/i.test(typeof c.className === 'string' ? c.className : '') &&
    (linkOf(c) === '' || new URL(linkOf(c)).host === location.host);
  const statementWords = new RegExp(v.statementWords, 'i');
  const candidates = [...root.querySelectorAll(v.controls)].filter((c) => pressable(c) && !c.hasAttribute('data-agentifi-spent'));
  const showsMore = (c) => moreWords.test(words(c)) && !notAStatement.test(words(c));
  const more = candidates.find((c) => showsMore(c) && statementWords.test(words(c))) || candidates.find(showsMore);
  const next = candidates.find((c) => nextWords.test(words(c)) || c.getAttribute('rel') === 'next' ||
    /^next( page)?$/i.test((c.getAttribute('aria-label') || '').trim()));
  const chosen = more || next;
  if (!chosen) return null;
  const said = words(chosen);
  for (const other of document.querySelectorAll('[data-agentifi-last]')) other.removeAttribute('data-agentifi-last');
  chosen.setAttribute('data-agentifi-last', '1');
  chosen.click();
  return { words: said, kind: more ? 'more' : 'next' };
}`

// myChartViewingsScript names the periods the page's main region offers to
// show a list for that reach further back than the one shown (the radios a
// "Viewing options" panel holds, by their labels: shown, or in a collapsed
// panel beside the control that opens it, never on a tab not shown), or,
// given v.choose, ticks
// that one and presses the control that applies it, naming that control ("" for
// a page that shows the period as soon as it is ticked); null when the period
// is gone. A radio is ticked by its own click, which a hidden one takes too.
const myChartViewingsScript = `(v) => {` + agent.VisibleJS + myChartWordsJS + `
  const viewing = new RegExp(v.viewingWords, 'i');
  const apply = new RegExp(v.applyWords, 'i');
  const root = document.querySelector('main, [role="main"]') || document.body;
  const labelOf = (radio) => {
    const label = labelEl(radio);
    return ((label && label.textContent) || radio.getAttribute('aria-label') || radio.value || '').trim().slice(0, 80);
  };
  const toggle = new RegExp(v.viewingToggle, 'i');
  const labelEl = (radio) => (radio.id && root.querySelector('label[for="' + CSS.escape(radio.id) + '"]')) || radio.closest('label');
  const offered = (radio) => {
    if (visible(radio) || visible(labelEl(radio))) return true;
    let hidden = radio;
    while (hidden.parentElement && hidden.parentElement !== root && !visible(hidden.parentElement)) hidden = hidden.parentElement;
    const shown = hidden.parentElement;
    return !!shown && visible(shown) && [...shown.querySelectorAll(v.controls)]
      .some((c) => !hidden.contains(c) && visible(c) && (c.hasAttribute('aria-expanded') || toggle.test(words(c))));
  };
  const radios = [...root.querySelectorAll('input[type="radio"]')]
    .filter((r) => !r.disabled && viewing.test(labelOf(r)) && offered(r));
  if (v.choose === '') return radios.filter((r) => !r.checked).map(labelOf);
  const radio = radios.find((r) => labelOf(r) === v.choose);
  if (!radio) return null;
  radio.click();
  if (!radio.checked) {
    radio.checked = true;
    radio.dispatchEvent(new Event('change', { bubbles: true }));
  }
  const said = (c) => (words(c) || c.value || '').trim();
  const scope = radio.form || root;
  const applies = [...scope.querySelectorAll('button, input[type="submit"], input[type="button"], a, [role="button"]')]
    .filter((c) => apply.test(said(c)) && !c.disabled);
  const chosen = applies.find(visible) || applies[0];
  if (!chosen) return '';
  chosen.click();
  return said(chosen);
}`

// myChartSpentScript marks the control pressed last as one that shows
// nothing more, so the next press tries another.
const myChartSpentScript = `() => {
  const last = document.querySelector('[data-agentifi-last]');
  if (last) last.setAttribute('data-agentifi-spent', '1');
}`

// myChartPressScript presses a statement control the rows script marked, and
// says whether it was still there.
const myChartPressScript = `(mark) => {
  const control = document.querySelector('[data-agentifi-statement="' + String(mark).replace(/"/g, '') + '"]');
  if (!control) return false;
  control.click();
  return true;
}`

// myChartDialogScript says whether a dialog stands in front of the page.
const myChartDialogScript = `() => {` + agent.DialogScopeJS + `
  return agentifiScope !== document;
}`

// myChartViewerScript is where a statement's page keeps the file: every
// frame, embed and object, every link that downloads or names a PDF, and the
// file a PDF viewer's address names. A blob: address is read here, while
// this page still holds it. With v.scoped, only the dialog in front is
// looked in, and nothing when there is none.
const myChartViewerScript = `async (v) => {` + agent.DialogScopeJS + myChartWordsJS + `
  if (v.scoped && agentifiScope === document) return [];
  const scope = v.scoped ? agentifiScope : document;
  const out = [];
  const seen = new Set();
  const add = (raw, how) => {
    if (!raw || raw.startsWith('#') || /^javascript:/i.test(raw)) return;
    let address = '';
    try { address = new URL(raw, location.href).href; } catch (e) { return; }
    if (seen.has(address)) return;
    seen.add(address);
    out.push({ address, how });
  };
  for (const el of scope.querySelectorAll('iframe[src], frame[src], embed[src], object[data]')) {
    add(el.getAttribute('src') || el.getAttribute('data'), el.tagName.toLowerCase());
  }
  for (const a of scope.querySelectorAll('a[href]')) {
    const href = a.getAttribute('href') || '';
    if (a.hasAttribute('download') || /pdf/i.test(href) || /download|pdf|save/i.test(words(a))) add(href, 'link');
  }
  for (const one of [...out]) {
    try {
      const file = new URL(one.address).searchParams.get('file');
      if (file) add(file, 'viewer file');
    } catch (e) {}
  }
  for (const one of out) {
    if (!one.address.startsWith('blob:')) continue;
    try {
      const bytes = new Uint8Array(await (await fetch(one.address)).arrayBuffer());
      let text = '';
      for (let at = 0; at < bytes.length; at += 8192) text += String.fromCharCode(...bytes.subarray(at, at + 8192));
      one.data = btoa(text);
    } catch (e) {}
  }
  return out.slice(0, 12);
}`

// myChartSource is one place a statement's page keeps its file.
type myChartSource struct {
	Address string `json:"address"`
	How     string `json:"how"`
	// Data is a blob's bytes, base64, read in the page that held it.
	Data string `json:"data"`
}

func (m *MyChart) summaryURL() string {
	if m.site == "" {
		return ""
	}
	return m.site + myChartPage.Summary
}

// cards reads the billing summary. It answers false for a profile that is not
// signed in.
func (m *MyChart) cards(call Call) ([]MyChartCard, bool) {
	page := call.Page
	// The navigation's own error is not fatal: where the browser stands
	// afterwards is the answer.
	_ = page.Goto(m.summaryURL())
	page.Settle()
	if !MyChartInside(m.site, page.URL()) {
		call.Notes.Tracef("MyChart sent the billing summary on to %s; the profile is not signed in",
			browser.WithoutQuery(page.URL()))
		call.Saw("MyChart sent the billing summary on to this page: the profile is not signed in")
		return nil, false
	}
	call.Saw("MyChart's billing summary")
	var cards []MyChartCard
	if err := browser.EvaluateIntoContext(call.Ctx, page, myChartSummaryScript, myChartArgs(nil), &cards); err != nil {
		call.Notes.Addf("MyChart's billing summary could not be read: %v", err)
		return nil, true
	}
	call.Mark("the billing summary shows %d billing account card%s", len(cards), plural(len(cards)))
	if len(cards) == 0 {
		call.Notes.Addf("MyChart's billing summary listed no billing account this reader recognises (%s)",
			browser.Glimpse(page, 160))
	}
	return cards, true
}

// pageAccount is the account number a card that printed none has on its own
// page, or "", and false for a profile that is not signed in.
func (m *MyChart) pageAccount(call Call, href string) (string, bool) {
	page := call.Page
	_ = page.Goto(href)
	page.Settle()
	if !MyChartInside(m.site, page.URL()) {
		return "", false
	}
	call.Saw("a MyChart billing account whose card printed no number: its page")
	var read MyChartAccountPage
	if err := browser.EvaluateIntoContext(call.Ctx, page, myChartRowsScript, myChartArgs(nil), &read); err != nil {
		call.Notes.Addf("a MyChart billing account's page could not be read: %v", err)
	}
	return read.Account, true
}

func (m *MyChart) Subaccounts(call Call) ([]Subaccount, error) {
	if call.Page == nil {
		return nil, nil
	}
	cards, signedIn := m.cards(call)
	if !signedIn {
		return nil, ErrNeedsSignIn
	}
	for i := range cards {
		if MyChartAccountOf(cards[i].Text) != "" || !MyChartInside(m.site, cards[i].Href) {
			continue
		}
		account, signedIn := m.pageAccount(call, cards[i].Href)
		if !signedIn {
			return nil, ErrNeedsSignIn
		}
		cards[i].Account = account
	}
	found := MyChartSubaccounts(cards, call.Notes)
	call.Notes.Addf("MyChart lists %d billing account%s", len(found), plural(len(found)))
	return found, nil
}

func (m *MyChart) FetchBills(call Call) (Pull, error) {
	if call.Page == nil {
		return m.NoPage(), nil
	}
	cards, signedIn := m.cards(call)
	if !signedIn {
		return m.SignInAgain(nil), nil
	}
	walk := newMyChartWalk(m, call)
	var out Pull
	for i := range cards {
		card := &cards[i]
		account := MyChartAccountOf(card.Text)
		if account != "" && len(call.Subaccounts) > 0 && !call.Wanted(account) {
			continue
		}
		named := "a billing account"
		if account != "" {
			named = "billing account " + domain.MaskAccount(account)
		}
		if card.Href == "" {
			call.Notes.Addf("MyChart's card for %s offers no link to its page; it was not read", named)
			continue
		}
		if !MyChartInside(m.site, card.Href) {
			call.Notes.Addf("MyChart's link to %s leaves the portal; it was not followed", named)
			continue
		}
		read, signedIn := walk.account(card.Href, named)
		if !signedIn {
			return m.SignInAgain(out.Bills), nil
		}
		if account == "" {
			account = read.Account
			card.Account = account
			if account == "" {
				call.Notes.Addf("a MyChart billing account printed no account number this reader recognises, " +
					"on its card or its page; it is left out")
				continue
			}
			if len(call.Subaccounts) > 0 && !call.Wanted(account) {
				continue
			}
		}
		bills := MyChartStatements(account, read.Statements, call.Notes)
		payments := MyChartPayments(account, read.Dated, call.Notes)
		said := fmt.Sprintf("MyChart billing account %s: %d statement%s and %d payment%s read",
			domain.MaskAccount(account), len(bills), plural(len(bills)), len(payments), plural(len(payments)))
		call.Notes.Add(said)
		call.Mark("%s, from %d statement row%s and %d dated row%s", said,
			len(read.Statements), plural(len(read.Statements)), len(read.Dated), plural(len(read.Dated)))
		out.Bills = append(out.Bills, bills...)
		out.Payments = append(out.Payments, payments...)
	}
	out.Subaccounts = MyChartSubaccounts(cards, nil)
	return out, nil
}

// myChartWalk is one pull's reading of the account pages: what it has seen
// open from them (a window, a download), and how many statement controls it
// has pressed to learn where their files are.
type myChartWalk struct {
	m      *MyChart
	call   Call
	opened *myChartOpened
	opens  int
	// reads numbers each reading of the page, so a control it marks is told
	// apart from one an earlier load of a page marked; tried is every mark
	// pressed.
	reads int
	tried map[string]bool
}

// myChartOpened is the windows and downloads the page opened, kept from the
// driver's goroutine for the walk's.
type myChartOpened struct {
	mu        sync.Mutex
	popups    []browser.Page
	downloads []browser.Download
}

func newMyChartWalk(m *MyChart, call Call) *myChartWalk {
	walk := &myChartWalk{m: m, call: call, opened: &myChartOpened{}, tried: map[string]bool{}}
	call.Page.OnPopup(func(popup browser.Page) {
		walk.opened.mu.Lock()
		defer walk.opened.mu.Unlock()
		walk.opened.popups = append(walk.opened.popups, popup)
	})
	call.Page.OnDownload(func(download browser.Download) {
		walk.opened.mu.Lock()
		defer walk.opened.mu.Unlock()
		walk.opened.downloads = append(walk.opened.downloads, download)
	})
	return walk
}

// take is what opened since the last take: the first window (the rest are
// closed) and the first download.
func (o *myChartOpened) take() (browser.Page, browser.Download) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var popup browser.Page
	var download browser.Download
	for i, one := range o.popups {
		if i == 0 {
			popup = one
			continue
		}
		_ = one.Close()
	}
	if len(o.downloads) > 0 {
		download = o.downloads[0]
	}
	o.popups, o.downloads = nil, nil
	return popup, download
}

// myChartRead is one account's rows as they gather across its tabs, its
// lists' further rows and pages and the periods a list is shown for, keyed by
// their words so a row read twice is one row. view is what the walk is
// showing, which each row it adds carries.
type myChartRead struct {
	page MyChartAccountPage
	seen map[string]int
	view string
}

func newMyChartRead() *myChartRead {
	return &myChartRead{seen: map[string]int{}}
}

// add keeps the rows not read before and answers how many there were. A row
// read again under a tab gains the tab as its section, and a link it lacked.
func (r *myChartRead) add(read MyChartAccountPage) int {
	added := 0
	keep := func(list string, rows *[]MyChartRow, row MyChartRow) {
		key := list + "\x00" + row.Text
		at, held := r.seen[key]
		if !held {
			r.seen[key] = len(*rows)
			row.View = r.view
			*rows = append(*rows, row)
			added++
			return
		}
		kept := &(*rows)[at]
		if kept.Section == "" {
			kept.Section = row.Section
		}
		if myChartStatementLink(kept.Controls) == "" && myChartStatementLink(row.Controls) != "" {
			kept.Controls, kept.Opens = row.Controls, ""
		}
	}
	for _, row := range read.Statements {
		keep("statements", &r.page.Statements, row)
	}
	for _, row := range read.Dated {
		keep("dated", &r.page.Dated, row)
	}
	r.page.Account = cmp.Or(r.page.Account, read.Account)
	return added
}

// opened gives the statement row whose control carries mark the link it
// opened.
func (r *myChartRead) opened(mark, link string) {
	for i := range r.page.Statements {
		if r.page.Statements[i].Opens == mark {
			r.page.Statements[i].Controls = append(r.page.Statements[i].Controls,
				MyChartControl{Words: "statement", Href: link})
			r.page.Statements[i].Opens = ""
		}
	}
}

// account reads one account's page: what it shows, then each tab worth
// showing, each with its lists followed to their end. False for a profile
// that is not signed in.
func (w *myChartWalk) account(href, named string) (MyChartAccountPage, bool) {
	page := w.call.Page
	_ = page.Goto(href)
	page.Settle()
	if !MyChartInside(w.m.site, page.URL()) {
		return MyChartAccountPage{}, false
	}
	home := page.URL()
	w.call.Saw("MyChart " + named + ": its page")
	read := newMyChartRead()
	if !w.readHere(read, "", named) || !w.readViewings(read, "", named) {
		return MyChartAccountPage{}, false
	}
	var tabs []string
	if err := browser.EvaluateIntoContext(w.call.Ctx, page, myChartTabsScript,
		myChartArgs(map[string]any{"press": -1}), &tabs); err != nil {
		w.call.Notes.Addf("the tabs of MyChart's page for %s could not be read: %v", named, err)
	}
	w.call.Mark("%s: tabs worth showing: %s", named, cmp.Or(strings.Join(tabs, " · "), "none"))
	for at := range tabs {
		if page.URL() != home {
			_ = page.Goto(home)
			page.Settle()
			if !MyChartInside(w.m.site, page.URL()) {
				return MyChartAccountPage{}, false
			}
		}
		var pressed []string
		if err := browser.EvaluateIntoContext(w.call.Ctx, page, myChartTabsScript,
			myChartArgs(map[string]any{"press": at}), &pressed); err != nil || len(pressed) == 0 {
			w.call.Mark("%s: the tab “%s” could not be pressed", named, tabs[at])
			continue
		}
		page.Settle()
		page.Sleep(myChartWait)
		if !MyChartInside(w.m.site, page.URL()) {
			return MyChartAccountPage{}, false
		}
		w.call.Saw(fmt.Sprintf("MyChart %s: after showing the tab “%s”", named, pressed[0]))
		read.view = pressed[0]
		if !w.readHere(read, pressed[0], named) || !w.readViewings(read, pressed[0], named) {
			return MyChartAccountPage{}, false
		}
	}
	return read.page, true
}

// readViewings shows the list each period the page offers that reaches
// further back than the one it opened on ("Year to date", "Last year"), in
// the page's order, and reads it as readHere does. False for a profile that
// is not signed in.
func (w *myChartWalk) readViewings(read *myChartRead, section, named string) bool {
	page := w.call.Page
	var periods []string
	if err := browser.EvaluateIntoContext(w.call.Ctx, page, myChartViewingsScript,
		myChartArgs(map[string]any{"choose": ""}), &periods); err != nil || len(periods) == 0 {
		return true
	}
	tab := read.view
	defer func() { read.view = tab }()
	w.call.Mark("%s: %s can be shown for %s", named, cmp.Or(section, "the page"), strings.Join(periods, " · "))
	for _, period := range periods {
		var applied *string
		if err := browser.EvaluateIntoContext(w.call.Ctx, page, myChartViewingsScript,
			myChartArgs(map[string]any{"choose": period}), &applied); err != nil || applied == nil {
			w.call.Mark("%s: the period “%s” could not be chosen", named, period)
			continue
		}
		page.Settle()
		page.Sleep(myChartWait)
		if !MyChartInside(w.m.site, page.URL()) {
			return false
		}
		read.view = strings.TrimSpace(tab + " · " + period)
		before := len(read.page.Statements) + len(read.page.Dated)
		if read.add(w.rows(section)) == 0 {
			// A list the page swaps in late is given longer once.
			page.Sleep(2 * myChartWait)
			read.add(w.rows(section))
		}
		w.call.Saw(fmt.Sprintf("MyChart %s: %s shown for “%s”", named, cmp.Or(section, "the page"), period))
		if !w.readHere(read, section, named) {
			return false
		}
		added := len(read.page.Statements) + len(read.page.Dated) - before
		w.call.Mark("%s: chose “%s” (applied by “%s”): %d new row%s", named, period,
			cmp.Or(*applied, "ticking it"), added, plural(added))
	}
	return true
}

// rows reads what the page shows, under section.
func (w *myChartWalk) rows(section string) MyChartAccountPage {
	w.reads++
	var read MyChartAccountPage
	if err := browser.EvaluateIntoContext(w.call.Ctx, w.call.Page, myChartRowsScript,
		myChartArgs(map[string]any{"section": section, "mark": fmt.Sprint(w.reads)}), &read); err != nil {
		w.call.Notes.Addf("a MyChart billing account's page could not be read: %v", err)
	}
	return read
}

// readHere reads the rows the page shows, then presses "more" or "next" while
// there is one that has not shown nothing new, then learns where each
// statement control that is no link leads. False for a profile that is not
// signed in.
func (w *myChartWalk) readHere(read *myChartRead, section, named string) bool {
	page := w.call.Page
	read.add(w.rows(section))
	presses := 0
	for presses < myChartPresses {
		var more *struct {
			Words string `json:"words"`
			Kind  string `json:"kind"`
		}
		if err := browser.EvaluateIntoContext(w.call.Ctx, page, myChartMoreScript, myChartArgs(nil), &more); err != nil ||
			more == nil {
			break
		}
		presses++
		page.Settle()
		page.Sleep(myChartWait)
		if !MyChartInside(w.m.site, page.URL()) {
			return false
		}
		added := read.add(w.rows(section))
		if added == 0 {
			// A list that loads its rows late is given longer once.
			page.Sleep(2 * myChartWait)
			added = read.add(w.rows(section))
		}
		w.call.Mark("%s: pressed “%s” (%s): %d new row%s", named, more.Words, more.Kind, added, plural(added))
		if added == 0 {
			_, _ = page.Evaluate(myChartSpentScript, nil)
		}
	}
	if presses > 0 {
		w.call.Saw(fmt.Sprintf("MyChart %s: after %d press%s of more or next", named, presses, pluralWith(presses, "es")))
	}
	if !w.openStatements(read, named) {
		return false
	}
	w.closeDialog()
	return true
}

// closeDialog shuts a dialog the reading left in front of the page.
func (w *myChartWalk) closeDialog() {
	var open bool
	if err := browser.EvaluateIntoContext(w.call.Ctx, w.call.Page, myChartDialogScript, nil, &open); err == nil && open {
		_ = w.call.Page.Press("Escape")
		w.call.Page.Settle()
	}
}

// openStatements presses each statement control that is no link, once, to
// learn where its file is: the window it opens, the file it downloads, the
// page it goes to (and back), or the viewer it shows in a dialog. False for a
// profile that is not signed in.
func (w *myChartWalk) openStatements(read *myChartRead, named string) bool {
	page := w.call.Page
	for _, row := range slices.Clone(read.page.Statements) {
		if row.Opens == "" || w.tried[row.Opens] || w.opens >= myChartOpens {
			continue
		}
		w.tried[row.Opens] = true
		w.opens++
		_, _ = w.opened.take()
		before := page.URL()
		var pressed bool
		if err := browser.EvaluateIntoContext(w.call.Ctx, page, myChartPressScript, row.Opens, &pressed); err != nil ||
			!pressed {
			continue
		}
		page.Settle()
		page.Sleep(myChartWait)
		link, how := "", ""
		popup, download := w.opened.take()
		switch {
		case popup != nil:
			popup.Settle()
			link, how = popup.URL(), "a window"
			_ = popup.Close()
		case download != nil:
			link, how = download.URL(), "a download"
		case page.URL() != before:
			link, how = page.URL(), "a page"
			_, _ = page.Evaluate(`() => history.back()`, nil)
			page.Settle()
			if page.URL() != before {
				_ = page.Goto(before)
				page.Settle()
				if !MyChartInside(w.m.site, page.URL()) {
					return false
				}
				w.call.Mark("%s: the statement controls past this one were lost when the page was opened again", named)
				read.opened(row.Opens, link)
				return true
			}
		default:
			var sources []myChartSource
			if err := browser.EvaluateIntoContext(w.call.Ctx, page, myChartViewerScript,
				map[string]any{"scoped": true}, &sources); err == nil && len(sources) > 0 {
				link, how = sources[0].Address, "a viewer in a dialog"
			}
			w.closeDialog()
		}
		first := strings.SplitN(row.Text, "\n", 2)[0]
		switch {
		case link == "" || strings.HasPrefix(link, "blob:"):
			w.call.Mark("%s: the statement “%s” opened nothing this reader can fetch again", named, first)
		case !MyChartInside(w.m.site, link):
			w.call.Mark("%s: the statement “%s” opened %s off the portal; it is not fetched", named, first, how)
		default:
			read.opened(row.Opens, link)
			w.call.Mark("%s: the statement “%s” opened %s at %s", named, first, how, myChartWhere(link))
		}
	}
	return true
}

// myChartWhere is an address as the trail names it: its path, without the
// query, the fragment or a long number.
func myChartWhere(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return longDigits.ReplaceAllString(parsed.Path, "…")
}

// FetchDocument is the statement the row's link or control opens, fetched with
// the session's cookies and only from the portal itself. A link that answers
// a page is a statement viewer: the file it frames or links is fetched, and a
// viewer that offers none is printed, so a statement always carries a file.
func (m *MyChart) FetchDocument(call Call, bill Bill) (*Document, error) {
	raw := rawOf[myChartRaw](bill)
	if call.Page == nil {
		return nil, nil
	}
	what := "the MyChart statement of " + bill.IssuedOn
	if raw.Statement == "" {
		call.Mark("%s: its row offered no link to a file", what)
		return nil, nil
	}
	if !MyChartInside(m.site, raw.Statement) {
		call.Notes.Addf("%s is not on the portal's own address, so it was not fetched", what)
		return nil, nil
	}
	filename := statementFilename("mychart", bill, bill.IssuedOn)
	status, kind, body, err := call.Page.Bytes(raw.Statement)
	if err == nil && status == 200 && isPDF(body) {
		call.Mark("%s: its link answered the PDF", what)
		return pdfDocument(body, filename), nil
	}
	if err != nil || status != 200 {
		call.Notes.Addf("%s answered HTTP %d as %s, not a PDF", what, status, cmp.Or(kind, "nothing"))
		return nil, nil
	}
	if found, how := m.viewerPDF(call, raw.Statement, what); found != nil {
		call.Mark("%s: %s", what, how)
		return pdfDocument(found, filename), nil
	}
	call.Notes.Addf("%s answered a page (%s) that offered no PDF and could not be printed", what, cmp.Or(kind, "no type"))
	return nil, nil
}

// viewerPDF is the file a statement's page keeps, and how it was had: the
// file its frame, embed, object or download link names (one frame deep), a
// blob the page made, or, failing all of those, a print of the page itself.
func (m *MyChart) viewerPDF(call Call, address, what string) ([]byte, string) {
	page := call.Page
	_ = page.Goto(address)
	page.Settle()
	page.Sleep(myChartWait)
	if !MyChartInside(m.site, page.URL()) {
		call.Mark("%s: its page went on to %s, off the account area", what, myChartWhere(page.URL()))
		return nil, ""
	}
	call.Saw(what + ": the page its link answered")
	for depth := 0; depth < 2; depth++ {
		var sources []myChartSource
		if err := browser.EvaluateIntoContext(call.Ctx, page, myChartViewerScript,
			map[string]any{"scoped": false}, &sources); err != nil {
			call.Mark("%s: its page could not be read: %v", what, err)
			break
		}
		framed := ""
		for _, one := range sources {
			if one.Data != "" {
				if body, err := base64.StdEncoding.DecodeString(one.Data); err == nil && isPDF(body) {
					return body, "read the PDF its page made (" + one.How + ")"
				}
				continue
			}
			if strings.HasPrefix(one.Address, "blob:") {
				continue
			}
			if !MyChartInside(m.site, one.Address) {
				call.Mark("%s: its page's %s is off the portal; not fetched", what, one.How)
				continue
			}
			status, kind, body, err := page.Bytes(one.Address)
			if err == nil && status == 200 && isPDF(body) {
				return body, "followed its page's " + one.How + " at " + myChartWhere(one.Address) + " to the PDF"
			}
			call.Mark("%s: its page's %s at %s answered HTTP %d as %s", what, one.How, myChartWhere(one.Address),
				status, cmp.Or(kind, "nothing"))
			if framed == "" && err == nil && status == 200 && one.How != "link" {
				framed = one.Address
			}
		}
		if framed == "" {
			break
		}
		_ = page.Goto(framed)
		page.Settle()
		page.Sleep(myChartWait)
		if !MyChartInside(m.site, page.URL()) {
			break
		}
		call.Saw(what + ": the page its viewer frames")
	}
	printer, prints := page.(browser.PDFPrinter)
	if !prints {
		call.Mark("%s: this browser cannot print its page", what)
		return nil, ""
	}
	if browser.WithoutQuery(page.URL()) != browser.WithoutQuery(address) {
		_ = page.Goto(address)
		page.Settle()
		page.Sleep(myChartWait)
	}
	printed, err := printer.PDF()
	if err != nil || !isPDF(printed) {
		call.Mark("%s: its page would not print: %v", what, err)
		return nil, ""
	}
	return printed, "printed its page, which offered no PDF of its own"
}

// MyChartAccountOf is the billing account number a card or row prints, digits
// only, or "".
func MyChartAccountOf(text string) string {
	found := myChartAccount.FindStringSubmatch(text)
	if found == nil {
		return ""
	}
	return strings.ReplaceAll(found[1], "-", "")
}

// MyChartSubaccounts is one billed account per summary card that prints an
// account number, or whose page did, labelled with its masked number and the
// name the card prints beside the number (the practice, or whose account it
// is).
func MyChartSubaccounts(cards []MyChartCard, notes *Notes) []Subaccount {
	var found []Subaccount
	seen := map[string]bool{}
	for _, card := range cards {
		account := cmp.Or(MyChartAccountOf(card.Text), card.Account)
		if account == "" {
			notes.Addf("a MyChart billing account printed no account number this reader recognises; it is left out")
			continue
		}
		if seen[account] {
			continue
		}
		seen[account] = true
		label := "Billing account ****" + domain.LastFour(account)
		if named := myChartName(card.Text); named != "" {
			label += " · " + named
		}
		found = append(found, Subaccount{ExternalID: account, Label: label, MaskedNumber: domain.MaskAccount(account)})
	}
	return found
}

// myChartControlWords are a card's buttons and figures' labels, which name
// nothing.
var myChartControlWords = regexp.MustCompile(
	`(?i)^(?:pay|view|see|details|billing|account|paperless|payment plan|amount|balance|due|last|guarantor|patients?|` +
		`your|can't|sign up|set up|contact|questions?)\b`)

// myChartName is the card's own name for its account: the earliest line that
// names something among the three before the line with the number, else among
// the three after it, and never a line of the portal's frame.
func myChartName(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	at := slices.IndexFunc(lines, func(line string) bool { return myChartAccount.MatchString(line) })
	names := func(line string) bool {
		return len(line) <= 60 && !strings.ContainsAny(line, "0123456789$#") &&
			!myChartControlWords.MatchString(line) && !myChartChrome.MatchString(line)
	}
	if at < 0 {
		for _, line := range lines {
			if names(line) {
				return line
			}
		}
		return ""
	}
	for _, line := range lines[max(0, at-3):at] {
		if names(line) {
			return line
		}
	}
	for _, line := range lines[at+1 : min(len(lines), at+4)] {
		if names(line) {
			return line
		}
	}
	return ""
}

// myChartSpelled is a date with its month spelled, in its parts.
var myChartSpelled = regexp.MustCompile(`(?i)^([a-z]+\.?)\s*(\d{1,2}),?\s*(\d{4})$`)

// myChartFlat is a row's words with each date written one way, on one line:
// a calendar icon prints a day's month, day and year on lines of their own,
// or with nothing between them.
func myChartFlat(text string) string {
	return myChartDay.ReplaceAllStringFunc(text, func(day string) string {
		parts := myChartSpelled.FindStringSubmatch(day)
		if parts == nil {
			return day
		}
		return parts[1] + " " + parts[2] + ", " + parts[3]
	})
}

// myChartFigure is one day or amount a row prints and the words that label it.
type myChartFigure struct {
	label string
	text  string
}

// myChartFigures is every match of pattern in the text, each labelled by the
// words before it on its line, or by the line above when it stands alone.
func myChartFigures(text string, pattern *regexp.Regexp) []myChartFigure {
	var out []myChartFigure
	previous := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		at := 0
		for _, span := range pattern.FindAllStringIndex(line, -1) {
			label := strings.Trim(line[at:span[0]], " :-–|\t")
			if label == "" && at == 0 {
				label = previous
			}
			out = append(out, myChartFigure{label: label, text: line[span[0]:span[1]]})
			at = span[1]
		}
		if line != "" {
			previous = strings.Trim(line, " :")
		}
	}
	return out
}

func firstLabelled(figures []myChartFigure, words *regexp.Regexp) (myChartFigure, bool) {
	for _, one := range figures {
		if words.MatchString(one.label) {
			return one, true
		}
	}
	return myChartFigure{}, false
}

// MyChartStatements is one bill per statement of a billing account.
//
// A statement is dated by the day it was issued: labelled so, or the first
// day the row prints. Its due date is the one labelled due, else the day it
// was issued, since a statement list prints none. One that owes something is
// filed open (a newer one supersedes it, as the balance carries forward), and
// one that owes nothing is filed paid. What it is owed is the amount labelled
// owed, or the row's only amount; a row with neither is a note and no bill. Charges,
// insurance and adjustments are kept beside it as printed. A statement is
// known by its account and issue date; of the rows that tell of one, a
// statement list's (one day printed, fewest amounts) is read over a summary's
// "last statement" (an account's balance and last payment beside it), and its
// file is the first link any of them offers.
func MyChartStatements(account string, rows []MyChartRow, notes *Notes) []Bill {
	type told struct {
		row     MyChartRow
		text    string
		issued  string
		days    []myChartFigure
		amounts []myChartFigure
		owed    domain.Money
	}
	var order []string
	byKey := map[string][]told{}
	for _, row := range rows {
		text := myChartFlat(row.Text)
		days := myChartFigures(text, myChartDay)
		amounts := myChartFigures(text, myChartMoney)
		issued := ""
		if one, ok := firstLabelled(days, myChartIssuedWords); ok && !myChartDueWords.MatchString(one.label) {
			issued = DayIn(one.text)
		} else if len(days) > 0 {
			issued = DayIn(days[0].text)
		}
		if issued == "" {
			notes.Addf("a MyChart statement of billing account %s prints no day this reader recognises; it is left out",
				domain.MaskAccount(account))
			continue
		}
		owed, ok := firstLabelled(amounts, myChartOwedWords)
		if !ok && len(amounts) == 1 {
			owed, ok = amounts[0], true
		}
		amount, priced := billmail.ParseAmount(owed.text)
		if !ok || !priced {
			notes.Addf("MyChart's statement of %s for billing account %s prints no amount owed this reader recognises; "+
				"it is not filed", issued, domain.MaskAccount(account))
			continue
		}
		key := account + ":" + issued
		if _, held := byKey[key]; !held {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], told{row: row, text: text, issued: issued, days: days, amounts: amounts, owed: amount})
	}

	var bills []Bill
	for _, key := range order {
		tellings := byKey[key]
		rank := func(one told) int {
			listed := 0
			if len(myChartDays(one.days)) != 1 {
				listed = 100
			}
			return listed + len(one.amounts)
		}
		best := tellings[0]
		for _, one := range tellings[1:] {
			if rank(one) < rank(best) {
				best = one
			}
		}
		link := myChartStatementLink(best.row.Controls)
		for _, one := range tellings {
			link = cmp.Or(link, myChartStatementLink(one.row.Controls))
		}

		amount, issued, days, amounts := best.owed, best.issued, best.days, best.amounts
		due, status := issued, "Open"
		if one, ok := firstLabelled(days, myChartDueWords); ok {
			due = DayIn(one.text)
		}
		if !Owes(amount) {
			amount, status = domain.Zero, "Paid"
		}
		var serviced []string
		for _, one := range days {
			if myChartServiceWords.MatchString(one.label) {
				serviced = append(serviced, DayIn(one.text))
			}
		}
		slices.Sort(serviced)
		raw := myChartRaw{Account: account, Statement: link}
		if one, ok := firstLabelled(amounts, myChartChargeWords); ok {
			raw.Charges = one.text
		}
		if one, ok := firstLabelled(amounts, myChartInsuranceWords); ok {
			raw.Insurance = one.text
		}
		if one, ok := firstLabelled(amounts, myChartAdjustWords); ok {
			raw.Adjustment = one.text
		}
		encoded, _ := json.Marshal(raw)
		bill := Bill{
			Subaccount: account, ExternalID: key, IssuedOn: issued, DueOn: due,
			AmountDue: amount, Currency: "USD", Status: status, Raw: encoded,
		}
		if len(serviced) > 0 {
			bill.PeriodStart, bill.PeriodEnd = serviced[0], serviced[len(serviced)-1]
		}
		bills = append(bills, bill)
	}
	slices.SortStableFunc(bills, func(a, b Bill) int { return cmp.Compare(a.IssuedOn, b.IssuedOn) })
	return bills
}

// myChartStatementLink is the link a statement row opens its file with, or ""
// for a row whose control is a button. Whether it is the portal's own is
// FetchDocument's question.
func myChartStatementLink(controls []MyChartControl) string {
	for _, one := range controls {
		if (strings.HasPrefix(one.Href, "https://") || strings.HasPrefix(one.Href, "http://")) &&
			(myChartLinkWords.MatchString(one.Words) || myChartLinkWords.MatchString(one.Href) ||
				strings.TrimSpace(one.Words) == "" || myChartDay.MatchString(one.Words)) {
			return one.Href
		}
	}
	return ""
}

// MyChartPayments is every payment the household made on a billing account:
// a dated row under a payments tab or heading, or one whose words say it is a
// payment, that prints one day (in as many spellings as it likes) and one
// amount, and is not an insurer's. A payment the portal shows as not yet
// settled, or as reversed, is a note.
//
// A view (a tab, or a period a list is shown for) lists each payment once, so
// two copays alike on one day are two, and another view listing them again
// adds none: payments are told apart by day, amount and method and their
// place among those alike in one view. A payment is known by its account, day
// and amount and its place among the payments of that day and amount.
func MyChartPayments(account string, rows []MyChartRow, notes *Notes) []Payment {
	var payments []Payment
	inView := map[string]int{}
	held := map[string]int{}
	count := map[string]int{}
	for _, row := range rows {
		text := myChartFlat(row.Text)
		underPayments := myChartPaymentSection.MatchString(row.Section)
		if !underPayments && !myChartPaymentWords.MatchString(text) {
			continue
		}
		if myChartInsuranceWords.MatchString(text) || myChartAdjustWords.MatchString(text) ||
			myChartIsStatementRow(row) {
			continue
		}
		days := myChartDays(myChartFigures(text, myChartDay))
		amounts := myChartFigures(text, myChartMoney)
		if len(days) != 1 || len(amounts) == 0 {
			continue
		}
		paidOn := days[0]
		chosen, ok := firstLabelled(amounts, myChartPaidWords)
		if !ok && len(amounts) == 1 {
			chosen, ok = amounts[0], true
		}
		amount, priced := billmail.ParseAmount(chosen.text)
		if paidOn == "" || !ok || !priced {
			notes.Addf("a MyChart payment on billing account %s prints no day or amount this reader recognises; "+
				"it is left out", domain.MaskAccount(account))
			continue
		}
		amount = amount.Abs()
		if !Owes(amount) {
			continue
		}
		method := strings.TrimSpace(myChartMethodWords.FindString(text))
		alike := paidOn + "|" + amount.String() + "|" + strings.ToLower(method)
		inView[row.View+"|"+alike]++
		if inView[row.View+"|"+alike] <= held[alike] {
			continue
		}
		held[alike]++
		if myChartUnsettledWords.MatchString(text) {
			notes.Addf("MyChart shows a payment of %s on %s that is not settled; it is not paired with a bank row",
				amount, paidOn)
			continue
		}
		key := account + ":" + paidOn + ":" + amount.String()
		count[key]++
		external := key
		if count[key] > 1 {
			external = key + ":" + strings.Repeat("+", count[key]-1)
		}
		payments = append(payments, Payment{
			Subaccount: account, ExternalID: external, PaidOn: paidOn, Amount: amount, Method: method,
		})
	}
	return payments
}

// myChartDays is the distinct days among figures, in the order first printed;
// a row that prints its day in two spellings prints one day.
func myChartDays(figures []myChartFigure) []string {
	var days []string
	for _, one := range figures {
		if day := DayIn(one.text); day != "" && !slices.Contains(days, day) {
			days = append(days, day)
		}
	}
	return days
}

// myChartIsStatementRow says the row offers a statement, which a payment row
// does not; a control that chooses the period a list shows ("Currently
// viewing: Payments since last statement") offers none.
func myChartIsStatementRow(row MyChartRow) bool {
	if row.Opens != "" {
		return true
	}
	for _, one := range row.Controls {
		if myChartStatementControlWords.MatchString(one.Words) && !myChartNotAStatement.MatchString(one.Words) {
			return true
		}
	}
	return false
}
