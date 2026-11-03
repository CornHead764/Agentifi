import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@agentifi/core/money'

import {
  absorbRemainder,
  filingFields,
  newSplitDraft,
  unsplitCategory,
  validateSplits,
  type SplitDraft,
} from './splits'
import type { Split } from './types'

/** A $200.00 receipt, stored negative because it is money out. */
const RECEIPT = moneyFromCents(-20_000)

function draft(amount: string, category: string | null = null): SplitDraft {
  return { ...newSplitDraft(amount), category_id: category }
}

function part(position: number, cents: number, category: string | null): Split {
  return {
    id: `s${position}`,
    position,
    amount: moneyFromCents(cents),
    category_id: category,
    memo: null,
    tag_ids: [],
  }
}

describe('validateSplits', () => {
  it('accepts a set that sums to the parent', () => {
    const result = validateSplits([draft('-50.00', 'groceries'), draft('-150.00', 'household')], RECEIPT)

    expect(result.problems).toEqual([])
    expect(result.remainder).toBe(0)
    expect(result.rows).toEqual([
      { amount: '-50.00', category_id: 'groceries', memo: null, tag_ids: [] },
      { amount: '-150.00', category_id: 'household', memo: null, tag_ids: [] },
    ])
  })

  it('refuses a set that leaves part of the row unallocated', () => {
    const result = validateSplits([draft('-50.00'), draft('-140.00')], RECEIPT)

    expect(result.rows).toBeNull()
    expect(result.problems).toEqual([{ kind: 'unbalanced', remainder: moneyFromCents(-1_000), over: false }])
  })

  it('refuses a set that allocates more than the row', () => {
    const result = validateSplits([draft('-50.00'), draft('-160.00')], RECEIPT)

    expect(result.rows).toBeNull()
    expect(result.problems).toEqual([{ kind: 'unbalanced', remainder: moneyFromCents(1_000), over: true }])
  })

  it('refuses a single part, which is not a split', () => {
    const result = validateSplits([draft('-200.00')], RECEIPT)

    expect(result.rows).toBeNull()
    expect(result.problems).toContainEqual({ kind: 'too-few' })
  })

  it('refuses an amount that is not a number, naming the row', () => {
    const rows = [draft('-50.00'), draft('one fifty')]
    const result = validateSplits(rows, RECEIPT)

    expect(result.rows).toBeNull()
    expect(result.problems).toContainEqual({ kind: 'unparseable', key: rows[1].key })
  })

  it('sums exactly across cents that a float would drift on', () => {
    const result = validateSplits(
      [draft('-0.10'), draft('-0.20'), draft('-199.70')],
      RECEIPT,
    )

    expect(result.problems).toEqual([])
    expect(result.allocated).toBe(RECEIPT)
  })

  it('gives the remainder to the row the user pointed at', () => {
    const rows = [draft('-50.00'), draft('-10.00')]
    const balanced = absorbRemainder(rows, rows[1].key, RECEIPT)

    expect(balanced[1].amount).toBe('-150.00')
    expect(validateSplits(balanced, RECEIPT).problems).toEqual([])
  })
})

describe('unsplitCategory', () => {
  it('offers the category that holds the most money', () => {
    expect(unsplitCategory([part(0, -5_000, 'household'), part(1, -15_000, 'groceries')])).toBe(
      'groceries',
    )
  })

  it('offers the earlier part on a tie', () => {
    expect(unsplitCategory([part(0, -10_000, 'household'), part(1, -10_000, 'groceries')])).toBe(
      'household',
    )
  })

  it('measures a refund split by its size, not its sign', () => {
    expect(unsplitCategory([part(0, 2_000, 'household'), part(1, -500, 'groceries')])).toBe(
      'household',
    )
  })

  it('offers no category when the largest part has none', () => {
    expect(unsplitCategory([part(0, -15_000, null), part(1, -5_000, 'groceries')])).toBeNull()
  })
})

describe('filingFields', () => {
  it('sends the category alone when the splits are left as they are', () => {
    expect(filingFields('groceries', null)).toEqual({ category_id: 'groceries' })
  })

  it('sends the category with an empty list when the splits are removed', () => {
    // Without the category the row would be left uncategorized.
    expect(filingFields('groceries', [])).toEqual({ category_id: 'groceries', splits: [] })
  })

  it('sends the parts and no category when the row is split', () => {
    const rows = validateSplits([draft('-50.00', 'groceries'), draft('-150.00', 'household')], RECEIPT).rows!

    expect(filingFields('groceries', rows)).toEqual({ splits: rows })
  })
})
