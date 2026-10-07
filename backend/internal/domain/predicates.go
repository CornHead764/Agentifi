package domain

import "fmt"

// What counts. Three questions that look like one and are not:
//
//   - does this transaction move the profit-and-loss numbers?  CountsAsIncomeOrExpense
//   - does it consume this month's free-to-spend?              CountsTowardSpendingPlan
//   - did money actually leave the bank?                       CountsTowardBalance
//
// Every report, total and plan asks these functions rather than restating
// their clauses, so no copy is ever edited alone. The clauses callers must ask
// on their own are named too.

// IsTransfer reports whether this row is money moving inside the household:
// either leg of a matched pair, or a row filed under a transfer-type category.
func (p Posting) IsTransfer() bool {
	if p.Txn.TransferPairID != "" {
		return true
	}
	return p.HasCategory && p.Category.IsTransfer()
}

// The category-level half of both counting questions, askable without a
// transaction because a split row's category lives on its splits. They fold
// the transfer rule in, since a split path has no pair id to consult.

// CountsAsIncomeOrExpense reports whether rows filed under this category belong
// in reports, watchlists and income/expense totals.
func (c Category) CountsAsIncomeOrExpense() bool {
	return !c.ExcludedFromReports && !c.IsTransfer()
}

// CountsTowardSpendingPlan reports whether rows filed under this category
// consume the month's free-to-spend, independently of the above.
func (c Category) CountsTowardSpendingPlan() bool {
	return !c.ExcludedFromSpendingPlan && !c.IsTransfer()
}

// CountsAsIncomeOrExpense reports whether this row belongs in reports,
// watchlists and income/expense totals. The exclusions are listed flat so the
// whole rule reads at once.
//
// visibleAccountsOnly has no default: whether a closed account counts differs
// per screen.
func CountsAsIncomeOrExpense(p Posting, visibleAccountsOnly bool) bool {
	txn, account := p.Txn, p.Account

	if txn.IsDeleted {
		return false
	}
	// A forecast is in the ledger only to hold its slot on the Bills screen.
	if txn.IsEstimate {
		return false
	}
	if txn.ExcludedFromReports {
		return false
	}
	if account.ExcludedFromReports {
		return false
	}
	if account.IsIgnored {
		return false
	}
	if account.IsClosed && visibleAccountsOnly {
		return false
	}
	if p.HasCategory && p.Category.ExcludedFromReports {
		return false
	}
	if p.IsTransfer() {
		return false
	}
	// Opening balances and asset revaluations move net worth, not cash flow.
	if !txn.Source.IsCashFlow() {
		return false
	}
	return true
}

// CountsTowardSpendingPlan reports whether this row consumes this month's
// free-to-spend.
//
// Deliberately not CountsAsIncomeOrExpense: the flags are independent at every
// level. monthExcludedTxnIDs, the plan month's own exclusion list, may be nil.
func CountsTowardSpendingPlan(p Posting, monthExcludedTxnIDs map[ID]bool) bool {
	txn, account := p.Txn, p.Account

	if txn.IsDeleted {
		return false
	}
	// The Bills bucket already projects the occurrence from the series.
	if txn.IsEstimate {
		return false
	}
	if monthExcludedTxnIDs[txn.ID] {
		return false
	}
	if txn.ExcludedFromSpendingPlan {
		return false
	}
	if account.ExcludedFromSpendingPlan {
		return false
	}
	if account.IsIgnored {
		return false
	}
	if account.IsClosed {
		return false
	}
	// A loan or an asset is not spent from; a payment landing on a loan is the
	// far leg of a transfer whose near leg already counted.
	if !account.Kind.SpendsFrom() {
		return false
	}
	if p.HasCategory && p.Category.ExcludedFromSpendingPlan {
		return false
	}
	// A credit-card payment is a transfer: the purchase already counted.
	if p.IsTransfer() {
		return false
	}
	if !txn.Source.IsCashFlow() {
		return false
	}
	return true
}

// CountsTowardBalance reports whether money actually moved in this account.
// Report exclusions do not apply. Pending rows count (IsSettled excludes
// them); estimates do not, having moved nothing.
func CountsTowardBalance(p Posting) bool {
	return !p.Txn.IsDeleted && !p.Txn.IsEstimate
}

// IsSettled is the subset of CountsTowardBalance the bank has finished
// processing.
func IsSettled(p Posting) bool {
	return CountsTowardBalance(p) && !p.Txn.IsPending
}

// ReportingDate is which date a report should file this row under. The zero
// DateMode resolves to the posted date, as Transaction.ReportingDate does.
func ReportingDate(txn Transaction, mode DateMode) Date {
	return txn.ReportingDate(mode)
}

// ResolvePostings joins transactions to their account and category. An unknown
// account or category is an error, not a skipped row: dropping it turns a loud
// failure into a wrong total, and an unresolved transfer category would start
// counting as income or expense. The schema nulls deleted categories, so a
// dangling id means the caller passed a partial map.
func ResolvePostings(
	transactions []Transaction,
	accounts map[ID]Account,
	categories map[ID]Category,
) ([]Posting, error) {
	postings := make([]Posting, 0, len(transactions))
	for _, txn := range transactions {
		account, ok := accounts[txn.AccountID]
		if !ok {
			return nil, fmt.Errorf(
				"transaction %s references unknown account %s", txn.ID, txn.AccountID)
		}
		posting := Posting{Txn: txn, Account: account}
		if txn.CategoryID != "" {
			category, found := categories[txn.CategoryID]
			if !found {
				return nil, fmt.Errorf(
					"transaction %s references unknown category %s", txn.ID, txn.CategoryID)
			}
			posting.Category, posting.HasCategory = category, true
		}
		postings = append(postings, posting)
	}
	return postings, nil
}

// FindOrphanTransferLegs returns transfer legs whose partner no longer exists.
//
// Such a leg stays dropped from income and expense with nothing balancing it,
// so the money silently stops existing in the reports.
//
// Groups of one are orphans; a group of three or more is the same corruption
// and is reported whole so every leg can be released. Deleted rows do not
// count as a partner.
func FindOrphanTransferLegs(transactions []Transaction) []Transaction {
	// First-seen order, so a repair run twice reports the same rows.
	var pairs []ID
	groups := map[ID][]Transaction{}
	for _, txn := range transactions {
		if txn.TransferPairID == "" || txn.IsDeleted {
			continue
		}
		if _, seen := groups[txn.TransferPairID]; !seen {
			pairs = append(pairs, txn.TransferPairID)
		}
		groups[txn.TransferPairID] = append(groups[txn.TransferPairID], txn)
	}

	var orphans []Transaction
	for _, pair := range pairs {
		switch legs := groups[pair]; {
		case len(legs) == 1:
			orphans = append(orphans, legs[0])
		case len(legs) > 2:
			orphans = append(orphans, legs...)
		}
	}
	return orphans
}
