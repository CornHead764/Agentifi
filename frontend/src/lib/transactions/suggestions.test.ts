import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { category, transaction } from '@/test/builders'

import {
  appliedSuggestion,
  describeSuggestion,
  needsReview,
  nextToReview,
  suggestedCategoryId,
} from './suggestions'
import type { Suggestion, Uuid } from './types'

function suggestion(overrides: Partial<Suggestion> = {}): Suggestion {
  return {
    action_id: 'p1' as Uuid,
    conversation_id: 'c1' as Uuid,
    run_id: 'r1' as Uuid,
    tool: 'update_transaction',
    summary: 'Costco is Groceries, as the last 11 times',
    category_id: 'groceries' as Uuid,
    splits: [],
    created_at: '2026-09-04T12:00:08Z',
    ...overrides,
  }
}

const CATEGORIES = [
  category('groceries', 'Groceries'),
  category('pet', 'Pet Supplies'),
  category('transport', 'Auto & Transport'),
  category('gas', 'Gas', 'transport'),
]

describe('the category a suggestion proposes', () => {
  it('is the one the change would file the row under', () => {
    expect(suggestedCategoryId(suggestion())).toBe('groceries')
  })

  it('is absent when the proposal names no category: uncategorized is never suggested', () => {
    expect(suggestedCategoryId(suggestion({ category_id: null }))).toBeUndefined()
  })

  it('is absent for a proposed split, which no single cell can stand in for', () => {
    expect(suggestedCategoryId(suggestion({ tool: 'split_transaction' }))).toBeUndefined()
    expect(suggestedCategoryId(null)).toBeUndefined()
  })
})

describe('what a suggestion would do, in words', () => {
  it('names the category, with its parent where it has one', () => {
    expect(describeSuggestion(suggestion(), CATEGORIES)).toBe('File this under Groceries')
    expect(describeSuggestion(suggestion({ category_id: 'gas' as Uuid }), CATEGORIES)).toBe(
      'File this under Auto & Transport › Gas',
    )
  })

  it('falls back to the reason when the proposal names no category', () => {
    expect(
      describeSuggestion(suggestion({ category_id: null, summary: 'Tidy the payee' }), CATEGORIES),
    ).toBe('Tidy the payee')
  })

  it('names every part of a proposed split with its amount', () => {
    const split = suggestion({
      tool: 'split_transaction',
      splits: [
        { amount: moneyFromCents(-3_000), category_id: 'pet' as Uuid, memo: 'Dog food' },
        { amount: moneyFromCents(-4_000), category_id: 'groceries' as Uuid, memo: 'Batteries' },
      ],
    })
    const line = describeSuggestion(split, CATEGORIES)
    expect(line).toBe('Split into 2 categories')
  })

  it('files a suggestion for a category that has since been deleted under "Uncategorized"', () => {
    expect(describeSuggestion(suggestion({ category_id: 'gone' as Uuid }), CATEGORIES)).toBe(
      'File this under Uncategorized',
    )
  })
})

describe('which rows the review flow visits', () => {
  it('counts an unreviewed row, and a reviewed one something is waiting on', () => {
    expect(needsReview(transaction())).toBe(true)
    expect(needsReview(transaction({ is_reviewed: true }))).toBe(false)
    expect(needsReview(transaction({ is_reviewed: true, suggestion: suggestion() }))).toBe(true)
  })

  it('goes down the register from the row in hand, never back up it', () => {
    const rows = [
      transaction({ id: 'a', is_reviewed: false }),
      transaction({ id: 'b', is_reviewed: true }),
      transaction({ id: 'c', is_reviewed: false }),
      transaction({ id: 'd', is_reviewed: false }),
    ]
    expect(nextToReview(rows, 'a')?.id).toBe('c')
    expect(nextToReview(rows, 'c')?.id).toBe('d')
    expect(nextToReview(rows, 'd')).toBeNull()
  })

  it('answers with nothing when the rest of the list is reviewed', () => {
    const rows = [transaction({ id: 'a' }), transaction({ id: 'b', is_reviewed: true })]
    expect(nextToReview(rows, 'a')).toBeNull()
  })
})

describe('what applying a proposal does to the row', () => {
  it('ticks the row reviewed and takes the proposal off it', () => {
    const patch = appliedSuggestion(transaction({ suggestion: suggestion() }))

    expect(patch.is_reviewed).toBe(true)
    expect(patch.suggestion).toBeNull()
  })

  it('files the row under what the proposal chose', () => {
    expect(appliedSuggestion(transaction({ suggestion: suggestion() })).category_id).toBe('groceries')
  })

  it('files it under the category somebody picked instead, including the empty one', () => {
    const txn = transaction({ suggestion: suggestion() })

    expect(appliedSuggestion(txn, 'pet' as Uuid).category_id).toBe('pet')
    // Choosing no category is an override too.
    expect(appliedSuggestion(txn, null).category_id).toBeNull()
  })

  it('moves no category for a proposal that named none', () => {
    const patch = appliedSuggestion(transaction({ suggestion: suggestion({ category_id: null }) }))

    expect('category_id' in patch).toBe(false)
  })

  it('draws a proposed split whose parts add up to the row', () => {
    const txn = transaction({
      amount: moneyFromCents(-7_000),
      suggestion: suggestion({
        tool: 'split_transaction',
        splits: [
          { amount: moneyFromCents(-3_000), category_id: 'pet' as Uuid, memo: 'Dog food' },
          { amount: moneyFromCents(-4_000), category_id: 'groceries' as Uuid, memo: '' },
        ],
      }),
    })

    const patch = appliedSuggestion(txn)

    expect(patch.splits).toHaveLength(2)
    expect(patch.splits?.[0].category_id).toBe('pet')
    expect(patch.splits?.[0].position).toBe(0)
    expect(patch.splits?.[1].memo).toBeNull()
    expect('category_id' in patch).toBe(false)
  })

  it('draws no split whose parts are not this row', () => {
    const txn = transaction({
      amount: moneyFromCents(-7_000),
      suggestion: suggestion({
        tool: 'split_transaction',
        splits: [
          { amount: moneyFromCents(-3_000), category_id: 'pet' as Uuid, memo: '' },
          { amount: moneyFromCents(-1), category_id: 'groceries' as Uuid, memo: '' },
        ],
      }),
    })

    const patch = appliedSuggestion(txn)

    expect(patch.splits).toBeUndefined()
    expect(patch.suggestion).toBeNull()
  })
})
