/**
 * Wire vocabulary shared by more than one screen. A `Money | null` field means
 * unknown, never zero. Accounts, categories and tags live with the register,
 * never redefined here, so there is one cache for them.
 */

import type { IsoDate } from '@/lib/transactions/types'

export type { IsoDate, Uuid } from '@/lib/transactions/types'

/** A fraction (0.2173 is 21.73%), null when the calculation had no answer. Not money. */
export type WireRate = string | number | null

/** `auto` lets the server coarsen a long window. */
export type Granularity = 'auto' | 'day' | 'week' | 'month'

/** The window a report, a net worth series or an investment series actually ran against, echoed back. */
export interface EchoedWindow {
  from: IsoDate | null
  to: IsoDate | null
  date_field: string
}

/**
 * `null` and `undefined` are omitted. For a repeated key, a null list is
 * omitted (every row) while an empty list is sent as one empty value (none).
 */
export function queryString(
  params: Record<string, string | number | boolean | null | undefined>,
  repeated: Record<string, readonly string[] | null | undefined> = {},
): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue
    search.set(key, String(value))
  }
  for (const [key, values] of Object.entries(repeated)) {
    if (values === null || values === undefined) continue
    if (values.length === 0) search.append(key, '')
    for (const one of values) search.append(key, one)
  }
  const rendered = search.toString()
  return rendered ? `?${rendered}` : ''
}

export function idsKey(ids: readonly string[] | null): string {
  return ids === null ? 'all' : [...ids].sort().join(',')
}
