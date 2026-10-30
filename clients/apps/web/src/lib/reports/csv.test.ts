import { describe, expect, it } from 'vitest'

import type {
  ReportNode,
  ReportResult,
  ReportSummaryResult,
  ReportTransactionResult,
} from '@/lib/clients/reports'
import { moneyFromCents } from '@/lib/money'

import { reportToCsv } from './csv'

function result(parts: {
  transaction?: ReportTransactionResult
  summary?: ReportSummaryResult
}): ReportResult {
  return {
    window: { from: null, to: null, date_field: 'effective' },
    config: {} as ReportResult['config'],
    filter_id: null,
    totals: {
      income: moneyFromCents(0),
      expenses: moneyFromCents(0),
      net: moneyFromCents(0),
      savings_rate: null,
      count: 0,
    },
    transaction: parts.transaction ?? null,
    summary: parts.summary ?? null,
  }
}

function node(label: string, transactions: ReportNode['transactions']): ReportNode {
  return {
    key: label,
    label,
    depth: 0,
    total: moneyFromCents(-2_500),
    count: transactions.length,
    children: [],
    transactions,
  }
}

function txn(payee: string, notes: string | null): ReportNode['transactions'][number] {
  return {
    transaction_id: 'a',
    split_id: null,
    on: '2026-08-04',
    payee,
    account_id: 'b',
    category_id: null,
    amount: moneyFromCents(-2_500),
    notes,
  }
}

describe('the report CSV', () => {
  it('writes the columns the server writes, in the server’s order', () => {
    const csv = reportToCsv(
      result({
        transaction: {
          groups: [node('Auto', [txn('Shell', 'fill up')])],
          total: moneyFromCents(-2_500),
          count: 1,
        },
      }),
    )
    const lines = csv.split('\r\n')
    expect(lines[0]).toBe('Group,Date,Payee,Amount,Notes')
    expect(lines[1]).toBe('Auto,2026-08-04,Shell,-25.00,fill up')
    expect(lines[2]).toBe('Auto — Subtotal,,,-25.00,')
    expect(lines[3]).toBe('Total,,,-25.00,')
  })

  it('neutralizes a payee, a note and a group that open like a formula', () => {
    const csv = reportToCsv(
      result({
        transaction: {
          groups: [node('=SUM(A1)', [txn('=cmd|calc', '@import')])],
          total: moneyFromCents(-2_500),
          count: 1,
        },
      }),
    )
    // The apostrophe is what every spreadsheet reads as "this cell is text".
    expect(csv.split('\r\n')[1]).toBe("'=SUM(A1),2026-08-04,'=cmd|calc,-25.00,'@import")
  })

  it('leaves a negative amount alone, as the server does', () => {
    const csv = reportToCsv(
      result({
        summary: {
          row_dimension: 'category',
          column_dimension: 'month',
          columns: [{ key: '2026-08', label: 'Aug 2026' }],
          rows: [
            {
              key: 'auto',
              label: '-Auto',
              section: 'Expenses',
              cells: [moneyFromCents(-2_500)],
              total: moneyFromCents(-2_500),
            },
          ],
          sections: [],
          column_totals: [moneyFromCents(-2_500)],
          total: moneyFromCents(-2_500),
        },
      }),
    )
    const lines = csv.split('\r\n')
    expect(lines[0]).toBe('Row,Aug 2026,Total')
    // The label is neutralized; the amounts beside it are not, or the column
    // stops being a number.
    expect(lines[1]).toBe("'-Auto,-25.00,-25.00")
    expect(lines[2]).toBe('Total,-25.00,-25.00')
  })
})
