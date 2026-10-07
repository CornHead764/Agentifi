/**
 * Date windows, in one vocabulary. A window is chosen as a token ("this-month",
 * "-3m"), stored as that token rather than the dates it resolves to today, so
 * "this month" keeps meaning this month. A space's default range (`1M`, `YTD`,
 * …) is a token too, by way of `tokenForRangePreset`, so the register, the
 * reports and the charts open one default on the same window.
 */

import {
  addDays,
  endOfMonth,
  endOfQuarter,
  endOfYear,
  startOfMonth,
  startOfQuarter,
  startOfYear,
  subDays,
  subMonths,
  subQuarters,
  subWeeks,
  subYears,
} from 'date-fns'

import { formatDate, plural, toIsoDate } from '@/lib/format'

/** `YYYY-MM-DD`, no time and no zone. */
type IsoDay = string

export interface DateRange {
  from: IsoDay | null
  to: IsoDay | null
}

/** A token and the two dates it resolved to, or a hand-picked range with `preset` null. */
export interface DateSelection {
  range: DateRange
  preset: string | null
}

/** A window as the endpoints are sent it: a null `from` is unbounded, and every window ends. */
export interface ResolvedWindow {
  from: IsoDay | null
  to: IsoDay
}

export const ALL_TIME: DateRange = { from: null, to: null }

export const ALL_TIME_SELECTION: DateSelection = { range: ALL_TIME, preset: null }

export const DATE_PRESETS: readonly { id: string; label: string }[] = [
  { id: 'this-month', label: 'Month to date' },
  { id: 'this-quarter', label: 'Quarter to date' },
  { id: 'this-year', label: 'Year to date' },
  { id: 'last-month', label: 'Last month' },
  { id: 'last-quarter', label: 'Last quarter' },
  { id: 'last-year', label: 'Last year' },
  { id: '-3m', label: 'Recent 3 months' },
  { id: '-6m', label: 'Recent 6 months' },
  { id: '-12m', label: 'Recent 12 months' },
]

const RELATIVE = /^-(\d+)([dwmy])$/

/** "To date" windows end today: one running into the future changes an account summary's ending balance. */
export function resolveDatePreset(preset: string, today: Date): DateRange | null {
  switch (preset) {
    case 'all-time':
      return ALL_TIME
    case 'this-month':
      return { from: toIsoDate(startOfMonth(today)), to: toIsoDate(today) }
    case 'this-quarter':
      return { from: toIsoDate(startOfQuarter(today)), to: toIsoDate(today) }
    case 'this-year':
      return { from: toIsoDate(startOfYear(today)), to: toIsoDate(today) }
    case 'last-month': {
      const previous = subMonths(today, 1)
      return { from: toIsoDate(startOfMonth(previous)), to: toIsoDate(endOfMonth(previous)) }
    }
    case 'last-quarter': {
      const previous = subQuarters(today, 1)
      return { from: toIsoDate(startOfQuarter(previous)), to: toIsoDate(endOfQuarter(previous)) }
    }
    case 'last-year': {
      const previous = subYears(today, 1)
      return { from: toIsoDate(startOfYear(previous)), to: toIsoDate(endOfYear(previous)) }
    }
    default:
      break
  }

  const relative = RELATIVE.exec(preset)
  if (!relative) return null

  const count = Number(relative[1])
  const unit = relative[2]
  const start =
    unit === 'd'
      ? subDays(today, count)
      : unit === 'w'
        ? subWeeks(today, count)
        : unit === 'm'
          ? subMonths(today, count)
          : subYears(today, count)
  return { from: toIsoDate(start), to: toIsoDate(today) }
}

/** A token's selection; "all-time" is the absent window, drawn as "All time". */
export function selectionForToken(token: string, today: Date = new Date()): DateSelection | null {
  const range = resolveDatePreset(token, today)
  if (range === null) return null
  return token === 'all-time' ? ALL_TIME_SELECTION : { range, preset: token }
}

/**
 * The window a selection asks for today. A token resolves afresh, so a range
 * stored yesterday still means what it says; an open end is today.
 */
export function windowOf(selection: DateSelection, today: Date = new Date()): ResolvedWindow {
  const range =
    selection.preset === null
      ? selection.range
      : (resolveDatePreset(selection.preset, today) ?? selection.range)
  return { from: range.from, to: range.to ?? toIsoDate(today) }
}

/**
 * The days from `back` days before `today` to `ahead` days after, both ends
 * included. Counted in calendar days, so a daylight-saving change never moves
 * an end.
 */
export function dayWindow(back: number, ahead: number, today: Date = new Date()): {
  from: IsoDay
  to: IsoDay
} {
  return { from: toIsoDate(addDays(today, -back)), to: toIsoDate(addDays(today, ahead)) }
}

/** The six Sunday-first weeks a month calendar draws, which overrun the month at both ends. */
export function calendarGrid(month: Date): Date[] {
  const first = new Date(month.getFullYear(), month.getMonth(), 1)
  const start = addDays(first, -first.getDay())
  return Array.from({ length: 42 }, (_, index) => addDays(start, index))
}

export function labelForSelection({ preset, range }: DateSelection): string {
  if (preset === null) {
    if (range.from === null && range.to === null) return 'All time'
    if (range.from !== null && range.to !== null) {
      return `${formatDate(range.from)} – ${formatDate(range.to)}`
    }
    return range.from !== null
      ? `From ${formatDate(range.from)}`
      : `Through ${formatDate(range.to ?? '')}`
  }
  const listed = DATE_PRESETS.find((entry) => entry.id === preset)
  return listed?.label ?? relativeLabel(preset) ?? preset
}

/** The phone's Date button: a custom window's dates do not fit beside the filters. */
export function shortLabelForSelection(selection: DateSelection): string {
  const { preset, range } = selection
  if (preset === null && (range.from !== null || range.to !== null)) return 'Custom dates'
  return labelForSelection(selection)
}

const UNIT_NAMES: Record<string, string> = { d: 'day', w: 'week', m: 'month', y: 'year' }

/** "Recent 5 years" for a relative token the picker does not list. */
function relativeLabel(preset: string): string | null {
  const parts = RELATIVE.exec(preset)
  if (!parts) return null
  const count = Number(parts[1])
  return `Recent ${plural(count, UNIT_NAMES[parts[2]] ?? parts[2])}`
}

/**
 * A space's default range, and the chips on the charts. The durations are
 * trailing windows ending today: the calendar-anchored windows are `QTD` and
 * `YTD`, so `1Y` is the last twelve months, not the year so far.
 */
export const RANGE_PRESETS = ['1M', '3M', '6M', '1Y', '5Y', 'QTD', 'YTD', 'ALL'] as const
export type RangePreset = (typeof RANGE_PRESETS)[number]

const PRESET_TOKENS: Record<RangePreset, string> = {
  '1M': '-1m',
  '3M': '-3m',
  '6M': '-6m',
  '1Y': '-12m',
  '5Y': '-5y',
  QTD: 'this-quarter',
  YTD: 'this-year',
  ALL: 'all-time',
}

export function asRangePreset(value: string | null | undefined): RangePreset | null {
  return RANGE_PRESETS.find((preset) => preset === value) ?? null
}

export function tokenForRangePreset(preset: RangePreset): string {
  return PRESET_TOKENS[preset]
}

export function selectionForRangePreset(
  preset: RangePreset | null,
  today: Date = new Date(),
): DateSelection | null {
  return preset === null ? null : selectionForToken(PRESET_TOKENS[preset], today)
}

export function windowFor(preset: RangePreset, today: Date = new Date()): ResolvedWindow {
  return windowOf(selectionForToken(PRESET_TOKENS[preset], today) ?? ALL_TIME_SELECTION, today)
}

/** The chip's text. */
export function rangeLabel(preset: RangePreset): string {
  return preset === 'ALL' ? 'All' : preset
}

const RANGE_CHANGE_LABELS: Record<RangePreset, string> = {
  '1M': '1 month change',
  '3M': '3 month change',
  '6M': '6 month change',
  '1Y': '1 year change',
  '5Y': '5 year change',
  QTD: 'Quarter to date change',
  YTD: 'Year to date change',
  ALL: 'All time change',
}

export function rangeChangeLabel(preset: RangePreset): string {
  return RANGE_CHANGE_LABELS[preset]
}
