/**
 * What a row is filed under, the server's `Transaction.IsUncategorized` and
 * `CategoryIDs`. A split row's categories are its splits': its parent's
 * `category_id` is always null, so `category_id === null` alone is true of
 * every split row however it is filed.
 */

import type { Transaction, Uuid } from './types'

/** Whether any part of the row still needs a category. A paired transfer leg never does. */
export function needsCategory(
  txn: Pick<Transaction, 'category_id' | 'transfer_pair_id' | 'splits'>,
): boolean {
  if (txn.transfer_pair_id !== null) return false
  if (txn.splits.length === 0) return txn.category_id === null
  return txn.splits.some((split) => split.category_id === null)
}

/** Every category the row is filed under, its splits' for a split row; an unfiled part adds none. */
export function categoryIds(txn: Pick<Transaction, 'category_id' | 'splits'>): Uuid[] {
  const ids = txn.splits.length === 0 ? [txn.category_id] : txn.splits.map((split) => split.category_id)
  return ids.filter((id): id is Uuid => id !== null)
}
