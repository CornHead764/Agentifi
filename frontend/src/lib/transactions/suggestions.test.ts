import { describe, expect, it, vi } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { category, transaction } from '@/test/builders'

import {
  appliedSuggestion,
  basisLabel,
  confidenceBand,
  describeSuggestion,
  suggestedCategoryId,
  suggestionReason,
  suggestionSource,
  thenClose,
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
    confidence: 0.8,
    decided_by: 'model',
    basis: '',
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
        {
          amount: moneyFromCents(-3_000),
          category_id: 'pet' as Uuid,
          memo: 'Dog food',
          confidence: null,
        },
        {
          amount: moneyFromCents(-4_000),
          category_id: 'groceries' as Uuid,
          memo: 'Batteries',
          confidence: null,
        },
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
          {
            amount: moneyFromCents(-3_000),
            category_id: 'pet' as Uuid,
            memo: 'Dog food',
            confidence: null,
          },
          {
            amount: moneyFromCents(-4_000),
            category_id: 'groceries' as Uuid,
            memo: '',
            confidence: null,
          },
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
          {
            amount: moneyFromCents(-3_000),
            category_id: 'pet' as Uuid,
            memo: '',
            confidence: null,
          },
          {
            amount: moneyFromCents(-1),
            category_id: 'groceries' as Uuid,
            memo: '',
            confidence: null,
          },
        ],
      }),
    })

    const patch = appliedSuggestion(txn)

    expect(patch.splits).toBeUndefined()
    expect(patch.suggestion).toBeNull()
  })
})

describe('a confidence in words', () => {
  it('cuts where the category check is told the bands are', () => {
    expect(confidenceBand(0.95).label).toBe('Very likely')
    expect(confidenceBand(0.9).label).toBe('Very likely')
    expect(confidenceBand(0.89).label).toBe('Likely')
    expect(confidenceBand(0.7).label).toBe('Likely')
    expect(confidenceBand(0.4).label).toBe('Possible')
    expect(confidenceBand(0.39).label).toBe('A guess')
  })
})

describe('the reason a suggestion gives', () => {
  it('leaves out the figure the model ends its summary on', () => {
    expect(suggestionReason('Corner Store sells groceries (confidence 0.62).')).toBe(
      'Corner Store sells groceries.',
    )
  })

  it("leaves out the figure inside the history's evidence and keeps the counts", () => {
    expect(
      suggestionReason('Groceries, per payee history (8 of 10 past rows, confidence 0.85).'),
    ).toBe('Groceries, per payee history (8 of 10 past rows).')
  })

  it('keeps a summary with no figure as it is', () => {
    expect(suggestionReason('Costco is Groceries, as the last 11 times')).toBe(
      'Costco is Groceries, as the last 11 times',
    )
  })
})

describe('what decided a suggestion', () => {
  it('is the basis the proposal named', () => {
    expect(basisLabel(suggestion({ basis: 'payee_name' }))).toBe('Payee name')
    expect(basisLabel(suggestion({ basis: 'order_items' }))).toBe('Order items')
  })

  it('falls back on who decided when the proposal named none', () => {
    expect(basisLabel(suggestion({ basis: '', decided_by: 'agentifi' }))).toBe('Past transactions')
    expect(basisLabel(suggestion({ basis: '', decided_by: 'model' }))).toBe("Assistant's judgement")
    expect(basisLabel(suggestion({ basis: '', decided_by: '', run_id: null }))).toBe(
      'From a conversation',
    )
  })
})

describe('who decided on a suggestion', () => {
  it('names the history, the model or a conversation', () => {
    expect(suggestionSource(suggestion({ decided_by: 'agentifi' }))).toMatch(/without the model/)
    expect(suggestionSource(suggestion({ decided_by: 'model' }))).toMatch(/assistant/)
    expect(suggestionSource(suggestion({ decided_by: '', run_id: null }))).toMatch(/conversation/)
  })
})

describe('thenClose', () => {
  it('decides with the arguments given, then closes the popup', () => {
    const calls: string[] = []
    const decide = vi.fn((categoryId: string) => calls.push(`decide ${categoryId}`))
    const close = vi.fn(() => calls.push('close'))

    thenClose(decide, close)('groceries')

    expect(decide).toHaveBeenCalledWith('groceries')
    expect(calls).toEqual(['decide groceries', 'close'])
  })
})
