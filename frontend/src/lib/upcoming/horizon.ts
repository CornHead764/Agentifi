/**
 * How far ahead Bills & Income looks: horizons that start today, or a custom
 * range, which is the only one allowed to look backwards.
 */

import type { IsoDate } from '@/lib/clients/entities'
import { dayWindow } from '@/lib/dateRanges'

export interface Horizon {
  from: IsoDate
  to: IsoDate
  /** Null for a range the user picked; the day count otherwise. */
  days: number | null
}

export const HORIZON_DAYS = [30, 60, 90, 180] as const

/** A horizon starts today: what is due next is not a window over the past. */
export function horizonOf(days: number, today: Date = new Date()): Horizon {
  return { ...dayWindow(0, days, today), days }
}

/** A range the user typed; an end before the start is treated as swapped. */
export function customHorizon(from: IsoDate, to: IsoDate): Horizon {
  return from <= to ? { from, to, days: null } : { from: to, to: from, days: null }
}

export function horizonLabel(horizon: Horizon): string {
  return horizon.days === null ? 'Selected dates' : `Next ${horizon.days} days`
}

export function isCompleteRange(from: string, to: string): boolean {
  return from.trim() !== '' && to.trim() !== ''
}
