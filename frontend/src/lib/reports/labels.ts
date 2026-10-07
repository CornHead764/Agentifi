/** Report labels in one place, so the table, chart and CSV name a column the same way. */

import type { ColumnDimension, RowDimension, TimeGrain } from '@/lib/clients/reports'
import { formatDate } from '@/lib/format'

const ROW_DIMENSIONS: readonly RowDimension[] = ['category', 'account', 'tag', 'payee']

/**
 * `txf` is only the Taxes preset's grouping, so it stays out of the standing
 * list but joins the menu while active, so the menu can show its selection.
 */
export function rowDimensionOptions(active: RowDimension): readonly RowDimension[] {
  return ROW_DIMENSIONS.includes(active) ? ROW_DIMENSIONS : [...ROW_DIMENSIONS, active]
}
export const COLUMN_DIMENSIONS: readonly ColumnDimension[] = ['time', 'account', 'tag', 'payee']
export const TIME_GRAINS: readonly TimeGrain[] = ['day', 'week', 'month', 'quarter', 'year']

export const DIMENSION_LABELS: Record<RowDimension | ColumnDimension, string> = {
  category: 'Category',
  account: 'Account',
  tag: 'Tag',
  payee: 'Payee',
  txf: 'Tax form',
  time: 'Time',
}

export const GRAIN_LABELS: Record<TimeGrain, string> = {
  day: 'Day',
  week: 'Week',
  month: 'Month',
  quarter: 'Quarter',
  year: 'Year',
}

/** Radix hands back a plain string; these narrow it without a cast. */
export function asRowDimension(value: string): RowDimension {
  return ([...ROW_DIMENSIONS, 'txf'] as const).find((one) => one === value) ?? 'category'
}

export function asColumnDimension(value: string): ColumnDimension {
  return COLUMN_DIMENSIONS.find((one) => one === value) ?? 'time'
}

export function asGrain(value: string): TimeGrain {
  return TIME_GRAINS.find((one) => one === value) ?? 'month'
}

/** Only time keys are rewritten; other columns already carry their display name. */
export function columnHeading(
  key: string,
  label: string,
  dimension: string,
  grain: TimeGrain,
): string {
  if (dimension !== 'time') return label

  switch (grain) {
    case 'year':
      return key
    case 'quarter':
      return key.replace('-', ' ')
    case 'month':
      return formatDate(`${key}-01`, 'monthShort')
    default: {
      const [, month, day] = key.split('-').map(Number)
      return Number.isFinite(month) && Number.isFinite(day) ? `${month}/${day}` : label
    }
  }
}
