import { describe, expect, it } from 'vitest'

import { transaction } from '@/test/builders'

import { canBeARefund, categoryKindLookup } from './refunds'
import type { Money } from '../money'
import type { CategoryKind, Transaction, Uuid } from './types'

/** The same cases the Go test for `domain.CanBeARefund` pins. */

const GROCERIES = 'cat-groceries' as Uuid
const SALARY = 'cat-salary' as Uuid
const MOVING = 'cat-transfer' as Uuid

const kinds: { id: Uuid; kind: CategoryKind }[] = [
  { id: GROCERIES, kind: 'expense' },
  { id: SALARY, kind: 'income' },
  { id: MOVING, kind: 'transfer' },
]
const lookup = categoryKindLookup(kinds)

function row(overrides: Partial<Transaction> = {}): Transaction {
  return transaction({ amount: 20 as Money, category_id: GROCERIES, ...overrides })
}

describe('canBeARefund', () => {
  it('offers the link on a credit filed under a spending category', () => {
    expect(canBeARefund(row(), lookup)).toBe(true)
  })

  it('offers it on an uncategorized credit, which is what a link is for', () => {
    expect(canBeARefund(row({ category_id: null }), lookup)).toBe(true)
  })

  it('does not offer it on a charge', () => {
    expect(canBeARefund(row({ amount: -20 as Money }), lookup)).toBe(false)
    expect(canBeARefund(row({ amount: 0 as Money }), lookup)).toBe(false)
  })

  it('does not offer it on income the user has already named as such', () => {
    expect(canBeARefund(row({ category_id: SALARY }), lookup)).toBe(false)
  })

  it('does not offer it on either leg of a transfer', () => {
    expect(canBeARefund(row({ transfer_pair_id: 'pair-1' as Uuid }), lookup)).toBe(false)
    expect(canBeARefund(row({ category_id: MOVING }), lookup)).toBe(false)
  })

  it('treats a category it has never heard of as no category at all', () => {
    expect(canBeARefund(row({ category_id: 'cat-gone' as Uuid }), lookup)).toBe(true)
  })
})
