/**
 * Display formatting for everything that is not money. A percentage against a
 * zero prior period is undefined: an absent rate is `null` from the wire to the
 * glyph and renders "—", never "∞" or "0%".
 */

import { displayLocale } from '@/lib/locale'

import { moneyToNumber, type Money } from './money'

export const EM_DASH = '—'

/**
 * A rate off the wire, string or number (display-only, so a number is
 * accepted). An unparseable value is absent, not zero.
 */
export function parseRate(value: string | number | null | undefined): number | null {
  if (value === null || value === undefined) return null
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : null
}

/** A share count to four places; unparseable is a dash, since zero would read as a closed position. */
export function formatShares(shares: string | number | null): string {
  const parsed = parseRate(shares)
  if (parsed === null) return EM_DASH
  return formatNumber(parsed, { maximumFractionDigits: 4 })
}

/** Not for money, which is `formatMoney`. */
function formatNumber(value: number, options: Intl.NumberFormatOptions = {}): string {
  return new Intl.NumberFormat(displayLocale(), options).format(value)
}

export function formatCount(count: number): string {
  return formatNumber(count, { maximumFractionDigits: 0 })
}

/**
 * The change as a fraction, or null from zero. Divides by the magnitude of the
 * start, matching `NetWorthChangePct` in the Go core.
 */
export function changeFraction(start: Money, end: Money): number | null {
  if (start === 0) return null
  return (end - start) / Math.abs(start)
}

export interface PercentOptions {
  digits?: number
  showPlus?: boolean
}

/** A fraction as a percentage; null, infinite or NaN renders the em dash. */
export function formatPercent(fraction: number | null, options: PercentOptions = {}): string {
  if (fraction === null || !Number.isFinite(fraction)) return EM_DASH

  const { digits = 2, showPlus = false } = options
  const percent = fraction * 100
  const text = `${formatNumber(percent, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
    useGrouping: false,
  })}%`
  return showPlus && percent > 0 ? `+${text}` : text
}

/**
 * A wire value already in percent units: `4.21` prints as `4.21%`. Every
 * `*_pct` field is percent units (`domain.Percent`); proportion names like
 * `share` or `savings_rate` are fractions (`domain.Ratio`).
 */
export function formatPercentUnits(percent: number | null, options: PercentOptions = {}): string {
  return formatPercent(percent === null ? null : percent / 100, options)
}

/** For `PercentText` and `ChangeBadge`, which take percent units, from a local fraction. */
export function fractionToPercentUnits(fraction: number | null): number | null {
  return fraction === null ? null : fraction * 100
}

/** A share of a total; a share of nothing is null. */
export function shareOf(part: Money, total: Money): number | null {
  if (total === 0) return null
  return moneyToNumber(part) / moneyToNumber(total)
}

/**
 * A `YYYY-MM-DD` as a local calendar day, never through `new Date(string)`,
 * which reads a bare date as UTC midnight. An RFC3339 instant is cut to its
 * leading date.
 */
export function parseIsoDate(iso: string): Date {
  const [year, month, day] = iso.slice(0, 10).split('-').map(Number)
  return new Date(year, (month ?? 1) - 1, day ?? 1)
}

const ISO_DAY = /^(\d{4})-(\d{2})-(\d{2})$/

/**
 * A typed or imported `YYYY-MM-DD`, validated rather than trusted: `new Date`
 * rolls Feb 31 into March 3 instead of rejecting it, so the fields are read
 * back off the constructed date and checked against what was typed.
 */
export function parseIsoDay(value: string): Date | null {
  const match = ISO_DAY.exec(value.trim())
  if (!match) return null
  const [, yearText, monthText, dayText] = match
  const year = Number(yearText)
  const month = Number(monthText)
  const day = Number(dayText)
  const date = new Date(year, month - 1, day)
  const roundTrips =
    date.getFullYear() === year && date.getMonth() === month - 1 && date.getDate() === day
  return roundTrips ? date : null
}

export function toIsoDate(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

const DATE_FORMATS = {
  /** Aug 21, 2026 */
  full: { month: 'short', day: 'numeric', year: 'numeric' },
  /** Aug 21 */
  short: { month: 'short', day: 'numeric' },
  /** Aug 2026 */
  month: { month: 'short', year: 'numeric' },
  /** August 2026 */
  monthLong: { month: 'long', year: 'numeric' },
  /** Aug */
  monthShort: { month: 'short' },
} as const satisfies Record<string, Intl.DateTimeFormatOptions>

export type DateStyle = keyof typeof DATE_FORMATS

/**
 * Axis labels unique across the series: recharts resolves hover through the
 * label values, so a repeated label is two points claiming one column.
 */
export function axisLabels(isos: readonly string[]): string[] {
  for (const style of ['short', 'month', 'full'] as const) {
    const labels = isos.map((iso) => formatDate(iso, style))
    if (new Set(labels).size === labels.length) return labels
  }
  return [...isos]
}

export function formatDate(
  iso: string,
  style: DateStyle = 'full',
  locale: string | undefined = displayLocale(),
): string {
  const date = parseIsoDate(iso)
  // Intl throws on an Invalid Date, which would take down the whole page.
  if (Number.isNaN(date.getTime())) return iso
  return new Intl.DateTimeFormat(locale, DATE_FORMATS[style]).format(date)
}

const TIMESTAMP_FORMATS = {
  /** Aug 21, 2026 */
  date: { month: 'short', day: 'numeric', year: 'numeric' },
  /** Aug 21, 2026, 3:04 PM */
  dateTime: { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' },
  /** 3:04 PM */
  time: { hour: 'numeric', minute: '2-digit' },
} as const satisfies Record<string, Intl.DateTimeFormatOptions>

/** An instant (RFC3339), in the viewer's time zone; the raw string if it does not parse. */
export function formatTimestamp(
  iso: string,
  style: keyof typeof TIMESTAMP_FORMATS = 'dateTime',
  locale: string | undefined = displayLocale(),
): string {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return iso
  return new Intl.DateTimeFormat(locale, TIMESTAMP_FORMATS[style]).format(at)
}

/** "In 2 days", "6 days ago", "Today": whole calendar days, off local midnights. */
export function relativeDay(iso: string, today: Date = new Date()): string {
  const target = parseIsoDate(iso)
  const midnight = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  const days = Math.round((target.getTime() - midnight.getTime()) / 86_400_000)

  if (days === 0) return 'Today'
  if (days === 1) return 'Tomorrow'
  if (days === -1) return 'Yesterday'
  if (days > 1 && days <= 30) return `In ${days} days`
  if (days < -1 && days >= -30) return `${-days} days ago`
  return formatDate(iso, 'full')
}

/** `2026-08` */
export function monthKey(date: Date = new Date()): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
}

/** A month key in words: "August 2026", or "Aug 2026" with `month`. */
export function formatMonthKey(
  key: string,
  style: 'monthLong' | 'month' = 'monthLong',
  locale: string | undefined = displayLocale(),
): string {
  return formatDate(`${key}-01`, style, locale)
}

export function daysInMonth(key: string): number {
  const [year, month] = key.split('-').map(Number)
  return new Date(year, month, 0).getDate()
}

/** The first and last calendar day of a month key, as ISO dates. */
export function monthBounds(key: string): { from: string; to: string } {
  return { from: `${key}-01`, to: `${key}-${String(daysInMonth(key)).padStart(2, '0')}` }
}

/** "3d ago" (`short`) or "3 days ago" (`long`); a date after thirty days. */
export function timeAgo(
  timestamp: string | null,
  style: 'short' | 'long' = 'short',
  now: Date = new Date(),
): string {
  if (timestamp === null) return style === 'long' ? 'never' : ''
  const at = new Date(timestamp)
  if (Number.isNaN(at.getTime())) return ''

  const seconds = Math.round((now.getTime() - at.getTime()) / 1000)
  // A minute and a half either way is a clock difference, not an event.
  if (Math.abs(seconds) < 90) return 'just now'
  const minutes = Math.round(Math.abs(seconds) / 60)
  const hours = Math.round(minutes / 60)
  const days = Math.round(hours / 24)
  if (days >= 30) return formatTimestamp(timestamp, 'date')

  const [count, unit] =
    minutes < 60 ? [minutes, 'minute'] : hours < 24 ? [hours, 'hour'] : [days, 'day']
  const span = style === 'short' ? `${count}${unit[0]}` : plural(count, unit)
  return seconds < 0 ? `in ${span}` : `${span} ago`
}

export function ordinal(day: number): string {
  const remainder = day % 100
  if (remainder >= 11 && remainder <= 13) return `${day}th`
  switch (day % 10) {
    case 1:
      return `${day}st`
    case 2:
      return `${day}nd`
    case 3:
      return `${day}rd`
    default:
      return `${day}th`
  }
}

/**
 * A stored rate as the percentage typed into a field (`0.2499` reads `24.99`).
 * The point moves in text: multiplying gives float noise that would be saved back.
 */
export function rateAsPercentText(rate: string | null): string {
  if (rate === null) return ''
  const negative = rate.startsWith('-')
  const [whole = '0', fraction = ''] = (negative ? rate.slice(1) : rate).split('.')
  const padded = fraction.padEnd(2, '0')
  const shifted = `${whole}${padded.slice(0, 2)}`.replace(/^0+(?=\d)/, '')
  const rest = padded.slice(2).replace(/0+$/, '')
  return `${negative ? '-' : ''}${shifted}${rest === '' ? '' : `.${rest}`}`
}

/** Just the word a count takes, without the count itself: "month" or "months". */
export function pluralWord(count: number, singular: string, pluralForm = `${singular}s`): string {
  return count === 1 ? singular : pluralForm
}

export function plural(count: number, singular: string, pluralForm = `${singular}s`): string {
  return `${formatCount(count)} ${pluralWord(count, singular, pluralForm)}`
}

export function capitalize(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/** Trimmed, capitalised and ending in a stop, so a message from elsewhere reads as a sentence. */
export function asSentence(text: string): string {
  const trimmed = capitalize(text.trim())
  if (trimmed === '') return trimmed
  return /[.!?]$/.test(trimmed) ? trimmed : `${trimmed}.`
}

/** The upper-cased first letter drawn on an avatar, or "" for a blank label. */
export function initial(label: string): string {
  return label.trim().charAt(0).toUpperCase()
}

/** A blank field clears the stored value; it never writes an empty string. */
export function blankToNull(value: string): string | null {
  const trimmed = value.trim()
  return trimmed === '' ? null : trimmed
}

/**
 * A duration off the wire, string or number, clamped to the range the API
 * enforces. Out of range or unparseable falls back rather than animating or
 * dismissing on nonsense.
 */
export function coerceMs(value: unknown, fallback: number, min: number, max: number): number {
  const ms = typeof value === 'string' && value.trim() !== '' ? Number(value) : value
  if (typeof ms !== 'number' || !Number.isFinite(ms)) return fallback
  if (ms < min || ms > max) return fallback
  return Math.round(ms)
}
