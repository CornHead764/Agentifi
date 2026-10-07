import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { transaction } from '@/test/builders'

import { nextSelection, selectAllState, withoutSection } from './selection'
import type { Transaction, Uuid } from './types'

const A = 'a' as Uuid
const B = 'b' as Uuid
const C = 'c' as Uuid

function txn(id: string, overrides: Partial<Transaction> = {}): Transaction {
  return transaction({ id, date: '2026-08-15', amount: moneyFromCents(-1_000), ...overrides })
}

describe('the register header checkbox', () => {
  it('is unticked with nothing selected, and with nothing to select', () => {
    expect(selectAllState([A, B], new Set())).toBe('none')
    expect(selectAllState([], new Set([A]))).toBe('none')
  })

  it('is dashed while some of the visible rows are selected', () => {
    expect(selectAllState([A, B], new Set([A]))).toBe('some')
  })

  it('is ticked once every visible row is', () => {
    expect(selectAllState([A, B], new Set([A, B]))).toBe('all')
  })

  it('is ticked by rows the reader cannot see, and never reaches them', () => {
    expect(selectAllState([A], new Set([A, C]))).toBe('all')
    expect([...nextSelection([A], new Set())]).toEqual([A])
  })
})

describe('pressing it', () => {
  it('takes every visible row when none of them is taken', () => {
    expect([...nextSelection([A, B], new Set())]).toEqual([A, B])
  })

  it('clears from either of the other two states', () => {
    expect(nextSelection([A, B], new Set([A])).size).toBe(0)
    expect(nextSelection([A, B], new Set([A, B])).size).toBe(0)
  })

  it('clears the whole selection, not only its visible part', () => {
    expect(nextSelection([A], new Set([A, C])).size).toBe(0)
  })
})

describe('closing a section', () => {
  const ledger = [
    txn('a', { date: '2026-08-15' }),
    txn('b', { date: '2026-07-02' }),
    txn('c', { is_pending: true, date: '2026-08-29' }),
  ]

  it('lets go of the rows it hides', () => {
    expect([...withoutSection(new Set([A, B]), ledger, '2026-08')]).toEqual([B])
  })

  it('names the pending section the way the rows do', () => {
    expect([...withoutSection(new Set([A, C]), ledger, 'pending')]).toEqual([A])
  })

  it('leaves the selection alone where it holds nothing of that section', () => {
    const selected = new Set([A])
    expect(withoutSection(selected, ledger, '2026-06')).toBe(selected)
  })
})
