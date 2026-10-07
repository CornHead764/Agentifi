/**
 * Chart colours and the money axis tick, apart from the components for Fast
 * Refresh. The one list of categorical colours, in `tokens.css` order.
 */

import { maskMoney } from '@/components/moneyText'
import { usePrivacy } from '@/contexts/privacy'
import { formatPercentUnits } from '@/lib/format'
import { displayCurrency } from '@/lib/money'
import { displayLocale } from '@/lib/locale'

const SERIES_COLORS = [
  'var(--series-1)',
  'var(--series-2)',
  'var(--series-3)',
  'var(--series-4)',
  'var(--series-5)',
  'var(--series-6)',
  'var(--series-7)',
  'var(--series-8)',
] as const

export function seriesColor(index: number): string {
  return SERIES_COLORS[index % SERIES_COLORS.length]
}

// Every chart's axes, grid and hover cursor. Tick text is sized in
// screens.css with the rest of the scale: a font size read here runs at module
// load, which is not reliably after the stylesheet.
export const AXIS = { stroke: 'var(--text-faint)', tickLine: false, axisLine: false }
export const GRID = { stroke: 'var(--border)', strokeDasharray: '2 4' }
/** The band behind a hovered bar. */
export const BAR_CURSOR = { fill: 'var(--surface-hover)' }
/** The rule through a hovered point on a line or area. */
export const LINE_CURSOR = { stroke: 'var(--border-strong)' }

export const INCOME_COLOR = 'var(--income)'
export const EXPENSE_COLOR = 'var(--expense)'
export const ACCENT_COLOR = 'var(--accent)'

/** A compact money-axis tick (`$570K`) that disappears under privacy mode. */
export function useMoneyTick(): (value: number) => string {
  const { hidden } = usePrivacy()
  return (value: number) => {
    const text = compactMoney(displayCurrency()).format(value)
    return hidden ? maskMoney(text) : text
  }
}

const compactFormats = new Map<string, Intl.NumberFormat>()

function compactMoney(currency: string): Intl.NumberFormat {
  const locale = displayLocale()
  const key = `${locale ?? ''}|${currency}`
  let format = compactFormats.get(key)
  if (format === undefined) {
    format = new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      notation: 'compact',
      maximumFractionDigits: 1,
    })
    compactFormats.set(key, format)
  }
  return format
}

export function usePercentTick(): (value: number) => string {
  const { hidden } = usePrivacy()
  return (value: number) => (hidden ? '•••' : formatPercentUnits(value, { digits: 0 }))
}
