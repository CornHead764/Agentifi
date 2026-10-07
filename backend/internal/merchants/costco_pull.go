package merchants

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// costcoSession is the little this reads of a kept session. The session is
// handed back rotated and otherwise exactly as it arrived, because one written
// by another build may carry more than this one knows about.
type costcoSession struct {
	Kind         string `json:"kind"`
	Tenant       string `json:"tenant"`
	Policy       string `json:"policy"`
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Account      struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"account"`
}

func isCostcoSession(raw json.RawMessage) bool {
	var shape struct {
		Kind    string          `json:"kind"`
		Cookies json.RawMessage `json:"cookies"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &shape) != nil {
		return false
	}
	return shape.Kind == costcoSessionKind && len(shape.Cookies) == 0
}

func (m *costcoModule) Fetch(call Call) (Result, error) {
	if isCostcoSession(call.Session) {
		return m.fetchFromSession(call)
	}
	return m.fetchFromPage(call)
}

// --- The handed-over session ------------------------------------------------------

// tokens is what one refresh answered with.
type tokens struct {
	id      string
	access  string
	refresh string
}

// refreshSession trades the refresh token for a fresh id token and a new
// refresh token: B2C rotates them. Only the errors that mean a sign-in is owed
// answer one; a network failure, a 5xx or a body that is not JSON is a failed
// pull, not a session the household has to re-establish.
func refreshSession(call Call, session costcoSession) (tokens, bool, string, error) {
	tenant := cmp.Or(session.Tenant, costcoB2CTenant)
	policy := cmp.Or(session.Policy, costcoB2CPolicy)
	clientID := cmp.Or(session.ClientID, costcoB2CClientID)
	address := fmt.Sprintf("%s/%s/%s/oauth2/v2.0/token", costcoB2CHost, tenant, policy)

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientID)
	form.Set("refresh_token", session.RefreshToken)
	form.Set("scope", "openid offline_access "+clientID)

	req, err := http.NewRequestWithContext(call.Ctx, http.MethodPost, address,
		strings.NewReader(form.Encode()))
	if err != nil {
		return tokens{}, false, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", costcoUserAgent)

	response, err := httpx.Read(call, req, pullAnswerLimit)
	if err != nil {
		return tokens{}, false, "", fmt.Errorf("the Costco token endpoint could not be reached: %w", err)
	}
	status := response.Status
	var body struct {
		IDToken          string `json:"id_token"`
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	readable := httpx.DecodeJSON(response.Body, &body) == nil
	if status == http.StatusBadRequest && readable && costcoSignInAgainErrors[body.Error] {
		reason := textutil.FirstLine(body.ErrorDescription)
		if reason == "" {
			reason = "Costco's sign-in no longer accepts the saved session (" + body.Error + ")"
		}
		call.Notes.Tracef("the token refresh was refused (%s): %s", body.Error, reason)
		return tokens{}, true, reason, nil
	}
	if status < 200 || status >= 300 || !readable {
		return tokens{}, false, "", fmt.Errorf("the Costco token endpoint answered HTTP %d: %s",
			status, response.Excerpt())
	}
	minted := tokens{id: body.IDToken, access: body.AccessToken, refresh: body.RefreshToken}
	if minted.id == "" && minted.access == "" {
		return tokens{}, false, "", fmt.Errorf("the Costco token endpoint answered without a token: %s",
			response.Excerpt())
	}
	rotated := "a new refresh token"
	if minted.refresh == "" {
		rotated = "no new refresh token (the old one is kept)"
	}
	var said []string
	if minted.id != "" {
		said = append(said, "an id token")
	}
	if minted.access != "" {
		said = append(said, "an access token")
	}
	said = append(said, rotated)
	call.Notes.Tracef("the token refresh answered with %s", strings.Join(said, ", "))
	return minted, false, "", nil
}

// pullAnswerLimit is the largest answer a pull reads.
const pullAnswerLimit = 64 << 20

type graphQLAnswer struct {
	ok      bool
	status  int
	body    any
	excerpt string
}

func (a graphQLAnswer) errors() string {
	shaped, ok := a.body.(map[string]any)
	if !ok {
		return ""
	}
	listed, ok := shaped["errors"].([]any)
	if !ok || len(listed) == 0 {
		return ""
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		return ""
	}
	return textutil.Clip(string(encoded), 300)
}

func askGraphQL(call Call, token, query string, variables map[string]any) (graphQLAnswer, error) {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return graphQLAnswer{}, err
	}
	req, err := http.NewRequestWithContext(call.Ctx, http.MethodPost, costcoGraphQL, bytes.NewReader(payload))
	if err != nil {
		return graphQLAnswer{}, err
	}
	req.Header.Set("Content-Type", "application/json-patch+json")
	req.Header.Set("User-Agent", costcoUserAgent)
	req.Header.Set("client-identifier", costcoClientIdentifier)
	req.Header.Set("costco.service", costcoService)
	req.Header.Set("costco.env", costcoEnv)
	req.Header.Set("costco-x-wcs-clientId", costcoWCSClientID)
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("costco-x-authorization", "Bearer "+token)

	response, err := httpx.Read(call, req, pullAnswerLimit)
	if err != nil {
		return graphQLAnswer{}, err
	}
	var body any
	readable := httpx.DecodeJSON(response.Body, &body) == nil
	return graphQLAnswer{
		ok: response.OK() && readable, status: response.Status, body: body,
		excerpt: response.Excerpt(),
	}, nil
}

var refusedWords = regexp.MustCompile(
	`(?i)unauthori[sz]ed|unauthenticated|invalid (?:credentials?|token)|token (?:is )?(?:expired|invalid)|forbidden`)

// refusedToken says whether an answer means the token, rather than the query,
// was the problem.
func refusedToken(answer graphQLAnswer) bool {
	if answer.status == http.StatusUnauthorized || answer.status == http.StatusForbidden {
		return true
	}
	shaped, ok := answer.body.(map[string]any)
	if !ok {
		return false
	}
	listed, ok := shaped["errors"].([]any)
	if !ok {
		return false
	}
	for _, one := range listed {
		entry, ok := one.(map[string]any)
		if !ok {
			continue
		}
		words := text(entry["message"])
		if extensions, ok := entry["extensions"].(map[string]any); ok {
			words += " " + text(extensions["code"])
		}
		if refusedWords.MatchString(words) {
			return true
		}
	}
	return false
}

// fetchFromSession tries the id token first, because that is what the page
// itself sends, and the access token from the same refresh when the endpoint
// refuses it.
func (m *costcoModule) fetchFromSession(call Call) (Result, error) {
	var session costcoSession
	if err := json.Unmarshal(call.Session, &session); err != nil {
		return Result{}, fmt.Errorf("the handed-over Costco session is unreadable: %w", err)
	}
	if session.RefreshToken == "" {
		return Result{
			NeedsSignIn: true, Reason: "the handed-over Costco session carries no refresh token",
		}, nil
	}
	sinceISO := call.Since()
	endISO := isoDay(call.At().AddDate(0, 0, 1))

	minted, owed, reason, err := refreshSession(call, session)
	if err != nil {
		return Result{}, err
	}
	if owed {
		return Result{NeedsSignIn: true, Reason: reason}, nil
	}

	type candidate struct{ name, token string }
	var candidates []candidate
	if minted.id != "" {
		candidates = append(candidates, candidate{"the id token", minted.id})
	}
	if minted.access != "" {
		candidates = append(candidates, candidate{"the access token", minted.access})
	}
	var accepted *candidate
	var trace []string

	ask := func(label, query string, variables map[string]any) (graphQLAnswer, error) {
		tried := candidates
		if accepted != nil {
			tried = []candidate{*accepted}
		}
		var refusals []string
		for _, one := range tried {
			answer, err := askGraphQL(call, one.token, query, variables)
			if err != nil {
				return graphQLAnswer{}, err
			}
			if refusedToken(answer) {
				refusal := fmt.Sprintf("%s: HTTP %d", one.name, answer.status)
				if answer.excerpt != "" {
					refusal += fmt.Sprintf(" %q", textutil.Clip(answer.excerpt, 120))
				}
				refusals = append(refusals, refusal)
				continue
			}
			if accepted == nil {
				kept := one
				accepted = &kept
				call.Notes.Tracef("Costco's purchases API accepted %s", one.name)
			}
			return answer, nil
		}
		return graphQLAnswer{}, fmt.Errorf(
			"the Costco purchases API refused every token it was offered for %s — %s",
			label, strings.Join(refusals, "; "))
	}

	found := &harvested{}
	record := func(label string, answer graphQLAnswer) int {
		if !answer.ok {
			line := fmt.Sprintf("%s: HTTP %d", label, answer.status)
			if answer.excerpt != "" {
				line += fmt.Sprintf(" — %q", answer.excerpt)
			}
			trace = append(trace, line)
			return 0
		}
		if complaint := answer.errors(); complaint != "" {
			trace = append(trace, fmt.Sprintf(
				"the endpoint refused the %s query: %s — the query's field names may be out of date",
				label, complaint))
		}
		before := found.count()
		harvest(answer.body, found, 0)
		return found.count() - before
	}

	call.Notes.Tracef("asked Costco for purchases from %s to %s (%d days) with the kept session",
		sinceISO, endISO, call.SinceDays)

	receipts, err := ask("receipts", costcoReceiptsQuery, map[string]any{
		"startDate": costcoDate(sinceISO), "endDate": costcoDate(endISO),
		"documentType": "all", "documentSubType": "all",
	})
	if err != nil {
		return Result{}, err
	}
	record("receipts", receipts)
	trace = append(trace, fmt.Sprintf("receipts: %d", len(found.receipts)))

	// The list names each item by number only, so each receipt missing
	// descriptions is asked about in detail; the first refusal stops the
	// asking, since the endpoint refuses the query for every barcode alike.
	detailed := 0
	refusedDetails := ""
	for i := 0; i < len(found.receipts) && refusedDetails == ""; i++ {
		summary := found.receipts[i]
		barcode := clean(summary.TransactionBarcode)
		if barcode == "" || !needsDetail(summary) {
			continue
		}
		if detailed >= costcoReceiptDetailsMax {
			trace = append(trace, fmt.Sprintf(
				"more than %d receipts; the rest keep the list's lines", costcoReceiptDetailsMax))
			break
		}
		answer, err := ask("receipt "+barcode, costcoReceiptDetailQuery, map[string]any{
			"barcode": barcode, "documentType": detailDocumentType(summary),
		})
		if err != nil {
			return Result{}, err
		}
		one, readable := detailOf(answer.body)
		if !answer.ok || !readable || answer.errors() != "" {
			if answer.ok {
				refusedDetails = "the receipt detail query was refused: " + cmp.Or(answer.errors(), answer.excerpt)
			} else {
				refusedDetails = fmt.Sprintf("the receipt detail query answered HTTP %d", answer.status)
				if answer.excerpt != "" {
					refusedDetails += fmt.Sprintf(" — %q", textutil.Clip(answer.excerpt, 200))
				}
			}
			break
		}
		found.receipts[i] = mergeReceipt(summary, one)
		detailed++
	}
	switch {
	case refusedDetails != "":
		trace = append(trace, refusedDetails+
			"; items are by number only, so a row can be matched but not split")
		call.Notes.Add("Costco gave no item details for its receipts, so their items are by number only " +
			"and a charge can be matched but not split")
	case detailed > 0:
		trace = append(trace, fmt.Sprintf("receipt details: %d", detailed))
	}

	for pageNumber := 1; pageNumber <= costcoGraphQLMaxPages; pageNumber++ {
		label := fmt.Sprintf("online orders (page %d)", pageNumber)
		answer, err := ask(label, costcoOnlineOrdersQuery, map[string]any{
			"startDate": sinceISO, "endDate": endISO,
			"pageNumber": pageNumber, "pageSize": costcoGraphQLPageSize,
			"warehouseNumber": costcoWarehouseNumber,
		})
		if err != nil {
			return Result{}, err
		}
		if record(label, answer) == 0 {
			break
		}
		if total, ok := totalRecords(answer.body); ok && pageNumber*costcoGraphQLPageSize >= total {
			break
		}
	}
	trace = append(trace, fmt.Sprintf("online orders: %d", len(found.onlineOrders)))

	hint := cmp.Or(clean(session.Account.Name), clean(session.Account.Username))
	file := buildExport(*found, hint, sinceISO, call.At().UTC().Format(time.RFC3339))
	noteWhatWasFound(call.Notes, file, *found, trace, sinceISO, "")

	rotated, err := rotate(call.Session, minted, call.At())
	if err != nil {
		return Result{}, err
	}
	return Result{
		AccountHint: hint, Parsed: readFile(merchantimport.FromCostco(file)), StorageState: rotated,
		Orders: len(file.Orders), Charges: len(file.Charges),
		Invoices: costcoReceiptPages(*found, sinceISO, call.Invoiced),
	}, nil
}

// mergeReceipt lays the detail over the list's line, field by field.
func mergeReceipt(summary, detail receipt) receipt {
	merged := detail
	if merged.DocumentType == "" {
		merged.DocumentType = summary.DocumentType
	}
	if merged.ReceiptType == "" {
		merged.ReceiptType = summary.ReceiptType
	}
	if merged.DocumentSubType == "" {
		merged.DocumentSubType = summary.DocumentSubType
	}
	if merged.TransactionType == "" {
		merged.TransactionType = summary.TransactionType
	}
	if merged.TransactionBarcode == "" {
		merged.TransactionBarcode = summary.TransactionBarcode
	}
	return merged
}

func totalRecords(body any) (int, bool) {
	shaped, ok := body.(map[string]any)
	if !ok {
		return 0, false
	}
	data, ok := shaped["data"].(map[string]any)
	if !ok {
		return 0, false
	}
	listing, ok := data["getOnlineOrders"].(map[string]any)
	if !ok {
		return 0, false
	}
	for _, key := range []string{"totalNumberOfRecords", "recordCount"} {
		if count, ok := loose(text(listing[key])).Int(); ok {
			return count, true
		}
	}
	return 0, false
}

// rotate lays the minted tokens over the session that arrived: B2C invalidates
// the old refresh token on use.
func rotate(was json.RawMessage, minted tokens, at time.Time) (json.RawMessage, error) {
	shape := map[string]any{}
	if len(was) > 0 {
		if err := httpx.DecodeJSON(was, &shape); err != nil {
			return nil, err
		}
	}
	if minted.refresh != "" {
		shape["refresh_token"] = minted.refresh
	}
	if minted.id != "" {
		shape["id_token"] = minted.id
	}
	shape["refreshed_at"] = at.UTC().Format(time.RFC3339)
	return json.Marshal(shape)
}

func noteWhatWasFound(notes *Notes, file merchantimport.CostcoFile, found harvested, trace []string, sinceISO, seen string) {
	if len(file.Orders) == 0 {
		shapes := strings.Join(found.shapes, " | ")
		if shapes == "" {
			shapes = "none"
		}
		notes.Addf("no Costco purchases found since %s", sinceISO)
		traced := fmt.Sprintf("JSON shapes seen: %s. What was tried: %s",
			textutil.Clip(shapes, 300), textutil.Clip(strings.Join(trace, "; "), 900))
		if seen != "" {
			traced = "the page read: " + seen + ". " + traced
		}
		notes.Trace(traced)
		return
	}
	notes.Tracef("%d purchases and %d charges: %s",
		len(file.Orders), len(file.Charges), strings.Join(trace, "; "))
	if len(found.receipts) == 0 {
		notes.Tracef("no warehouse receipts found; only online orders")
	}
	if len(found.onlineOrders) == 0 {
		notes.Tracef("no online orders found; only warehouse receipts")
	}
	if len(file.Charges) == 0 {
		notes.Addf("no tenders found on any Costco purchase; " +
			"the bank rows will be matched on the total alone")
	}
}

// --- The browser path ------------------------------------------------------------

func (m *costcoModule) fetchFromPage(call Call) (Result, error) {
	page := call.Page
	if page == nil {
		return Result{}, fmt.Errorf("the Costco pull was given no page and no handed-over session")
	}
	sinceISO := call.Since()
	endISO := isoDay(call.At().AddDate(0, 0, 1))
	var trace []string

	// 1. Everything the page asks for on its own, kept as it answers.
	recorded := browser.RecordResponses(page, costcoGraphQLMatch)

	if err := page.Goto(costcoOrdersURL); err != nil {
		return Result{}, err
	}
	page.Settle()
	where, err := m.Classify(page)
	if err != nil {
		return Result{}, err
	}
	if where.State != StateSignedIn {
		// The hash route may not have rendered yet; the older page is the
		// second opinion before saying the session is gone.
		_ = page.Goto(costcoOrdersURLFallback)
		page.Settle()
		if where, err = m.Classify(page); err != nil {
			return Result{}, err
		}
	}
	if where.State != StateSignedIn {
		return Result{
			NeedsSignIn: true,
			Reason:      cmp.Or(where.Prompt, "Costco asked to sign in again ("+where.State+")"),
			Image:       agent.Screenshot(page),
		}, nil
	}
	hint, _ := m.AccountHint(page)

	widenFilters(page, &trace)
	page.Sleep(2 * time.Second)

	found := &harvested{}
	read := 0
	for _, response := range recorded.Responses() {
		raw, err := response.Body()
		if err != nil {
			trace = append(trace, fmt.Sprintf("a response from %s could not be read",
				textutil.Clip(response.URL(), 80)))
			continue
		}
		var body any
		if httpx.DecodeJSON(raw, &body) != nil {
			trace = append(trace, fmt.Sprintf("a response from %s was not JSON", textutil.Clip(response.URL(), 80)))
			continue
		}
		read++
		harvest(body, found, 0)
	}
	trace = append(trace, fmt.Sprintf(
		"the page's own calls: %d JSON responses, %d receipts, %d online orders",
		read, len(found.receipts), len(found.onlineOrders)))

	// 2. The same questions asked directly, with the page's own token.
	for _, asked := range []struct {
		label     string
		query     string
		variables map[string]any
	}{
		{"receipts", costcoReceiptsQuery, map[string]any{
			"startDate": costcoDate(sinceISO), "endDate": costcoDate(endISO),
			"documentType": "all", "documentSubType": "all",
		}},
		{"online orders", costcoOnlineOrdersQuery, map[string]any{
			"startDate": sinceISO, "endDate": endISO, "pageNumber": 1,
			"pageSize": costcoGraphQLPageSize, "warehouseNumber": costcoWarehouseNumber,
		}},
	} {
		answer, err := queryFromPage(call.context(), page, asked.query, asked.variables)
		if err != nil {
			trace = append(trace, fmt.Sprintf("asking the endpoint for %s directly: %v",
				asked.label, textutil.Clip(err.Error(), 160)))
			continue
		}
		if !answer.OK {
			line := fmt.Sprintf("asking the endpoint for %s directly: %s",
				asked.label, cmp.Or(answer.Error, fmt.Sprintf("HTTP %d", answer.Status)))
			if answer.Excerpt != "" {
				line += fmt.Sprintf(" — %q", answer.Excerpt)
			}
			if len(answer.Keys) > 0 {
				line += fmt.Sprintf(" (storage keys: %s)", textutil.Clip(strings.Join(answer.Keys, ", "), 200))
			}
			trace = append(trace, line)
			continue
		}
		before := found.count()
		harvest(answer.decoded, found, 0)
		trace = append(trace, fmt.Sprintf("asking the endpoint for %s directly added %d",
			asked.label, found.count()-before))
	}

	// 3. The cards on the page, when the JSON gave nothing at all.
	if found.count() == 0 {
		cards, err := readCards(page)
		if err == nil && len(cards) > 0 {
			for _, card := range cards {
				online, one := cardToRaw(card)
				if online {
					found.onlineOrders = append(found.onlineOrders, one.(onlineOrder))
				} else {
					found.receipts = append(found.receipts, one.(receipt))
				}
			}
			call.Notes.Addf("Costco's JSON could not be read, so %d purchases were read from the page "+
				"itself: dates and totals only, no items.", len(cards))
		}
	}

	file := buildExport(*found, hint, sinceISO, call.At().UTC().Format(time.RFC3339))
	noteWhatWasFound(call.Notes, file, *found, trace, sinceISO, browser.Glimpse(page, 400))
	return Result{
		AccountHint: hint, Parsed: readFile(merchantimport.FromCostco(file)),
		Orders: len(file.Orders), Charges: len(file.Charges),
		Invoices: costcoReceiptPages(*found, sinceISO, call.Invoiced),
	}, nil
}

func widenFilters(page browser.Page, trace *[]string) {
	picked, err := page.SelectMatching("select", costcoWidestRange)
	switch {
	case err != nil:
		*trace = append(*trace, "the widest date range: "+textutil.Clip(err.Error(), 120))
	case picked != "":
		*trace = append(*trace, fmt.Sprintf("the widest date range: date range set to %q", picked))
	default:
		*trace = append(*trace, "the widest date range: nothing on the page matched")
	}
	page.Sleep(1500 * time.Millisecond)

	for _, tab := range []struct {
		label    string
		controls string
		text     *regexp.Regexp
	}{
		{"the in-warehouse tab", `button, a, [data-testid*="warehouse"]`, regexp.MustCompile(`(?i)in-?warehouse|warehouse`)},
		{"the gas station tab", `button, a, [data-testid*="gas"]`, regexp.MustCompile(`(?i)gas station`)},
		{"the online orders tab", `button, a, [data-testid*="online"]`, regexp.MustCompile(`(?i)online orders|^orders$`)},
	} {
		pressed, err := page.ClickText(tab.controls, tab.text)
		switch {
		case err != nil:
			*trace = append(*trace, tab.label+": "+textutil.Clip(err.Error(), 120))
		case pressed:
			*trace = append(*trace, tab.label+": pressed")
			page.Settle()
		default:
			*trace = append(*trace, tab.label+": nothing on the page matched")
		}
		page.Sleep(1500 * time.Millisecond)
	}
}

var costcoWidestRange = regexp.MustCompile(`(?i)2 ?years|24 ?months|all`)

type pageAnswer struct {
	OK      bool
	Status  int
	Excerpt string
	Error   string
	// Keys are the browser-storage key names only: a value would be the token.
	Keys []string

	decoded any
}

// costcoPageToken is the longest id token the site's app keeps in either
// store, sent the two ways the endpoint reads it.
var costcoPageToken = browser.StorageToken{
	Stores:  []string{"localStorage", "sessionStorage"},
	Key:     "idtoken|id_token|access_token|accesstoken|authorization",
	Headers: []string{"authorization", "costco-x-authorization"},
	Scheme:  "Bearer ",
}

// queryFromPage's failure is a note, never an error: the captured responses
// are the first source and this is the second.
func queryFromPage(
	ctx context.Context, page browser.Page, query string, variables map[string]any,
) (pageAnswer, error) {
	token := costcoPageToken
	answer, err := browser.CallFromPage(ctx, page, browser.PageCall{
		URL: costcoGraphQL, Method: "POST", Credentials: "include", Token: &token,
		Headers: map[string]string{
			"Content-Type":      "application/json-patch+json",
			"client-identifier": costcoClientIdentifier,
		},
		Body: map[string]any{"query": query, "variables": variables},
	})
	if err != nil {
		return pageAnswer{}, err
	}
	out := pageAnswer{Status: answer.Status, Excerpt: answer.Excerpt, Error: answer.Error, Keys: answer.Keys}
	switch {
	case answer.NoToken:
		out.Error, out.Excerpt = "no id token in browser storage", ""
	case answer.Status >= 200 && answer.Status < 300 && len(answer.JSON) > 0 && string(answer.JSON) != "null":
		out.OK = httpx.DecodeJSON(answer.JSON, &out.decoded) == nil
	}
	return out, nil
}

type card struct {
	Line        string `json:"line"`
	Total       string `json:"total"`
	DateText    string `json:"date_text"`
	Barcode     string `json:"barcode"`
	OrderNumber string `json:"order_number"`
	Warehouse   string `json:"warehouse"`
	Online      bool   `json:"online"`
}

// costcoReadCards is the last resort: dates and totals, no items, which is
// enough to match a bank row.
const costcoReadCards = `() => {` + agent.CleanJS + `
  const out = [];
  const seen = new Set();
  const nodes = document.querySelectorAll(
    '[class*="order-card"], [class*="orderCard"], [class*="receipt"], [class*="purchase"], table tr, li'
  );
  for (const el of nodes) {
    const line = clean(el.innerText);
    if (!line || line.length > 400 || seen.has(line)) continue;
    const money = line.match(/\$\s*([\d,]+\.\d{2})/);
    const date = line.match(
      /\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.? \d{1,2},? \d{4}\b|\b\d{1,2}\/\d{1,2}\/\d{2,4}\b|\b\d{4}-\d{2}-\d{2}\b/i
    );
    if (!money || !date) continue;
    seen.add(line);
    const barcode = line.match(/\b\d{12,24}\b/);
    const orderNumber = line.match(/\border(?:\s*#|\s*number)?\s*:?\s*(\d{6,})\b/i);
    const warehouse = line.match(/\b(?:warehouse|costco)\s+([A-Za-z .'-]{3,30})\b/i);
    out.push({
      line,
      total: money[1].replace(/,/g, ''),
      date_text: date[0],
      barcode: barcode ? barcode[0] : '',
      order_number: orderNumber ? orderNumber[1] : '',
      warehouse: warehouse ? warehouse[1].trim() : '',
      online: /shipped|delivered|order placed|tracking/i.test(line),
    });
  }
  return out;
}`

func readCards(page browser.Page) ([]card, error) {
	var out []card
	if err := browser.EvaluateInto(page, costcoReadCards, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func cardToRaw(one card) (bool, any) {
	date := parseDateText(one.DateText)
	if one.Online {
		number := one.OrderNumber
		if number == "" {
			number = "costco-" + date + "-" + strings.ReplaceAll(one.Total, ".", "")
		}
		return true, onlineOrder{
			OrderNumber:     number,
			OrderPlacedDate: date,
			OrderTotal:      loose(one.Total),
			OrderLineItems:  []orderLine{},
		}
	}
	return false, receipt{
		TransactionBarcode: one.Barcode,
		TransactionDate:    date,
		TransactionNumber:  loose(strings.ReplaceAll(one.Total, ".", "")),
		WarehouseName:      one.Warehouse,
		Total:              loose(one.Total),
		ItemArray:          []receiptLine{},
		TenderArray:        []tender{},
	}
}
