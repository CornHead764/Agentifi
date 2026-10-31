/**
 * `fromFilterItems(toFilterItems(draft))` must equal `draft`, or the unsaved
 * dot lights on an untouched report. The non-literal encodings break first:
 * "has no tag" over every tag, expanded parent categories, and
 * uncategorized stored over every category.
 */

import { describe, expect, it } from 'vitest'

import { EMPTY_DRAFT, toFilterItems, type FilterDraft } from '@/lib/transactions/filter'
import type { Tag } from '@/lib/transactions/types'
import { category } from '@/test/builders'

import { fromFilterItems, splitSavedSearch } from './savedFilter'

const UNIVERSE = {
  categories: [
    category('auto', 'Auto & Transport'),
    category('gas', 'Gas & Fuel', 'auto'),
    category('food', 'Food & Dining'),
  ],
  tags: [
    { id: 'work', name: 'Work', color: null },
    { id: 'home', name: 'Home', color: null },
  ] satisfies Tag[],
}

/** What the round trip has to reproduce: the draft after one encode. */
function roundTrip(draft: FilterDraft): FilterDraft {
  return fromFilterItems(toFilterItems(draft, UNIVERSE), UNIVERSE)
}

describe('a saved report reopened', () => {
  it('is not reported as edited when nothing was touched', () => {
    const draft: FilterDraft = {
      ...EMPTY_DRAFT,
      // Already expanded, which is what comes back off the wire.
      categories: { ids: ['auto', 'gas'], negated: false },
      payees: { values: ['Shell'], negated: false },
      accounts: { ids: ['a1'], negated: false },
      isBillOrSubscription: true,
      excludedFromReports: false,
      texts: ['fuel'],
    }
    expect(roundTrip(draft)).toEqual(draft)
  })

  it('keeps an amount band and a date preset', () => {
    const draft: FilterDraft = {
      ...EMPTY_DRAFT,
      amount: { min: '100.00', max: '500.00' },
      date: { from: null, to: null, preset: 'this-month' },
    }
    expect(roundTrip(draft)).toEqual(draft)
  })

  it('round-trips "uncategorized" through its own field', () => {
    const draft: FilterDraft = { ...EMPTY_DRAFT, uncategorized: true }
    expect(toFilterItems(draft, UNIVERSE)[0].field).toBe('is_uncategorized')
    expect(roundTrip(draft)).toEqual(draft)
  })

  it('round-trips a waiting suggestion both ways round, beside the review state', () => {
    for (const hasCategorySuggestion of [true, false]) {
      const draft: FilterDraft = { ...EMPTY_DRAFT, isReviewed: false, hasCategorySuggestion }
      expect(toFilterItems(draft, UNIVERSE)).toContainEqual(
        expect.objectContaining({
          field: 'has_category_suggestion',
          operator: 'is_true',
          state: hasCategorySuggestion,
        }),
      )
      expect(roundTrip(draft)).toEqual(draft)
    }
  })

  it('round-trips the attachment question both ways round', () => {
    for (const hasAttachment of [true, false]) {
      const draft: FilterDraft = { ...EMPTY_DRAFT, hasAttachment }
      expect(toFilterItems(draft, UNIVERSE)).toEqual([
        expect.objectContaining({ field: 'has_attachment', operator: 'is_true', state: hasAttachment }),
      ])
      expect(roundTrip(draft)).toEqual(draft)
    }
    expect(toFilterItems(EMPTY_DRAFT, UNIVERSE)).toEqual([])
  })

  it('round-trips the missing receipt question both ways round', () => {
    for (const missingReceipt of [true, false]) {
      const draft: FilterDraft = { ...EMPTY_DRAFT, missingReceipt }
      expect(toFilterItems(draft, UNIVERSE)).toEqual([
        expect.objectContaining({ field: 'is_missing_receipt', operator: 'is_true', state: missingReceipt }),
      ])
      expect(roundTrip(draft)).toEqual(draft)
    }
  })

  it('reads a filter with no attachment question as any', () => {
    const noAttachment = toFilterItems({ ...EMPTY_DRAFT, isReviewed: true }, UNIVERSE)
    expect(fromFilterItems(noAttachment, UNIVERSE).hasAttachment).toBeNull()
  })

  it('still reads "uncategorized" from a filter saved as "not any of every category"', () => {
    const stored = toFilterItems(
      { ...EMPTY_DRAFT, categories: { ids: ['auto', 'gas', 'food'], negated: true } },
      UNIVERSE,
    )
    expect(fromFilterItems(stored, UNIVERSE).uncategorized).toBe(true)
  })

  it('reads posted-only back as posted-only', () => {
    const posted: FilterDraft = { ...EMPTY_DRAFT, isPending: false }
    expect(roundTrip(posted)).toEqual(posted)
  })

  it('decodes "has no tag" without mistaking it for two chosen tags', () => {
    const none: FilterDraft = { ...EMPTY_DRAFT, hasTags: false }
    const any: FilterDraft = { ...EMPTY_DRAFT, hasTags: true }
    expect(roundTrip(none)).toEqual(none)
    expect(roundTrip(any)).toEqual(any)
  })

  it('keeps two chosen tags as two chosen tags', () => {
    // Encoded identically to "covers everything", so that reading wins.
    const draft: FilterDraft = { ...EMPTY_DRAFT, tags: { ids: ['work'], negated: false } }
    expect(roundTrip(draft)).toEqual(draft)
  })

  it('survives a universe that has not loaded, without inventing facets', () => {
    const draft: FilterDraft = {
      ...EMPTY_DRAFT,
      categories: { ids: ['auto'], negated: false },
    }
    const decoded = fromFilterItems(toFilterItems(draft, UNIVERSE), {
      categories: [],
      tags: [],
    })
    expect(decoded.uncategorized).toBe(false)
    expect(decoded.categories.ids).toEqual(['auto', 'gas'])
  })
})

describe('the search box on a reopened report', () => {
  it('comes back to the box, not to the panel as a facet', () => {
    const items = toFilterItems({ ...EMPTY_DRAFT, texts: ['amazon'] }, UNIVERSE)
    const opened = splitSavedSearch(items, 'amazon', UNIVERSE)

    expect(opened.search).toBe('amazon')
    expect(opened.filter.texts).toEqual([])
  })

  it('follows the items when the stored text is stale', () => {
    // `PATCH /reports/{id}` has no query_text field.
    const items = toFilterItems({ ...EMPTY_DRAFT, texts: ['target'] }, UNIVERSE)
    const opened = splitSavedSearch(items, 'amazon', UNIVERSE)

    expect(opened.search).toBe('target')
    expect(opened.filter.texts).toEqual([])
  })

  it('leaves the other text facets in the panel', () => {
    const items = toFilterItems({ ...EMPTY_DRAFT, texts: ['fuel', 'amazon'] }, UNIVERSE)
    const opened = splitSavedSearch(items, 'amazon', UNIVERSE)

    expect(opened.search).toBe('amazon')
    expect(opened.filter.texts).toEqual(['fuel'])
  })

  it('is an empty box for a report saved without one', () => {
    const opened = splitSavedSearch(toFilterItems(EMPTY_DRAFT, UNIVERSE), null, UNIVERSE)

    expect(opened.search).toBe('')
    expect(opened.filter.texts).toEqual([])
  })
})
