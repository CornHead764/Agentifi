/**
 * Category checks in flight, by row. Pending is a set, and only a row's own
 * result takes it out; how far a whole request has got is its batch's, from
 * the server. "Undetermined" is derived, not stored, so filing the row by hand
 * settles it with no flag to clear.
 */

import { needsCategory } from './category'
import type { Transaction, Uuid } from './types'

export type CheckAction =
  /** Rows asked about here, or ones the server says are busy, perhaps from a run this page did not start. */
  | { kind: 'checking'; ids: readonly Uuid[] }
  | { kind: 'settled'; ids: readonly Uuid[] }

export const NO_CHECKS: ReadonlySet<Uuid> = new Set()

export function checkReducer(pending: ReadonlySet<Uuid>, action: CheckAction): ReadonlySet<Uuid> {
  switch (action.kind) {
    case 'checking': {
      const fresh = action.ids.filter((id) => !pending.has(id))
      if (fresh.length === 0) return pending
      const next = new Set(pending)
      for (const id of fresh) next.add(id)
      return next
    }
    case 'settled': {
      const next = new Set(pending)
      let changed = false
      for (const id of action.ids) changed = next.delete(id) || changed
      return changed ? next : pending
    }
  }
}

/**
 * The assistant looked at this row and filed and proposed nothing, and it still needs a category. A split
 * row is drawn by its parts, never as one undetermined row.
 */
export function isUndetermined(txn: Transaction): boolean {
  return (
    txn.category_checked_at !== null &&
    txn.splits.length === 0 &&
    needsCategory(txn) &&
    txn.suggestion === null
  )
}

/** The run behind "why this category?"; null while a check is in flight, since that run is not decided yet. */
export function categoryWhyRunId(txn: Transaction): Uuid | null {
  if (txn.checking_category) return null
  return txn.category_check_run_id
}

export function checkingIds(rows: readonly Transaction[]): Uuid[] {
  return rows.filter((row) => row.checking_category).map((row) => row.id)
}
