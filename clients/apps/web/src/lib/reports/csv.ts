/**
 * CSV export from the rendered result, not a second request, so the file
 * matches the screen. Amounts are plain `1234.56` so a spreadsheet can parse them.
 */

import type {
  ReportNode,
  ReportResult,
  ReportSummaryResult,
  ReportTransactionResult,
} from '@/lib/clients/reports'
import { amountToWire, type Money } from '@/lib/money'
import { csvRow, csvText } from '../csv'

export function reportToCsv(result: ReportResult): string {
  if (result.transaction) return transactionCsv(result.transaction)
  if (result.summary) return summaryCsv(result.summary)
  return ''
}

function transactionCsv(result: ReportTransactionResult): string {
  const lines = [csvRow(['Group', 'Date', 'Payee', 'Amount', 'Notes'])]

  const walk = (node: ReportNode, trail: readonly string[]): void => {
    const path = [...trail, node.label]
    const joined = path.join(GROUP_SEPARATOR)
    for (const entry of node.transactions) {
      lines.push(
        csvRow([csvText(joined), entry.on, csvText(entry.payee), amount(entry.amount), csvText(entry.notes ?? '')]),
      )
    }
    for (const child of node.children) walk(child, path)
    lines.push(csvRow([`${joined} — Subtotal`, '', '', amount(node.total), '']))
  }

  for (const group of result.groups) walk(group, [])
  lines.push(csvRow(['Total', '', '', amount(result.total), '']))
  return lines.join('\r\n')
}

function summaryCsv(result: ReportSummaryResult): string {
  const lines = [csvRow(['Row', ...result.columns.map((column) => column.label), 'Total'])]

  for (const pivotRow of result.rows) {
    lines.push(csvRow([csvText(pivotRow.label), ...pivotRow.cells.map(amount), amount(pivotRow.total)]))
  }

  lines.push(csvRow(['Total', ...result.column_totals.map(amount), amount(result.total)]))
  return lines.join('\r\n')
}

/** The trail separator between group levels: U+203A, not a plain angle. */
const GROUP_SEPARATOR = ' \u203a '

function amount(value: Money): string {
  return amountToWire(value)
}
