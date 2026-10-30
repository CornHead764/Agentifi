/**
 * Neither rendering adds anything up: every figure comes from the engine's one
 * grouping pass. A tag report files a two-tag allocation under both tags, so
 * the rows deliberately sum to more than the grand total, and a component that
 * re-derived a total would print a different number.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { ReportSummaryResult, ReportTransactionResult } from '@/lib/clients/reports'
import { moneyFromCents } from '@/lib/money'

import { SummaryReport, TransactionReport, type NameLookup } from './renderings'

const NAMES: NameLookup = {
  account: () => 'Everyday Checking',
  category: () => 'Gas & Fuel',
}

function text(node: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(node).replace(/<[^>]*>/g, ' ')
}

const TRANSACTION: ReportTransactionResult = {
  total: moneyFromCents(-15_000),
  count: 2,
  groups: [
    {
      key: 'work',
      label: 'Work',
      depth: 0,
      total: moneyFromCents(-10_000),
      count: 1,
      children: [],
      transactions: [
        {
          transaction_id: 't1',
          split_id: null,
          on: '2026-08-03',
          payee: 'Shell',
          account_id: 'a1',
          category_id: 'c1',
          amount: moneyFromCents(-10_000),
          notes: null,
        },
      ],
    },
    {
      key: 'home',
      label: 'Home',
      depth: 0,
      total: moneyFromCents(-10_000),
      count: 1,
      children: [],
      transactions: [
        {
          transaction_id: 't1',
          split_id: 's1',
          on: '2026-08-03',
          payee: 'Shell',
          account_id: 'a1',
          category_id: 'c1',
          amount: moneyFromCents(-10_000),
          notes: null,
        },
      ],
    },
  ],
}

const SUMMARY: ReportSummaryResult = {
  row_dimension: 'tag',
  column_dimension: 'time',
  columns: [{ key: '2026-08', label: '2026-08' }],
  rows: [
    {
      key: 'work',
      label: 'Work',
      section: '',
      cells: [moneyFromCents(-10_000)],
      total: moneyFromCents(-10_000),
    },
    {
      key: 'home',
      label: 'Home',
      section: '',
      cells: [moneyFromCents(-10_000)],
      total: moneyFromCents(-10_000),
    },
  ],
  sections: [],
  column_totals: [moneyFromCents(-15_000)],
  total: moneyFromCents(-15_000),
}

/** Both signs at once: the engine splits the rows into families. */
const SECTIONED: ReportSummaryResult = {
  row_dimension: 'category',
  column_dimension: 'time',
  columns: [{ key: '2026-08', label: '2026-08' }],
  rows: [
    {
      key: 'salary',
      label: 'Salary',
      section: 'income',
      cells: [moneyFromCents(300_000)],
      total: moneyFromCents(300_000),
    },
    {
      key: 'groceries',
      label: 'Groceries',
      section: 'expense',
      cells: [moneyFromCents(-10_000)],
      total: moneyFromCents(-10_000),
    },
  ],
  sections: [
    {
      key: 'income',
      label: 'Income',
      cells: [moneyFromCents(300_000)],
      total: moneyFromCents(300_000),
    },
    {
      key: 'expense',
      label: 'Expenses',
      cells: [moneyFromCents(-10_000)],
      total: moneyFromCents(-10_000),
    },
  ],
  column_totals: [moneyFromCents(290_000)],
  total: moneyFromCents(290_000),
}

describe('the transaction rendering', () => {
  it('prints the engine’s grand total, not the sum of its groups', () => {
    const rendered = text(
      <TransactionReport result={TRANSACTION} heading="Tag" names={NAMES} />,
    )
    expect(rendered).toContain('Grand total')
    expect(rendered).toContain('-$150.00')
    // Two groups of $100 each. A rendering that added them would say $200.
    expect(rendered).not.toContain('-$200.00')
  })

  it('prints every subtotal the engine sent', () => {
    const rendered = text(<TransactionReport result={TRANSACTION} heading="Tag" names={NAMES} />)
    expect(rendered).toContain('Total Work')
    expect(rendered).toContain('Total Home')
  })

  it('says so rather than rendering an empty grid', () => {
    const rendered = text(
      <TransactionReport
        result={{ groups: [], total: moneyFromCents(0), count: 0 }}
        heading="Category"
        names={NAMES}
      />,
    )
    expect(rendered).toContain('No transactions fall inside this report')
  })
})

describe('the summary rendering', () => {
  it('splits into the engine’s sections, income above expenses, with its subtotals', () => {
    const rendered = text(<SummaryReport result={SECTIONED} heading="Category" grain="month" />)
    expect(rendered).toContain('Total Income')
    expect(rendered).toContain('Total Expenses')
    // Income's section header precedes the expense rows.
    expect(rendered.indexOf('Salary')).toBeLessThan(rendered.indexOf('Groceries'))
    // The subtotals are the engine's, rendered as sent.
    expect(rendered).toContain('$3,000.00')
    expect(rendered).toContain('-$100.00')
  })

  it('stays one flat list when the engine sent no sections', () => {
    const rendered = text(<SummaryReport result={SUMMARY} heading="Tag" grain="month" />)
    expect(rendered).not.toContain('Total Work')
    expect(rendered).not.toContain('Total Home')
  })

  it('prints the engine’s column total, not the sum of the cells above it', () => {
    const rendered = text(<SummaryReport result={SUMMARY} heading="Tag" grain="month" />)
    expect(rendered).toContain('-$150.00')
    expect(rendered).not.toContain('-$200.00')
  })

  it('formats a time column rather than echoing its sort key', () => {
    const rendered = text(<SummaryReport result={SUMMARY} heading="Tag" grain="month" />)
    expect(rendered).toContain('Aug')
    expect(rendered).not.toContain('2026-08')
  })

  it('leaves a non-time column heading exactly as the engine named it', () => {
    const rendered = text(
      <SummaryReport
        result={{
          ...SUMMARY,
          column_dimension: 'account',
          columns: [{ key: 'a1', label: 'Everyday Checking' }],
        }}
        heading="Tag"
        grain="month"
      />,
    )
    expect(rendered).toContain('Everyday Checking')
  })
})

describe('both renderings over the same query', () => {
    /** The drill-down and the pivot are two views of one grouping pass, so their grand totals are identical. */
  it('agree on the grand total', () => {
    const drill = text(<TransactionReport result={TRANSACTION} heading="Tag" names={NAMES} />)
    const pivot = text(<SummaryReport result={SUMMARY} heading="Tag" grain="month" />)
    expect(drill).toContain('-$150.00')
    expect(pivot).toContain('-$150.00')
  })
})
