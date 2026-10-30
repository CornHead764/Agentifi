/** The register's range calendar. The windows themselves are `lib/dateRanges.ts`. */

import type { DateRange } from '@/lib/dateRanges'

import type { IsoDate } from './types'

/** One click on the range calendar: open, move the start back, or close. */
export function pickRangeDay(range: DateRange, day: IsoDate): DateRange {
  if (range.from === null || (range.from !== null && range.to !== null)) {
    return { from: day, to: null }
  }
  if (day < range.from) {
    return { from: day, to: null }
  }
  return { from: range.from, to: day }
}
