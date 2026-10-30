import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'

import { absorbRemainder, newSplitDraft, validateSplits, type SplitDraft } from './splits'

/** A $200.00 receipt, stored negative because it is money out. */
const RECEIPT = moneyFromCents(-20_000)

function draft(amount: string, category: string | null = null): SplitDraft {
  return { ...newSplitDraft(amount), category_id: category }
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
