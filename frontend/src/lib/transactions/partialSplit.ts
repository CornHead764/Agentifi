import type { Money } from '@/lib/money'

import type { Split, Transaction } from './types'

export interface PartialSplit {
  /** The kept splits' sum, in the primary currency. */
  amount: Money
  /** The whole transaction, in the same currency. */
  whole: Money
  splits: Split[]
}

/** Null for a row shown whole. The server decides which splits matched. */
export function partialSplit(txn: Transaction): PartialSplit | null {
  if (txn.matched_amount == null || txn.matched_split_ids == null) return null
  const kept = new Set(txn.matched_split_ids)
  return {
    amount: txn.matched_amount,
    whole: txn.amount_primary ?? txn.amount,
    splits: txn.splits.filter((split) => kept.has(split.id)),
  }
}
