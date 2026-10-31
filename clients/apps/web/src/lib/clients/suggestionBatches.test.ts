import { describe, expect, it } from 'vitest'

import {
  BULK_SUGGESTION_ROWS,
  asksFirst,
  batchComparison,
  batchHeadline,
  bulkSuggestionPrompt,
  undeterminedIn,
  type SuggestionBatch,
  type SuggestionTally,
} from './suggestionBatches'

const NONE: SuggestionTally = {
  agreed: 0,
  differs: 0,
  unsure: 0,
  suggested: 0,
  undetermined: 0,
  skipped: 0,
  failed: 0,
}

function batch(overrides: Partial<SuggestionBatch> = {}): SuggestionBatch {
  return {
    id: 'b1',
    created_at: '2026-09-30T10:00:00Z',
    cancelled: false,
    dismissed: false,
    rows: 0,
    done: 0,
    pending: 0,
    not_run: 0,
    finished: true,
    reviewed: NONE,
    unreviewed: NONE,
    ...overrides,
  }
}

describe('asking before a bulk request', () => {
  it('asks only above the bulk size', () => {
    expect(BULK_SUGGESTION_ROWS).toBe(200)
    expect(asksFirst(200)).toBe(false)
    expect(asksFirst(201)).toBe(true)
    expect(asksFirst(1)).toBe(false)
  })

  it('names the count and what it costs', () => {
    const prompt = bulkSuggestionPrompt(4310)
    expect(prompt.title).toBe('Suggest categories for 4,310 transactions?')
    expect(prompt.description).toContain('can take hours')
    expect(prompt.description).toContain('keeps the model busy')
    expect(prompt.confirmLabel).toBe('Suggest for 4,310')
  })
})

describe('batchHeadline', () => {
  it('counts off a batch still running', () => {
    expect(batchHeadline(batch({ rows: 4310, done: 1207, pending: 3103, finished: false }))).toBe(
      'Suggesting categories — 1,207 of 4,310',
    )
  })

  it('says a finished batch is done', () => {
    expect(batchHeadline(batch({ rows: 3, done: 3 }))).toBe('Suggestions done for 3 rows')
  })

  it('says how far a cancelled batch got, not counting the rows it dropped', () => {
    expect(batchHeadline(batch({ rows: 900, done: 900, not_run: 760, cancelled: true }))).toBe(
      'Stopped after 140 of 900 rows',
    )
  })
})

describe('batchComparison', () => {
  it('compares the reviewed and unreviewed rows apart', () => {
    const summary = batchComparison(
      batch({
        rows: 120,
        done: 120,
        reviewed: { ...NONE, agreed: 70, differs: 6, unsure: 4 },
        unreviewed: { ...NONE, agreed: 25, differs: 9, unsure: 1, suggested: 3, skipped: 2 },
      }),
    )
    expect(summary).toBe(
      'Agreed with 95 of 115 categories already set: 70 of 80 reviewed, 25 of 35 unreviewed. ' +
        '15 differ (6 reviewed), 3 suggested for uncategorized rows, 5 unsure, 2 skipped.',
    )
  })

  it('leaves out the split when only one side had categories', () => {
    expect(
      batchComparison(batch({ rows: 4, done: 4, unreviewed: { ...NONE, agreed: 3, differs: 1 } })),
    ).toBe('Agreed with 3 of 4 categories already set. 1 differs.')
  })

  it('reports a batch over uncategorized rows without a comparison', () => {
    expect(
      batchComparison(
        batch({ rows: 7, done: 7, not_run: 2, unreviewed: { ...NONE, suggested: 4, failed: 1 } }),
      ),
    ).toBe('4 suggested for uncategorized rows, 1 failed, 2 not run.')
  })

  it('says nothing before anything has finished', () => {
    expect(batchComparison(batch({ rows: 9, finished: false, pending: 9 }))).toBeNull()
  })
})

describe('undeterminedIn', () => {
  it('counts the rows that needed a category on both sides', () => {
    expect(
      undeterminedIn(
        batch({
          reviewed: { ...NONE, undetermined: 2 },
          unreviewed: { ...NONE, undetermined: 5, unsure: 4 },
        }),
      ),
    ).toBe(7)
  })
})
