/**
 * The recurrence rule; mirrors `backend/internal/domain/recurrence.go` (see
 * calculations.md §9). The rule fields are the truth and `alias` only names
 * them. "Twice a month" is `byMonthDay: [n, m]`, not a frequency, and "Every X
 * weeks/months/years" reuse the `EVERY_X_DAYS` alias at another `frequency`,
 * so no alias is invented that the domain would reject.
 */

import { addDays } from 'date-fns'

import { ordinal, pluralWord } from './format'

export type Frequency = 'DAILY' | 'WEEKLY' | 'MONTHLY' | 'YEARLY'

export type RecurrenceAlias =
  | 'ONE_TIME'
  | 'EVERY_WEEK'
  | 'EVERY_MONTH'
  | 'TWICE_A_MONTH'
  | 'EVERY_QUARTER'
  | 'EVERY_YEAR'
  | 'EVERY_X_DAYS'
  | 'MULTIPLE_FIXED'

export const WEEKDAYS = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'] as const
export type Weekday = (typeof WEEKDAYS)[number]

export const WEEKDAY_NAMES: Record<Weekday, string> = {
  MO: 'Monday',
  TU: 'Tuesday',
  WE: 'Wednesday',
  TH: 'Thursday',
  FR: 'Friday',
  SA: 'Saturday',
  SU: 'Sunday',
}

export interface Recurrence {
  alias: RecurrenceAlias
  /** Null is the one-time case, which never repeats. */
  frequency: Frequency | null
  interval: number
  /** Days of the month. Negative counts back from the end, so -1 is the last day. */
  by_month_day: readonly number[]
  by_day: readonly Weekday[]
  /** The months (1 to 12) the rule is active in; empty is every month. */
  by_month: readonly number[]
}

/**
 * The rule as the server sends it: Go's one-time frequency is `''`, which must
 * become `null` before `previewOccurrences` reads it, or it falls through to
 * the monthly branch.
 */
export interface WireRecurrence extends Omit<Recurrence, 'frequency'> {
  frequency: Frequency | '' | null
}

export function recurrenceFromWire(raw: WireRecurrence): Recurrence {
  return { ...raw, frequency: raw.frequency === '' ? null : raw.frequency }
}

export const FREQUENCY_OPTIONS = [
  'ONE_TIME',
  'EVERY_WEEK',
  'EVERY_MONTH',
  'EVERY_YEAR',
  'TWICE_A_MONTH',
  'EVERY_QUARTER',
  'EVERY_X_DAYS',
  'EVERY_X_WEEKS',
  'EVERY_X_MONTHS',
  'EVERY_X_YEARS',
  'MULTIPLE_FIXED',
] as const

export type FrequencyOption = (typeof FREQUENCY_OPTIONS)[number]

export const FREQUENCY_OPTION_LABELS: Record<FrequencyOption, string> = {
  ONE_TIME: 'One-time payment',
  EVERY_WEEK: 'Every week',
  EVERY_MONTH: 'Every month',
  EVERY_YEAR: 'Every year',
  TWICE_A_MONTH: 'Twice a month',
  EVERY_QUARTER: 'Every quarter',
  EVERY_X_DAYS: 'Every X days',
  EVERY_X_WEEKS: 'Every X weeks',
  EVERY_X_MONTHS: 'Every X months',
  EVERY_X_YEARS: 'Every X years',
  MULTIPLE_FIXED: 'Multiple fixed dates',
}

/**
 * Which dropdown entry a stored rule reads back as. A one-time alias on a rule
 * that repeats is read from the rule instead, since nothing expands from the alias.
 */
export function optionFor(recurrence: Recurrence): FrequencyOption {
  if (recurrence.alias === 'ONE_TIME' && recurrence.frequency !== null) {
    return optionForRule(recurrence)
  }
  if (recurrence.alias !== 'EVERY_X_DAYS') return recurrence.alias
  return everyXOption(recurrence)
}

function optionForRule(recurrence: Recurrence): FrequencyOption {
  const interval = Math.max(1, recurrence.interval)
  const days = recurrence.by_month_day.length
  switch (recurrence.frequency) {
    case 'WEEKLY':
      return interval === 1 ? 'EVERY_WEEK' : 'EVERY_X_WEEKS'
    case 'MONTHLY':
      if (interval === 1) return days === 2 ? 'TWICE_A_MONTH' : days > 2 ? 'MULTIPLE_FIXED' : 'EVERY_MONTH'
      return interval === 3 ? 'EVERY_QUARTER' : 'EVERY_X_MONTHS'
    case 'YEARLY':
      return interval === 1 ? 'EVERY_YEAR' : 'EVERY_X_YEARS'
    case 'DAILY':
      return 'EVERY_X_DAYS'
    default:
      return 'ONE_TIME'
  }
}

function everyXOption(recurrence: Recurrence): FrequencyOption {
  switch (recurrence.frequency) {
    case 'WEEKLY':
      return 'EVERY_X_WEEKS'
    case 'MONTHLY':
      return 'EVERY_X_MONTHS'
    case 'YEARLY':
      return 'EVERY_X_YEARS'
    default:
      return 'EVERY_X_DAYS'
  }
}

export function takesInterval(option: FrequencyOption): boolean {
  return option.startsWith('EVERY_X_')
}

export function takesMonthDays(option: FrequencyOption): boolean {
  return option === 'EVERY_MONTH' || option === 'TWICE_A_MONTH' || option === 'MULTIPLE_FIXED'
}

/**
 * A daily, weekly or monthly rule can be limited to some months of the year. A
 * yearly rule falls in its start date's month and a one-time rule on its day,
 * so the server refuses active months on either.
 */
export function takesActiveMonths(option: FrequencyOption): boolean {
  return option !== 'ONE_TIME' && option !== 'EVERY_YEAR' && option !== 'EVERY_X_YEARS'
}

export const MONTH_NAMES = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
] as const

/** Sorted and distinct, and all twelve is none: a rule active every month is not seasonal. */
export function activeMonths(months: readonly number[]): number[] {
  const out = [...new Set(months)].filter((month) => month >= 1 && month <= 12).sort((a, b) => a - b)
  return out.length === 12 ? [] : out
}

/**
 * "April to October" for a run of consecutive months, else the list. A run
 * that wraps the new year reads "November to February".
 */
export function describeMonths(months: readonly number[], length: 'long' | 'short' = 'long'): string {
  const name = (month: number) =>
    length === 'short' ? MONTH_NAMES[month - 1].slice(0, 3) : MONTH_NAMES[month - 1]
  const sorted = activeMonths(months)
  if (sorted.length === 0) return 'every month'
  const runs: number[][] = []
  for (const month of sorted) {
    const last = runs[runs.length - 1]
    if (last && last[last.length - 1] === month - 1) last.push(month)
    else runs.push([month])
  }
  const lastRun = runs[runs.length - 1]
  if (runs.length > 1 && runs[0][0] === 1 && lastRun[lastRun.length - 1] === 12) {
    runs.pop()
    runs[0] = [...lastRun, ...runs[0]]
  }
  const names = runs.flatMap((run) =>
    run.length >= 3 ? [`${name(run[0])} to ${name(run[run.length - 1])}`] : run.map(name),
  )
  if (names.length === 1) return names[0]
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}

/**
 * Carry a rule over to a new start date: a single month day or weekday taken
 * from the old start follows the new one; anything picked by hand stays.
 */
export function reanchorRecurrence(recurrence: Recurrence, from: Date, to: Date): Recurrence {
  const days = recurrence.by_month_day
  if (days.length === 1 && days[0] === from.getDate() && to.getDate() !== from.getDate()) {
    return { ...recurrence, by_month_day: [to.getDate()] }
  }
  const weekdays = recurrence.by_day
  const wasStartWeekday = weekdays.length === 1 && weekdays[0] === WEEKDAYS[(from.getDay() + 6) % 7]
  if (wasStartWeekday && to.getDay() !== from.getDay()) {
    return { ...recurrence, by_day: [WEEKDAYS[(to.getDay() + 6) % 7]] }
  }
  return recurrence
}

/** The anchor supplies what the entry leaves unsaid, the same fallback `ExpandOccurrences` applies. */
export function recurrenceFor(
  option: FrequencyOption,
  anchor: Date,
  overrides: {
    interval?: number
    byMonthDay?: readonly number[]
    byDay?: readonly Weekday[]
    byMonth?: readonly number[]
  } = {},
): Recurrence {
  const built = ruleFor(option, anchor, overrides)
  return takesActiveMonths(option) ? { ...built, by_month: activeMonths(overrides.byMonth ?? []) } : built
}

function ruleFor(
  option: FrequencyOption,
  anchor: Date,
  overrides: { interval?: number; byMonthDay?: readonly number[]; byDay?: readonly Weekday[] },
): Recurrence {
  const day = overrides.byMonthDay ?? [anchor.getDate()]
  const weekday = overrides.byDay ?? [WEEKDAYS[(anchor.getDay() + 6) % 7]]
  const interval = Math.max(1, overrides.interval ?? 1)

  switch (option) {
    case 'ONE_TIME':
      return rule('ONE_TIME', null, 1)
    case 'EVERY_WEEK':
      return rule('EVERY_WEEK', 'WEEKLY', 1, [], weekday)
    case 'EVERY_MONTH':
      return rule('EVERY_MONTH', 'MONTHLY', 1, overrides.byDay?.length ? [] : day, overrides.byDay ?? [])
    case 'EVERY_YEAR':
      return rule('EVERY_YEAR', 'YEARLY', 1)
    case 'TWICE_A_MONTH':
      return rule('TWICE_A_MONTH', 'MONTHLY', 1, twoDays(overrides.byMonthDay, anchor))
    case 'EVERY_QUARTER':
      return rule('EVERY_QUARTER', 'MONTHLY', 3, day)
    case 'EVERY_X_DAYS':
      return rule('EVERY_X_DAYS', 'DAILY', interval)
    case 'EVERY_X_WEEKS':
      return rule('EVERY_X_DAYS', 'WEEKLY', interval, [], weekday)
    case 'EVERY_X_MONTHS':
      return rule('EVERY_X_DAYS', 'MONTHLY', interval, day)
    case 'EVERY_X_YEARS':
      return rule('EVERY_X_DAYS', 'YEARLY', interval)
    case 'MULTIPLE_FIXED':
      return rule('MULTIPLE_FIXED', 'MONTHLY', 1, overrides.byMonthDay ?? day)
  }
}

/** Twice a month defaults to the anchor and the day a fortnight from it. */
function twoDays(given: readonly number[] | undefined, anchor: Date): number[] {
  if (given && given.length >= 2) return [...given]
  const first = anchor.getDate()
  return [first, first <= 16 ? first + 15 : first - 15].sort((a, b) => a - b)
}

function rule(
  alias: RecurrenceAlias,
  frequency: Frequency | null,
  interval: number,
  byMonthDay: readonly number[] = [],
  byDay: readonly Weekday[] = [],
): Recurrence {
  return { alias, frequency, interval, by_month_day: [...byMonthDay], by_day: [...byDay], by_month: [] }
}

export function describeRecurrence(recurrence: Recurrence): string {
  const rule = describeRule(recurrence)
  if (recurrence.by_month.length === 0) return rule
  return `${rule} Only in ${describeMonths(recurrence.by_month)}.`
}

function describeRule(recurrence: Recurrence): string {
  const days = recurrence.by_month_day
  const weekdays = recurrence.by_day.map((code) => WEEKDAY_NAMES[code]).join(' and ')
  const every = recurrence.interval > 1 ? `every ${recurrence.interval} ` : 'every '

  switch (recurrence.alias) {
    case 'ONE_TIME':
      return 'Happens once and does not repeat.'
    case 'EVERY_WEEK':
      return weekdays ? `Repeats every week on ${weekdays}.` : 'Repeats every week.'
    case 'EVERY_MONTH':
      if (weekdays) return `Repeats every month on ${weekdays}.`
      return `Repeats every month on the ${listDays(days)}.`
    case 'TWICE_A_MONTH':
      return `Repeats twice a month, on the ${listDays(days)}.`
    case 'EVERY_QUARTER':
      return days.length ? `Repeats every quarter on the ${listDays(days)}.` : 'Repeats every quarter.'
    case 'EVERY_YEAR':
      return 'Repeats every year on the start date.'
    case 'MULTIPLE_FIXED':
      return `Repeats on the ${listDays(days)} of every month.`
    case 'EVERY_X_DAYS':
      switch (recurrence.frequency) {
        case 'WEEKLY':
          return weekdays
            ? `Repeats ${every}weeks on ${weekdays}.`
            : `Repeats ${every}${pluralWord(recurrence.interval, 'week')}.`
        case 'MONTHLY':
          return days.length
            ? `Repeats ${every}${pluralWord(recurrence.interval, 'month')} on the ${listDays(days)}.`
            : `Repeats ${every}${pluralWord(recurrence.interval, 'month')}.`
        case 'YEARLY':
          return `Repeats ${every}${pluralWord(recurrence.interval, 'year')}.`
        default:
          return `Repeats ${every}${pluralWord(recurrence.interval, 'day')}.`
      }
  }
}

/** "1st and 15th"; "1st, 15th and 28th". A day-31 rule clamps, never skips. */
function listDays(days: readonly number[]): string {
  const labels = [...days].sort((a, b) => a - b).map((day) => (day < 0 ? 'last day' : ordinal(day)))
  if (labels.length === 0) return 'start date'
  if (labels.length === 1) return labels[0]
  return `${labels.slice(0, -1).join(', ')} and ${labels[labels.length - 1]}`
}

export function shortLabel(recurrence: Recurrence): string {
  const label = shortRuleLabel(recurrence)
  if (recurrence.by_month.length === 0) return label
  return `${label}, ${describeMonths(recurrence.by_month, 'short')}`
}

function shortRuleLabel(recurrence: Recurrence): string {
  const option = optionFor(recurrence)
  if (!takesInterval(option)) return FREQUENCY_OPTION_LABELS[option]
  const unit = FREQUENCY_OPTION_LABELS[option].replace('Every X ', '')
  return recurrence.interval > 1 ? `Every ${recurrence.interval} ${unit}` : `Every ${unit.replace(/s$/, '')}`
}

/**
 * A small local expansion for *Preview Next Occurrence* only; anything a
 * projection depends on comes from the server's expansion.
 */
export function previewOccurrences(recurrence: Recurrence, startOn: Date, count = 3): Date[] {
  if (recurrence.frequency === null) return [startOn]
  if (recurrence.by_month.length === 0) return expandPreview(recurrence, startOn, count)
  // Expanded past the off season, then filtered, as the server's expansion does.
  return expandPreview(recurrence, startOn, count + 400)
    .filter((on) => recurrence.by_month.includes(on.getMonth() + 1))
    .slice(0, count)
}

function expandPreview(recurrence: Recurrence, startOn: Date, count: number): Date[] {
  const out: Date[] = []
  const interval = Math.max(1, recurrence.interval)

  if (recurrence.frequency === 'DAILY') {
    for (let index = 0; index < count; index += 1) {
      out.push(addDays(startOn, index * interval))
    }
    return out
  }

  if (recurrence.frequency === 'WEEKLY') {
    const wanted = recurrence.by_day.length ? recurrence.by_day : [WEEKDAYS[(startOn.getDay() + 6) % 7]]
    let week = addDays(startOn, -((startOn.getDay() + 6) % 7))
    while (out.length < count) {
      for (const code of wanted) {
        const on = addDays(week, WEEKDAYS.indexOf(code))
        if (on >= startOn && out.length < count) out.push(on)
      }
      week = addDays(week, 7 * interval)
    }
    return out
  }

  const step = recurrence.frequency === 'YEARLY' ? 12 * interval : interval
  const days = recurrence.by_month_day.length ? [...recurrence.by_month_day] : [startOn.getDate()]
  let cursor = new Date(startOn.getFullYear(), startOn.getMonth(), 1)
  while (out.length < count) {
    for (const day of [...days].sort((a, b) => a - b)) {
      const on = clampToMonth(cursor.getFullYear(), cursor.getMonth(), day)
      if (on >= startOn && out.length < count) out.push(on)
    }
    cursor = new Date(cursor.getFullYear(), cursor.getMonth() + step, 1)
  }
  return out
}

/** A day-31 rule lands on the month's last day: never skipped, never rolled into the next month. */
function clampToMonth(year: number, month: number, day: number): Date {
  const length = new Date(year, month + 1, 0).getDate()
  const wanted = day < 0 ? length + 1 + day : day
  return new Date(year, month, Math.min(Math.max(wanted, 1), length))
}
