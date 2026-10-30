/**
 * Ordering the Portfolio table, and grouping it by account. **An unknown
 * figure sorts last in both directions**: a holding whose lots never arrived
 * has no gain, and sorting it as zero would read as a fact about the position.
 */

import type { SortDirection } from '@/components/ui'
import type { Holding } from '@/lib/clients/investments'
import { parseRate } from '@/lib/format'

export type HoldingSortKey =
  | 'symbol'
  | 'name'
  | 'shares'
  | 'price'
  | 'market_value'
  | 'cost_basis'
  | 'total_gain'
  | 'day_change'
  | 'day_change_pct'
  | 'share'

export interface HoldingSort {
  key: HoldingSortKey
  direction: SortDirection
}

/** Value first, the way somebody opening the screen wants to read it. */
export const DEFAULT_SORT: HoldingSort = { key: 'market_value', direction: 'desc' }

/** A new column starts descending for a number and ascending for a name; clicking the sorted column flips it. */
export function nextSort(current: HoldingSort, key: HoldingSortKey): HoldingSort {
  if (current.key === key) {
    return { key, direction: current.direction === 'asc' ? 'desc' : 'asc' }
  }
  return { key, direction: key === 'symbol' || key === 'name' ? 'asc' : 'desc' }
}

/** The figure a column sorts on, or null where the row has none. */
function valueOf(holding: Holding, key: HoldingSortKey): number | string | null {
  switch (key) {
    case 'symbol':
      return holding.symbol.toLowerCase()
    case 'name':
      return holding.name.toLowerCase()
    case 'shares':
      return parseRate(holding.shares)
    case 'price':
      return holding.is_unquoted ? null : parseRate(holding.price)
    case 'market_value':
      return holding.market_value
    case 'cost_basis':
      return holding.is_cost_basis_complete ? holding.cost_basis : null
    case 'total_gain':
      return holding.total_gain
    case 'day_change':
      return holding.day_change
    case 'day_change_pct':
      return parseRate(holding.day_change_pct)
    case 'share':
      return parseRate(holding.share)
  }
}

export function sortHoldings(rows: readonly Holding[], sort: HoldingSort): Holding[] {
  const sign = sort.direction === 'asc' ? 1 : -1
  return [...rows].sort((left, right) => {
    const a = valueOf(left, sort.key)
    const b = valueOf(right, sort.key)
    // Unknown last in both directions, so the sign is deliberately not applied.
    if (a === null && b === null) return left.symbol.localeCompare(right.symbol)
    if (a === null) return 1
    if (b === null) return -1
    if (typeof a === 'string' || typeof b === 'string') {
      return sign * String(a).localeCompare(String(b))
    }
    if (a === b) return left.symbol.localeCompare(right.symbol)
    return sign * (a - b)
  })
}

export interface HoldingGroup {
  accountId: string
  label: string
  rows: Holding[]
}

/**
 * The rows split by the account holding them, groups ordered by value. Within
 * a group the incoming order is kept, so the chosen sort still applies.
 */
export function groupHoldingsByAccount(
  rows: readonly Holding[],
  accountName: (id: string) => string,
): HoldingGroup[] {
  const groups = new Map<string, HoldingGroup>()
  for (const row of rows) {
    const group = groups.get(row.account_id)
    if (group) group.rows.push(row)
    else
      groups.set(row.account_id, {
        accountId: row.account_id,
        label: accountName(row.account_id),
        rows: [row],
      })
  }
  return [...groups.values()].sort((left, right) => {
    const value = (group: HoldingGroup) =>
      group.rows.reduce((sum, row) => sum + row.market_value, 0)
    return value(right) - value(left) || left.label.localeCompare(right.label)
  })
}
