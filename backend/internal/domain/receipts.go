package domain

import (
	"slices"
	"strings"
	"unicode"
)

// Receipts an account requires. A health savings account's withdrawals have
// to be substantiated, so the household keeps a document behind every one,
// and an FSA or a card kept for eligible expenses may be held to the same.
//
// Which rows of such an account need one: money leaving it to pay for
// something. Not money arriving (a contribution, interest, a dividend, a
// refund), not a transfer (a contribution carried in from checking, money
// sent to another of the household's accounts), not a forecast, and not an
// opening balance or a revaluation. A person may settle a row as needing none,
// a bank fee say, so the count of missing receipts can honestly reach zero.

// The two sides of a health savings account. AccountTypeHSA is the banking
// side, the account the debit card draws on, which pays for care and so
// requires receipts unless the household says otherwise.
// AccountTypeHSAInvestment is the brokerage the cash may be swept into, whose
// outflows buy funds or carry money back to the cash side.
const (
	AccountTypeHSA           = "hsa"
	AccountTypeHSAInvestment = "hsa_investment"
)

// AccountRequiresReceipts is the account's own setting, or, with none set,
// whether it is the cash side of a health savings account: a cash account
// typed as an HSA or named as an HSA or an FSA. The name is read because an
// import may type a bank's HSA as checking; only cash is read at all, since an
// invested HSA buys funds rather than paying for care.
func AccountRequiresReceipts(accountType string, kind AccountKind, name string, setting, hasSetting bool) bool {
	if hasSetting {
		return setting
	}
	if kind != KindCash {
		return false
	}
	if accountType == AccountTypeHSA {
		return true
	}
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return slices.Contains(words, "hsa") || slices.Contains(words, "fsa")
}

// ReceiptStatus is where a row stands on its receipt.
type ReceiptStatus string

const (
	// ReceiptNotRequired is every row ReceiptStatusOf does not hold to one.
	ReceiptNotRequired ReceiptStatus = ""
	ReceiptMissing     ReceiptStatus = "missing"
	ReceiptOnFile      ReceiptStatus = "on_file"
	// ReceiptNotNeeded is a row a person said needs none.
	ReceiptNotNeeded ReceiptStatus = "not_needed"
)

// ReceiptStatusOf says whether this row needs a receipt and whether it has
// one. hasDocument is any document behind the row: one a person attached, or
// a bill's statement or an order's invoice filed on it as its receipt.
//
// A split row is a transfer only when every split is filed under a transfer
// category, since a split row's parent carries no category; categories must
// hold the space's own, deleted included.
func ReceiptStatusOf(p Posting, categories map[ID]Category, hasDocument bool) ReceiptStatus {
	txn := p.Txn
	if !p.Account.RequiresReceipts || p.Account.IsIgnored {
		return ReceiptNotRequired
	}
	if txn.IsDeleted || txn.IsEstimate || !txn.Source.IsCashFlow() {
		return ReceiptNotRequired
	}
	if !txn.Amount.IsNegative() {
		return ReceiptNotRequired
	}
	if receiptTransfer(p, categories) {
		return ReceiptNotRequired
	}
	switch {
	case hasDocument:
		return ReceiptOnFile
	case txn.ReceiptNotNeeded:
		return ReceiptNotNeeded
	}
	return ReceiptMissing
}

func receiptTransfer(p Posting, categories map[ID]Category) bool {
	if len(p.Txn.Splits) == 0 || p.Txn.TransferPairID != "" {
		return p.IsTransfer()
	}
	for _, split := range p.Txn.Splits {
		category, ok := categories[split.CategoryID]
		if !ok || !category.IsTransfer() {
			return false
		}
	}
	return true
}
