package service

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The merchant connectors (Amazon, Costco) as one engine: files or a browser
// pull in, orders stored, bank rows matched, the match divided by what was
// bought. Neither merchant has an API for a person's own purchases; see
// docs/connectors/merchants.md. Which merchant a row belongs to is its wording
// (domain.MerchantsFor): a Costco row is never offered an Amazon order.

type Merchants struct {
	base
	// Agent is the browser this process drives to sign in and pull. Nil means
	// files only.
	Agent MerchantAgent
	// Catalog looks up item numbers for merchants that have one
	// (domain.Merchant.HasCatalog). Nil means a receipt's own words stand.
	Catalog provider.MerchantCatalog
	// Automations, when set, is told about a row matched after it arrived, so
	// automations that read the match look again. A sync's own rows are not
	// reported here: the sync queues them itself, after the match.
	Automations *Automations
	// Documents keeps the invoices a pull fetches. Nil, or a deployment with
	// no file storage, keeps none.
	Documents *Documents
	// Alerts delivers the sign-in alert. Nil raises none.
	Alerts *Alerts
	// MailedCode answers a merchant sign-in waiting on a code from the
	// household's mailbox. Nil is no mailbox.
	MailedCode func(ctx context.Context, spaceID store.SpaceID, merchant domain.MerchantID, since time.Time) (string, bool)
	HasMailbox func(ctx context.Context, spaceID store.SpaceID) bool
	Log        *slog.Logger
}

func NewMerchants(st *store.Store) *Merchants { return &Merchants{base: newBase(st)} }

func (a *Merchants) log() *slog.Logger {
	if a.Log == nil {
		return slog.Default()
	}
	return a.Log
}

type MerchantImportReport struct {
	Format      string
	AccountHint string
	// New* count what the account did not already have.
	Orders     int
	NewOrders  int
	Items      int
	Charges    int
	NewCharges int
	Refunds    int
	NewRefunds int
	Matched    int
	Warnings   []string
	// GiftCardRows is how many gift card activity lines were new to the ledger.
	GiftCardRows int
}

// Import files a parsed file under a merchant account. dryRun writes nothing.
func (a *Merchants) Import(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, parsed merchantimport.Parsed,
	dryRun bool,
) (MerchantImportReport, error) {
	account, err := a.store.GetMerchantAccount(ctx, spaceID, accountID)
	if err != nil {
		return MerchantImportReport{}, err
	}
	report := MerchantImportReport{
		Format: parsed.Format, AccountHint: parsed.AccountHint, Orders: len(parsed.Orders),
		Charges: len(parsed.Charges), Refunds: len(parsed.Refunds), Warnings: parsed.Warnings,
	}
	numbers := make([]string, 0, len(parsed.Orders))
	for _, order := range parsed.Orders {
		report.Items += len(order.Items)
		numbers = append(numbers, order.Number)
	}
	existing, err := a.store.ExistingMerchantOrderNumbers(ctx, spaceID, accountID, numbers)
	if err != nil {
		return MerchantImportReport{}, err
	}
	for _, number := range numbers {
		if !existing[number] {
			report.NewOrders++
		}
	}
	if dryRun {
		// Charges and refunds have no cheap "is it new" short of writing them.
		report.NewCharges, report.NewRefunds = report.Charges, report.Refunds
		return report, nil
	}

	report.NewOrders = 0
	for _, order := range parsed.Orders {
		row := store.MerchantOrder{
			Merchant: account.Merchant, MerchantAccountID: accountID, OrderNumber: order.Number,
			OrderedOn: order.OrderedOn, Kind: order.Kind, Location: order.Location,
			Total: order.Total, Currency: order.Currency, Status: order.Status,
			DetailsURL: order.URL, Source: parsed.Format,
			GiftCard: order.GiftCard, HasGiftCard: order.HasGiftCard,
			Tax: order.Tax, HasTax: order.HasTax, Shipping: order.Shipping, HasShipping: order.HasShipping,
		}
		for _, item := range order.Items {
			row.Items = append(row.Items, store.MerchantOrderItem{
				SKU: item.SKU, Title: item.Title, Quantity: item.Quantity,
				UnitPrice: item.UnitPrice, HasUnitPrice: item.HasUnitPrice,
				TotalOwed: item.TotalOwed, HasTotalOwed: item.HasTotalOwed,
				ShippedOn: item.ShippedOn, Condition: item.Condition, URL: item.URL,
			})
		}
		inserted, err := a.store.UpsertMerchantOrder(ctx, spaceID, &row)
		if err != nil {
			return MerchantImportReport{}, fmt.Errorf("order %s: %w", order.Number, err)
		}
		if inserted {
			report.NewOrders++
		}
	}
	for _, charge := range parsed.Charges {
		row := store.MerchantCharge{
			Merchant: account.Merchant, MerchantAccountID: accountID, OrderNumber: charge.OrderNumber,
			ChargedOn: charge.ChargedOn, Amount: charge.Amount, Instrument: charge.Instrument,
		}
		inserted, err := a.store.UpsertMerchantCharge(ctx, spaceID, &row)
		if err != nil {
			return MerchantImportReport{}, err
		}
		if inserted {
			report.NewCharges++
		}
	}
	// A credit on the payments page is a refund too, and for a merchant whose
	// returns page cannot be read it is the only one. It knows the order but
	// not the line, so it is filed with no item; the matcher prefers the
	// returns page's record when one arrives. A blank payment method here is
	// the gift card balance.
	refunds := make([]merchantimport.Refund, 0, len(parsed.Refunds))
	refunds = append(refunds, parsed.Refunds...)
	told := map[string]bool{}
	for _, refund := range parsed.Refunds {
		told[refund.OrderNumber+"/"+refund.Amount.Abs().String()] = true
	}
	for _, charge := range parsed.Charges {
		if !charge.Amount.IsPositive() || told[charge.OrderNumber+"/"+charge.Amount.String()] {
			continue
		}
		refunds = append(refunds, merchantimport.Refund{
			OrderNumber: charge.OrderNumber, Quantity: 1, RefundedOn: charge.ChargedOn,
			Amount: charge.Amount, Instrument: charge.Instrument,
			ToGiftCard: domain.IsGiftCardInstrument(charge.Instrument),
		})
	}
	report.Refunds = len(refunds)
	for _, refund := range refunds {
		row := store.MerchantRefund{
			Merchant: account.Merchant, MerchantAccountID: accountID, OrderNumber: refund.OrderNumber,
			SKU: refund.SKU, Title: refund.Title, Quantity: refund.Quantity,
			RefundedOn: refund.RefundedOn, Amount: refund.Amount, Instrument: refund.Instrument,
			Destination: store.MerchantRefundToCard, Status: refund.Status, Source: parsed.Format,
		}
		if refund.ToGiftCard {
			row.Destination = store.MerchantRefundToGiftCard
		}
		inserted, err := a.store.UpsertMerchantRefund(ctx, spaceID, &row)
		if err != nil {
			return MerchantImportReport{}, fmt.Errorf("refund for order %s: %w", refund.OrderNumber, err)
		}
		if inserted {
			report.NewRefunds++
		}
	}
	for _, refunded := range parsed.RefundTotals {
		err := a.store.SetMerchantOrderRefundTotal(ctx, spaceID, accountID, refunded.OrderNumber,
			refunded.Amount, refunded.ReadOn)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return MerchantImportReport{}, fmt.Errorf("refund total for order %s: %w", refunded.OrderNumber, err)
		}
	}
	if parsed.GiftCard != nil && merchantOf(account).HasGiftCardBalance {
		rows, matched, err := a.importGiftCard(ctx, spaceID, accountID, *parsed.GiftCard)
		if err != nil {
			return MerchantImportReport{}, err
		}
		report.GiftCardRows, report.Matched = rows, matched
	}
	matched, err := a.MatchUnmatched(ctx, spaceID, account.Merchant)
	if err != nil {
		return MerchantImportReport{}, err
	}
	report.Matched += matched
	a.enrichSoon(account.Merchant)
	return report, nil
}

// merchantOf reads an account whose merchant the catalog does not know as
// Amazon's rather than as nobody's.
func merchantOf(account store.MerchantAccount) domain.Merchant {
	if m, ok := domain.MerchantByID(account.Merchant); ok {
		return m
	}
	return domain.Merchants[0]
}

// importGiftCard keeps the gift card balance as an account of the
// household's, created on the first balance read, with each activity line a
// row deduplicated by date, amount and wording. The rows say "Amazon", so the
// matcher offers them the orders like any other Amazon row.
func (a *Merchants) importGiftCard(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, card merchantimport.GiftCard,
) (rows, matched int, err error) {
	merchantAccount, err := a.store.GetMerchantAccount(ctx, spaceID, accountID)
	if err != nil {
		return 0, 0, err
	}
	now := time.Now().UTC()
	var account store.Account
	if merchantAccount.GiftCardAccountID != uuid.Nil {
		if account, err = a.store.GetAccount(ctx, spaceID, merchantAccount.GiftCardAccountID); err != nil {
			return 0, 0, err
		}
	} else {
		account = store.Account{
			Name: "Amazon gift card · " + merchantAccount.Name(), Kind: domain.KindCash, Type: "gift_card",
			Currency: "USD", IncludeInNetWorth: true,
		}
		if err := a.store.CreateAccount(ctx, spaceID, &account); err != nil {
			return 0, 0, err
		}
	}
	if card.HasBalance {
		account.ProviderBalance, account.HasProviderBalance, account.ProviderBalanceAt = card.Balance, true, &now
		if err := a.store.UpdateAccount(ctx, spaceID, &account); err != nil {
			return 0, 0, err
		}
	}
	if err := a.store.SetMerchantGiftCard(ctx, spaceID, accountID, account.ID, card.Balance, card.HasBalance, now); err != nil {
		return 0, 0, err
	}

	existing, err := a.store.ListTransactions(ctx, spaceID, store.TransactionQuery{
		AccountIDs: []uuid.UUID{account.ID}, IncludeDeleted: true, IncludeEstimates: true,
	})
	if err != nil {
		return 0, 0, err
	}
	seen := map[string]bool{}
	for _, row := range existing {
		if row.ExternalID != "" {
			seen[row.ExternalID] = true
		}
	}
	var added []uuid.UUID
	for _, line := range card.Activity {
		if line.Amount.IsZero() {
			continue
		}
		key := giftCardLineID(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		statement := line.Description
		if statement == "" {
			statement = "Amazon gift card balance"
		}
		txn := store.Transaction{
			AccountID: account.ID, ExternalID: key, Date: line.On, Amount: line.Amount,
			Currency: account.Currency, StatementName: statement, Payee: giftCardPayee(line),
			Source: domain.SourceSync, IsReviewed: account.Kind.BornReviewed(),
			NeedsSettle: true,
		}
		if err := a.store.CreateTransaction(ctx, spaceID, &txn); err != nil {
			return len(added), 0, err
		}
		added = append(added, txn.ID)
	}
	ingest := Ingest{Merchants: a, Automations: a.Automations}
	ingested, err := ingest.AfterIngest(ctx, a.store, spaceID, added)
	if err != nil {
		return len(added), 0, err
	}
	return len(added), ingested.Matched, nil
}

// giftCardLineID stands in for an aggregator id the page does not give, so the
// same line read tomorrow is the same row.
func giftCardLineID(line merchantimport.GiftCardActivity) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(line.Description))))
	return fmt.Sprintf("amazon-gc:%s:%s:%x", line.On, line.Amount, sum[:6])
}

// giftCardPayee: an order paid from the balance is Amazon's; money arriving on
// the balance is the gift card itself.
func giftCardPayee(line merchantimport.GiftCardActivity) string {
	if line.Amount.IsNegative() || line.OrderNumber != "" {
		return "Amazon"
	}
	return "Amazon gift card"
}

// MatchUnmatched offers every unmatched row worded as the merchant's the
// orders on file, and reports how many found one.
func (a *Merchants) MatchUnmatched(ctx context.Context, spaceID store.SpaceID, merchant domain.MerchantID) (int, error) {
	rows, err := a.store.ListUnmatchedMerchantTransactions(ctx, spaceID, merchant, 0)
	if err != nil {
		return 0, err
	}
	var matched []uuid.UUID
	for _, row := range rows {
		ok, err := a.matchOne(ctx, spaceID, row)
		if err != nil {
			return len(matched), err
		}
		if ok {
			matched = append(matched, row.ID)
		}
	}
	return len(matched), a.lookAgain(ctx, spaceID, matched)
}

func (a *Merchants) lookAgain(ctx context.Context, spaceID store.SpaceID, ids []uuid.UUID) error {
	if a.Automations == nil || len(ids) == 0 {
		return nil
	}
	_, err := a.Automations.EnqueueForRows(ctx, spaceID, ids, domain.AutomationFiredByMatch, false)
	return err
}

// MatchTransactions offers the rows a sync just wrote to the orders on file.
func (a *Merchants) MatchTransactions(ctx context.Context, spaceID store.SpaceID, ids []uuid.UUID) (int, error) {
	matched := 0
	for _, id := range ids {
		txn, err := a.store.GetTransaction(ctx, spaceID, id)
		if err != nil {
			continue
		}
		if len(domain.MerchantsFor(txn.StatementName, txn.Payee)) == 0 {
			continue
		}
		// A forecast or deleted row is not a purchase: the same rule
		// merchantMatchable applies in SQL.
		if !store.CountsTowardBalance(txn) {
			continue
		}
		if _, err := a.store.GetMerchantMatch(ctx, spaceID, id); err == nil {
			continue
		}
		ok, err := a.matchOne(ctx, spaceID, txn)
		if err != nil {
			return matched, err
		}
		if ok {
			matched++
		}
	}
	return matched, nil
}

// matchOne offers one row to each merchant its wording names, in display
// order, until one explains it. A row on an Amazon gift card balance is
// Amazon's whatever it says.
func (a *Merchants) matchOne(ctx context.Context, spaceID store.SpaceID, txn store.Transaction) (bool, error) {
	// The Go side of store.NotInIgnoredAccountOn, which the queries apply.
	account, err := a.store.GetAccount(ctx, spaceID, txn.AccountID)
	if err != nil {
		return false, err
	}
	if account.IsIgnored() {
		return false, nil
	}
	// A gift card balance row is money the bank never saw: it matches the gift
	// card's charges and the gift card's share of an order, never the card's.
	giftCard, err := a.store.IsMerchantGiftCardAccount(ctx, spaceID, txn.AccountID)
	if err != nil {
		return false, err
	}
	merchants := domain.MerchantsFor(txn.StatementName, txn.Payee)
	if giftCard {
		merchants = []domain.Merchant{domain.Merchants[0]}
	}
	for _, merchant := range merchants {
		ok, err := a.matchWith(ctx, spaceID, txn, merchant.ID, giftCard)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// matchWith runs the pure match for one row against one merchant's orders
// and charges in its window, and records it. A match to a whole order with
// several priced items is split one per item.
func (a *Merchants) matchWith(
	ctx context.Context, spaceID store.SpaceID, txn store.Transaction, merchant domain.MerchantID,
	giftCard bool,
) (bool, error) {
	// A credit is asked first which return it gives back, since only a return
	// says which item came back.
	matched, err := a.matchRefund(ctx, spaceID, txn, merchant, giftCard)
	if err != nil || matched {
		return matched, err
	}
	from, to := domain.MerchantOrderSpan(txn.Date)
	orders, err := a.store.MerchantOrdersBetween(ctx, spaceID, merchant, from, to)
	if err != nil {
		return false, err
	}
	from, to = domain.MerchantChargeSpan(txn.Date)
	charges, err := a.store.MerchantChargesBetween(ctx, spaceID, merchant, from, to)
	if err != nil {
		return false, err
	}
	if len(orders) == 0 && len(charges) == 0 {
		return false, nil
	}
	byNumber := map[string]uuid.UUID{}
	byID := map[uuid.UUID]store.MerchantOrder{}
	for _, order := range orders {
		byNumber[order.OrderNumber] = order.ID
		byID[order.ID] = order
	}
	// A charge for an order older than the window (a back-ordered item): look
	// it up by number so the charge can still match and split.
	for _, charge := range charges {
		if _, known := byNumber[charge.OrderNumber]; known {
			continue
		}
		candidate, err := a.store.GetMerchantOrderByNumber(ctx, spaceID, charge.MerchantAccountID, charge.OrderNumber)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if candidate.Ignored() {
			continue
		}
		byNumber[candidate.OrderNumber] = candidate.ID
		byID[candidate.ID] = candidate
		orders = append(orders, candidate)
	}

	facts, _, err := a.orderFacts(ctx, spaceID, orders, giftCard)
	if err != nil {
		return false, err
	}
	ids := make([]uuid.UUID, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, order.ID)
	}
	chargeFacts := make([]domain.MerchantChargeFacts, 0, len(charges))
	for _, charge := range charges {
		ref, known := byNumber[charge.OrderNumber]
		if !known || domain.IsGiftCardInstrument(charge.Instrument) != giftCard {
			continue
		}
		chargeFacts = append(chargeFacts, domain.MerchantChargeFacts{
			OrderRef: ref.String(), ChargedOn: charge.ChargedOn, Amount: charge.Amount,
		})
	}
	existing, err := a.store.MerchantMatchesForOrders(ctx, spaceID, ids)
	if err != nil {
		return false, err
	}
	taken := make([]domain.MerchantMatch, 0, len(existing))
	for _, m := range existing {
		if m.TransactionID == txn.ID {
			continue
		}
		taken = append(taken, domain.MerchantMatch{OrderRef: m.OrderID.String(), Amount: m.Amount, Basis: m.Basis})
	}
	match, ok := domain.MatchMerchantOrder(txn.Amount, txn.Date, facts, chargeFacts, taken)
	if !ok {
		return false, nil
	}
	orderID, err := uuid.Parse(match.OrderRef)
	if err != nil {
		return false, err
	}
	if err := a.store.SetMerchantMatch(ctx, spaceID, &store.MerchantMatch{
		TransactionID: txn.ID, OrderID: orderID, Amount: match.Amount, Basis: match.Basis,
		Confidence: match.Confidence,
	}); err != nil {
		return false, err
	}
	if order, found := byID[orderID]; found {
		if err := a.splitByItems(ctx, spaceID, txn, order, match); err != nil {
			return true, err
		}
	}
	if txn.Amount.IsNegative() {
		return true, a.linkCreditsToPurchase(ctx, spaceID, orderID)
	}
	return true, nil
}

// linkCreditsToPurchase links the credits already matched to an order's
// returns to the purchase just matched. The credit is often matched first,
// being the newer row, and then finds no purchase to link to.
func (a *Merchants) linkCreditsToPurchase(ctx context.Context, spaceID store.SpaceID, orderID uuid.UUID) error {
	order, err := a.store.GetMerchantOrder(ctx, spaceID, orderID)
	if err != nil {
		return err
	}
	matches, err := a.store.MerchantMatchesForOrders(ctx, spaceID, []uuid.UUID{orderID})
	if err != nil {
		return err
	}
	type credit struct {
		txn    store.Transaction
		refund store.MerchantRefund
	}
	var credits []credit
	for _, match := range matches {
		if !match.Amount.IsPositive() || (match.RefundID == nil && match.Basis != domain.MerchantMatchRefundTotal) {
			continue
		}
		var refund store.MerchantRefund
		if match.RefundID != nil {
			refund, err = a.store.GetMerchantRefund(ctx, spaceID, *match.RefundID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
		}
		txn, err := a.store.GetTransaction(ctx, spaceID, match.TransactionID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		credits = append(credits, credit{txn: txn, refund: refund})
	}
	// Oldest first, as they would have been linked had the purchase come first.
	slices.SortFunc(credits, func(a, b credit) int {
		return cmp.Or(a.txn.Date.Time().Compare(b.txn.Date.Time()), strings.Compare(a.txn.ID.String(), b.txn.ID.String()))
	})
	for _, one := range credits {
		if err := a.carryRefundCategory(ctx, spaceID, one.txn, one.refund, order); err != nil {
			return err
		}
	}
	return nil
}

// matchRefund offers one credit the returns on file for one merchant, then
// the orders whose invoice says something was refunded. The order is looked
// up by number, not a window: a refund routinely comes months after the
// purchase. A gift card balance's line is offered only if it reads as a
// refund: a reload is money in too.
func (a *Merchants) matchRefund(
	ctx context.Context, spaceID store.SpaceID, txn store.Transaction, merchant domain.MerchantID,
	giftCard bool,
) (bool, error) {
	if !txn.Amount.IsPositive() || (giftCard && !giftCardRefundLine.MatchString(txn.StatementName)) {
		return false, nil
	}
	from, to := domain.MerchantRefundSpan(txn.Date)
	refunds, err := a.store.MerchantRefundsBetween(ctx, spaceID, merchant, from, to)
	if err != nil {
		return false, err
	}
	if len(refunds) == 0 {
		return a.matchRefundTotal(ctx, spaceID, txn, merchant, giftCard)
	}
	facts := make([]domain.MerchantRefundFacts, 0, len(refunds))
	byRef := make(map[string]store.MerchantRefund, len(refunds))
	var taken []domain.MerchantMatch
	for _, refund := range refunds {
		ref := refund.ID.String()
		byRef[ref] = refund
		facts = append(facts, domain.MerchantRefundFacts{
			Ref: ref, OrderRef: refund.OrderNumber, ItemRef: refund.SKU + refund.Title,
			RefundedOn: refund.RefundedOn, Amount: refund.Amount, ToGiftCard: refund.ToGiftCard(),
		})
		if refund.TransactionID != uuid.Nil && refund.TransactionID != txn.ID {
			taken = append(taken, domain.MerchantMatch{RefundRef: ref})
		}
	}
	match, ok := domain.MatchMerchantRefund(txn.Amount, txn.Date, facts, taken, giftCard)
	if !ok {
		return a.matchRefundTotal(ctx, spaceID, txn, merchant, giftCard)
	}
	refund := byRef[match.RefundRef]
	order, err := a.store.GetMerchantOrderByNumber(ctx, spaceID, refund.MerchantAccountID, refund.OrderNumber)
	if errors.Is(err, store.ErrNotFound) {
		// The order is not on file yet; a later pull fixes that.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if order.Ignored() {
		return false, nil
	}
	refundID := refund.ID
	if err := a.store.SetMerchantMatch(ctx, spaceID, &store.MerchantMatch{
		TransactionID: txn.ID, OrderID: order.ID, RefundID: &refundID, Amount: match.Amount,
		Basis: match.Basis, Confidence: match.Confidence,
	}); err != nil {
		return false, err
	}
	return true, a.carryRefundCategory(ctx, spaceID, txn, refund, order)
}

// giftCardRefundLine is a gift card balance's line for a refund to it.
var giftCardRefundLine = regexp.MustCompile(`(?i)\brefund`)

// matchRefundTotal offers a credit no refund record explains the orders whose
// invoice says something was refunded (domain.MatchMerchantRefundTotal), and
// links it to the purchase as a matched return is.
func (a *Merchants) matchRefundTotal(
	ctx context.Context, spaceID store.SpaceID, txn store.Transaction, merchant domain.MerchantID,
	giftCard bool,
) (bool, error) {
	orders, err := a.store.MerchantOrdersRefundedBetween(ctx, spaceID, merchant,
		txn.Date.AddDays(-domain.RefundCandidateWindowDays), txn.Date)
	if err != nil || len(orders) == 0 {
		return false, err
	}
	ids := make([]uuid.UUID, 0, len(orders))
	byRef := make(map[string]store.MerchantOrder, len(orders))
	for _, order := range orders {
		ids = append(ids, order.ID)
		byRef[order.ID.String()] = order
	}
	matches, err := a.store.MerchantMatchesForOrders(ctx, spaceID, ids)
	if err != nil {
		return false, err
	}
	claimed := map[uuid.UUID]domain.Money{}
	for _, m := range matches {
		if m.TransactionID == txn.ID || !m.Amount.IsPositive() {
			continue
		}
		claimed[m.OrderID] = claimed[m.OrderID].Add(m.Amount)
	}
	facts := make([]domain.MerchantRefundTotalFacts, 0, len(orders))
	for _, order := range orders {
		facts = append(facts, domain.MerchantRefundTotalFacts{
			OrderRef: order.ID.String(), OrderedOn: order.OrderedOn,
			RefundTotal: order.RefundTotal, Claimed: claimed[order.ID],
		})
	}
	match, ok := domain.MatchMerchantRefundTotal(txn.Amount, txn.Date, facts, giftCard)
	if !ok {
		return false, nil
	}
	order := byRef[match.OrderRef]
	if err := a.store.SetMerchantMatch(ctx, spaceID, &store.MerchantMatch{
		TransactionID: txn.ID, OrderID: order.ID, Amount: match.Amount,
		Basis: match.Basis, Confidence: match.Confidence,
	}); err != nil {
		return false, err
	}
	return true, a.carryRefundCategory(ctx, spaceID, txn, store.MerchantRefund{}, order)
}

// carryRefundCategory links a matched credit to the purchase it gives back
// and files it where that purchase was filed. The link stops the credit
// counting as income and carries the purchase's category; but a purchase
// split per item has no category of its own, so the returned item's split
// category is used, found by the item's position as splitByItems wrote it.
//
// A credit that already has a link is left as it is: the person made it, or
// removed it and the credit is matched again without it.
func (a *Merchants) carryRefundCategory(
	ctx context.Context, spaceID store.SpaceID, txn store.Transaction,
	refund store.MerchantRefund, order store.MerchantOrder,
) error {
	linked, err := a.store.ListRefundLinksFor(ctx, spaceID, []uuid.UUID{txn.ID})
	if err != nil {
		return err
	}
	for _, link := range linked {
		if link.RefundTxnID == txn.ID {
			return nil
		}
	}
	purchase, found, err := a.purchaseBehind(ctx, spaceID, order, txn)
	if err != nil || !found {
		return err
	}
	if err := a.store.LinkRefund(ctx, spaceID, txn.ID, purchase.ID); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		return err
	}
	categoryID, ok := returnedItemCategory(purchase, order, refund)
	if !ok {
		return nil
	}
	credit, err := a.store.GetTransaction(ctx, spaceID, txn.ID)
	if err != nil {
		return err
	}
	if len(credit.Splits) > 0 || credit.CategoryID == categoryID {
		return nil
	}
	credit.CategoryID = categoryID
	return a.store.UpdateTransaction(ctx, spaceID, &credit)
}

// purchaseBehind is the bank row a credit gives money back to: one of the
// rows that paid for the order, chosen by domain.ChooseRefundedCharge from
// those with room left after the credits already linked to them.
func (a *Merchants) purchaseBehind(
	ctx context.Context, spaceID store.SpaceID, order store.MerchantOrder, credit store.Transaction,
) (store.Transaction, bool, error) {
	matches, err := a.store.MerchantMatchesForOrders(ctx, spaceID, []uuid.UUID{order.ID})
	if err != nil {
		return store.Transaction{}, false, err
	}
	byID := map[uuid.UUID]store.Transaction{}
	var ids []uuid.UUID
	for _, match := range matches {
		if match.TransactionID == credit.ID || !match.Amount.IsNegative() {
			continue
		}
		purchase, err := a.store.GetTransaction(ctx, spaceID, match.TransactionID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return store.Transaction{}, false, err
		}
		if !store.CountsTowardBalance(purchase) || !purchase.Amount.IsNegative() {
			continue
		}
		if _, seen := byID[purchase.ID]; !seen {
			byID[purchase.ID] = purchase
			ids = append(ids, purchase.ID)
		}
	}
	if len(ids) == 0 {
		return store.Transaction{}, false, nil
	}
	links, err := a.store.ListRefundLinksFor(ctx, spaceID, ids)
	if err != nil {
		return store.Transaction{}, false, err
	}
	refunded := map[uuid.UUID]domain.Money{}
	for _, link := range links {
		if _, isCharge := byID[link.ChargeTxnID]; !isCharge {
			continue
		}
		earlier, err := a.store.GetTransaction(ctx, spaceID, link.RefundTxnID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return store.Transaction{}, false, err
		}
		refunded[link.ChargeTxnID] = refunded[link.ChargeTxnID].Add(earlier.Amount)
	}
	charges := make([]domain.RefundTarget, 0, len(ids))
	for _, id := range ids {
		purchase := byID[id]
		charges = append(charges, domain.RefundTarget{
			Ref: id.String(), On: purchase.Date, Amount: purchase.Amount, Refunded: refunded[id],
		})
	}
	ref, ok := domain.ChooseRefundedCharge(credit.Amount, charges)
	if !ok {
		return store.Transaction{}, false, nil
	}
	id, err := uuid.Parse(ref)
	if err != nil {
		return store.Transaction{}, false, err
	}
	return byID[id], true, nil
}

// returnedItemCategory is the category of the returned line's split on the
// purchase row. By position, not memo: memos are trimmed titles and two
// variants can trim alike. Splits that do not count out to the items were
// edited by hand and are not this function's to read.
func returnedItemCategory(
	purchase store.Transaction, order store.MerchantOrder, refund store.MerchantRefund,
) (uuid.UUID, bool) {
	if len(purchase.Splits) == 0 || len(purchase.Splits) != len(order.Items) {
		return uuid.Nil, false
	}
	for i, item := range order.Items {
		named := refund.SKU != "" && item.SKU == refund.SKU
		if !named && refund.SKU == "" && refund.Title != "" {
			named = domain.FoldName(item.DisplayTitle()) == domain.FoldName(refund.Title)
		}
		if !named {
			continue
		}
		if purchase.Splits[i].CategoryID == uuid.Nil {
			return uuid.Nil, false
		}
		return purchase.Splits[i].CategoryID, true
	}
	return uuid.Nil, false
}

// ItemShares divides a matched amount across an order's items in proportion
// to price, or returns nil when there are fewer than two items, an item has
// no price, or the amount is not the whole card charge for the order.
func ItemShares(order store.MerchantOrder, match domain.MerchantMatch) []domain.Money {
	if len(order.Items) < 2 || !match.Amount.Abs().Equal(order.CardTotal()) {
		return nil
	}
	weights := make([]domain.Money, 0, len(order.Items))
	for _, item := range order.Items {
		switch {
		case item.HasTotalOwed:
			weights = append(weights, item.TotalOwed)
		case item.HasUnitPrice:
			quantity := item.Quantity
			if quantity <= 0 {
				quantity = 1
			}
			weights = append(weights, item.UnitPrice.MulInt(quantity))
		default:
			return nil
		}
	}
	return domain.SplitByWeight(match.Amount, weights)
}

// splitByItems writes one split per item onto a row matched to a whole
// order. Each split keeps the row's category so reports read the same total.
// A row somebody already split is left alone.
func (a *Merchants) splitByItems(
	ctx context.Context, spaceID store.SpaceID, txn store.Transaction, order store.MerchantOrder,
	match domain.MerchantMatch,
) error {
	if len(txn.Splits) > 0 {
		return nil
	}
	shares := ItemShares(order, match)
	if shares == nil {
		return nil
	}
	for i, item := range order.Items {
		txn.Splits = append(txn.Splits, store.Split{
			Position: i, Amount: shares[i], CategoryID: txn.CategoryID,
			Memo: domain.ItemMemo(item.DisplayTitle()),
		})
	}
	txn.CategoryID = uuid.Nil
	return a.store.UpdateTransaction(ctx, spaceID, &txn)
}

// MerchantEnrichment is the order or orders behind one bank row. Match and
// Order are the first; Matches and Orders are all of them, and Account is
// the first order's login.
type MerchantEnrichment struct {
	Match   store.MerchantMatch
	Order   store.MerchantOrder
	Account store.MerchantAccount
	Matches []store.MerchantMatch
	Orders  []store.MerchantOrder
	// Refund is the return a credit row gives back; nil for a purchase.
	Refund *store.MerchantRefund
}

func (e *MerchantEnrichment) Merchant() domain.Merchant { return merchantOf(e.Account) }

func (e *MerchantEnrichment) Items() []store.MerchantOrderItem {
	var out []store.MerchantOrderItem
	for _, order := range e.Orders {
		out = append(out, order.Items...)
	}
	return out
}

// Shares is each item's share of the row, in Items order, or nil when the row
// cannot be divided that way (see ItemShares).
func (e *MerchantEnrichment) Shares() []domain.Money {
	var out []domain.Money
	for i, order := range e.Orders {
		match := domain.MerchantMatch{Amount: e.Matches[i].Amount, Basis: e.Matches[i].Basis}
		switch {
		case len(order.Items) == 1:
			out = append(out, match.Amount)
		case len(order.Items) == 0:
			return nil
		default:
			shares := ItemShares(order, match)
			if shares == nil {
				return nil
			}
			out = append(out, shares...)
		}
	}
	return out
}

// EnrichmentFor is the orders a bank row is matched to, matching it now if the
// row is worded as some merchant's, or nil when no order fits. A row matched
// by hand is enriched whatever its wording.
func (a *Merchants) EnrichmentFor(ctx context.Context, spaceID store.SpaceID, txn store.Transaction) (*MerchantEnrichment, error) {
	matches, err := a.store.ListMerchantMatches(ctx, spaceID, txn.ID)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		if len(domain.MerchantsFor(txn.StatementName, txn.Payee)) == 0 {
			return nil, nil
		}
		ok, err := a.matchOne(ctx, spaceID, txn)
		if err != nil || !ok {
			return nil, err
		}
		if matches, err = a.store.ListMerchantMatches(ctx, spaceID, txn.ID); err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, nil
		}
	}
	out := &MerchantEnrichment{Matches: matches, Orders: make([]store.MerchantOrder, 0, len(matches))}
	for _, match := range matches {
		order, err := a.store.GetMerchantOrder(ctx, spaceID, match.OrderID)
		if err != nil {
			return nil, err
		}
		out.Orders = append(out.Orders, order)
	}
	// So a credit's panel can link back to the purchase.
	if err := a.store.AttachMerchantMatches(ctx, out.Orders); err != nil {
		return nil, err
	}
	out.Match, out.Order = matches[0], out.Orders[0]
	if out.Match.RefundID != nil {
		refund, err := a.store.GetMerchantRefund(ctx, spaceID, *out.Match.RefundID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			out.Refund = &refund
		}
	}
	if out.Account, err = a.store.GetMerchantAccount(ctx, spaceID, out.Order.MerchantAccountID); err != nil {
		return nil, err
	}
	return out, nil
}

// AddMatchByHand adds an order to a row that settled several. Each order then
// carries its own card total as its share of the row.
func (a *Merchants) AddMatchByHand(ctx context.Context, spaceID store.SpaceID, transactionID, orderID uuid.UUID) error {
	txn, err := a.store.GetTransaction(ctx, spaceID, transactionID)
	if err != nil {
		return err
	}
	if err := a.unignore(ctx, spaceID, orderID); err != nil {
		return err
	}
	if err := a.store.AddMerchantMatch(ctx, spaceID, &store.MerchantMatch{
		TransactionID: txn.ID, OrderID: orderID, Amount: txn.Amount,
		Basis: domain.MerchantMatchManual, Confidence: 1,
	}); err != nil {
		return err
	}
	if err := a.rebalance(ctx, spaceID, txn); err != nil {
		return err
	}
	return a.lookAgain(ctx, spaceID, []uuid.UUID{txn.ID})
}

func (a *Merchants) RemoveMatch(ctx context.Context, spaceID store.SpaceID, transactionID, orderID uuid.UUID) error {
	if err := a.store.DeleteMerchantMatchForOrder(ctx, spaceID, transactionID, orderID); err != nil {
		return err
	}
	txn, err := a.store.GetTransaction(ctx, spaceID, transactionID)
	if err != nil {
		return err
	}
	return a.rebalance(ctx, spaceID, txn)
}

// rebalance gives each of a row's orders its share: the whole row for one
// order, each order's own card total (signed as the row is) for several.
func (a *Merchants) rebalance(ctx context.Context, spaceID store.SpaceID, txn store.Transaction) error {
	matches, err := a.store.ListMerchantMatches(ctx, spaceID, txn.ID)
	if err != nil {
		return err
	}
	for _, match := range matches {
		share := txn.Amount
		if len(matches) > 1 {
			order, err := a.store.GetMerchantOrder(ctx, spaceID, match.OrderID)
			if err != nil {
				return err
			}
			share = order.CardTotal()
			if txn.Amount.IsNegative() {
				share = share.Neg()
			}
		}
		if share.Equal(match.Amount) {
			continue
		}
		if err := a.store.SetMerchantMatchAmount(ctx, spaceID, txn.ID, match.OrderID, share); err != nil {
			return err
		}
	}
	return nil
}

func (a *Merchants) unignore(ctx context.Context, spaceID store.SpaceID, orderID uuid.UUID) error {
	order, err := a.store.GetMerchantOrder(ctx, spaceID, orderID)
	if err != nil {
		return err
	}
	if !order.Ignored() {
		return nil
	}
	return a.store.SetMerchantOrderIgnored(ctx, spaceID, orderID, false)
}

// IgnoreOrder records that no bank row will explain the order (it was charged
// to an untracked card), or takes that back. Rows already matched are left.
func (a *Merchants) IgnoreOrder(ctx context.Context, spaceID store.SpaceID, orderID uuid.UUID, ignored bool) (store.MerchantOrder, error) {
	if err := a.store.SetMerchantOrderIgnored(ctx, spaceID, orderID, ignored); err != nil {
		return store.MerchantOrder{}, err
	}
	return a.store.GetMerchantOrder(ctx, spaceID, orderID)
}

// --- The hands-off pull --------------------------------------------------------
//
// A person signs in once; the session, and unless declined the password, is
// sealed onto the account and the daily pull switched on. Every pull re-seals
// the session because Amazon rolls its cookies and Costco rotates its token.
// A pull that meets a sign-in screen tries the kept password once; when that
// needs a person, the account is marked, the household told, and the
// password paused until somebody signs in, forgets it, or presses Update now.

// signInAgent is the agent for a sign-in this account started, and a sign-in
// started anywhere else is not found.
func (a *Merchants) signInAgent(spaceID store.SpaceID, accountID uuid.UUID, sessionID string) (MerchantAgent, error) {
	if err := ownSignIn(sessionID, spaceID, accountID); err != nil {
		return nil, err
	}
	return a.agent()
}

// StartSignIn begins a sign-in for one account. The password and
// authenticator key are held in memory and sealed only on CompleteSignIn, so
// a password the merchant refused is never one a pull keeps trying.
func (a *Merchants) StartSignIn(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID,
	email, password, secret string, factor domain.SecondFactor,
) (provider.MerchantSignInState, error) {
	account, err := a.store.GetMerchantAccount(ctx, spaceID, accountID)
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	if account.SecondFactor != factor {
		if err := a.store.SetMerchantSecondFactor(ctx, spaceID, accountID, factor); err != nil {
			return provider.MerchantSignInState{}, err
		}
	}
	agent, err := a.agent()
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	state, err := agent.StartSignIn(ctx, account.Merchant, email, password)
	if err != nil {
		return state, err
	}
	rememberSignIn(state.SessionID, spaceID, accountID)
	if state.State != provider.MerchantSignInFailed {
		rememberCredential(state.SessionID, store.MerchantCredential{
			Username: email, Password: password, TOTPSecret: secret,
		})
	}
	return state, nil
}

func (a *Merchants) AnswerSignIn(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, sessionID, code string,
) (provider.MerchantSignInState, error) {
	agent, err := a.signInAgent(spaceID, accountID, sessionID)
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	return agent.AnswerSignIn(ctx, sessionID, code)
}

// mailedCode is AnswerFromMail's wait for an account's code.
func (a *Merchants) mailedCode(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID,
) (func(since time.Time) (string, bool), error) {
	account, err := a.store.GetMerchantAccount(ctx, spaceID, accountID)
	if err != nil {
		return nil, err
	}
	if _, err := a.agent(); err != nil {
		return nil, err
	}
	if a.MailedCode == nil || a.HasMailbox == nil || !a.HasMailbox(ctx, spaceID) {
		return nil, nil
	}
	return func(since time.Time) (string, bool) {
		return a.MailedCode(ctx, spaceID, account.Merchant, since)
	}, nil
}

func (a *Merchants) signInStatus(ctx context.Context, sessionID string) (provider.MerchantSignInState, error) {
	agent, err := a.agent()
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	return agent.SignInStatus(ctx, sessionID)
}

func (a *Merchants) answerSignIn(ctx context.Context, sessionID, code string) (provider.MerchantSignInState, error) {
	agent, err := a.agent()
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	return agent.AnswerSignIn(ctx, sessionID, code)
}

func (a *Merchants) SignInStatus(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, sessionID string,
) (provider.MerchantSignInState, error) {
	agent, err := a.signInAgent(spaceID, accountID, sessionID)
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	return agent.SignInStatus(ctx, sessionID)
}

// CompleteSignIn seals the finished session onto the account and switches
// the daily pull on; the password is kept or forgotten as the person asked.
func (a *Merchants) CompleteSignIn(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, sessionID, email string,
) error {
	if _, err := a.store.GetMerchantAccount(ctx, spaceID, accountID); err != nil {
		return err
	}
	agent, err := a.signInAgent(spaceID, accountID, sessionID)
	if err != nil {
		return err
	}
	credential, held := takeCredential(sessionID)
	state, hint, err := agent.CompleteSignIn(ctx, sessionID)
	signIns.Delete(sessionID)
	if err != nil {
		return err
	}
	if len(state) == 0 {
		return fmt.Errorf("the agent handed back no session")
	}
	// A hand sign-in typed no email; the site's greeting name identifies it.
	if email == "" {
		email = hint
	}
	if err := a.store.SaveMerchantSession(ctx, spaceID, accountID, email, string(state), true); err != nil {
		return err
	}
	if !held {
		return nil
	}
	return a.store.SaveMerchantCredential(ctx, spaceID, accountID, credential)
}

// Forget drops the session and turns the pull off. A kept password stays.
func (a *Merchants) Forget(ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID) error {
	return a.store.ClearMerchantSession(ctx, spaceID, accountID)
}

// ForgetPassword drops the kept password and authenticator key; the session
// stays.
func (a *Merchants) ForgetPassword(ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID) error {
	return a.store.ClearMerchantCredential(ctx, spaceID, accountID)
}

// ErrMerchantNeedsSignIn is a pull that cannot go on until a person signs in:
// the session was challenged, or there is none. The account says so already.
var ErrMerchantNeedsSignIn = errors.New("merchant: sign in again")

// PullStopped is a pull that ended on a failure the account records, and
// whether the page it stopped on was kept for the failure-screenshot route.
type PullStopped struct {
	Err        error
	Screenshot bool
}

func (e *PullStopped) Error() string { return e.Err.Error() }
func (e *PullStopped) Unwrap() error { return e.Err }

// Pull is a pull a person asked for: it tries a kept password once even when
// paused. days is how far back to read; zero takes the account's setting.
// It outlives the caller's request, since a pull cut off part way has neither
// its orders nor its "last updated" stamp written.
func (a *Merchants) Pull(ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, days int) (MerchantImportReport, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pullTimeout)
	defer cancel()
	return a.pull(ctx, spaceID, accountID, days, true)
}

// PullAfterSignIn starts a background pull of a just-signed-in account, and
// answers false when one is already running.
func (a *Merchants) PullAfterSignIn(spaceID store.SpaceID, accountID uuid.UUID) bool {
	if !a.HasAgent() || !merchantPulls.claim(accountID) {
		return false
	}
	merchantPulls.inBackground(accountID, func(ctx context.Context) {
		report, err := a.pullClaimed(ctx, spaceID, accountID, 0, true)
		switch {
		case errors.Is(err, ErrMerchantNeedsSignIn):
			a.log().Warn("merchant: the pull after a sign-in met a sign-in", "account", accountID)
		case err != nil:
			a.log().Error("merchant: the pull after a sign-in failed", "account", accountID, "error", err)
		default:
			a.log().Info("merchant: pulled after a sign-in", "account", accountID, "orders", report.Orders,
				"new_orders", report.NewOrders, "charges", report.Charges, "matched", report.Matched)
		}
	})
	return true
}

// pull is Pull for either caller. byPerson false is the scheduler, which never
// tries a paused password. ErrPullRunning when a pull is already running here.
func (a *Merchants) pull(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, days int, byPerson bool,
) (MerchantImportReport, error) {
	if !merchantPulls.claim(accountID) {
		return MerchantImportReport{}, ErrPullRunning
	}
	defer merchantPulls.release(accountID)
	return a.pullClaimed(ctx, spaceID, accountID, days, byPerson)
}

func (a *Merchants) pullClaimed(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, days int, byPerson bool,
) (MerchantImportReport, error) {
	ctx, done := trackMerchantPull(ctx, accountID)
	defer done()
	account, err := a.store.GetMerchantAccount(ctx, spaceID, accountID)
	if err != nil {
		return MerchantImportReport{}, err
	}
	agent, err := a.agent()
	if err != nil {
		return MerchantImportReport{}, err
	}
	state, err := a.store.MerchantSession(ctx, spaceID, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return MerchantImportReport{}, fmt.Errorf("%s has no session: %w", account.Name(), ErrMerchantNeedsSignIn)
	}
	if err != nil {
		return MerchantImportReport{}, err
	}
	if days <= 0 {
		days = account.SyncDays
	}
	if days <= 0 {
		days = 30
	}
	// The first pull after a sign-in reads a year back.
	if account.LastSyncedAt == nil && days < 365 {
		days = 365
	}
	if days > 3650 {
		days = 3650
	}
	known, err := a.store.MerchantOrdersWithInvoice(ctx, spaceID, accountID,
		domain.DateOf(time.Now()).AddDays(-days-14))
	if err != nil {
		return MerchantImportReport{}, err
	}
	// With nowhere to keep an invoice, no order is opened again for one.
	invoiced := known
	if a.filesInvoices() {
		if invoiced, err = a.store.MerchantOrdersWithInvoiceDocument(ctx, spaceID, accountID,
			domain.DateOf(time.Now()).AddDays(-days-14)); err != nil {
			return MerchantImportReport{}, err
		}
	}
	refundChecks, err := a.store.MerchantOrdersDueRefundCheck(ctx, spaceID, accountID, domain.DateOf(time.Now()))
	if err != nil {
		return MerchantImportReport{}, err
	}
	var credential *provider.MerchantCredential
	if account.HasPassword && (byPerson || account.SignInPausedAt == nil) {
		kept, err := a.store.MerchantCredentialOf(ctx, spaceID, accountID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return MerchantImportReport{}, err
		}
		if err == nil {
			credential = &provider.MerchantCredential{
				Email:    textutil.FirstNonBlank(kept.Username, account.Email),
				Password: kept.Password, TOTPSecret: kept.TOTPSecret,
				SecondFactor: account.SecondFactor,
			}
			if a.MailedCode != nil {
				merchant := account.Merchant
				credential.MailedCode = func(ctx context.Context, since time.Time) (string, bool) {
					return a.MailedCode(ctx, spaceID, merchant, since)
				}
			}
		}
	}
	result, err := agent.Fetch(ctx, account.Merchant, json.RawMessage(state), days, known, invoiced, refundChecks, credential)
	if err != nil {
		shot := failureShot(err, "")
		a.stopped(ctx, spaceID, account, store.MerchantSyncFailed, err.Error(), shot)
		return MerchantImportReport{}, &PullStopped{Err: err, Screenshot: shot != nil}
	}
	// The session a pull leaves is the merchant's latest; the one it started
	// from may already be spent, so it is kept whatever the import makes of
	// the orders.
	if len(result.StorageState) > 0 {
		if err := a.store.SaveMerchantSession(ctx, spaceID, accountID, "", string(result.StorageState), false); err != nil {
			return MerchantImportReport{}, err
		}
	}
	if result.NeedsSignIn {
		reason := result.Reason
		if reason == "" {
			reason = merchantOf(account).Name + " asked to sign in again"
		}
		// A lapsed session is no fault of the password, so it is never forgotten
		// here; one that needs a person is paused so the scheduler stops trying it.
		if result.Paused != "" {
			reason = strings.TrimRight(reason, ". ") + ". The password is still kept, but Agentifi " +
				"will not sign in with it on its own again until you sign in, forget it, or press Update now."
			if err := a.store.PauseMerchantSignIn(ctx, spaceID, accountID, result.Paused); err != nil {
				return MerchantImportReport{}, err
			}
		}
		shot := failureShot(nil, result.Image)
		a.stopped(ctx, spaceID, account, store.MerchantSyncNeedsSignIn, reason, shot)
		return MerchantImportReport{}, &PullStopped{Err: ErrMerchantNeedsSignIn, Screenshot: shot != nil}
	}
	if result.Parsed == nil {
		// Reading nothing is a page-layout problem: record it as a failure so the
		// settings page shows it.
		detail := "nothing to import"
		for _, note := range result.Notes {
			detail += "; " + note
		}
		a.stopped(ctx, spaceID, account, store.MerchantSyncFailed, detail, nil)
		return MerchantImportReport{}, fmt.Errorf("the pull read no orders: %s", detail)
	}
	provider.ReportPull(ctx, fmt.Sprintf("Saving %d orders and matching them to your transactions", result.Orders))
	report, err := a.Import(ctx, spaceID, accountID, *result.Parsed, false)
	if err != nil {
		a.stopped(ctx, spaceID, account, store.MerchantSyncFailed, err.Error(), nil)
		return MerchantImportReport{}, err
	}
	report.Warnings = append(report.Warnings, result.Notes...)
	if len(result.Invoices) > 0 {
		provider.ReportPull(ctx, fmt.Sprintf("Filing %d invoices", len(result.Invoices)))
	}
	filed, warnings := a.fileInvoices(ctx, spaceID, accountID, result.Invoices)
	report.Warnings = append(report.Warnings, warnings...)
	if note := a.invoiceNote(ctx, spaceID, account, filed); note != "" {
		report.Warnings = append(report.Warnings, note)
	}
	detail := ""
	if len(result.Notes) > 0 {
		detail = result.Notes[0]
	}
	if err := a.store.MarkMerchantSync(ctx, spaceID, accountID, store.MerchantSyncOK, detail, nil); err != nil {
		return report, err
	}
	if a.Alerts != nil {
		merchant := merchantOf(account)
		for _, condition := range []string{
			merchantSignInCondition(merchant, accountID), merchantPullCondition(merchant, accountID),
		} {
			if err := a.Alerts.ResolveCondition(ctx, spaceID, condition); err != nil {
				a.log().Error("merchant: withdrawing a stopped-pull alert", "error", err)
			}
		}
	}
	return report, nil
}

// stopped records a pull that ended badly, with the page it stopped on or nil,
// and tells the household: a sign-in wall through notifySignIn, anything else
// once per failing streak.
func (a *Merchants) stopped(
	ctx context.Context, spaceID store.SpaceID, account store.MerchantAccount, status, detail string, shot []byte,
) {
	// Read before the mark: "is this new" compares with what the row said before
	// this pull.
	repeated := account.LastSyncStatus == status
	if err := a.store.MarkMerchantSync(ctx, spaceID, account.ID, status, detail, shot); err != nil {
		a.log().Error("merchant: recording a stopped pull", "account", account.Name(), "error", err)
	}
	if status == store.MerchantSyncNeedsSignIn {
		a.notifySignIn(ctx, spaceID, account, detail)
		return
	}
	if repeated || a.Alerts == nil {
		return
	}
	merchant := merchantOf(account)
	hit := domain.AlertHit{
		Type:  domain.AlertMerchantPullFailed,
		Title: fmt.Sprintf("%s (%s) stopped pulling %s", merchant.Name, account.Name(), merchant.Plural),
		Body: fmt.Sprintf("The daily pull of %s %s for %s failed: %s. Charges are not matched to "+
			"new orders until it succeeds.", merchant.Name, merchant.Plural, account.Name(), detail),
		URL:       merchant.SettingsPath,
		DedupeKey: merchantPullCondition(merchant, account.ID), Ongoing: true,
	}
	if _, err := a.Alerts.DeliverToSpace(ctx, spaceID, hit); err != nil {
		a.log().Error("merchant: delivering a stopped-pull alert", "error", err)
	}
}

func (a *Merchants) filesInvoices() bool { return a.Documents != nil && a.Documents.Storage != nil }

// fileInvoices keeps each fetched invoice as its order's document, which the
// store then files as a receipt on every row matched to the order, and says
// how many it kept. An invoice that cannot be kept is a warning, not a failed
// pull: the orders are in.
func (a *Merchants) fileInvoices(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, invoices []provider.MerchantInvoice,
) (int, []string) {
	if len(invoices) == 0 || !a.filesInvoices() {
		return 0, nil
	}
	var warnings []string
	filed, failed := 0, 0
	for _, one := range invoices {
		order, err := a.store.GetMerchantOrderByNumber(ctx, spaceID, accountID, one.OrderNumber)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err == nil {
			_, _, err = a.Documents.Store(ctx, spaceID, DocumentUpload{
				Bytes: one.PDF, Filename: one.Filename,
				Source: store.DocumentSourceMerchantPull, SourceRef: string(order.Merchant) + ":" + order.OrderNumber,
				Link: store.DocumentLink{
					Kind: store.DocumentLinkMerchantOrder, TargetID: order.ID, Role: store.DocumentRoleInvoice,
				},
			})
		}
		if err != nil {
			failed++
			a.log().Error("merchant: filing an invoice", "order", one.OrderNumber, "error", err)
			continue
		}
		filed++
	}
	if failed > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of %d invoices could not be filed", failed, len(invoices)))
	}
	return filed, warnings
}

// merchantSignInCondition is the state "this account's pull is stopped at a
// sign-in", as an alert condition.
func merchantSignInCondition(merchant domain.Merchant, accountID uuid.UUID) string {
	return string(merchant.ID) + "-sign-in:" + accountID.String()
}

// merchantPullCondition is the state "this account's pull fails after getting
// in", as an alert condition.
func merchantPullCondition(merchant domain.Merchant, accountID uuid.UUID) string {
	return string(merchant.ID) + "-pull-failed:" + accountID.String()
}

// PullDue runs the daily pull for every account not pulled since the window
// opened, across every space, and reports how many it pulled.
func (a *Merchants) PullDue(ctx context.Context, since time.Time) int {
	// Runs without a browser too: uploaded files bring numbers, and misses retry.
	a.enrichAll(ctx)
	if !a.HasAgent() {
		return 0
	}
	return runDue(ctx, a.store, a.log(), dueRun[store.MerchantAccount]{
		kind: "merchant",
		list: func(ctx context.Context) ([]store.MerchantAccount, error) {
			return a.store.ListMerchantAccountsDue(ctx, since)
		},
		run: a.pullDue,
	})
}

func (a *Merchants) pullDue(ctx context.Context, account store.MerchantAccount) bool {
	report, err := a.pull(ctx, account.SpaceID, account.ID, 0, false)
	switch {
	case errors.Is(err, ErrPullRunning):
		a.log().Info("merchant: already pulling", "merchant", account.Merchant, "account", account.Name())
		return false
	case errors.Is(err, ErrMerchantNeedsSignIn):
		a.log().Warn("merchant: account needs a sign-in", "merchant", account.Merchant, "account", account.Name())
	case err != nil:
		a.log().Error("merchant: pull failed", "merchant", account.Merchant, "account", account.Name(), "error", err)
	default:
		a.log().Info("merchant: pulled", "merchant", account.Merchant, "account", account.Name(), "orders", report.Orders,
			"new_orders", report.NewOrders, "charges", report.Charges, "matched", report.Matched)
	}
	return true
}

// The catalog pass: every item number a merchant's orders name, looked up once
// and kept, so a receipt line can be named, categorised and split.

const (
	catalogLookupsPerRun     = 200
	catalogRetryMissingAfter = 30 * 24 * time.Hour
)

// catalogEnriching is process-wide because a service is built per request.
var catalogEnriching atomic.Bool

type CatalogReport struct {
	LookedUp int
	Found    int
	Missing  int
}

// EnrichCatalog looks up up to limit unanswered numbers. A failure to ask
// stops the pass and is returned with what was done.
func (a *Merchants) EnrichCatalog(ctx context.Context, merchant domain.MerchantID, limit int) (CatalogReport, error) {
	var report CatalogReport
	facts, ok := domain.MerchantByID(merchant)
	if !ok || !facts.HasCatalog || a.Catalog == nil {
		return report, nil
	}
	wanted, err := a.store.CatalogSKUsToLookUp(ctx, merchant, time.Now().Add(-catalogRetryMissingAfter), limit)
	if err != nil {
		return report, err
	}
	for _, sku := range wanted {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		entry, found, err := a.Catalog.Lookup(ctx, merchant, sku)
		if err != nil {
			return report, fmt.Errorf("looking up %s item %s: %w", facts.Name, sku, err)
		}
		now := time.Now().UTC()
		row := store.CatalogItem{
			Merchant: merchant, SKU: sku, Status: store.CatalogMissing, LookedUpAt: now,
		}
		if found {
			row.Status, row.FoundAt = store.CatalogFound, &now
			row.Title, row.Brand, row.Size, row.Category = entry.Title, entry.Brand, entry.Size, entry.Category
			row.ImageURL, row.URL, row.Price, row.HasPrice = entry.ImageURL, entry.URL, entry.Price, entry.HasPrice
			row.Source, row.SourceRef, row.Raw = entry.Source, entry.SourceRef, entry.Raw
			report.Found++
		} else {
			report.Missing++
		}
		if err := a.store.UpsertCatalogItem(ctx, &row); err != nil {
			return report, err
		}
		report.LookedUp++
	}
	return report, nil
}

func (a *Merchants) enrichSoon(merchant domain.MerchantID) {
	facts, ok := domain.MerchantByID(merchant)
	if !ok || !facts.HasCatalog || a.Catalog == nil {
		return
	}
	if !catalogEnriching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer catalogEnriching.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		a.enrichOne(ctx, merchant)
	}()
}

func (a *Merchants) enrichAll(ctx context.Context) {
	if a.Catalog == nil {
		return
	}
	if !catalogEnriching.CompareAndSwap(false, true) {
		return
	}
	defer catalogEnriching.Store(false)
	for _, facts := range domain.Merchants {
		if facts.HasCatalog {
			a.enrichOne(ctx, facts.ID)
		}
	}
}

func (a *Merchants) enrichOne(ctx context.Context, merchant domain.MerchantID) {
	report, err := a.EnrichCatalog(ctx, merchant, catalogLookupsPerRun)
	if err != nil {
		a.log().Warn("merchant: catalog pass stopped", "merchant", merchant, "looked_up", report.LookedUp, "error", err)
		return
	}
	if report.LookedUp > 0 {
		a.log().Info("merchant: catalog pass", "merchant", merchant, "looked_up", report.LookedUp,
			"found", report.Found, "missing", report.Missing)
	}
}

// merchantSignInURL opens the account's sign-in directly, so the alert lands
// on the form.
func merchantSignInURL(merchant domain.Merchant, accountID uuid.UUID) string {
	return merchant.SettingsPath + "?sign-in=" + accountID.String()
}

// notifySignIn tells the household the pull is stopped, once per stretch: the
// next pull that gets in withdraws it.
func (a *Merchants) notifySignIn(ctx context.Context, spaceID store.SpaceID, account store.MerchantAccount, reason string) {
	if a.Alerts == nil {
		return
	}
	merchant := merchantOf(account)
	hit := domain.AlertHit{
		Type:  merchant.SignInAlert,
		Title: fmt.Sprintf("%s (%s) needs you to sign in again", merchant.Name, account.Name()),
		Body: fmt.Sprintf("The daily pull of %s %s for %s stopped: %s. Open this to sign in "+
			"again and resume it.", merchant.Name, merchant.Plural, account.Name(), reason),
		URL:       merchantSignInURL(merchant, account.ID),
		DedupeKey: merchantSignInCondition(merchant, account.ID), Ongoing: true,
	}
	if _, err := a.Alerts.DeliverToSpace(ctx, spaceID, hit); err != nil {
		a.log().Error("merchant: delivering a sign-in alert", "error", err)
	}
}
