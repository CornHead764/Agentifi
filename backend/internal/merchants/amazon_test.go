package merchants

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

// The Amazon module's pure halves, and its classifier over a page that is not
// a browser. Every fixture here is invented.

// showing is a page that answers the one reading script with these groups
// visible, and nothing else.
func showing(address string, visible []string, texts map[string]string, body string) *browser.StubPage {
	page := &browser.StubPage{Location: address}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		shown := map[string]bool{}
		for _, group := range visible {
			shown[group] = true
		}
		if texts == nil {
			texts = map[string]string{}
		}
		return map[string]any{
			"url": page.Location, "title": "", "text": body, "visible": shown, "texts": texts,
		}, nil
	}
	return page
}

func TestTheAmazonClassifierReadsTheScreenItIsOn(t *testing.T) {
	module := Amazon()
	for _, one := range []struct {
		name   string
		page   *browser.StubPage
		want   string
		check  bool
		prompt string
	}{
		{
			name: "the email step",
			page: showing("https://www.amazon.com/ap/signin", []string{"email"}, nil, ""),
			want: StateEmail,
		},
		{
			name: "the password step",
			page: showing("https://www.amazon.com/ap/signin", []string{"password"}, nil, ""),
			want: StatePassword,
		},
		{
			name: "the one-time code, with Amazon's own wording",
			page: showing("https://www.amazon.com/ap/mfa", []string{"otp", "otpPrompt"},
				map[string]string{"otpPrompt": "Enter the code we sent to your phone"}, ""),
			want:   StateOTP,
			prompt: "Enter the code we sent to your phone",
		},
		{
			name: "a verification code on a challenge page",
			page: showing("https://www.amazon.com/ap/cvf/verify", []string{"code"}, nil, ""),
			want: StateOTP,
		},
		{
			name: "the check page, which is not under /ap/",
			page: showing("https://www.amazon.com/errors/validateCaptcha", nil, nil, ""),
			want: StateCaptcha, check: true,
		},
		{
			name: "the approval tap",
			page: showing("https://www.amazon.com/ap/challenge", []string{"approval"}, nil, ""),
			want: StateApproval,
		},
		{
			name: "signed in, because the account menu greets somebody",
			page: showing("https://www.amazon.com/your-orders/orders", []string{"greeting"},
				map[string]string{"greeting": "Hello, Alex"}, ""),
			want: StateSignedIn,
		},
		{
			name: "signed in, because the orders are on the page",
			page: showing("https://www.amazon.com/your-orders/orders", []string{"orders"}, nil, ""),
			want: StateSignedIn,
		},
		{
			name: "a wrong password, in Amazon's words",
			page: showing("https://www.amazon.com/ap/signin", []string{"error"},
				map[string]string{"error": "Your password is incorrect"}, ""),
			want: StateFailed,
		},
		{
			name: "a page nobody recognises",
			page: showing("https://www.amazon.com/somewhere", nil, nil, ""),
			want: StateFailed,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			where, err := module.Classify(one.page)
			require.NoError(t, err)
			require.Equal(t, one.want, where.State)
			require.Equal(t, one.check, where.Blocking)
			if one.prompt != "" {
				require.Equal(t, one.prompt, where.Prompt)
			}
		})
	}
}

// "Hello, sign in" is the signed-out menu, and it must not read as a name.
func TestASignedOutAccountMenuIsNotASignedInSession(t *testing.T) {
	page := showing("https://www.amazon.com/your-orders/orders", []string{"greeting"},
		map[string]string{"greeting": "Hello, sign in"}, "")
	where, err := Amazon().Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFailed, where.State)
}

func TestTheAccountHintIsTheNameAmazonGreetsSomebodyBy(t *testing.T) {
	page := showing("https://www.amazon.com/your-orders/orders", []string{"greeting"},
		map[string]string{"greeting": "Hello, Alex"}, "")
	hint, err := Amazon().AccountHint(page)
	require.NoError(t, err)
	require.Equal(t, "Alex", hint)
}

func TestTheSignInFormTypesAndSubmits(t *testing.T) {
	module := Amazon()
	page := showing("https://www.amazon.com/ap/signin", []string{"email"}, nil, "")
	_, err := module.FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, "someone@example.test", page.Filled[0].Value)
	require.NotEmpty(t, page.Clicked, "the form's own Continue is pressed")

	page = showing("https://www.amazon.com/ap/signin", []string{"password"}, nil, "")
	page.OnCheck = func(string) (bool, error) { return true, nil }
	_, err = module.FillPassword(page, "a-password", "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, `#ap_password`, page.Filled[0].Selector)
	require.Equal(t, []string{`input[name="rememberMe"]`}, page.Checked,
		"keep me signed in is ticked, or the session is challenged every night")
}

// A page with no button of its own is submitted with Enter rather than left
// sitting there.
func TestAFormWithNoButtonIsSubmittedWithEnter(t *testing.T) {
	page := showing("https://www.amazon.com/ap/signin", []string{"email"}, nil, "")
	page.Missing = func(string) bool { return true }
	_, err := Amazon().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, []string{"Enter"}, page.Pressed)
}

func TestAnswerTypesTheCodeAndTicksRememberThisDevice(t *testing.T) {
	page := showing("https://www.amazon.com/ap/mfa", []string{"otp"}, nil, "")
	page.OnCheck = func(string) (bool, error) { return true, nil }
	step, err := Amazon().Answer(page, State{State: StateOTP}, "123456", "")
	require.NoError(t, err)
	require.True(t, step.Acted)
	require.Equal(t, "123456", page.Filled[0].Value)
	require.Equal(t, []string{`#auth-mfa-remember-device`}, page.Checked)
}

// A state the module does not type into is reported back unchanged, which is
// what lets the dialog draw whatever the page is asking for instead.
func TestAnswerDoesNothingAtAPageThatIsNotAskingForACode(t *testing.T) {
	page := showing("https://www.amazon.com/ap/signin", []string{"email"}, nil, "")
	step, err := Amazon().Answer(page, State{State: StateEmail}, "123456", "")
	require.NoError(t, err)
	require.False(t, step.Acted)
	require.Empty(t, page.Filled)
}

func TestFiltersForCoverTheWindowAskedFor(t *testing.T) {
	require.Equal(t, []string{"last30"}, filtersFor(30, domain.NewDate(2026, time.September, 12)))
	require.Equal(t, []string{"months-3"}, filtersFor(90, domain.NewDate(2026, time.September, 12)))
	require.Equal(t, []string{"year-2026"}, filtersFor(200, domain.NewDate(2026, time.September, 12)))
	require.Equal(t, []string{"year-2026", "year-2025"}, filtersFor(365, domain.NewDate(2026, time.September, 12)))
	require.Equal(t,
		[]string{"year-2026", "year-2025", "year-2024"}, filtersFor(800, domain.NewDate(2026, time.September, 12)))
}

func TestTheDatesEachPageWrites(t *testing.T) {
	require.Equal(t, "2026-09-05", parseDateText("September 5, 2026"))
	require.Equal(t, "2026-09-05", parseDateText("Sep 5, 2026"))
	require.Equal(t, "2026-09-05", parseDateText("Sept 5, 2026"))
	require.Equal(t, "2026-09-05", parseDateText("Sept. 5, 2026"))
	require.Equal(t, "2026-09-05", parseDateText("9/5/26"))
	require.Equal(t, "2026-09-05", parseDateText("9/5/2026"))
	require.Equal(t, "2026-09-05", parseDateText("2026-09-05T14:22:11.000"))
	require.Equal(t, "", parseDateText("whenever"))
	require.Equal(t, "", parseDateText(""))
}

// The invoice is where an order's prices come from; the card has the ASIN and
// the picture and the invoice has what each line cost.
func TestMergeInvoicePutsThePricesOnTheCardsOwnItems(t *testing.T) {
	order := &amazonOrder{
		OrderID: "111-2222222-3333333", Total: "0.00",
		Items: []amazonItem{
			{Title: "Paper towels, 12 rolls", ASIN: "B00TESTONE", Quantity: 1},
			{Title: "USB cable", ASIN: "B00TESTTWO", Quantity: 1},
		},
	}
	mergeInvoice(order, invoice{
		Items: []invoiceItem{
			{Title: "Paper towels, 12 rolls", ASIN: "B00TESTONE", Quantity: 2, Price: "31.98"},
			{Title: "USB cable", ASIN: "", Price: "9.99"},
			{Title: "Batteries, AA", ASIN: "B00TESTTHR", Quantity: 1, Price: "12.49"},
		},
		Subtotal: "54.46", Tax: "3.81", Shipping: "0.00", GiftCard: "-5.00", GrandTotal: "53.27",
	})
	require.Equal(t, "31.98", order.Items[0].Price)
	require.Equal(t, 2, order.Items[0].Quantity, "a quantity the card did not carry")
	require.Equal(t, "9.99", order.Items[1].Price, "matched by its title when the invoice has no ASIN")
	require.Len(t, order.Items, 3, "an item only the invoice knew about is added")
	require.Equal(t, "Batteries, AA", order.Items[2].Title)
	require.Equal(t, "53.27", order.Total, "the card had no total, so the invoice's stands")
	require.Equal(t, "5.00", order.GiftCard)
	require.Equal(t, "3.81", order.Tax)
	require.Equal(t, "0.00", order.Shipping)
}

// One item, and a subtotal that is a whole multiple of the price the page
// showed: a quantity nobody spelled out.
func TestOneItemTakesItsQuantityFromTheSubtotal(t *testing.T) {
	order := &amazonOrder{
		OrderID: "111-2222222-3333333",
		Items:   []amazonItem{{Title: "Paper towels", ASIN: "B00TESTONE", Quantity: 1, Price: "12.00"}},
	}
	mergeInvoice(order, invoice{Subtotal: "36.00", GrandTotal: "38.50"})
	require.Equal(t, 3, order.Items[0].Quantity)
	require.Equal(t, "12.00", order.Items[0].Price)
}

// One item whose price does not divide the subtotal at all: the subtotal is
// what it cost, because that is the figure the invoice is sure of.
func TestOneItemWhosePriceDisagreesWithTheSubtotalTakesTheSubtotal(t *testing.T) {
	order := &amazonOrder{
		OrderID: "111-2222222-3333333",
		Items:   []amazonItem{{Title: "A thing", ASIN: "B00TESTONE", Quantity: 1, Price: "9.99"}},
	}
	mergeInvoice(order, invoice{Subtotal: "14.37"})
	require.Equal(t, "14.37", order.Items[0].Price)
	require.Equal(t, 1, order.Items[0].Quantity)
}

// An order with no gift card share says so as a figure rather than leaving the
// field to be guessed at.
func TestAnOrderWithNoGiftCardShareSaysZero(t *testing.T) {
	order := &amazonOrder{OrderID: "1", Items: []amazonItem{}}
	mergeInvoice(order, invoice{})
	require.Equal(t, "0.00", order.GiftCard)
	require.Equal(t, "0.00", order.Tax)
	require.Equal(t, "0.00", order.Shipping)
}

// The balance page writes money leaving the balance with a minus, or only in
// words. Either way an order paid from the balance is money out.
func TestTheGiftCardActivityIsSignedTheWayABankWouldSeeIt(t *testing.T) {
	lines := giftCardLines([]giftCardRow{
		{Line: "September 5, 2026 Order 111-2222222-3333333 $24.99", Sign: "",
			Amount: "24.99", OrderID: "111-2222222-3333333"},
		{Line: "September 3, 2026 Gift card reload $50.00", Sign: "", Amount: "50.00"},
		{Line: "September 1, 2026 Refund to your gift card balance $12.00", Sign: "", Amount: "12.00"},
		{Line: "August 30, 2026 Applied to order $7.25", Sign: "-", Amount: "7.25"},
		{Line: "no date here $1.00", Sign: "", Amount: "1.00"},
	})
	require.Len(t, lines, 4, "a line with no date is not activity")
	require.Equal(t, "-24.99", lines[0].Amount)
	require.Equal(t, "111-2222222-3333333", lines[0].OrderID)
	require.Equal(t, "50.00", lines[1].Amount, "a reload is money in")
	require.Equal(t, "12.00", lines[2].Amount, "a refund to the balance is money in")
	require.Equal(t, "-7.25", lines[3].Amount)
	require.Equal(t, "2026-09-05", lines[0].Date)
	require.Contains(t, lines[1].Description, "Gift card reload")
}

// The puzzle Amazon puts on the sign-in clears the password box under it, so
// the answer has to put the password back before it submits. Without this the
// form posts a blank password, Amazon says the password is wrong, and no retry
// can ever succeed.
func TestAnswerRefillsThePasswordAmazonClearedUnderItsPuzzle(t *testing.T) {
	page := showing("https://www.amazon.com/ap/signin", []string{"captcha"}, nil, "")
	step, err := Amazon().Answer(page, State{State: StateCaptcha}, "ABCDEF", "invented-password")
	require.NoError(t, err)
	require.True(t, step.Acted)
	require.Equal(t, []browser.StubFill{
		{Selector: `#auth-captcha-guess`, Value: "ABCDEF"},
		{Selector: amazonPassword, Value: "invented-password"},
	}, page.Filled)
}

// And where the puzzle stands on its own page, with no password box on it,
// nothing is typed where nothing is.
func TestAnswerLeavesThePasswordAloneWhereThePuzzleHasItsOwnPage(t *testing.T) {
	page := showing("https://www.amazon.com/errors/validateCaptcha", []string{"captcha"}, nil, "")
	page.Missing = func(selector string) bool { return selector == amazonPassword }
	step, err := Amazon().Answer(page, State{State: StateCaptcha}, "ABCDEF", "invented-password")
	require.NoError(t, err)
	require.True(t, step.Acted)
	require.Equal(t, []browser.StubFill{{Selector: `#auth-captcha-guess`, Value: "ABCDEF"}}, page.Filled)
}

// The payments page pages by offset, so a charge posting between two fetches
// shifts the window and the last row of one page arrives again as the first of
// the next. Filing it twice double-counts the money.
func TestChargesRepeatedByAShiftingWindowAreTakenOnce(t *testing.T) {
	page := showing("https://www.amazon.com/cpe/yourpayments/transactions",
		[]string{"orders"}, nil, "")
	classify := page.OnEvaluate

	// The same charge sits at the bottom of the first page and the top of the
	// second, the way it does when a new one posts underneath it.
	repeated := map[string]any{
		"order_id": "111-2222222-3333333", "date_text": "September 12, 2026",
		"amount": "12.34", "instrument": "Visa ****0000",
	}
	pages := []map[string]any{
		{"charges": []any{
			map[string]any{"order_id": "111-4444444-5555555", "date_text": "September 13, 2026",
				"amount": "56.78", "instrument": "Visa ****0000"},
			repeated,
		}, "hasNext": true},
		{"charges": []any{repeated}, "hasNext": false},
	}
	fetched := 0
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == amazonReadCharges {
			out := pages[min(fetched, len(pages)-1)]
			fetched++
			return out, nil
		}
		return classify(script, arg)
	}

	charges := amazonModule{}.readTransactions(
		Call{Page: page, Notes: &Notes{}}, "2026-09-01", nil)

	require.Equal(t, []amazonCharge{
		{OrderID: "111-4444444-5555555", Date: "2026-09-13", Amount: "56.78", Instrument: "Visa ****0000"},
		{OrderID: "111-2222222-3333333", Date: "2026-09-12", Amount: "12.34", Instrument: "Visa ****0000"},
	}, charges)
}

// printingPage is a stub that prints what it last visited.
type printingPage struct {
	*browser.StubPage
	fails bool
}

func (p *printingPage) PDF() ([]byte, error) {
	if p.fails {
		return nil, errors.New("printing is only supported in headless Chromium")
	}
	return []byte("%PDF-1.4 " + p.Location), nil
}

func invoicePage(fails bool) *printingPage {
	page := &printingPage{
		StubPage: showing("https://www.amazon.com/your-orders/orders", []string{"orders"}, nil, ""),
		fails:    fails,
	}
	classify := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == amazonReadInvoice {
			return map[string]any{
				"items":    []any{map[string]any{"quantity": 1, "title": "Desk lamp", "price": "24.99"}},
				"subtotal": "24.99", "tax": "1.75", "grand_total": "26.74", "gift_card": "0.00",
				"charges": []any{},
			}, nil
		}
		return classify(script, arg)
	}
	return page
}

func invoiceURL(orderID string) string {
	return "https://www.amazon.com/gp/css/summary/print.html?ie=UTF8&orderID=" + orderID
}

func TestTheInvoicePageIsPrintedWhileItIsOpen(t *testing.T) {
	page := invoicePage(false)
	orders := []*amazonOrder{
		{OrderID: "111-0000001-0000001", Status: "Delivered", Items: []amazonItem{{Title: "Desk lamp"}}},
		// Read in full before, but never printed: opened again for the print.
		{OrderID: "111-0000002-0000002", Status: "Delivered", Items: []amazonItem{{Title: "Desk lamp"}}},
		// Read and printed: not opened.
		{OrderID: "111-0000003-0000003", Status: "Delivered"},
		{OrderID: "111-0000004-0000004", Status: "Cancelled"},
	}
	call := Call{
		Page: page, Notes: &Notes{},
		SkipDetails: map[string]bool{"111-0000002-0000002": true, "111-0000003-0000003": true},
		Invoiced:    map[string]bool{"111-0000003-0000003": true},
	}

	_, invoices, _ := amazonModule{}.readInvoices(call, orders)

	require.Equal(t, []string{invoiceURL("111-0000001-0000001"), invoiceURL("111-0000002-0000002")}, page.Visited)
	require.Len(t, invoices, 2)
	require.Equal(t, "111-0000001-0000001", invoices[0].OrderID)
	require.Equal(t, "amazon-invoice-111-0000001-0000001.pdf", invoices[0].Filename)
	require.Equal(t, "%PDF-1.4 "+invoiceURL("111-0000001-0000001"), string(invoices[0].PDF))
	require.Equal(t, "111-0000002-0000002", invoices[1].OrderID)
	require.Equal(t, "24.99", orders[0].Items[0].Price, "a new order's invoice is still read")
	require.Empty(t, orders[1].Items[0].Price, "an order already read is only printed")
	require.Empty(t, call.Notes.List())
}

func TestABrowserThatCannotPrintStillReadsTheInvoices(t *testing.T) {
	page := invoicePage(true)
	orders := []*amazonOrder{
		{OrderID: "111-0000001-0000001", Status: "Delivered", Items: []amazonItem{{Title: "Desk lamp"}}},
		{OrderID: "111-0000002-0000002", Status: "Delivered", Items: []amazonItem{{Title: "Desk lamp"}}},
		{OrderID: "111-0000005-0000005", Status: "Delivered", Items: []amazonItem{{Title: "Desk lamp"}}},
	}
	call := Call{
		Page: page, Notes: &Notes{},
		SkipDetails: map[string]bool{"111-0000002-0000002": true},
	}

	_, invoices, _ := amazonModule{}.readInvoices(call, orders)

	require.Empty(t, invoices)
	require.Equal(t, []string{invoiceURL("111-0000001-0000001"), invoiceURL("111-0000005-0000005")}, page.Visited,
		"once printing fails, an order opened only to print it is left alone")
	require.Equal(t, "24.99", orders[2].Items[0].Price)
	require.Len(t, call.Notes.List(), 1)
	require.Contains(t, call.Notes.List()[0], "could not be printed")
}

func TestAPageThatCannotPrintOpensNothingItHasReadBefore(t *testing.T) {
	page := showing("https://www.amazon.com/your-orders/orders", []string{"orders"}, nil, "")
	orders := []*amazonOrder{{OrderID: "111-0000002-0000002", Status: "Delivered"}}
	call := Call{Page: page, Notes: &Notes{}, SkipDetails: map[string]bool{"111-0000002-0000002": true}}

	_, invoices, _ := amazonModule{}.readInvoices(call, orders)

	require.Empty(t, invoices)
	require.Empty(t, page.Visited)
}

// A new order's invoice keeps its refund total, and an order on file due a
// check is opened for it alone: once, after the new orders, even when it is
// one of them.
func TestTheInvoicesSayWhatWasRefundedAndDueOrdersAreCheckedAgain(t *testing.T) {
	page := invoicePage(false)
	classify := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		answer, err := classify(script, arg)
		if script == amazonReadInvoice && strings.HasSuffix(page.Location, "111-0000009-0000009") {
			answer.(map[string]any)["refund"] = "12.50"
		}
		return answer, err
	}
	orders := []*amazonOrder{
		{OrderID: "111-0000001-0000001", Status: "Delivered", Items: []amazonItem{{Title: "Desk lamp"}}},
	}
	call := Call{
		Page: page, Notes: &Notes{}, Invoiced: map[string]bool{"111-0000009-0000009": true},
		RefundChecks: []string{"111-0000009-0000009", "111-0000001-0000001"},
		Now:          func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) },
	}

	_, _, refunded := amazonModule{}.readInvoices(call, orders)

	require.Equal(t, []string{invoiceURL("111-0000001-0000001"), invoiceURL("111-0000009-0000009")}, page.Visited)
	require.Equal(t, []merchantimport.AmazonRefundTotal{
		{OrderID: "111-0000001-0000001", Amount: "0.00", Date: "2026-09-20"},
		{OrderID: "111-0000009-0000009", Amount: "12.50", Date: "2026-09-20"},
	}, refunded)
}

// blankFor makes the invoice page of these orders give nothing to read.
func blankFor(page *printingPage, orderIDs ...string) {
	read := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == amazonReadInvoice {
			for _, id := range orderIDs {
				if strings.HasSuffix(page.Location, "orderID="+id) {
					return map[string]any{"items": []any{}, "charges": []any{}}, nil
				}
			}
		}
		return read(script, arg)
	}
}

// sentAway makes the invoice page of this order land on another page, which
// shows these groups instead of an invoice.
func sentAway(page *printingPage, orderID, to string, visible ...string) {
	read := page.OnEvaluate
	page.OnGoto = func(url string) error {
		if strings.HasSuffix(url, "orderID="+orderID) {
			page.Location = to
		}
		return nil
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != amazonReadInvoice && page.Location == to {
			shown := map[string]bool{}
			for _, group := range visible {
				shown[group] = true
			}
			return map[string]any{"url": to, "title": "", "text": "", "visible": shown, "texts": map[string]string{}}, nil
		}
		return read(script, arg)
	}
}

// backfilled runs the backfill and keeps what it handed over, in order.
func backfilled(call Call, orders []string) (filed, missed []string, invoices []Invoice, stopped string, signIn bool) {
	stopped, signIn = amazonModule{}.BackfillInvoices(call, orders, func(orderID string, printed *Invoice) {
		if printed == nil {
			missed = append(missed, orderID)
			return
		}
		filed = append(filed, orderID)
		invoices = append(invoices, *printed)
	})
	return filed, missed, invoices, stopped, signIn
}

func TestABackfillPrintsEveryOrderInTurnAndPausesBetweenThem(t *testing.T) {
	page := invoicePage(false)
	blankFor(page, "111-0000008-0000008")
	var orders []string
	for n := 9; n >= 1; n-- {
		orders = append(orders, fmt.Sprintf("111-000000%d-000000%d", n, n))
	}

	filed, missed, invoices, stopped, signIn := backfilled(Call{Page: page, Notes: &Notes{}}, orders)

	var visited []string
	for _, id := range orders {
		visited = append(visited, invoiceURL(id))
	}
	require.Equal(t, visited, page.Visited, "every order, in the order given, each once")
	require.Equal(t, time.Duration(len(orders)-1)*amazonBackfillPause, page.Slept,
		"a pause before every page but the first")
	require.Equal(t, []string{"111-0000008-0000008"}, missed)
	require.Len(t, filed, len(orders)-1)
	require.Equal(t, "111-0000009-0000009", filed[0])
	require.Equal(t, "amazon-invoice-111-0000009-0000009.pdf", invoices[0].Filename)
	require.Equal(t, "%PDF-1.4 "+invoiceURL("111-0000009-0000009"), string(invoices[0].PDF))
	require.Empty(t, stopped)
	require.False(t, signIn)
}

func TestABackfillStopsAtTheFirstPageThatAsksToSignIn(t *testing.T) {
	page := invoicePage(false)
	sentAway(page, "111-0000002-0000002", "https://www.amazon.com/ap/signin?openid.return_to=invented", "email")
	orders := []string{"111-0000003-0000003", "111-0000002-0000002", "111-0000001-0000001"}

	filed, missed, _, stopped, signIn := backfilled(Call{Page: page, Notes: &Notes{}}, orders)

	require.Equal(t, []string{"111-0000003-0000003"}, filed)
	require.Empty(t, missed, "the order it stopped at has not missed")
	require.True(t, signIn)
	require.NotEmpty(t, stopped)
	require.Len(t, page.Visited, 2, "nothing is opened after the sign-in page")
}

func TestABackfillStopsAtACheckPage(t *testing.T) {
	page := invoicePage(false)
	sentAway(page, "111-0000003-0000003", "https://www.amazon.com/errors/validateCaptcha", "check")
	orders := []string{"111-0000003-0000003", "111-0000002-0000002"}

	filed, missed, _, stopped, signIn := backfilled(Call{Page: page, Notes: &Notes{}}, orders)

	require.Empty(t, filed)
	require.Empty(t, missed)
	require.True(t, signIn)
	require.NotEmpty(t, stopped)
	require.Len(t, page.Visited, 1)
}

func TestABackfillInABrowserThatCannotPrintOpensNothing(t *testing.T) {
	page := showing("https://www.amazon.com/your-orders/orders", []string{"orders"}, nil, "")

	stopped, signIn := amazonModule{}.BackfillInvoices(Call{Page: page, Notes: &Notes{}},
		[]string{"111-0000001-0000001"}, func(string, *Invoice) { t.Fatal("nothing is handed over") })

	require.Contains(t, stopped, "cannot print")
	require.False(t, signIn)
	require.Empty(t, page.Visited)
}

func TestABackfillWhosePrintFailsStopsThere(t *testing.T) {
	page := invoicePage(true)
	orders := []string{"111-0000002-0000002", "111-0000001-0000001"}

	filed, missed, _, stopped, signIn := backfilled(Call{Page: page, Notes: &Notes{}}, orders)

	require.Empty(t, filed)
	require.Empty(t, missed)
	require.Contains(t, stopped, "could not be printed")
	require.False(t, signIn)
	require.Len(t, page.Visited, 1)
}

func TestACancelledBackfillStopsBetweenOrders(t *testing.T) {
	page := invoicePage(false)
	ctx, cancel := context.WithCancel(context.Background())

	stopped, signIn := amazonModule{}.BackfillInvoices(Call{Ctx: ctx, Page: page, Notes: &Notes{}},
		[]string{"111-0000002-0000002", "111-0000001-0000001"}, func(string, *Invoice) { cancel() })

	require.Contains(t, stopped, "run it again")
	require.False(t, signIn)
	require.Len(t, page.Visited, 1)
}

func TestOnlyACodePromptNamingAnAuthenticatorIsOneAKeptKeyAnswers(t *testing.T) {
	for _, one := range []struct {
		name   string
		module Module
		page   *browser.StubPage
		want   bool
	}{
		{"Amazon's authenticator step", Amazon(), showing("https://www.amazon.com/ap/mfa",
			[]string{"otp", "otpPrompt"},
			map[string]string{"otpPrompt": "Enter the OTP from the Authenticator App"}, ""), true},
		{"Amazon's texted code", Amazon(), showing("https://www.amazon.com/ap/mfa",
			[]string{"otp", "otpPrompt"},
			map[string]string{"otpPrompt": "Enter the code we sent to your phone"}, ""), false},
		{"Amazon's code with no words to go on", Amazon(), showing("https://www.amazon.com/ap/mfa",
			[]string{"otp"}, nil, ""), false},
		{"Costco's authenticator step", Costco(), showing("https://signin.costco.com/verify",
			[]string{"code", "prompt"},
			map[string]string{"prompt": "Enter the code from your authenticator app"}, ""), true},
		{"Costco's emailed code", Costco(), showing("https://signin.costco.com/verify",
			[]string{"code", "prompt"},
			map[string]string{"prompt": "Enter the verification code we emailed you"}, ""), false},
	} {
		t.Run(one.name, func(t *testing.T) {
			where, err := one.module.Classify(one.page)
			require.NoError(t, err)
			require.Equal(t, StateOTP, where.State)
			require.Equal(t, one.want, where.Authenticator())
		})
	}
}
