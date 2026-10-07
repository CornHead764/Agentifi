import { DEFAULT_QUERY } from '@/lib/transactions/api'
import { flattenPages } from '@/lib/transactions/cache'
import { useRegister } from '@/lib/transactions/queries'
import type { Transaction, Uuid } from '@/lib/transactions/types'

/**
 * The rows behind a figure that sums a filter over a window: a watchlist's
 * month, a report's period. Read-only; rows are edited in the register.
 *
 * **`date_field=effective`**, as the figures' arithmetic, or a card charge
 * would be listed a statement cycle away from the period that counted it.
 *
 * A figure sums matching *allocations* while this lists whole transactions,
 * so a split row shows its full amount here; `splitCount` is how many, for
 * the caller to say so where it applies.
 */
export function useMatchingRows(
  scope: { filterId: Uuid | null; from: string | null; to: string | null },
  enabled = true,
) {
  const rows = useRegister({ ...DEFAULT_QUERY, ...scope, dateField: 'effective' }, enabled)
  const items: Transaction[] = flattenPages(rows.data)
  return { rows, items, splitCount: items.filter((one) => one.splits.length > 0).length }
}
