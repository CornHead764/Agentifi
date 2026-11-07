import type { QueryClient, QueryKey } from '@tanstack/react-query'

import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'

/** A connector's row: a login, or a provider connection. */
interface Pullable {
  id: string
  /** The server's claim on a pull of this row, which it drops after ten minutes at most. */
  pulling: boolean
}

/** How often a list is asked again while one of its rows is pulling. */
export const PULL_POLL_MS = 3_000

/**
 * What a pull leaves stale outside its connector's own screens: it links what
 * it read to bank rows, splits them and files documents on them.
 */
export const PULLED_ROWS: readonly QueryKey[] = [TRANSACTIONS_KEY, ['transaction'], ['attachments']]

function isPulling(row: unknown): row is Pullable {
  return (
    typeof row === 'object' &&
    row !== null &&
    'pulling' in row &&
    row.pulling === true &&
    'id' in row &&
    typeof row.id === 'string'
  )
}

/** The ids of the rows a connector's list says are pulling; none for anything else. */
export function pullingIds(data: unknown): ReadonlySet<string> {
  if (!Array.isArray(data)) return new Set()
  return new Set(data.filter(isPulling).map((row) => row.id))
}

/**
 * The row reads as pulling in the cached list as the request goes out, which
 * is when the server takes its claim. Every mount of the list reads the busy
 * state from there, and the list is polled until the server says it is done,
 * so a page left and come back to still shows the pull its toast reports.
 */
export async function startPulling(client: QueryClient, key: QueryKey, id: string): Promise<void> {
  await client.cancelQueries({ queryKey: key, exact: true })
  client.setQueryData<Pullable[]>(key, (rows) =>
    rows?.map((row) => (row.id === id ? { ...row, pulling: true } : row)),
  )
}

export function anyPulling(rows: readonly Pullable[] | undefined): boolean {
  return rows?.some((row) => row.pulling) ?? false
}

/** "42s", "3m 05s": how long a pull has been running. */
export function formatElapsed(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}m ${String(seconds % 60).padStart(2, '0')}s`
}
