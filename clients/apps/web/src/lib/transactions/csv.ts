import { csvRow, csvText } from '@/lib/csv'
import { amountToWire } from '@/lib/money'

import type { Transaction, Uuid } from './types'

/** Amounts in wire format, not display format, so a spreadsheet can sum them. */
export function registerToCsv(
  rows: readonly Transaction[],
  accountName: (id: Uuid) => string,
  categoryName: (id: Uuid | null) => string,
): string {
  const header = [
    'Date',
    'Account',
    'Statement name',
    'Payee',
    'Category',
    'Amount',
    'Reviewed',
    'Excluded from reports',
    'Excluded from spending plan',
  ]
  const lines = rows.map((row) =>
    csvRow([
      row.date,
      csvText(accountName(row.account_id)),
      csvText(row.statement_name),
      csvText(row.payee),
      csvText(categoryCell(row, categoryName)),
      amountToWire(row.amount),
      String(row.is_reviewed),
      String(row.excluded_from_reports),
      String(row.excluded_from_spending_plan),
    ]),
  )
  return [csvRow(header), ...lines].join('\n')
}

/** A split row's parent carries no category, so its cell names each split's. */
function categoryCell(row: Transaction, categoryName: (id: Uuid | null) => string): string {
  if (row.splits.length === 0) return categoryName(row.category_id)
  return [...new Set(row.splits.map((split) => categoryName(split.category_id)))].join(', ')
}
