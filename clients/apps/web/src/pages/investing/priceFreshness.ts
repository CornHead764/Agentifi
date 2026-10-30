/**
 * How old the quotes behind the portfolio's figures are. Four days, not one:
 * Friday's close is still current on Monday, and a long weekend stretches that
 * to Tuesday.
 */
import { timeAgo } from '@/lib/format'

const STALE_AFTER_MS = 4 * 24 * 60 * 60 * 1000

export interface PriceFreshness {
  label: string
  /** How long ago the newest quote was, "6 days ago". */
  since: string
  stale: boolean
}

export function priceFreshness(
  stamps: readonly (string | null)[],
  now: Date = new Date(),
): PriceFreshness | undefined {
  const known = stamps.filter((one): one is string => one !== null)
  if (known.length === 0) return undefined
  // The newest: one security whose quote never arrived does not make the
  // whole portfolio stale, and the oldest stamp would say it did.
  const stamp = known.reduce((a, b) => (a > b ? a : b))
  const newest = new Date(stamp)
  const since = timeAgo(stamp, 'long', now)
  return {
    label: `Priced ${since}`,
    since,
    stale: now.getTime() - newest.getTime() > STALE_AFTER_MS,
  }
}
