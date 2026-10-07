package domain

import (
	"sort"
)

// What the household's own history says a row's category should be, and how
// sure it is, so a model is asked only when the history does not settle it.
//
// The score is not a probability: it weighs how much of the weighted history
// agrees, how many rows agree, and how much was ever categorized at all.

// CategoryEvidence is one past row from the same payee.
type CategoryEvidence struct {
	// CategoryID is empty for an uncategorized row, which votes for nothing
	// but counts against confidence.
	CategoryID string
	// IsSplit is a row split across categories with every part filed. It
	// votes for none of them and is not uncategorized either: it counts
	// neither for nor against confidence. A split with a part still unfiled
	// is uncategorized evidence, CategoryID empty and IsSplit false.
	IsSplit bool
	// Similarity is on any scale; only the ratio to the best row matters.
	Similarity int
	// AgeDays is negative for a row dated after the one being decided.
	AgeDays int
	// IsReviewed is ReviewedByHousehold. ReviewedOnArrival is a row that
	// carries the flag without anybody here having set it.
	IsReviewed        bool
	ReviewedOnArrival bool
	// IsTransfer is a transfer in either sense, not the pair id alone:
	// otherwise a payee filed as a transfer but never matched would vote for
	// the transfer category as if it were an ordinary one.
	IsTransfer bool
	IsCharge   bool
	// IsPurchase and PurchaseAgeDays are OriginalPurchaseAge of this row for
	// the credit being decided; TxnID names it.
	IsPurchase      bool
	PurchaseAgeDays int
	TxnID           string
}

type CategoryVerdict struct {
	Kind       string
	CategoryID string
	// Confidence is 0..1.
	Confidence float64
	// Agreeing is how many categorized rows voted for the winner, of Voters.
	Agreeing int
	Voters   int
	// Uncategorized, Transfers and Splits are out of Total.
	Uncategorized int
	Transfers     int
	Splits        int
	Total         int
	// LeftAlone counts uncategorized non-transfer rows the household reviewed
	// and kept that way; ArrivedReviewed those that came marked reviewed and
	// AwaitingReview those nobody has reviewed. None of them forbids a
	// category: they are weak evidence, LeftAlone the strongest of the three.
	LeftAlone       int
	ArrivedReviewed int
	AwaitingReview  int
	// Shares are largest first.
	Shares []CategoryShare
	// KindConflict marks a winner that runs the wrong way for the money.
	KindConflict bool
	// IsRefund marks a verdict only the payee's charges voted on
	// (VoteRefundCategory). RefundOf is the purchase it gives back, if any.
	IsRefund bool
	RefundOf string
}

type CategoryShare struct {
	CategoryID string
	Share      float64
	Rows       int
}

// WithKindCheck downgrades a confident verdict whose winning category runs the
// wrong way for the money: often a misfiring rule, but a refund is legitimately
// this shape, so the verdict is only stopped from acting alone.
//
// A refund verdict is read in the direction of the charges that voted for it.
func (v CategoryVerdict) WithKindCheck(amountPositive, categoryIsIncome bool) CategoryVerdict {
	if v.Kind != CategoryVerdictConfident || v.CategoryID == "" {
		return v
	}
	if v.IsRefund {
		amountPositive = false
	}
	if amountPositive != categoryIsIncome {
		v.Kind = CategoryVerdictUncertain
		v.KindConflict = true
	}
	return v
}

const (
	// CategoryVerdictNoHistory: no past rows with a category.
	CategoryVerdictNoHistory = "no_history"
	CategoryVerdictTransfer  = "transfer"
	CategoryVerdictConfident = "confident"
	CategoryVerdictUncertain = "uncertain"
)

// ReviewedByHousehold reports whether a row's reviewed flag is somebody here
// deciding about it. An import carries the flag from the file, and an account
// whose rows are born reviewed (AccountKind.BornReviewed) sets it on every row
// however it arrives, so neither says anything about the category.
func ReviewedByHousehold(isReviewed bool, source Source, account AccountKind) bool {
	if !isReviewed || account.BornReviewed() {
		return false
	}
	return source != SourceSimplifiImport && source != SourceFileImport
}

// ReviewedCategoryStands reports whether the row being decided carries a
// reviewed flag that is a decision about its own category. Unlike
// ReviewedByHousehold, an import's flag counts: a row reviewed in the
// application it was exported from was looked at there. Only an account whose
// rows are born reviewed says nothing.
func ReviewedCategoryStands(isReviewed bool, account AccountKind) bool {
	return isReviewed && !account.BornReviewed()
}

// The row's own category as a category suggestion is told it. Neither is
// taken as fact; the reviewed one sets a high bar for a different answer.
const (
	CategoryStandingReviewed = "Reviewed: somebody looked at this row and kept this category. " +
		"It stands. Propose a different one only when the evidence plainly shows it is wrong " +
		"(money the wrong way for it, the items of an order behind the charge, a correction " +
		"or the guidance naming this case), never because the history or the wording leans " +
		"another way."
	CategoryStandingUnreviewed = "Not reviewed: the category came with the row (a rule, the " +
		"bank or an import) and nobody has confirmed it. Weigh it as one piece of evidence, " +
		"not as the answer."
)

// CategoryStanding is CategoryStandingReviewed or CategoryStandingUnreviewed
// for a row with a category, and blank for one without.
func CategoryStanding(categorized, reviewedStands bool) string {
	switch {
	case !categorized:
		return ""
	case reviewedStands:
		return CategoryStandingReviewed
	default:
		return CategoryStandingUnreviewed
	}
}

const (
	// categoryVoteFullAgreementRows is where count stops adding confidence.
	categoryVoteFullAgreementRows = 5
	categoryVoteTransferShare     = 0.6
	categoryVoteRecentDays        = 180
	categoryVoteStaleDays         = 730
)

// VoteCategory weighs the payee's history and returns a verdict. threshold is
// the automation's own setting, at or above which the history acts without a
// model.
func VoteCategory(evidence []CategoryEvidence, threshold float64) CategoryVerdict {
	verdict := CategoryVerdict{Kind: CategoryVerdictNoHistory, Total: len(evidence)}
	if len(evidence) == 0 {
		return verdict
	}

	best := 0
	for _, one := range evidence {
		if one.Similarity > best {
			best = one.Similarity
		}
		switch {
		case one.IsTransfer:
			verdict.Transfers++
		case one.IsSplit:
			verdict.Splits++
		}
		if one.CategoryID == "" && !one.IsSplit {
			verdict.Uncategorized++
		}
	}
	if float64(verdict.Transfers)/float64(len(evidence)) >= categoryVoteTransferShare {
		verdict.Kind = CategoryVerdictTransfer
		verdict.Confidence = float64(verdict.Transfers) / float64(len(evidence))
		return verdict
	}
	if best == 0 {
		best = 1
	}

	weights := map[string]float64{}
	rows := map[string]int{}
	total := 0.0
	for _, one := range evidence {
		if one.CategoryID == "" && !one.IsTransfer && !one.IsSplit {
			switch {
			case one.IsReviewed:
				verdict.LeftAlone++
			case one.ReviewedOnArrival:
				verdict.ArrivedReviewed++
			default:
				verdict.AwaitingReview++
			}
		}
		if one.CategoryID == "" || one.IsTransfer {
			continue
		}
		weight := float64(one.Similarity) / float64(best)
		switch {
		case one.AgeDays <= categoryVoteRecentDays:
		case one.AgeDays <= categoryVoteStaleDays:
			weight *= 0.7
		default:
			weight *= 0.5
		}
		if one.IsReviewed {
			weight *= 1.25
		}
		weights[one.CategoryID] += weight
		rows[one.CategoryID]++
		verdict.Voters++
		total += weight
	}
	if verdict.Voters == 0 {
		return verdict
	}

	for id, weight := range weights {
		verdict.Shares = append(verdict.Shares, CategoryShare{
			CategoryID: id, Share: weight / total, Rows: rows[id],
		})
	}
	sort.Slice(verdict.Shares, func(i, j int) bool {
		if verdict.Shares[i].Share != verdict.Shares[j].Share {
			return verdict.Shares[i].Share > verdict.Shares[j].Share
		}
		return verdict.Shares[i].CategoryID < verdict.Shares[j].CategoryID
	})
	top := verdict.Shares[0]
	verdict.CategoryID = top.CategoryID
	verdict.Agreeing = top.Rows

	// Each factor is in 0..1, so the product is too.
	count := float64(top.Rows) / categoryVoteFullAgreementRows
	if count > 1 {
		count = 1
	}
	coverage := float64(verdict.Voters) / float64(len(evidence)-verdict.Transfers-verdict.Splits)
	verdict.Confidence = top.Share * count * coverage

	verdict.Kind = CategoryVerdictUncertain
	if threshold > 0 && verdict.Confidence >= threshold {
		verdict.Kind = CategoryVerdictConfident
	}
	return verdict
}

// VoteRefundCategory decides a credit from the purchases it might be giving
// back, and reports whether the payee's history is a merchant's at all. The
// ordinary vote's direction check would hold every refund back.
//
//   - A merchant's charges outnumber its credits, transfers aside; otherwise
//     false is returned and the ordinary vote stands.
//   - The charges alone vote.
//   - A categorized clear original purchase (OriginalPurchaseAge) wins
//     outright (nearest, then lowest id). A threshold of zero still keeps it
//     from acting alone.
func VoteRefundCategory(evidence []CategoryEvidence, threshold float64) (CategoryVerdict, bool) {
	var charges []CategoryEvidence
	credits := 0
	for _, one := range evidence {
		switch {
		case one.IsTransfer:
		case one.IsCharge:
			charges = append(charges, one)
		default:
			credits++
		}
	}
	if len(charges) == 0 || len(charges) <= credits {
		return CategoryVerdict{}, false
	}

	verdict := VoteCategory(charges, threshold)
	verdict.IsRefund = true

	var purchase *CategoryEvidence
	for i := range charges {
		one := &charges[i]
		if one.CategoryID == "" || !one.IsPurchase {
			continue
		}
		if purchase == nil || one.PurchaseAgeDays < purchase.PurchaseAgeDays ||
			(one.PurchaseAgeDays == purchase.PurchaseAgeDays && one.TxnID < purchase.TxnID) {
			purchase = one
		}
	}
	if purchase != nil {
		verdict.CategoryID = purchase.CategoryID
		verdict.RefundOf = purchase.TxnID
		verdict.Kind = CategoryVerdictUncertain
		if threshold > 0 {
			verdict.Kind = CategoryVerdictConfident
		}
	}
	return verdict, true
}
