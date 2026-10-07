/** Month keys are `YYYY-MM` strings compared as strings, so nothing here passes through a time zone. */

import type { BucketKey } from './types'

function monthParts(month: string): { year: number; month: number } {
  const [year, index] = month.split('-')
  return { year: Number(year), month: Number(index) }
}

export function shiftMonth(month: string, by: number): string {
  const { year, month: index } = monthParts(month)
  const shifted = new Date(Date.UTC(year, index - 1 + by, 1))
  return `${shifted.getUTCFullYear()}-${String(shifted.getUTCMonth() + 1).padStart(2, '0')}`
}

/** Income lives at the bare /spending-plan; rollover is a figure on the rail, not a route. */
export type RoutableBucket = Exclude<BucketKey, 'rollover'>

export const BUCKET_PATHS: Record<RoutableBucket, string> = {
  income: '',
  bills: 'bills',
  planned_spend: 'planned-spending',
  other_spend: 'spent-so-far',
  goals: 'goals',
}

const ROUTABLE_BUCKETS: readonly RoutableBucket[] = [
  'income',
  'bills',
  'planned_spend',
  'other_spend',
  'goals',
]

export function bucketFromPath(segment: string | undefined): RoutableBucket | null {
  if (segment === undefined || segment === '') return 'income'
  return ROUTABLE_BUCKETS.find((bucket) => BUCKET_PATHS[bucket] === segment) ?? null
}

/** The ?date= parameter carries the first of the month; a bare YYYY-MM is accepted too. */
export function monthFromParam(value: string | null): string | null {
  if (value === null) return null
  const match = /^(\d{4}-\d{2})(-\d{2})?$/.exec(value)
  return match === null ? null : match[1]
}

/** The `count` months that finished before `month`, newest first; `month` itself is partial and left out. */
export function completedMonthsBefore(month: string, count: number): string[] {
  return Array.from({ length: count }, (_, index) => shiftMonth(month, -(index + 1)))
}
