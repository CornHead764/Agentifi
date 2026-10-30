/** How long a toast stays before it dismisses itself, in milliseconds. */

import { coerceMs } from './format'

export const DEFAULT_TOAST_MS = 6000

/** The account stores milliseconds, so a value not listed here still works. */
export const TOAST_DURATIONS: readonly number[] = [3000, 6000, 10000, 15000, 30000]

/** The bounds the API enforces, repeated here so the client refuses the same. */
export const MIN_TOAST_MS = 1000
export const MAX_TOAST_MS = 60000

export function asToastMs(value: unknown, fallback = DEFAULT_TOAST_MS): number {
  return coerceMs(value, fallback, MIN_TOAST_MS, MAX_TOAST_MS)
}

export function toastDurationLabel(ms: number): string {
  const seconds = ms / 1000
  return `${Number.isInteger(seconds) ? seconds : seconds.toFixed(1)} seconds`
}
