package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	envGroceriesID ID = "env-groceries"
	envDiningID    ID = "env-dining"
)

var envCreatedEarly = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

var envGroceriesCategory = Category{ID: "cat-groceries", Name: "Groceries", Kind: CategoryExpense}

func envEnvelope(mutate ...func(*Envelope)) Envelope {
	envelope := Envelope{
		ID:           envGroceriesID,
		Name:         "Groceries",
		FilterID:     "filter-groceries",
		TargetAmount: MustFromString("400"),
		Recurring:    true,
		CreatedAt:    envCreatedEarly,
	}
	for _, apply := range mutate {
		apply(&envelope)
	}
	return envelope
}

func envSpend(txnID, amount string) Posting {
	account := Account{ID: "acct-1", Name: "Checking 1", Kind: KindCash}
	return Posting{
		Txn: Transaction{
			ID:            ID(txnID),
			AccountID:     account.ID,
			Date:          NewDate(2026, time.August, 15),
			Amount:        MustFromString(amount),
			StatementName: "SQ *COFFEE 1234",
			Payee:         "Coffee",
			CategoryID:    envGroceriesCategory.ID,
			Source:        SourceSync,
		},
		Account:     account,
		Category:    envGroceriesCategory,
		HasCategory: true,
	}
}

var envHomeCategory = Category{ID: "cat-home", Name: "Home Improvement", Kind: CategoryExpense}

type envShare = struct {
	Category Category
	Amount   string
}

func share(category Category, amount string) envShare {
	return envShare{Category: category, Amount: amount}
}

func envPart(txnID, amount string) Part {
	return PartsOf(envSpend(txnID, amount), nil)[0]
}

func envParts(txnID string, shares ...struct {
	Category Category
	Amount   string
}) []Part {
	posting := envSpend(txnID, "0")
	posting.HasCategory, posting.Txn.CategoryID = false, ""
	out := make([]Part, 0, len(shares))
	for i, share := range shares {
		split := Split{
			ID:         ID(txnID + "-s" + string(rune('1'+i))),
			Amount:     MustFromString(share.Amount),
			CategoryID: share.Category.ID,
		}
		posting.Txn.Splits = append(posting.Txn.Splits, split)
		out = append(out, NewPart(posting,
			Match{Posting: posting, Split: &split, Amount: split.Amount},
			map[ID]Category{share.Category.ID: share.Category}))
	}
	return out
}

// envMatcher stands in for the filter engine: envelope id -> the part keys it
// claims. A bare transaction id names an unsplit row, whose part key is the id.
func envMatcher(claims map[ID][]ID) EnvelopeMatcher {
	return func(envelope Envelope, part Part) bool {
		for _, key := range claims[envelope.ID] {
			if key == part.Key() {
				return true
			}
		}
		return false
	}
}

func TestAnOverwrittenTargetWinsOverTheStoredOne(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) {
		e.OverwrittenTargetAmount = MustFromString("250")
		e.HasOverwrittenTarget = true
	})
	require.Equal(t, "250.00", envelope.Target().String())
}

func TestAnOverwrittenTargetOfZeroIsAnOverrideNotAnAbsence(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) {
		e.OverwrittenTargetAmount = Zero
		e.HasOverwrittenTarget = true
	})
	require.Equal(t, "0.00", envelope.Target().String())
}

func TestEnvelopeSpendIsPositiveHoweverTheTransactionsAreSigned(t *testing.T) {
	status := EnvelopeStatusFor(envEnvelope(), []Part{envPart("t1", "-120"), envPart("t2", "-30.50")})

	require.Equal(t, "150.50", status.Spent.String())
	require.Equal(t, "249.50", status.Available().String())
	require.Equal(t, []ID{"t1", "t2"}, status.TxnIDs)
}

func TestARefundInsideTheEnvelopeGivesTheMoneyBack(t *testing.T) {
	// Nets refunds: the departure from calculations.md §5 recorded in §13.
	status := EnvelopeStatusFor(envEnvelope(), []Part{envPart("t1", "-120"), envPart("t2", "20")})

	require.Equal(t, "100.00", status.Spent.String())
}

func TestPctUsedDividesByTargetPlusRollover(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.RolloverIn = MustFromString("100") })
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-250")})

	require.Equal(t, "500.00", status.Budget().String())
	require.Equal(t, "50.00", status.PctUsed().StringFixed(2))
}

func TestAZeroTargetWithSpendIsOneHundredPercentNotADivideByZero(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.TargetAmount = Zero })
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-50")})

	require.Equal(t, "100.00", status.PctUsed().StringFixed(2))
	require.Equal(t, "-50.00", status.Available().String())
	require.Equal(t, EnvelopeOverspent, status.State())
}

func TestAZeroTargetWithNoSpendIsZeroPercentUsed(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.TargetAmount = Zero })
	status := EnvelopeStatusFor(envelope, nil)

	require.Equal(t, "0.00", status.PctUsed().StringFixed(2))
	require.Equal(t, EnvelopeNormal, status.State())
}

func TestPctUsedHasNoUpperBoundButTheBarFillsAtOneHundred(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.TargetAmount = MustFromString("100") })
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-250")})

	require.Equal(t, "250.00", status.PctUsed().StringFixed(2))
	require.Equal(t, "100.00", status.BarPct().StringFixed(2))
}

func TestAnEnvelopeCarryingRolloverSaysSo(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.RolloverIn = MustFromString("50") })
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-20")})

	require.Equal(t, EnvelopeWithRollover, status.State())
}

func TestOverspendingWinsOverCarryingRollover(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) {
		e.TargetAmount = MustFromString("100")
		e.RolloverIn = MustFromString("50")
	})
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-200")})

	require.Equal(t, "-50.00", status.Available().String())
	require.Equal(t, EnvelopeOverspent, status.State())
}

func TestATransactionMatchingTwoEnvelopesBelongsToTheOlderOne(t *testing.T) {
	older := envEnvelope()
	newer := envEnvelope(func(e *Envelope) {
		e.ID = envDiningID
		e.Name = "Dining"
		e.CreatedAt = time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	})
	claims := envMatcher(map[ID][]ID{envGroceriesID: {"t-both"}, envDiningID: {"t-both"}})

	assignment := AssignEnvelopes([]Envelope{newer, older}, []Part{envPart("t-both", "-40")}, claims)

	require.Equal(t, map[ID]ID{"t-both": envGroceriesID}, assignment.EnvelopeByPart)
	require.Empty(t, assignment.PartsByEnvelope[envDiningID])
	require.Equal(t, map[ID][]ID{"t-both": {envDiningID}}, assignment.Contested)
}

func TestCreationOrderDecidesEvenWhenTheEnvelopesArriveReversed(t *testing.T) {
	older := envEnvelope()
	newer := envEnvelope(func(e *Envelope) {
		e.ID = envDiningID
		e.Name = "Dining"
		e.CreatedAt = time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	})
	claims := envMatcher(map[ID][]ID{envGroceriesID: {"t-both"}, envDiningID: {"t-both"}})

	forward := AssignEnvelopes([]Envelope{older, newer}, []Part{envPart("t-both", "-40")}, claims)
	backward := AssignEnvelopes([]Envelope{newer, older}, []Part{envPart("t-both", "-40")}, claims)

	require.Equal(t, forward.EnvelopeByPart, backward.EnvelopeByPart)
}

func TestEnvelopesCreatedAtTheSameInstantBreakTheTieOnID(t *testing.T) {
	first := envEnvelope(func(e *Envelope) { e.ID = "env-a" })
	second := envEnvelope(func(e *Envelope) { e.ID = "env-b" })
	claims := envMatcher(map[ID][]ID{"env-a": {"t-both"}, "env-b": {"t-both"}})

	assignment := AssignEnvelopes([]Envelope{second, first}, []Part{envPart("t-both", "-40")}, claims)

	require.Equal(t, map[ID]ID{"t-both": "env-a"}, assignment.EnvelopeByPart)
}

func TestAnUnmatchedTransactionIsClaimedByNobody(t *testing.T) {
	claims := envMatcher(map[ID][]ID{envGroceriesID: {"t-other"}})

	assignment := AssignEnvelopes([]Envelope{envEnvelope()}, []Part{envPart("t-loose", "-40")}, claims)

	require.False(t, assignment.Claimed("t-loose"))
	require.Empty(t, assignment.Contested)
}

func TestStatusesComeBackInResolutionOrderWithTheirOwnRows(t *testing.T) {
	older := envEnvelope()
	newer := envEnvelope(func(e *Envelope) {
		e.ID = envDiningID
		e.Name = "Dining"
		e.CreatedAt = time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	})
	parts := []Part{envPart("t-g", "-30"), envPart("t-d", "-45")}
	claims := envMatcher(map[ID][]ID{envGroceriesID: {"t-g"}, envDiningID: {"t-d"}})
	envelopes := []Envelope{newer, older}

	statuses := EnvelopeStatusesFor(envelopes, AssignEnvelopes(envelopes, parts, claims))

	require.Equal(t, []ID{envGroceriesID, envDiningID}, []ID{statuses[0].EnvelopeID, statuses[1].EnvelopeID})
	require.Equal(t, "30.00", statuses[0].Spent.String())
	require.Equal(t, "45.00", statuses[1].Spent.String())
}

func TestSettingTheRolloverAmountReplacesItOutright(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.RolloverIn = MustFromString("50") })

	changed := SetRollover(envelope, MustFromString("125"))

	require.Equal(t, "125.00", changed.RolloverIn.String())
}

func TestReleasingRolloverClearsItAndHandsBackWhatWasReleased(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.RolloverIn = MustFromString("50") })

	emptied, released := ReleaseRollover(envelope)

	require.True(t, emptied.RolloverIn.IsZero())
	require.Equal(t, "50.00", released.String())
}

func TestUnspentFundsCarryToNextMonth(t *testing.T) {
	envelope := envEnvelope()
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-150")})

	require.Equal(t, "250.00", CarriedRollover(envelope, status).String())
}

func TestAutoReleaseCarriesNothingBecauseItAlreadyWentBack(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.AutoReleaseRollover = true })
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-150")})

	require.True(t, CarriedRollover(envelope, status).IsZero())
}

func TestAnOverspentEnvelopeStartsNextMonthInTheHole(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.TargetAmount = MustFromString("100") })
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-150")})

	require.Equal(t, "-50.00", CarriedRollover(envelope, status).String())
}

func TestRollingForwardCarriesTheBalanceAndDropsTheTargetOverride(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) {
		e.OverwrittenTargetAmount = MustFromString("600")
		e.HasOverwrittenTarget = true
	})
	status := EnvelopeStatusFor(envelope, []Part{envPart("t1", "-100")})

	next, ok := RollForward(envelope, status)

	require.True(t, ok)
	require.False(t, next.HasOverwrittenTarget)
	require.Equal(t, "400.00", next.Target().String())
	require.Equal(t, "500.00", next.RolloverIn.String())
	require.True(t, next.RolloverCarried, "the carried figure follows the prior month until the user sets one")
	require.Equal(t, envGroceriesID, next.Group(), "every month's copy shares the first month's id as its group")
}

func TestSettingOrReleasingRolloverMakesItTheUsers(t *testing.T) {
	carried := envEnvelope(func(e *Envelope) { e.RolloverCarried = true })

	set := SetRollover(carried, MustFromString("10"))
	released, _ := ReleaseRollover(carried)

	require.False(t, set.RolloverCarried)
	require.False(t, released.RolloverCarried)
}

func TestAThisMonthOnlyEnvelopeDoesNotRollForward(t *testing.T) {
	envelope := envEnvelope(func(e *Envelope) { e.Recurring = false })
	status := EnvelopeStatusFor(envelope, nil)

	_, ok := RollForward(envelope, status)

	require.False(t, ok)
}

func TestOnlyTheSplitAnEnvelopeMatchesIsChargedToIt(t *testing.T) {
	envelope := envEnvelope()
	parts := envParts("t-costco",
		share(envGroceriesCategory, "-240.00"),
		share(envHomeCategory, "-100.00"))
	claims := envMatcher(map[ID][]ID{envGroceriesID: {parts[0].Key()}})

	assignment := AssignEnvelopes([]Envelope{envelope}, parts, claims)
	statuses := EnvelopeStatusesFor([]Envelope{envelope}, assignment)

	require.Equal(t, "240.00", statuses[0].Spent.String(),
		"the envelope was charged the whole receipt")
	require.False(t, assignment.Claimed(parts[1].Key()),
		"the Home Improvement share belongs to no envelope and must reach Other Spend")
}

func TestTwoEnvelopesEachTakeTheirOwnSplitOfOneRow(t *testing.T) {
	gaming := envEnvelope(func(e *Envelope) { e.ID = envGroceriesID; e.Name = "Gaming" })
	gifts := envEnvelope(func(e *Envelope) {
		e.ID = envDiningID
		e.Name = "Gifts"
		e.CreatedAt = time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	})
	parts := envParts("t-shared",
		share(envGroceriesCategory, "-30.00"),
		share(envHomeCategory, "-30.00"))
	claims := envMatcher(map[ID][]ID{
		envGroceriesID: {parts[0].Key()},
		envDiningID:    {parts[1].Key()},
	})

	envelopes := []Envelope{gaming, gifts}
	statuses := EnvelopeStatusesFor(envelopes, AssignEnvelopes(envelopes, parts, claims))

	require.Equal(t, "30.00", statuses[0].Spent.String(), "Gaming")
	require.Equal(t, "30.00", statuses[1].Spent.String(), "Gifts")
	require.Equal(t, []ID{"t-shared"}, statuses[0].TxnIDs)
	require.Equal(t, []ID{"t-shared"}, statuses[1].TxnIDs)
}

func TestOneSplitContestedByTwoEnvelopesStillGoesToExactlyOne(t *testing.T) {
	older := envEnvelope()
	newer := envEnvelope(func(e *Envelope) {
		e.ID = envDiningID
		e.CreatedAt = time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	})
	parts := envParts("t-row",
		share(envGroceriesCategory, "-30.00"),
		share(envHomeCategory, "-70.00"))
	claims := envMatcher(map[ID][]ID{
		envGroceriesID: {parts[0].Key()},
		envDiningID:    {parts[0].Key()},
	})

	envelopes := []Envelope{newer, older}
	assignment := AssignEnvelopes(envelopes, parts, claims)
	statuses := EnvelopeStatusesFor(envelopes, assignment)

	require.Equal(t, "30.00", statuses[0].Spent.String())
	require.Equal(t, "0.00", statuses[1].Spent.String())
	require.Equal(t, map[ID][]ID{"t-row": {envDiningID}},
		assignment.ContestedByTxn(parts), "the loser is named against the row")
	require.False(t, assignment.Claimed(parts[1].Key()))
}

func TestTheChargedPartsAddUpToWhatTheEnvelopeSaysItSpent(t *testing.T) {
	// The list under an envelope is drawn from Parts and the figure above it is
	// Spent, so the two have to agree.
	envelope := envEnvelope()
	parts := envParts("t-costco",
		share(envGroceriesCategory, "-240.00"),
		share(envHomeCategory, "-100.00"))
	claims := envMatcher(map[ID][]ID{envGroceriesID: {parts[0].Key()}})

	statuses := EnvelopeStatusesFor([]Envelope{envelope},
		AssignEnvelopes([]Envelope{envelope}, parts, claims))

	require.Len(t, statuses[0].Parts, 1, "the Home Improvement share is not this envelope's")
	require.Equal(t, "-240.00", statuses[0].Parts[0].Amount.String(),
		"the row's own total would be 340.00")
	require.Equal(t, ID("cat-groceries"), statuses[0].Parts[0].CategoryID,
		"the split's category, not its parent row's")
	require.Equal(t, statuses[0].Spent.String(),
		Total(statuses[0].Parts[0].Amount).Neg().String())
}

func TestTwoSharesOfOneRowStayTwoRowsUnderTheEnvelope(t *testing.T) {
	envelope := envEnvelope()
	parts := envParts("t-target",
		share(envGroceriesCategory, "-55.50"),
		share(envGroceriesCategory, "-55.50"))
	claims := envMatcher(map[ID][]ID{envGroceriesID: {parts[0].Key(), parts[1].Key()}})

	statuses := EnvelopeStatusesFor([]Envelope{envelope},
		AssignEnvelopes([]Envelope{envelope}, parts, claims))

	require.Equal(t, []ID{"t-target"}, statuses[0].TxnIDs, "one row")
	require.Equal(t, []ID{parts[0].Key(), parts[1].Key()},
		[]ID{statuses[0].Parts[0].Key(), statuses[0].Parts[1].Key()}, "two lines")
	require.Equal(t, "111.00", statuses[0].Spent.String())
	require.Equal(t, statuses[0].Spent.String(),
		Total(statuses[0].Parts[0].Amount, statuses[0].Parts[1].Amount).Neg().String())
}

func TestAnUnsplitRowIsOnePartCarryingTheWholeAmount(t *testing.T) {
	status := EnvelopeStatusFor(envEnvelope(), []Part{envPart("t1", "-120")})

	require.Len(t, status.Parts, 1)
	require.Empty(t, status.Parts[0].SplitID)
	require.Equal(t, ID("t1"), status.Parts[0].Key(), "no split, so the key is the row")
	require.Equal(t, "-120.00", status.Parts[0].Amount.String())
}
func TestAnUnsplitRowIsOnePart(t *testing.T) {
	envelope := envEnvelope()
	parts := []Part{envPart("t-plain", "-40.00")}
	claims := envMatcher(map[ID][]ID{envGroceriesID: {"t-plain"}})

	assignment := AssignEnvelopes([]Envelope{envelope}, parts, claims)
	statuses := EnvelopeStatusesFor([]Envelope{envelope}, assignment)

	require.Equal(t, "40.00", statuses[0].Spent.String())
	require.Equal(t, []ID{"t-plain"}, statuses[0].TxnIDs)
	require.True(t, assignment.Claimed("t-plain"),
		"an unsplit row's part key is still its transaction id")
}
