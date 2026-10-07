/**
 * Pull to refresh, as arithmetic: how far the indicator travels for a drag,
 * and whether letting go there refreshes. The touch handling is in
 * `components/shell/PullToRefresh.tsx`.
 */

/** Indicator travel, in CSS pixels, at which letting go refreshes. */
export const PULL_THRESHOLD = 64

/** The indicator stops here however far the finger goes. */
export const PULL_MAX = 96

/** Half the finger's travel, so the indicator lags it the way the page's bounce does. */
export function pullTravel(dragged: number): number {
  return Math.min(PULL_MAX, Math.max(0, dragged / 2))
}

export function pullRefreshes(travel: number): boolean {
  return travel >= PULL_THRESHOLD
}

/**
 * Which way a drag that has just started is going. Sideways belongs to the
 * register's row swipes, and up is the page scrolling; only down is a pull.
 * Undecided until the finger has moved far enough to tell.
 */
export function pullDirection(dx: number, dy: number): 'pull' | 'other' | undefined {
  if (Math.abs(dx) < 6 && Math.abs(dy) < 6) return undefined
  return dy > 0 && dy > Math.abs(dx) ? 'pull' : 'other'
}
