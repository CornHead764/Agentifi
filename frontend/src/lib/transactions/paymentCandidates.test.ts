import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { transaction } from '@/test/builders'

import type { Occurrence } from '@/lib/clients/upcoming'

import { paymentCandidates, paymentSlot, type PaymentSlot } from './paymentCandidates'

const SLOT: PaymentSlot = {
  series_id: 's1',
  due_on: '2026-08-01',
  label: 'Lakeside Power',
  amount: moneyFromCents(-9_000),
  account_id: 'checking',
}

describe('the charges that could pay a slot', () => {
  it('leaves out money moving the other way and rows a series already claims', () => {
    const rows = [
      transaction({ id: 'deposit', account_id: 'checking', amount: moneyFromCents(9_000) }),
      transaction({ id: 'claimed', account_id: 'checking', series_id: 's2', amount: moneyFromCents(-9_000) }),
      transaction({ id: 'charge', account_id: 'checking', amount: moneyFromCents(-9_000) }),
    ]
    expect(paymentCandidates(SLOT, rows).map((row) => row.id)).toEqual(['charge'])
  })

  it("puts the slot's own account first, then the nearest amount, then the nearest date", () => {
    const rows = [
      transaction({ id: 'card', account_id: 'card', date: '2026-08-01', amount: moneyFromCents(-9_000) }),
      transaction({ id: 'off', account_id: 'checking', date: '2026-08-01', amount: moneyFromCents(-4_000) }),
      transaction({ id: 'late', account_id: 'checking', date: '2026-08-20', amount: moneyFromCents(-9_000) }),
      transaction({ id: 'near', account_id: 'checking', date: '2026-08-03', amount: moneyFromCents(-9_000) }),
    ]
    expect(paymentCandidates(SLOT, rows).map((row) => row.id)).toEqual(['near', 'late', 'off', 'card'])
  })

  it('ranks every account alike for a slot that belongs to none', () => {
    const rows = [
      transaction({ id: 'checking', account_id: 'checking', amount: moneyFromCents(-5_000) }),
      transaction({ id: 'card', account_id: 'card', amount: moneyFromCents(-9_000) }),
    ]
    expect(
      paymentCandidates({ ...SLOT, account_id: null }, rows).map((row) => row.id),
    ).toEqual(['card', 'checking'])
  })
})

describe('the slot a reminder can be linked to', () => {
  const occurrence = {
    series_id: 'ser-1',
    account_id: 'acct-1',
    label: 'Power',
    due_on: '2026-10-28',
    amount: moneyFromCents(-9000),
    bill: null,
  } as unknown as Occurrence

  it('names the series, the due date and the account', () => {
    expect(paymentSlot(occurrence)).toEqual({
      series_id: 'ser-1',
      due_on: '2026-10-28',
      label: 'Power',
      amount: moneyFromCents(-9000),
      account_id: 'acct-1',
    })
  })

  it('is none for a statement with no series', () => {
    expect(paymentSlot({ ...occurrence, series_id: null })).toBeNull()
  })
})
