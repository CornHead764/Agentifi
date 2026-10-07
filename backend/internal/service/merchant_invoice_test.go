package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The invoices a pull fetches, kept as their orders' documents and filed on
// the rows matched to those orders. Every order and figure is invented.

const invoicedOrder = "111-0000000-7171717"

func invoicingPull(t *testing.T) (*fakeMerchantAgent, *Merchants, *store.Store, store.SpaceID, store.MerchantAccount) {
	t.Helper()
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
	merchants, st, space, account := merchantsWithAgent(t, agent)
	merchants.Documents = NewDocuments(st, &provider.LocalStorage{BasePath: t.TempDir()})
	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept", "", "")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))
	agent.mu.Lock()
	agent.orders = []merchantimport.Order{{
		Number: invoicedOrder, OrderedOn: domain.DateOf(time.Now()).AddDays(-3),
		Total: domain.MustFromString("42.00"), Currency: "USD",
		Items: []merchantimport.Item{{Title: "Invented Widget", Quantity: 1}},
	}}
	agent.invoices = []provider.MerchantInvoice{{
		OrderNumber: invoicedOrder, Filename: "amazon-invoice-" + invoicedOrder + ".pdf",
		PDF: []byte("%PDF-1.4 an invented invoice"),
	}}
	agent.invoiced = nil
	agent.mu.Unlock()
	return agent, merchants, st, space, account
}

func TestAPulledInvoiceIsKeptOnItsOrderAndFiledOnTheMatchedRow(t *testing.T) {
	agent, merchants, st, space, account := invoicingPull(t)

	report, err := merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Equal(t, []string{"Invoices: 1 invoice filed"}, report.Warnings,
		"an order with no page to open is not counted as still to fetch")
	require.Empty(t, agent.invoiced[0], "nothing was on file before the first pull")

	order, err := st.GetMerchantOrderByNumber(t.Context(), space, account.ID, invoicedOrder)
	require.NoError(t, err)
	docs, err := st.ListDocumentsByLink(t.Context(), space, store.DocumentLinkMerchantOrder, order.ID)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "amazon-invoice-"+invoicedOrder+".pdf", docs[0].Filename)
	require.Equal(t, store.DocumentSourceMerchantPull, docs[0].Source)

	bank := newAccount(t, space, "Rewards Card")
	on := domain.DateOf(time.Now()).AddDays(-2)
	row := &store.Transaction{
		AccountID: bank.ID, Date: on, EffectiveDate: on, Amount: domain.MustFromString("-42.00"),
		Currency: "USD", StatementName: "AMZN MKTP US", Payee: "Amazon", Source: domain.SourceSync,
	}
	require.NoError(t, st.CreateTransaction(t.Context(), space, row))
	require.NoError(t, merchants.AddMatchByHand(t.Context(), space, row.ID, order.ID))

	behind, err := st.DocumentsBehindTransaction(t.Context(), space, row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, docs[0].ID, behind[0].Document.ID)
	require.Equal(t, store.DocumentLinkReceipt, behind[0].Via)
	require.Equal(t, invoicedOrder, behind[0].Receipt.OrderNumber)

	// The next pull is told the invoice is on file, and filing the same
	// invoice again keeps one document.
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Equal(t, []string{invoicedOrder}, agent.invoiced[1])
	again, err := st.ListDocumentsByLink(t.Context(), space, store.DocumentLinkMerchantOrder, order.ID)
	require.NoError(t, err)
	require.Len(t, again, 1)

	require.NoError(t, merchants.RemoveMatch(t.Context(), space, row.ID, order.ID))
	behind, err = st.DocumentsBehindTransaction(t.Context(), space, row.ID)
	require.NoError(t, err)
	require.Empty(t, behind, "unmatching takes the invoice off the row")
	kept, err := st.ListDocumentsByLink(t.Context(), space, store.DocumentLinkMerchantOrder, order.ID)
	require.NoError(t, err)
	require.Len(t, kept, 1, "and leaves it on its order")
}

func TestAnInvoiceForAnOrderThePullDidNotBringIsNotFiled(t *testing.T) {
	agent, merchants, st, space, account := invoicingPull(t)
	agent.mu.Lock()
	agent.invoices[0].OrderNumber = "111-0000000-0000001"
	agent.mu.Unlock()

	report, err := merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Empty(t, report.Warnings)
	order, err := st.GetMerchantOrderByNumber(t.Context(), space, account.ID, invoicedOrder)
	require.NoError(t, err)
	docs, err := st.ListDocumentsByLink(t.Context(), space, store.DocumentLinkMerchantOrder, order.ID)
	require.NoError(t, err)
	require.Empty(t, docs)
}

func TestWithNowhereToKeepAnInvoiceThePullAsksForNone(t *testing.T) {
	agent, merchants, _, space, account := invoicingPull(t)
	merchants.Documents = nil

	_, err := merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Equal(t, agent.skipped[1], agent.invoiced[1],
		"no order is opened again only for an invoice it has nowhere to keep")
}

const (
	olderOrder  = "111-0000000-8080808"
	oldestOrder = "111-0000000-9090909"
)

// withOlderOrders is an invoicing pull whose history holds two older orders
// with invoice pages, one with no page and one cancelled, all read by a first
// pull.
func withOlderOrders(t *testing.T) (*fakeMerchantAgent, *Merchants, *store.Store, store.SpaceID, store.MerchantAccount) {
	t.Helper()
	agent, merchants, st, space, account := invoicingPull(t)
	today := domain.DateOf(time.Now())
	older := func(number string, days int, url, status string) merchantimport.Order {
		return merchantimport.Order{
			Number: number, OrderedOn: today.AddDays(-days), Total: domain.MustFromString("19.00"),
			Currency: "USD", Status: status, URL: url,
			Items: []merchantimport.Item{{Title: "Invented Gadget", Quantity: 1}},
		}
	}
	agent.mu.Lock()
	agent.orders[0].URL = "https://www.amazon.com/gp/your-account/order-details?orderID=" + invoicedOrder
	agent.orders = append(agent.orders,
		older(olderOrder, 80, "https://www.amazon.com/gp/your-account/order-details?orderID="+olderOrder, ""),
		older(oldestOrder, 90, "https://www.amazon.com/gp/your-account/order-details?orderID="+oldestOrder, ""),
		older("111-0000000-1010101", 100, "", ""),
		older("111-0000000-8585858", 85, "https://www.amazon.com/gp/your-account/order-details?orderID=111-0000000-8585858", "Cancelled"),
	)
	agent.mu.Unlock()
	report, err := merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Equal(t, []string{
		"Invoices: 1 invoice filed; 2 orders on file still without one; Backfill invoices files them",
	}, report.Warnings)
	agent.mu.Lock()
	agent.invoices = nil
	agent.mu.Unlock()
	return agent, merchants, st, space, account
}

// backfilled starts a backfill and waits for it and its claim to be done.
func backfilled(t *testing.T, merchants *Merchants, space store.SpaceID, account store.MerchantAccount) store.MerchantAccount {
	t.Helper()
	require.NoError(t, merchants.StartInvoiceBackfill(t.Context(), space, account.ID))
	backfillDone(t, account)
	return accountNow(t, merchants.store, space, account)
}

func backfillDone(t *testing.T, account store.MerchantAccount) {
	t.Helper()
	require.Eventually(t, func() bool { return !merchantPulls.held(account.ID) }, 5*time.Second, 5*time.Millisecond)
}

func TestARegularPullReopensNoOlderOrder(t *testing.T) {
	agent, merchants, _, space, account := withOlderOrders(t)

	for range 2 {
		report, err := merchants.Pull(t.Context(), space, account.ID, 30)
		require.NoError(t, err)
		require.Equal(t, []string{
			"Invoices: 2 orders on file still without one; Backfill invoices files them",
		}, report.Warnings)
	}
	require.Empty(t, agent.backfills(), "only a backfill reopens an order the pull did not read")
}

func TestABackfillFilesEveryOlderInvoiceNewestFirstAndLetsAnEmptyPageGoAfterThree(t *testing.T) {
	agent, merchants, st, space, account := withOlderOrders(t)
	agent.mu.Lock()
	agent.blank = map[string]bool{oldestOrder: true}
	agent.mu.Unlock()

	wanting, err := merchants.InvoicesWanting(t.Context(), space, account)
	require.NoError(t, err)
	require.Equal(t, 2, wanting, "none without a page to open and none cancelled")

	order, err := st.GetMerchantOrderByNumber(t.Context(), space, account.ID, olderOrder)
	require.NoError(t, err)
	bank := newAccount(t, space, "Rewards Card")
	on := domain.DateOf(time.Now()).AddDays(-79)
	row := &store.Transaction{
		AccountID: bank.ID, Date: on, EffectiveDate: on, Amount: domain.MustFromString("-19.00"),
		Currency: "USD", StatementName: "AMZN MKTP US", Payee: "Amazon", Source: domain.SourceSync,
	}
	require.NoError(t, st.CreateTransaction(t.Context(), space, row))
	require.NoError(t, merchants.AddMatchByHand(t.Context(), space, row.ID, order.ID))

	done := backfilled(t, merchants, space, account)
	require.Equal(t, [][]string{{olderOrder, oldestOrder}}, agent.backfills())
	require.NotNil(t, done.InvoiceBackfillAt)
	require.Equal(t, 1, done.InvoiceBackfillFiled)
	require.Equal(t, 1, done.InvoiceBackfillLeft)
	require.Empty(t, done.InvoiceBackfillStopped)

	behind, err := st.DocumentsBehindTransaction(t.Context(), space, row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1, "the backfilled invoice is filed on the row already matched to its order")
	require.Equal(t, store.DocumentLinkReceipt, behind[0].Via)
	require.Equal(t, "amazon-invoice-"+olderOrder+".pdf", behind[0].Document.Filename)
	kept, err := st.MerchantSession(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Contains(t, kept, "backfilled", "the session it ended with is kept")

	for range 2 {
		done = backfilled(t, merchants, space, account)
	}
	require.Equal(t, [][]string{{olderOrder, oldestOrder}, {oldestOrder}, {oldestOrder}}, agent.backfills())
	require.Equal(t, 0, done.InvoiceBackfillLeft, "three empty pages and the order is let go")

	done = backfilled(t, merchants, space, account)
	require.Len(t, agent.backfills(), 3, "with nothing left the merchant is not asked")
	require.Equal(t, 0, done.InvoiceBackfillFiled)
}

func TestABackfillThatMeetsASignInStopsAndSaysSo(t *testing.T) {
	agent, merchants, st, space, account := withOlderOrders(t)
	agent.mu.Lock()
	agent.stop, agent.stopAt = "Amazon asked to sign in again", 1
	agent.mu.Unlock()

	done := backfilled(t, merchants, space, account)
	require.Equal(t, 1, done.InvoiceBackfillFiled)
	require.Equal(t, 1, done.InvoiceBackfillLeft)
	require.Equal(t,
		"Amazon asked to sign in again. Sign in or press Update now, then backfill again to go on",
		done.InvoiceBackfillStopped)
	kept, err := st.MerchantSession(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.NotContains(t, kept, "backfilled", "a session the merchant refused is not kept over the last good one")

	// Run again, it goes on from the order it stopped at, which has not
	// missed: its empty page is still tried three more times.
	agent.mu.Lock()
	agent.stop, agent.blank = "", map[string]bool{oldestOrder: true}
	agent.mu.Unlock()
	for range 3 {
		done = backfilled(t, merchants, space, account)
	}
	require.Equal(t, [][]string{{olderOrder, oldestOrder}, {oldestOrder}, {oldestOrder}, {oldestOrder}},
		agent.backfills())
	require.Equal(t, 0, done.InvoiceBackfillLeft)
	require.Empty(t, done.InvoiceBackfillStopped)
}

func TestABackfillHoldsTheAccountAgainstAPullAndASecondBackfill(t *testing.T) {
	agent, merchants, _, space, account := withOlderOrders(t)
	gate := make(chan struct{})
	agent.mu.Lock()
	agent.backfillGate = gate
	agent.mu.Unlock()

	require.NoError(t, merchants.StartInvoiceBackfill(t.Context(), space, account.ID))
	_, running := MerchantBackfillRunning(account.ID)
	require.True(t, running)
	require.False(t, MerchantPullRunning(account.ID), "a backfill is not a pull")

	_, err := merchants.Pull(t.Context(), space, account.ID, 30)
	require.ErrorIs(t, err, ErrPullRunning)
	require.ErrorIs(t, merchants.StartInvoiceBackfill(t.Context(), space, account.ID), ErrPullRunning)

	close(gate)
	backfillDone(t, account)
	_, running = MerchantBackfillRunning(account.ID)
	require.False(t, running)
	_, err = merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Len(t, agent.backfills(), 1)
}

func TestABackfillIsRefusedWithNowhereToFileOrNoSession(t *testing.T) {
	agent, merchants, st, space, account := withOlderOrders(t)
	docs := merchants.Documents
	merchants.Documents = nil
	var refused BackfillRefused
	require.ErrorAs(t, merchants.StartInvoiceBackfill(t.Context(), space, account.ID), &refused)
	merchants.Documents = docs

	fresh := store.MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Sam"}
	require.NoError(t, st.CreateMerchantAccount(t.Context(), space, &fresh))
	require.ErrorAs(t, merchants.StartInvoiceBackfill(t.Context(), space, fresh.ID), &refused)
	require.Equal(t, "Sign in to Amazon before backfilling its invoices", refused.Reason)
	require.Empty(t, agent.backfills())
}

// costcoLayingOut is a signed-in Costco account holding stored purchases and
// a place to keep their receipts.
func costcoLayingOut(t *testing.T) (*fakeMerchantAgent, *Merchants, *store.Store, store.SpaceID, store.MerchantAccount) {
	t.Helper()
	agent := &fakeMerchantAgent{signIn: provider.MerchantSignInSignedIn}
	merchants, st, space, _ := merchantsWithAgent(t, agent)
	merchants.Documents = NewDocuments(st, &provider.LocalStorage{BasePath: t.TempDir()})
	account := store.MerchantAccount{Merchant: domain.MerchantCostco, Label: "Alex"}
	require.NoError(t, st.CreateMerchantAccount(t.Context(), space, &account))
	state, err := merchants.StartSignIn(t.Context(), space, account.ID, "alex@example.com", "kept", "", "")
	require.NoError(t, err)
	require.NoError(t, merchants.CompleteSignIn(t.Context(), space, account.ID, state.SessionID, ""))

	today := domain.DateOf(time.Now())
	purchase := func(number, kind string, days int, total string, items ...merchantimport.Item) merchantimport.Order {
		return merchantimport.Order{
			Number: number, OrderedOn: today.AddDays(-days), Kind: kind, Location: "Invented Warehouse",
			Total: domain.MustFromString(total), Currency: "USD", Items: items,
		}
	}
	older := purchase(costcoOlder, domain.PurchaseWarehouse, 90, "25.50",
		merchantimport.Item{SKU: "1000001", Title: "INVENTED BREAD", Quantity: 2,
			TotalOwed: domain.MustFromString("24.00"), HasTotalOwed: true})
	older.Tax, older.HasTax = domain.MustFromString("1.50"), true
	_, err = merchants.Import(t.Context(), space, account.ID, merchantimport.Parsed{
		Orders: []merchantimport.Order{
			older,
			purchase(costcoFuel, domain.PurchaseFuel, 10, "40.00",
				merchantimport.Item{SKU: "2000002", Title: "REGULAR", Quantity: 1,
					TotalOwed: domain.MustFromString("40.00"), HasTotalOwed: true}),
			purchase("21100000000000000103", domain.PurchaseWarehouse, 20, "12.00",
				merchantimport.Item{SKU: "3000003", Quantity: 1}),
			purchase("1200000104", domain.PurchaseOnline, 30, "60.00",
				merchantimport.Item{Title: "INVENTED CHAIR", Quantity: 1}),
		},
		Charges: []merchantimport.Charge{{
			OrderNumber: costcoOlder, ChargedOn: today.AddDays(-90),
			Amount: domain.MustFromString("25.50"), Instrument: "VISA ••••5678",
		}},
	}, false)
	require.NoError(t, err)
	require.NoError(t, st.MarkMerchantSync(t.Context(), space, account.ID, store.MerchantSyncOK, "", nil))
	return agent, merchants, st, space, account
}

const (
	costcoOlder = "21100000000000000101"
	costcoFuel  = "21100000000000000102"
)

func TestARegularCostcoPullLaysOutNoReceiptItDidNotRead(t *testing.T) {
	agent, merchants, _, space, account := costcoLayingOut(t)

	report, err := merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Empty(t, agent.laidOut)
	require.Equal(t, []string{
		"Invoices: 2 purchases on file still without one; Backfill invoices files them",
	}, report.Warnings)
}

func TestACostcoBackfillLaysOutEveryStoredReceiptAndAsksCostcoNothing(t *testing.T) {
	agent, merchants, st, space, account := costcoLayingOut(t)
	wanting, err := merchants.InvoicesWanting(t.Context(), space, account)
	require.NoError(t, err)
	require.Equal(t, 2, wanting)

	done := backfilled(t, merchants, space, account)
	require.Equal(t, 2, done.InvoiceBackfillFiled)
	require.Equal(t, 0, done.InvoiceBackfillLeft)
	require.Empty(t, done.InvoiceBackfillStopped)
	require.Empty(t, agent.days, "no pull was made")
	require.Empty(t, agent.backfills(), "and no page reopened")

	// Newest first; the receipt listed by item number alone and the online
	// order have nothing to lay out.
	require.Len(t, agent.laidOut, 2)
	require.Equal(t, costcoFuel, agent.laidOut[0].Number)
	require.Equal(t, provider.MerchantReceipt{
		Merchant: domain.MerchantCostco, Number: costcoOlder, Location: "Invented Warehouse",
		Date: domain.DateOf(time.Now()).AddDays(-90).String(), Tax: "1.50", Total: "25.50",
		Items:   []provider.MerchantReceiptLine{{Number: "1000001", Title: "INVENTED BREAD", Quantity: 2, Amount: "24.00"}},
		Tenders: []provider.MerchantReceiptLine{{Title: "VISA ••••5678", Amount: "25.50"}},
	}, agent.laidOut[1])

	order, err := st.GetMerchantOrderByNumber(t.Context(), space, account.ID, costcoOlder)
	require.NoError(t, err)
	docs, err := st.ListDocumentsByLink(t.Context(), space, store.DocumentLinkMerchantOrder, order.ID)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "costco-receipt-"+costcoOlder+".pdf", docs[0].Filename)

	bank := newAccount(t, space, "Rewards Card")
	on := domain.DateOf(time.Now()).AddDays(-89)
	row := &store.Transaction{
		AccountID: bank.ID, Date: on, EffectiveDate: on, Amount: domain.MustFromString("-25.50"),
		Currency: "USD", StatementName: "COSTCO WHSE #0000", Payee: "Costco", Source: domain.SourceSync,
	}
	require.NoError(t, st.CreateTransaction(t.Context(), space, row))
	require.NoError(t, merchants.AddMatchByHand(t.Context(), space, row.ID, order.ID))
	behind, err := st.DocumentsBehindTransaction(t.Context(), space, row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, docs[0].ID, behind[0].Document.ID)

	report, err := merchants.Pull(t.Context(), space, account.ID, 30)
	require.NoError(t, err)
	require.Equal(t, []string{costcoFuel}, agent.invoiced[0],
		"the pull is told the receipt in its window is on file, so it does not lay it out again")
	require.Empty(t, report.Warnings)
	backfilled(t, merchants, space, account)
	require.Len(t, agent.laidOut, 2, "a receipt on file is not laid out again")
}

func TestACostcoBackfillThatCannotPrintStopsAndSaysSo(t *testing.T) {
	agent, merchants, _, space, account := costcoLayingOut(t)
	agent.mu.Lock()
	agent.layOutFails = true
	agent.mu.Unlock()

	done := backfilled(t, merchants, space, account)
	require.Equal(t, 0, done.InvoiceBackfillFiled)
	require.Equal(t, 2, done.InvoiceBackfillLeft)
	require.Equal(t, "the receipts could not be printed: this browser cannot print", done.InvoiceBackfillStopped)
}
