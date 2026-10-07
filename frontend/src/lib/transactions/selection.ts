/**
 * The register's select-all. It reaches visible rows only: the ids handed in
 * are the ones the grid put in its list, so a bulk action never touches a row
 * the reader cannot see.
 */

import { sectionKey } from './rows'
import type { Transaction, Uuid } from './types'

export type SelectAllState = 'none' | 'some' | 'all'

export function selectAllState(
  visible: readonly Uuid[],
  selected: ReadonlySet<Uuid>,
): SelectAllState {
  if (visible.length === 0 || selected.size === 0) return 'none'
  return visible.every((id) => selected.has(id)) ? 'all' : 'some'
}

/** Anything but `none` clears the whole selection, not just its visible part. */
export function nextSelection(
  visible: readonly Uuid[],
  selected: ReadonlySet<Uuid>,
): ReadonlySet<Uuid> {
  if (selectAllState(visible, selected) !== 'none') return new Set()
  return new Set(visible)
}

/** Drop a closing section's rows, so a bulk action never reaches rows put away. */
export function withoutSection(
  selected: ReadonlySet<Uuid>,
  transactions: readonly Transaction[],
  section: string,
): ReadonlySet<Uuid> {
  const leaving = new Set(
    transactions.filter((txn) => sectionKey(txn) === section).map((txn) => txn.id),
  )
  if (leaving.size === 0) return selected
  return new Set([...selected].filter((id) => !leaving.has(id)))
}
