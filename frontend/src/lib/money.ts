/**
 * Money on the client: amounts arrive as decimal strings, are parsed once at
 * the boundary, and past it are `Money` — branded integer cents, so bare `+`
 * fails to compile and every combination goes through the functions below.
 * Multiplication and division round half away from zero exactly once, in
 * `scaleMoney`/`divideMoney`, matching `quantize()` in
 * `backend/internal/domain/money.go`.
 */

import { displayLocale } from '@/lib/locale'

declare const MONEY: unique symbol

export type Money = number & { readonly [MONEY]: true }

export const ZERO_MONEY = 0 as Money

/** The exact shape `Decimal.quantize(Decimal("0.01"))` serializes to. */
const WIRE_FORMAT = /^([+-])?(\d+)(?:\.(\d+))?$/

/**
 * Coerce one serialized amount to `Money`, parsing the digits rather than
 * through a float. A JSON number is rejected: it has already lost precision
 * on the server.
 */
export function parseMoney(value: string): Money {
  if (typeof value !== 'string') {
    throw new TypeError(
      `money field arrived as ${typeof value} (${String(value)}); the server must serialize Decimal as a string`,
    )
  }

  const match = WIRE_FORMAT.exec(value.trim())
  if (!match) throw new TypeError(`unparseable money value: ${JSON.stringify(value)}`)

  const [, sign, whole, fraction = ''] = match
  const units = digitsToCents(whole, fraction)

  return (sign === '-' ? -units : units) as Money
}

/** Whole and fractional digits to cents, never through a float; a third digit rounds half up, as `quantize()` does. */
export function digitsToCents(whole: string, fraction: string): number {
  const units = Number(whole || '0') * 100 + Number((fraction + '00').slice(0, 2))
  return fraction.length > 2 && Number(fraction[2]) >= 5 ? units + 1 : units
}

/** Integer cents to the 2-dp wire string, without float arithmetic. */
export function amountToWire(value: Money): string {
  return (value < 0 ? '-' : '') + placeDecimal(Math.abs(value))
}

function placeDecimal(units: number): string {
  const digits = String(units).padStart(3, '0')
  return `${digits.slice(0, -2)}.${digits.slice(-2)}`
}

const TYPED_AMOUNT = /^([+-])?[$£€]?\s*(\d{1,3}(?:,\d{3})*|\d*)(?:\.(\d{0,6}))?$/

export interface ParsedAmount {
  wire: string
  cents: Money
}

/**
 * Parse a typed amount without ever touching a float: `parseFloat("0.07") * 100`
 * is 7.000000000000001. Accounting notation wraps a negative figure in parens;
 * a sign inside them is redundant, not a second negation, so `(-5)` is still -5.
 */
export function parseAmountInput(text: string): ParsedAmount | null {
  let trimmed = text.trim()
  let parenNegative = false
  if (trimmed.startsWith('(') && trimmed.endsWith(')')) {
    parenNegative = true
    trimmed = trimmed.slice(1, -1).trim()
  }

  const match = TYPED_AMOUNT.exec(trimmed)
  if (!match) return null

  const [, sign, wholeRaw, fractionRaw] = match
  const whole = (wholeRaw ?? '').replace(/,/g, '')
  const fraction = fractionRaw ?? ''
  if (whole === '' && fraction === '') return null

  const units = digitsToCents(whole, fraction)
  if (!Number.isSafeInteger(units)) return null

  const negative = parenNegative || sign === '-'
  return {
    wire: amountToWire(moneyFromCents(negative ? -units : units)),
    cents: moneyFromCents(negative ? -units : units),
  }
}

/** A typed amount that may be left blank: its wire string, `null` when blank, `undefined` when it is not an amount. */
export function optionalAmountWire(text: string): string | null | undefined {
  if (text.trim() === '') return null
  return parseAmountInput(text)?.wire
}

/** The field error under typed text that is not an amount; null when it is one, or is blank. */
export function amountFieldError(text: string): string | null {
  return optionalAmountWire(text) === undefined ? `"${text.trim()}" is not an amount.` : null
}

const TYPED_WHOLE = /^(\d{1,3}(?:,\d{3})+|\d+)$/

/** Parse a typed count or day of the month: digits, optionally grouped by commas. */
export function parseWholeNumber(text: string): number | null {
  const match = TYPED_WHOLE.exec(text.trim())
  if (!match) return null
  const value = Number(match[1].replace(/,/g, ''))
  return Number.isSafeInteger(value) ? value : null
}

/** For literals and fixtures: `moneyFromCents(-190_000)` is -$1,900.00. */
export function moneyFromCents(cents: number): Money {
  if (!Number.isSafeInteger(cents)) {
    throw new TypeError(`money is an integer count of cents, got ${cents}`)
  }
  return cents as Money
}

export function addMoney(a: Money, b: Money): Money {
  return (a + b) as Money
}

export function subMoney(a: Money, b: Money): Money {
  return (a - b) as Money
}

export function sumMoney(values: Iterable<Money>): Money {
  let total = 0
  for (const value of values) total += value
  return total as Money
}

export function absMoney(value: Money): Money {
  return Math.abs(value) as Money
}

/**
 * Multiply by a plain factor, rounding half away from zero. Round once, at the
 * end of a chain: scaling an already-scaled value compounds the error.
 */
export function scaleMoney(value: Money, factor: number): Money {
  const scaled = value * factor
  return (scaled < 0 ? -Math.round(-scaled) : Math.round(scaled)) as Money
}

export interface CurrencyAmount {
  currency: string
  amount: Money
}

/** One total per currency, in first-seen order. Amounts in different currencies are never added. */
export function sumByCurrency(items: Iterable<CurrencyAmount>): CurrencyAmount[] {
  const totals = new Map<string, number>()
  for (const { currency, amount } of items) {
    const code = currency.toUpperCase()
    totals.set(code, (totals.get(code) ?? 0) + amount)
  }
  return [...totals].map(([currency, amount]) => ({ currency, amount: amount as Money }))
}

/** Divide by a whole count, directly rather than by a rounded reciprocal; rounds like `scaleMoney`. */
export function divideMoney(value: Money, divisor: number): Money {
  const quotient = value / divisor
  return (quotient < 0 ? -Math.round(-quotient) : Math.round(quotient)) as Money
}

/** Major units as a float, for `Intl` and chart libraries only — never to add or compare. */
export function moneyToNumber(value: Money): number {
  return value / 100
}

export type SignConvention =
  /** Render the stored sign: expenses negative, income positive. */
  | 'stored'
  /** Flip, so an expense reads positive. Spending totals, envelopes, category rows. */
  | 'spend'
  /** Drop the sign entirely. */
  | 'absolute'

export interface FormatMoneyOptions {
  /** ISO 4217; defaults to the space's primary currency. */
  currency?: string
  locale?: string
  signs?: SignConvention
  /** Explicit `+` on positives, for deltas and net-worth changes. */
  showPlus?: boolean
  /** `false` drops the cents, for axis labels and dashboard tiles. */
  showCents?: boolean
  /** `false` drops the currency symbol; never use it where two currencies can meet. */
  showSymbol?: boolean
}

/**
 * A module variable rather than a context because `formatMoney` is called
 * outside components (cell formatters, chart ticks, CSV). USD matches the
 * server's default primary currency.
 */
let displayCurrencyCode = 'USD'

export function setDisplayCurrency(code: string): void {
  if (code) displayCurrencyCode = code.toUpperCase()
}

export function displayCurrency(): string {
  return displayCurrencyCode
}

/** `formatMoney`'s shape, for text that privacy mode masks (`useMoneyText`). */
export type MoneyFormatter = (value: Money, options?: FormatMoneyOptions) => string

/** Pass `signs` to change the displayed sign; never negate the stored value. */
export function formatMoney(value: Money, options: FormatMoneyOptions = {}): string {
  const {
    currency = displayCurrencyCode,
    locale = displayLocale(),
    signs = 'stored',
    showPlus = false,
    showCents = true,
    showSymbol = true,
  } = options

  let units: number = value
  if (signs === 'spend') units = -units
  else if (signs === 'absolute') units = Math.abs(units)
  // Flipping zero yields -0, which Intl renders as "-$0.00".
  if (units === 0) units = 0

  const digits = showCents ? 2 : 0
  // `currencyDisplay: 'code'` then stripping the code keeps Intl's locale grouping.
  const formatted = new Intl.NumberFormat(locale, {
    style: 'currency',
    currency,
    currencyDisplay: showSymbol ? 'symbol' : 'code',
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })
    // The exact decimal string, never cents / 100, so Intl is not handed a float.
    .format(amountToWire(units as Money) as `${number}`)

  // Strip the code with only the spaces touching it: some locales group
  // thousands with a non-breaking space.
  const rendered = showSymbol
    ? formatted
    : formatted.replace(new RegExp(`\\s*${currency}\\s*`, 'u'), '').trim()

  return showPlus && units > 0 ? `+${rendered}` : rendered
}
