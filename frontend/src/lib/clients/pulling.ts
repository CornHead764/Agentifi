import type { QueryClient, QueryKey } from '@tanstack/react-query'

/** A connector's row: a login, or a provider connection. */
interface Pullable {
  id: string
  /** The server's claim on a pull of this row, which it drops after ten minutes at most. */
  pulling: boolean
}

/** How often a list is asked again while one of its rows is pulling. */
export const PULL_POLL_MS = 3_000

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
