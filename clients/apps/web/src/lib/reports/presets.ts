/**
 * The report gallery's presentation. Each card's configuration is `reports.go`'s
 * preset table from `/reports/presets`; the copy here is the fallback rendered
 * before that lands. Cards with `servedBy` read something other than
 * transactions and have no row or column axes.
 */

import type { ReportConfig, ReportPreset } from '@/lib/clients/reports'

export type ReportPresetId =
  | 'spending'
  | 'income'
  | 'income_summary'
  | 'income_expense'
  | 'net_worth'
  | 'taxes'
  | 'savings'
  | 'monthly_summary'

export interface GalleryEntry {
  id: ReportPresetId
  name: string
  blurb: string
  /** The endpoint that answers this preset, when the engine does not. */
  servedBy: string | null
  /** A `lib/dateRanges.ts` token, so a new tab resolves it on the day it opens. */
  range: string
  config: ReportConfig
  series: 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8
}

function config(preset: ReportPresetId, over: Partial<ReportConfig>): ReportConfig {
  return {
    preset,
    mode: 'transaction',
    rows: 'category',
    columns: 'time',
    time_grain: 'month',
    sign: 'both',
    ...over,
  }
}

const GALLERY: readonly GalleryEntry[] = [
  {
    id: 'spending',
    name: 'Spending',
    blurb: 'Compare spending by month, quarter or year',
    servedBy: '/reports/spending',
    range: 'this-month',
    series: 1,
    // The period length rides in `time_grain` and the grouping in `rows`, so a
    // saved Spending report keeps both.
    config: config('spending', { mode: 'summary', sign: 'expenses' }),
  },
  {
    id: 'income',
    name: 'Income',
    blurb: 'Track your sources of income',
    servedBy: null,
    range: 'this-year',
    series: 3,
    config: config('income', { mode: 'transaction', sign: 'income' }),
  },
  {
    id: 'income_summary',
    name: 'Income Summary',
    blurb: 'View your income over time',
    servedBy: null,
    range: 'this-year',
    series: 3,
    config: config('income_summary', { mode: 'summary', sign: 'income' }),
  },
  {
    id: 'income_expense',
    name: 'Income & Expense',
    blurb: 'Compare income and expenses',
    servedBy: null,
    range: '-6m',
    series: 2,
    // The net line is income plus expenses per period, not a query of its own.
    config: config('income_expense', { mode: 'summary', sign: 'both' }),
  },
  {
    id: 'net_worth',
    name: 'Net Worth',
    blurb: 'Your net worth over time',
    servedBy: '/net-worth',
    range: '-6m',
    series: 7,
    config: config('net_worth', {}),
  },
  {
    id: 'taxes',
    name: 'Taxes',
    blurb: 'Taxable income and deductions',
    servedBy: null,
    range: 'this-year',
    series: 4,
    // Grouped by the category's TXF mapping: form, then line item, then payee.
    config: config('taxes', { mode: 'transaction', rows: 'txf', sign: 'both' }),
  },
  {
    id: 'savings',
    name: 'Savings',
    blurb: 'Savings progress over time',
    servedBy: '/reports/savings',
    range: 'this-year',
    series: 8,
    config: config('savings', { mode: 'summary', sign: 'both' }),
  },
  {
    id: 'monthly_summary',
    name: 'Monthly Summary',
    blurb: 'A snapshot of your income, spending, and goals for the month.',
    servedBy: '/reports/monthly-summary',
    range: 'last-month',
    series: 5,
    config: config('monthly_summary', {}),
  },
]

/** Whether a stored tab's report type is still in the gallery. */
export function isGalleryPreset(id: string): boolean {
  return GALLERY.some((entry) => entry.id === id)
}

export function galleryEntry(id: string): GalleryEntry {
  const found = GALLERY.find((entry) => entry.id === id)
  if (!found) throw new Error(`unknown report preset: ${id}`)
  return found
}

export function mergeGallery(presets: readonly ReportPreset[] | undefined): GalleryEntry[] {
  return GALLERY.map((entry) => {
    const served = presets?.find((preset) => preset.preset === entry.id)
    if (!served) return entry
    return {
      ...entry,
      name: served.label,
      servedBy: served.served_by,
      config: served.config,
    }
  })
}
