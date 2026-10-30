/**
 * The charges that could pay one recurring slot, best first, for linking from
 * the slot's side (a reminder, a bill in the plan) rather than the row's.
 */

import { isPayManually, type Occurrence } from '@/lib/clients/upcoming'
import { absMoney, subMoney, type Money } from '@agentifi/core/money'

import type { Transaction } from './types'

/** One slot of a series, as a reminder or a plan row names it. */
export interface PaymentSlot {
  series_id: string
  due_on: string
  label: string
  amount: Money
  /** Null for a slot that belongs to no account, which any account may pay. */
  account_id: string | null
}

/** The slot a charge can be linked to, or null for a reminder paid another way. */
export function paymentSlot(one: Occurrence): PaymentSlot | null {
  if (one.series_id === null || isPayManually(one)) return null
  return {
    series_id: one.series_id,
    due_on: one.due_on,
    label: one.label,
    amount: one.amount,
    account_id: one.account_id,
  }
}

function daysApart(a: string, b: string): number {
  return Math.abs(Date.parse(`${a}T00:00:00Z`) - Date.parse(`${b}T00:00:00Z`)) / 86_400_000
}

/**
 * Rows moving money the slot's way that no series claims, the slot's own
 * account first, then the nearest amount, then the nearest date. A row already
 * filed under a series is left out: linking it here would take it from there.
 */
export function paymentCandidates(
  slot: PaymentSlot,
  rows: readonly Transaction[],
): Transaction[] {
  const sameWay = (row: Transaction) => (row.amount < 0) === (slot.amount < 0)
  const otherAccount = (row: Transaction) =>
    slot.account_id !== null && row.account_id !== slot.account_id ? 1 : 0
  const amountGap = (row: Transaction) => absMoney(subMoney(row.amount, slot.amount))
  return rows
    .filter((row) => row.series_id === null && row.amount !== 0 && sameWay(row))
    .sort(
      (a, b) =>
        otherAccount(a) - otherAccount(b) ||
        amountGap(a) - amountGap(b) ||
        daysApart(a.date, slot.due_on) - daysApart(b.date, slot.due_on),
    )
}
