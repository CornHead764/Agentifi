/**
 * The one animation duration, written to the root as `--motion` by
 * `MotionProvider`. Zero means no animation, not a fast one.
 */

import { coerceMs } from './format'

export const MOTION_STORAGE_KEY = 'motion'

export const DEFAULT_MOTION_MS = 160

/** The account stores milliseconds, so a value not listed here still works. */
export const MOTION_SPEEDS: readonly number[] = [0, 100, 160, 240]

/** The ceiling the API enforces, repeated here so the client refuses the same. */
export const MAX_MOTION_MS = 1000

const MOTION_SPEED_NAMES: Readonly<Record<number, string>> = {
  100: 'Quick',
  160: 'Standard',
  240: 'Relaxed',
}

/** Out-of-range values fall back: in a `transition` a nonsense value is not an error, just a rail that never finishes sliding. */
export function asMotionMs(value: unknown, fallback = DEFAULT_MOTION_MS): number {
  return coerceMs(value, fallback, 0, MAX_MOTION_MS)
}

/**
 * The system's reduced-motion outranks the preference. Resolved here, not in
 * `tokens.css`, because the inline `--motion` outranks any stylesheet.
 */
export function motionDuration(preference: number, systemReducesMotion: boolean): number {
  return systemReducesMotion ? 0 : asMotionMs(preference)
}

export function motionSpeedLabel(ms: number): string {
  if (ms === 0) return 'Off'
  const name = MOTION_SPEED_NAMES[ms]
  return name ? `${name} · ${ms} ms` : `${ms} ms`
}
