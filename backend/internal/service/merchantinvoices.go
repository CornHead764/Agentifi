package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The invoice backfill: every stored order with no invoice document, which a
// pull does not reach because it prints only the orders its own window read.
// A person starts it from the account's menu. A merchant whose invoice is its
// own printable page has each order's page reopened, newest first and paced; a
// merchant whose receipt Agentifi lays out has one laid out for every stored
// purchase from the lines on file, with no request to the merchant at all. It
// holds the account's pull claim, so it and a pull never run at once. See
// docs/connectors/merchants.md.

// invoiceBackfillTimeout bounds one backfill. A paced walk through a long
// order history takes a while; one cut short keeps what it filed and the next
// one goes on from there.
const invoiceBackfillTimeout = 4 * time.Hour

// InvoiceBackfillProgress is a running backfill: the orders it set out to
// file, how many it has tried and how many it has filed.
type InvoiceBackfillProgress struct {
	Total int
	Done  int
	Filed int
}

type invoiceBackfill struct {
	mu       sync.Mutex
	progress InvoiceBackfillProgress
}

func (b *invoiceBackfill) step(filed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.progress.Done++
	if filed {
		b.progress.Filed++
	}
}

// invoiceBackfills is which accounts have a backfill running in this process,
// beside the pull claim it holds.
var invoiceBackfills sync.Map

// MerchantBackfillRunning is the progress of a backfill running for this
// account, if one is.
func MerchantBackfillRunning(id uuid.UUID) (InvoiceBackfillProgress, bool) {
	found, ok := invoiceBackfills.Load(id)
	if !ok {
		return InvoiceBackfillProgress{}, false
	}
	run := found.(*invoiceBackfill)
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.progress, true
}

// BackfillRefused is a backfill that cannot start, in words for the person.
type BackfillRefused struct{ Reason string }

func (e BackfillRefused) Error() string { return e.Reason }

// InvoicesWanting counts the account's stored orders a backfill would file an
// invoice for.
func (a *Merchants) InvoicesWanting(ctx context.Context, spaceID store.SpaceID, account store.MerchantAccount) (int, error) {
	if merchantOf(account).Invoices == domain.InvoicesLaidOut {
		return a.store.CountMerchantReceiptsWithoutDocument(ctx, spaceID, account.ID)
	}
	return a.store.CountMerchantOrdersWantingInvoice(ctx, spaceID, account.ID)
}

// StartInvoiceBackfill checks a backfill can run, takes the account's pull
// claim and runs it in the background. ErrPullRunning when a pull or another
// backfill of the account is running here.
func (a *Merchants) StartInvoiceBackfill(ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID) error {
	account, err := a.store.GetMerchantAccount(ctx, spaceID, accountID)
	if err != nil {
		return err
	}
	agent, err := a.agent()
	if err != nil {
		return err
	}
	if !a.filesInvoices() {
		return BackfillRefused{"This server has nowhere to keep documents, so there is nowhere to file an invoice"}
	}
	merchant := merchantOf(account)
	var state json.RawMessage
	if merchant.Invoices == domain.InvoicesFromPage {
		kept, err := a.store.MerchantSession(ctx, spaceID, accountID)
		if errors.Is(err, store.ErrNotFound) {
			return BackfillRefused{fmt.Sprintf("Sign in to %s before backfilling its invoices", merchant.Name)}
		}
		if err != nil {
			return err
		}
		state = json.RawMessage(kept)
	}
	if !merchantPulls.claim(accountID) {
		return ErrPullRunning
	}
	run := &invoiceBackfill{}
	invoiceBackfills.Store(accountID, run)
	merchantPulls.inBackgroundFor(accountID, invoiceBackfillTimeout, func(ctx context.Context) {
		defer invoiceBackfills.Delete(accountID)
		var stopped string
		if merchant.Invoices == domain.InvoicesLaidOut {
			stopped = a.layOutReceipts(ctx, spaceID, account, agent, run)
		} else {
			stopped = a.reopenInvoices(ctx, spaceID, account, agent, state, run)
		}
		left, err := a.InvoicesWanting(ctx, spaceID, account)
		if err != nil {
			a.log().Error("merchant: counting orders without an invoice", "account", accountID, "error", err)
		}
		progress, _ := MerchantBackfillRunning(accountID)
		if err := a.store.RecordMerchantInvoiceBackfill(ctx, spaceID, accountID, progress.Filed, left, stopped); err != nil {
			a.log().Error("merchant: recording an invoice backfill", "account", accountID, "error", err)
		}
		a.log().Info("merchant: invoice backfill finished", "account", accountID, "tried", progress.Done,
			"filed", progress.Filed, "left", left, "stopped", stopped)
	})
	return nil
}

// reopenInvoices walks the orders' own invoice pages and files each invoice
// as it is printed, and says why it stopped early.
func (a *Merchants) reopenInvoices(
	ctx context.Context, spaceID store.SpaceID, account store.MerchantAccount, agent MerchantAgent,
	state json.RawMessage, run *invoiceBackfill,
) string {
	orders, err := a.store.MerchantOrdersWantingInvoice(ctx, spaceID, account.ID)
	if err != nil {
		a.log().Error("merchant: listing orders without an invoice", "account", account.ID, "error", err)
		return "the orders without an invoice could not be listed"
	}
	run.mu.Lock()
	run.progress.Total = len(orders)
	run.mu.Unlock()
	if len(orders) == 0 {
		return ""
	}
	result, err := agent.BackfillInvoices(ctx, account.Merchant, state, orders,
		func(orderNumber string, invoice *provider.MerchantInvoice) {
			if invoice == nil {
				if err := a.store.MarkMerchantInvoicesMissed(ctx, spaceID, account.ID, []string{orderNumber}); err != nil {
					a.log().Error("merchant: counting an invoice not found", "order", orderNumber, "error", err)
				}
				run.step(false)
				return
			}
			filed, _ := a.fileInvoices(ctx, spaceID, account.ID, []provider.MerchantInvoice{*invoice})
			run.step(filed > 0)
		})
	if err != nil {
		a.log().Error("merchant: invoice backfill failed", "account", account.ID, "error", err)
		return err.Error()
	}
	for _, note := range result.Notes {
		a.log().Info("merchant: invoice backfill note", "account", account.ID, "note", note)
	}
	if len(result.StorageState) > 0 {
		if err := a.store.SaveMerchantSession(ctx, spaceID, account.ID, "", string(result.StorageState), false); err != nil {
			a.log().Error("merchant: keeping the session after a backfill", "account", account.ID, "error", err)
		}
	}
	if result.NeedsSignIn {
		return strings.TrimRight(result.Stopped, ". ") + ". Sign in or press Update now, then backfill again to go on"
	}
	return result.Stopped
}

// layOutReceipts files a laid-out receipt for each stored purchase that has
// none, asking the merchant nothing, and says why it stopped early.
func (a *Merchants) layOutReceipts(
	ctx context.Context, spaceID store.SpaceID, account store.MerchantAccount, agent MerchantAgent,
	run *invoiceBackfill,
) string {
	orders, err := a.store.MerchantReceiptsWithoutDocument(ctx, spaceID, account.ID)
	if err != nil {
		a.log().Error("merchant: listing receipts to lay out", "account", account.ID, "error", err)
		return "the purchases on file could not be listed"
	}
	run.mu.Lock()
	run.progress.Total = len(orders)
	run.mu.Unlock()
	if len(orders) == 0 {
		return ""
	}
	numbers := make([]string, len(orders))
	for i, order := range orders {
		numbers[i] = order.OrderNumber
	}
	charges, err := a.store.MerchantChargesForOrders(ctx, spaceID, numbers)
	if err != nil {
		a.log().Error("merchant: reading receipt tenders", "account", account.ID, "error", err)
		return "the purchases on file could not be read"
	}
	for _, order := range orders {
		if err := ctx.Err(); err != nil {
			return fmt.Sprintf("it stopped before the last purchase (%v); run it again to go on", err)
		}
		filename, printed, err := agent.LayOutReceipt(storedReceipt(order, charges))
		if err != nil {
			// One that cannot print is a browser that prints none.
			a.log().Error("merchant: laying out a receipt", "order", order.OrderNumber, "error", err)
			return fmt.Sprintf("the receipts could not be printed: %v", err)
		}
		filed, _ := a.fileInvoices(ctx, spaceID, account.ID, []provider.MerchantInvoice{{
			OrderNumber: order.OrderNumber, Filename: filename, PDF: printed,
		}})
		run.step(filed > 0)
	}
	return ""
}

// storedReceipt is a stored purchase as its receipt shows it: the register's
// own words for each line, and each tender positive.
func storedReceipt(order store.MerchantOrder, charges []store.MerchantCharge) provider.MerchantReceipt {
	out := provider.MerchantReceipt{
		Merchant: order.Merchant, Number: order.OrderNumber, Location: order.Location,
		Date: order.OrderedOn.String(), Return: order.Total.IsNegative(), Total: order.Total.String(),
	}
	if order.HasTax {
		out.Tax = order.Tax.String()
	}
	for _, item := range order.Items {
		line := provider.MerchantReceiptLine{Number: item.SKU, Title: item.Title, Quantity: item.Quantity}
		if item.HasTotalOwed {
			line.Amount = item.TotalOwed.String()
		}
		out.Items = append(out.Items, line)
	}
	for _, charge := range charges {
		if charge.MerchantAccountID != order.MerchantAccountID || charge.OrderNumber != order.OrderNumber {
			continue
		}
		out.Tenders = append(out.Tenders, provider.MerchantReceiptLine{
			Title: charge.Instrument, Amount: charge.Amount.Abs().String(),
		})
	}
	return out
}

// invoiceNote says what a pull filed and how many stored orders are still
// without an invoice, or nothing when there is nothing to say.
func (a *Merchants) invoiceNote(ctx context.Context, spaceID store.SpaceID, account store.MerchantAccount, filed int) string {
	if !a.filesInvoices() {
		return ""
	}
	merchant := merchantOf(account)
	var parts []string
	if filed > 0 {
		parts = append(parts, fmt.Sprintf("%s filed", counted(filed, "invoice", "invoices")))
	}
	wanting, err := a.InvoicesWanting(ctx, spaceID, account)
	if err != nil {
		a.log().Error("merchant: counting orders without an invoice", "account", account.ID, "error", err)
	} else if wanting > 0 {
		parts = append(parts, fmt.Sprintf("%s on file still without one; Backfill invoices files them",
			counted(wanting, merchant.Noun, merchant.Plural)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Invoices: " + strings.Join(parts, "; ")
}

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
