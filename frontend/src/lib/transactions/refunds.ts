/**
 * Whether to offer "This refunds…" on a row: a cheap per-row mirror of
 * `domain.CanBeARefund`, which the server still enforces.
 */

import type { CategoryKind, Transaction, Uuid } from './types'

/**
 * A credit that is not a transfer leg (by pair or by transfer category) and not
 * filed under income. An uncategorized credit qualifies.
 */
export function canBeARefund(
  txn: Transaction,
  categoryKind: (id: Uuid | null) => CategoryKind | null,
): boolean {
  if (txn.amount <= 0) return false
  if (txn.transfer_pair_id !== null) return false
  const kind = categoryKind(txn.category_id)
  return kind !== 'income' && kind !== 'transfer'
}

/** Built once per render rather than searched per row. */
export function categoryKindLookup(
  categories: readonly { id: Uuid; kind: CategoryKind }[],
): (id: Uuid | null) => CategoryKind | null {
  const kinds = new Map(categories.map((one) => [one.id, one.kind]))
  return (id) => (id === null ? null : kinds.get(id) ?? null)
}
