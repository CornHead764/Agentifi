package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// The bills bridge over HTTP.
//
// The property worth the whole stack is the last one: a statement entered on
// this resource changes what the /occurrences screen says a reminder costs and
// when it is due, on the one slot the bill speaks about and nowhere else.
//
// Every figure is invented. The clock is seriesClock — 2026-08-22 — so the
// next occurrence of a monthly-on-the-first series is 2026-09-01.

// newBillConnection posts a connection and returns it.
func newBillConnection(c *client, body map[string]any) map[string]any {
	c.t.Helper()
	payload := map[string]any{
		"biller": string(domain.BillerSpectrum),
		"label":  "Main account",
	}
	for key, value := range body {
		payload[key] = value
	}
	return c.post("/bills/connections", payload).requireStatus(http.StatusCreated).json()
}

func TestAConnectionNamesABillerThisBuildKnows(t *testing.T) {
	// A typo becomes a 422 rather than a connection nothing will ever pull.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	response := alex.post("/bills/connections", map[string]any{
		"biller": "spektrum", "label": "Main account",
	})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "biller")
}

func TestAConnectionNeverAnswersWithACredential(t *testing.T) {
	// The response says whether a session is on file and never what it is.
	// Checked over the fields rather than over the text, because
	// credential_source legitimately says "session": the fact a session is the
	// way in is not the session.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	created := newBillConnection(alex, nil)
	require.Equal(t, false, created["connected"])

	rows := alex.get("/bills/connections").requireStatus(http.StatusOK).list()
	require.Len(t, rows, 1)
	for field := range rows[0] {
		for _, refused := range []string{"session_state", "password", "secret", "token"} {
			require.NotEqual(t, refused, field, "a connection listing carries no credential")
		}
	}
}

func TestAnEmailProviderBillsForItselfFromTheStart(t *testing.T) {
	// Nothing signs in to a mailbox provider, so nothing could list what it
	// bills for; the connection is the one billed account, made with it. A
	// provider that signs in lists its own and starts with none.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	mailed := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerApple), "label": "Subscriptions",
	})
	rows := alex.get("/bills/subaccounts?connection_id=" + mailed["id"].(string)).
		requireStatus(http.StatusOK).list()
	require.Len(t, rows, 1)
	require.Equal(t, "Subscriptions", rows[0]["label"])
	require.Equal(t, true, rows[0]["is_selected"])

	signedIn := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerErie), "label": "Main vehicle",
	})
	require.Empty(t, alex.get("/bills/subaccounts?connection_id="+signedIn["id"].(string)).
		requireStatus(http.StatusOK).list())
}

func TestAConnectionNeedsNoNameUntilItHasATwin(t *testing.T) {
	// One login at a provider needs nothing added to the provider's name. A
	// second one at the same provider does, and the refusal says so rather
	// than naming an empty label.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	unnamed := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerErie), "label": "  ",
	})
	require.Equal(t, "", unnamed["label"])

	twin := alex.post("/bills/connections", map[string]any{
		"biller": string(domain.BillerErie), "label": "",
	})
	twin.requireStatus(http.StatusConflict)
	require.Contains(t, twin.Body.String(), "give this one a name")

	newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerErie), "label": "Second account",
	})

	// A mailed provider with no name bills for itself under the provider's.
	mailed := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerApple), "label": "",
	})
	rows := alex.get("/bills/subaccounts?connection_id=" + mailed["id"].(string)).
		requireStatus(http.StatusOK).list()
	require.Len(t, rows, 1)
	require.Equal(t, "Apple", rows[0]["label"])
}

func TestAViewerMayReadBillsAndChangeNothing(t *testing.T) {
	l := buildLedger(t)
	newBillConnection(frozenClient(l, "alex"), nil)

	vera := l.as("vera")
	vera.get("/bills/connections").requireStatus(http.StatusOK)
	vera.post("/bills/connections", map[string]any{
		"biller": string(domain.BillerErie), "label": "Main vehicle",
	}).requireStatus(http.StatusForbidden)
}

func TestOccurrencesCarryBillFigures(t *testing.T) {
	// The reminder says the 1st at 100.00. The statement says the 3rd at
	// 80.00, and the household's rule pays it two days before it is due, so
	// the money leaves on the 1st. The September slot carries all three; the
	// October slot, which no statement speaks about, carries none of them.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	// A statement speaks about the slot whose match window holds its due
	// date: the 3rd is September's.
	series := newSeries(alex, l, map[string]any{
		"description":        "SPECTRUM INTERNET",
		"display_name":       "Internet",
		"start_on":           "2026-09-01",
		"auto_adjust_due_on": true,
	})
	connection := newBillConnection(alex, map[string]any{
		"autopay_rule": string(domain.AutopayDaysBeforeDue),
		"autopay_days": 2,
	})
	subaccount := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated).json()
	alex.post("/bills/links", map[string]any{
		"series_id": series["id"], "subaccount_id": subaccount["id"],
	}).requireStatus(http.StatusCreated)

	ingested := alex.post("/bills/connections/"+connection["id"].(string)+"/bills", map[string]any{
		"due_on": "2026-09-03", "amount_due": "80.00",
	}).requireStatus(http.StatusCreated).json()
	require.EqualValues(t, 1, ingested["new"])

	list := occurrencesIn(alex, "from=2026-09-01&to=2026-10-31")
	byDue := map[string]map[string]any{}
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == series["id"] {
			byDue[item["due_on"].(string)] = item
		}
	}
	require.Len(t, byDue, 2, "the bill moves the September slot rather than adding one")

	claimed, found := byDue["2026-09-03"]
	require.True(t, found, "the slot moved to the statement's own due date")
	require.Equal(t, "-80.00", claimed["amount"])
	require.Equal(t, "2026-09-01", claimed["pays_on"])
	bill := claimed["bill"].(map[string]any)
	require.Equal(t, "80.00", bill["amount_due"], "the statement itself states what is owed")
	require.Equal(t, "2026-09-03", bill["due_on"])
	require.Equal(t, "open", bill["status"])
	require.Equal(t, "manual", bill["source"])
	require.Nil(t, bill["document_id"])

	next, found := byDue["2026-10-01"]
	require.True(t, found)
	require.Equal(t, "-100.00", next["amount"], "a bill speaks about one cycle")
	require.Nil(t, next["pays_on"])
	require.Nil(t, next["bill"])

	// The link, unlike the bill, is on every slot: the mark says whose
	// reminder this is, not which cycle a statement arrived for. With no
	// session on the connection it reads not_connected.
	for _, item := range byDue {
		link := item["bill_link"].(map[string]any)
		require.Equal(t, connection["id"], link["connection_id"])
		require.Equal(t, "Internet", link["subaccount_label"])
		require.Equal(t, "not_connected", link["health"])
	}
	unlinked := newSeries(alex, l, map[string]any{"description": "GYM", "start_on": "2026-09-05"})
	for _, raw := range occurrencesIn(alex, "from=2026-09-01&to=2026-10-31")["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] == unlinked["id"] {
			require.Nil(t, item["bill_link"], "an unlinked series carries no mark")
		}
	}
}

func TestTwoBillsDueTheSameDayAreTwoReminderLines(t *testing.T) {
	// A lawn-care provider invoices two services on one billed account, both
	// due the 3rd. Each is its own line on /occurrences, with its own
	// statement and amount, and paying the slot settles both.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	series := newSeries(alex, l, map[string]any{
		"description": "LAWN CARE CO", "start_on": "2026-09-01", "auto_adjust_due_on": true,
	})
	connection := newBillConnection(alex, nil)
	subaccount := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "yard-1", "label": "Yard"}).
		requireStatus(http.StatusCreated).json()
	alex.post("/bills/links", map[string]any{
		"series_id": series["id"], "subaccount_id": subaccount["id"],
	}).requireStatus(http.StatusCreated)

	spaceID := store.SpaceIDOf(l.id("space"))
	subaccountID := uuid.MustParse(subaccount["id"].(string))
	billIDs := map[string]string{}
	for invoice, amount := range map[string]string{"INV-A": "48.00", "INV-B": "66.00"} {
		bill := &store.Bill{
			SubaccountID: subaccountID, DueOn: domain.NewDate(2026, 9, 3), Invoice: invoice,
			AmountDue: domain.MustFromString(amount), Currency: "USD", Status: domain.BillOpen,
			Source: "provider", FetchedAt: l.env.now(),
		}
		require.NoError(t, l.env.DB.UpsertBill(context.Background(), spaceID, bill, false))
		billIDs["-"+amount] = bill.ID.String()
	}

	lines := map[string]string{}
	for _, raw := range occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")["items"].([]any) {
		item := raw.(map[string]any)
		if item["series_id"] != series["id"] {
			continue
		}
		require.Equal(t, "2026-09-03", item["due_on"])
		lines[item["amount"].(string)] = item["bill"].(map[string]any)["id"].(string)
	}
	require.Equal(t, billIDs, lines, "one line per bill, each naming its own statement")

	// Paid early, so the charge settles the slot rather than holding it.
	alex.post("/occurrences/accept", map[string]any{
		"series_id": series["id"], "due_on": "2026-09-03", "date": "2026-08-21",
	}).requireStatus(http.StatusCreated)
	for _, raw := range occurrencesIn(alex, "from=2026-09-01&to=2026-09-30")["items"].([]any) {
		require.NotEqual(t, series["id"], raw.(map[string]any)["series_id"], "the slot is paid, both bills with it")
	}
}

func TestAManualBillTypedTwiceIsOneBill(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnection(alex, nil)
	alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated)

	body := map[string]any{"due_on": "2026-09-03", "amount_due": "80.00"}
	first := alex.post("/bills/connections/"+connection["id"].(string)+"/bills", body).
		requireStatus(http.StatusCreated).json()
	require.EqualValues(t, 1, first["new"])

	second := alex.post("/bills/connections/"+connection["id"].(string)+"/bills", body).
		requireStatus(http.StatusCreated).json()
	require.EqualValues(t, 0, second["new"])
	require.EqualValues(t, 0, second["amended"])
	require.EqualValues(t, 1, second["unchanged"])
	require.Len(t, second["bills"].([]any), 1)
}

func TestABillAnswersTheDateTheRuleResolves(t *testing.T) {
	// autopay_on is the provider's own word and stays empty for a provider
	// nobody can sign in to. pays_on is the answer: the stated date where
	// there is one, else the connection's rule against the due date. Two
	// days before the 3rd is the 1st.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnection(alex, map[string]any{
		"autopay_rule": string(domain.AutopayDaysBeforeDue),
		"autopay_days": 2,
	})
	id := connection["id"].(string)
	subaccount := alex.post("/bills/connections/"+id+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated).json()

	ingested := alex.post("/bills/connections/"+id+"/bills",
		map[string]any{"due_on": "2026-09-03", "amount_due": "80.00"}).
		requireStatus(http.StatusCreated).json()
	written := ingested["bills"].([]any)[0].(map[string]any)
	require.Nil(t, written["autopay_on"], "the provider stated no date")
	require.Equal(t, "2026-09-01", written["pays_on"])

	listed := alex.get("/bills/subaccounts/" + subaccount["id"].(string) + "/bills").
		requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, "2026-09-01", listed[0]["pays_on"], "the listing resolves it too")

	// A provider that states its own date is believed over the household's
	// description of the arrangement.
	stated := alex.post("/bills/connections/"+id+"/bills", map[string]any{
		"due_on": "2026-10-03", "amount_due": "138.00", "autopay_on": "2026-09-30",
	}).requireStatus(http.StatusCreated).json()
	for _, raw := range stated["bills"].([]any) {
		one := raw.(map[string]any)
		if one["due_on"] == "2026-10-03" {
			require.Equal(t, "2026-09-30", one["pays_on"])
		}
	}
}

func TestABillOnAConnectionThatDoesNotAutopayPaysOnNothing(t *testing.T) {
	// No rule and no stated date is not "the due date" — it is nobody having
	// said when the money moves, and a row that guessed would be telling the
	// household a payment is scheduled that is not.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnection(alex, nil)
	id := connection["id"].(string)
	alex.post("/bills/connections/"+id+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated)

	ingested := alex.post("/bills/connections/"+id+"/bills",
		map[string]any{"due_on": "2026-09-03", "amount_due": "80.00"}).
		requireStatus(http.StatusCreated).json()
	require.Nil(t, ingested["bills"].([]any)[0].(map[string]any)["pays_on"])
}

func TestABillInAnotherSpaceIsInvisible(t *testing.T) {
	l := buildLedger(t)
	connection := newBillConnection(frozenClient(l, "alex"), nil)

	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get("/bills/connections/" + connection["id"].(string)).requireStatus(http.StatusNotFound)
}

func TestManualBillStoresItsStatement(t *testing.T) {
	// One request carries the bill and the PDF it came on. The same handler
	// still answers plain JSON, because the API's shape did not change: the
	// multipart form is what a browser can send a file in.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnection(alex, nil)
	subaccount := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated).json()

	ingested := alex.upload("/bills/connections/"+connection["id"].(string)+"/bills",
		"document", "september.pdf", storetest.PDF(),
		map[string]string{"due_on": "2026-09-03", "amount_due": "80.00"}).
		requireStatus(http.StatusCreated).json()
	require.EqualValues(t, 1, ingested["new"])

	bills := ingested["bills"].([]any)
	require.Len(t, bills, 1)
	stored := bills[0].(map[string]any)
	documentID := stored["document_id"]
	require.NotNil(t, documentID, "the bill row names the statement it arrived with")

	// The bytes come back, which is the whole point of storing them.
	content := alex.get("/documents/" + documentID.(string) + "/content").
		requireStatus(http.StatusOK)
	require.Equal(t, storetest.PDF(), content.Body.Bytes())
	require.Equal(t, "application/pdf", content.Header().Get("Content-Type"))

	document := alex.get("/documents/" + documentID.(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "upload", document["source"])

	// And the bill says so wherever it is read, not only in the reply to the
	// request that wrote it.
	listed := alex.get("/bills/subaccounts/" + subaccount["id"].(string) + "/bills").
		requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, documentID, listed[0]["document_id"])

	// The JSON path is untouched: no file, no document, same 201.
	plain := alex.post("/bills/connections/"+connection["id"].(string)+"/bills",
		map[string]any{"due_on": "2026-10-03", "amount_due": "138.00"}).
		requireStatus(http.StatusCreated).json()
	require.EqualValues(t, 1, plain["new"])
	for _, raw := range plain["bills"].([]any) {
		one := raw.(map[string]any)
		if one["due_on"] == "2026-10-03" {
			require.Nil(t, one["document_id"])
		}
	}
}

func TestAnUnsupportedStatementWritesNoBill(t *testing.T) {
	// The file is checked before the bill is written. Otherwise a household
	// that picked the wrong file would be left with a bill on the record and
	// an error saying the request failed.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnection(alex, nil)
	subaccount := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated).json()

	alex.upload("/bills/connections/"+connection["id"].(string)+"/bills",
		"document", "september.html", []byte("<html>not a statement</html>"),
		map[string]string{"due_on": "2026-09-03", "amount_due": "80.00"}).
		requireStatus(http.StatusConflict)

	require.Empty(t, alex.get("/bills/subaccounts/"+subaccount["id"].(string)+"/bills").
		requireStatus(http.StatusOK).list())
}

func TestTheRowThatPaidABillShowsItsStatement(t *testing.T) {
	// Nobody attached anything to the bank row. It shows the statement because
	// the slot it settled is the slot the bill speaks about — the point of one
	// document store behind both.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	series := newSeries(alex, l, map[string]any{
		"description":        "SPECTRUM INTERNET",
		"display_name":       "Internet",
		"start_on":           "2026-09-01",
		"auto_adjust_due_on": true,
	})
	connection := newBillConnection(alex, nil)
	subaccount := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated).json()
	alex.post("/bills/links", map[string]any{
		"series_id": series["id"], "subaccount_id": subaccount["id"],
	}).requireStatus(http.StatusCreated)

	ingested := alex.upload("/bills/connections/"+connection["id"].(string)+"/bills",
		"document", "september.pdf", storetest.PDF(),
		map[string]string{"due_on": "2026-09-03", "amount_due": "80.00"}).
		requireStatus(http.StatusCreated).json()
	documentID := ingested["bills"].([]any)[0].(map[string]any)["document_id"]
	require.NotNil(t, documentID)

	// The statement moved the occurrence to the 3rd, and paying it files the
	// charge under the slot the schedule landed on.
	charge := alex.post("/occurrences/accept", map[string]any{
		"series_id": series["id"], "due_on": "2026-09-03",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "2026-09-01", charge["series_due_on"])

	behind := alex.get("/documents?transaction_id=" + charge["id"].(string)).
		requireStatus(http.StatusOK).list()
	require.Len(t, behind, 1)
	require.Equal(t, documentID, behind[0]["id"])
	require.Equal(t, "receipt", behind[0]["via"], "nobody attached it; it is filed on the row")
	receiptOf := behind[0]["receipt_of"].(map[string]any)
	require.Equal(t, "bill", receiptOf["kind"])
	require.Equal(t, "2026-09-03", receiptOf["due_on"])
	require.Equal(t, connection["label"], receiptOf["name"])

	row := alex.get("/transactions/" + charge["id"].(string)).requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, row["attachment_count"], "the register's paperclip counts the receipt")

	// Only the pull and the bill write a receipt, and only they remove it.
	alex.del("/attachments/" + documentID.(string)).requireStatus(http.StatusNotFound)
	alex.upload("/documents", "file", "mine.pdf", storetest.PDF(),
		map[string]string{"kind": "receipt", "target_id": charge["id"].(string)}).
		requireStatus(http.StatusConflict)
	require.Len(t, alex.get("/documents?transaction_id="+charge["id"].(string)).
		requireStatus(http.StatusOK).list(), 1)

	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	bob.get("/documents?transaction_id=" + charge["id"].(string)).requireStatus(http.StatusNotFound)
	bob.get("/documents/" + documentID.(string) + "/content").requireStatus(http.StatusNotFound)
}

func TestDeletingAConnectionReleasesItsStatements(t *testing.T) {
	// The bills go with the connection through their foreign keys; the
	// statement stays, unlinked, for the purge's week. A link left pointing
	// at a bill that no longer exists would keep it forever.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	connection := newBillConnection(alex, nil)
	subaccount := alex.post("/bills/connections/"+connection["id"].(string)+"/subaccounts",
		map[string]any{"external_id": "line-1", "label": "Internet"}).
		requireStatus(http.StatusCreated).json()
	require.NotEmpty(t, subaccount["id"])
	ingested := alex.upload("/bills/connections/"+connection["id"].(string)+"/bills",
		"document", "september.pdf", storetest.PDF(),
		map[string]string{"due_on": "2026-09-03", "amount_due": "80.00"}).
		requireStatus(http.StatusCreated).json()
	stored := ingested["bills"].([]any)[0].(map[string]any)
	documentID := uuid.MustParse(stored["document_id"].(string))

	alex.del("/bills/connections/" + connection["id"].(string)).requireStatus(http.StatusNoContent)

	links, err := l.env.DB.ListDocumentLinks(context.Background(), store.SpaceIDOf(l.id("space")), documentID)
	require.NoError(t, err)
	require.Empty(t, links, "the deleted bill's link is released with it")
	alex.get("/documents/" + documentID.String()).requireStatus(http.StatusOK)
}

// The deployment slug is a hostname label and a path segment, so it is checked
// once, here, against the narrower of the two rules.
//
// Every value below is invented. A dot, a slash, a space or a capital letter
// is the whole point of the test: each of them, interpolated, is a signed-in
// browser pointed somewhere nobody chose.
func TestADeploymentSlugIsAHostnameLabelOrItIsRefused(t *testing.T) {
	for _, good := range []string{"exampletown", "fair-haven", "town2", "a", "x-1-y"} {
		require.Truef(t, ValidBillSite(good), "should be usable: %q", good)
	}
	for _, bad := range []string{
		"", " ", "example town", "Exampletown", "exampletown.example.test", "exampletown/home",
		"example_town", "exampletown:8000", "../exampletown", "exampletown?x=1",
		strings.Repeat("a", 64),
	} {
		require.Falsef(t, ValidBillSite(bad), "should be refused: %q", bad)
	}
	require.True(t, ValidBillSite(strings.Repeat("a", 63)), "63 characters is a label")
}

func TestAConnectionCarriesTheDeploymentItSignsInTo(t *testing.T) {
	// The site is the connection's, not the sign-in's: the nightly pull and
	// the keepalive open the same address the person did. Both slugs are
	// invented towns.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	created := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerCommunityConnect), "label": "Main account",
		"site": " Exampletown ",
	})
	require.Equal(t, "exampletown", created["site"], "a slug is folded once, at the edge")

	moved := alex.patch("/bills/connections/"+created["id"].(string),
		map[string]any{"site": "fair-haven"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "fair-haven", moved["site"])

	refused := alex.patch("/bills/connections/"+created["id"].(string),
		map[string]any{"site": "sampleton.example.test"})
	refused.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, refused.Body.String(), "site")

	// A shared provider has no deployment to name, so a value there is a field
	// somebody typed into by mistake rather than configuration.
	spread := alex.post("/bills/connections", map[string]any{
		"biller": string(domain.BillerSpectrum), "label": "Second account", "site": "exampletown",
	})
	spread.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, spread.Body.String(), "site")
}

func TestAMyChartConnectionKeepsThePortalAddressNotAPage(t *testing.T) {
	// Every health system runs its own MyChart, so the site is the address the
	// household copied, folded to the portal's root. The host is invented.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	created := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerMyChart), "label": "Example Health",
		"site": "https://MyChart.example-health.example/MyChart/Billing/Summary?tab=1",
	})
	require.Equal(t, "https://mychart.example-health.example/MyChart", created["site"])

	refused := alex.patch("/bills/connections/"+created["id"].(string),
		map[string]any{"site": "https://192.0.2.10/MyChart"})
	refused.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, refused.Body.String(), "address bar")
}
