package service

import (
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// syncPendingSettleWindowDays is how far apart a pending authorization and its
// posted charge may be dated and still be taken for one purchase. A wrong pairing
// swallows a transaction rather than showing as a duplicate, so the window stays
// well short of where date and amount stop being evidence.
const syncPendingSettleWindowDays = 7

// claimPool is the rows already in the ledger that an incoming bank row may be
// matched to by content, each claimable once: four identical $45 charges on one
// day are four charges, and a set keyed on content would drop three of them.
// Rows this sync just inserted never join the pool.
type claimPool struct {
	byContent map[string][]*store.Transaction
	// pending is the subset an incoming posted charge may settle: pending, live, and
	// without an aggregator id. Rows with an id are settled by settleExisting and
	// retired by retireVanishedPendings; neither can reach a row without one.
	pending []*store.Transaction
	claimed map[uuid.UUID]bool
}

func newClaimPool(existing []store.Transaction) *claimPool {
	pool := &claimPool{
		byContent: make(map[string][]*store.Transaction, len(existing)),
		claimed:   map[uuid.UUID]bool{},
	}
	for i := range existing {
		row := &existing[i]
		key := fingerprint(row.Date, row.Amount, row.StatementName)
		pool.byContent[key] = append(pool.byContent[key], row)
		if row.IsPending && !row.IsDeleted && row.ExternalID == "" {
			pool.pending = append(pool.pending, row)
		}
	}
	return pool
}

// claimByContent hands out a row dated, worded and amounted exactly like the
// incoming one, if an unclaimed one is left.
func (p *claimPool) claimByContent(row provider.Transaction) (*store.Transaction, bool) {
	for _, candidate := range p.byContent[fingerprint(row.Date, row.Amount, row.StatementName)] {
		if p.claimed[candidate.ID] {
			continue
		}
		p.claimed[candidate.ID] = true
		return candidate, true
	}
	return nil, false
}

// claimSettledPending hands out the pending row an incoming posted charge
// settles: same amount to the cent, within the window, nearest day first. The
// wording is deliberately not compared, because the pending and posted spellings
// of one charge differ.
func (p *claimPool) claimSettledPending(row provider.Transaction) (*store.Transaction, bool) {
	if row.Pending {
		return nil, false
	}
	var best *store.Transaction
	bestGap := 0
	for _, candidate := range p.pending {
		if p.claimed[candidate.ID] || !candidate.Amount.Equal(row.Amount) {
			continue
		}
		gap := absInt(domain.DaysBetween(candidate.Date, row.Date))
		if gap > syncPendingSettleWindowDays {
			continue
		}
		if best == nil || gap < bestGap {
			best, bestGap = candidate, gap
		}
	}
	if best == nil {
		return nil, false
	}
	p.claimed[best.ID] = true
	return best, true
}
