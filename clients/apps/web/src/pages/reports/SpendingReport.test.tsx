/**
 * The Spending report as a restored tab, over an invented answer seeded in
 * the cache. A static render cannot click, so each view is opened by the
 * stored choice it is restored from.
 */

import { afterEach, describe, expect, it } from 'vitest'

import {
  reportKeys,
  spendingReportPath,
  type SpendingDifference,
  type SpendingReportData,
} from '@/lib/clients/reports'
import { moneyFromCents as cents } from '@/lib/money'
import { galleryEntry } from '@/lib/reports/presets'
import { openTab, workspaceKey, writeWorkspace } from '@/lib/reports/tabs'
import { writeStored, writeStoredFlag } from '@/lib/storage'
import { renderScreen } from '@/test/renderScreen'

import { ReportsPage } from '../ReportsPage'

function installStorage() {
  const backing = new Map<string, string>()
  Reflect.set(globalThis, 'window', {
    localStorage: {
      getItem: (key: string) => backing.get(key) ?? null,
      setItem: (key: string, value: string) => void backing.set(key, value),
      removeItem: (key: string) => void backing.delete(key),
    },
  })
}

afterEach(() => Reflect.deleteProperty(globalThis, 'window'))

const change = (amount: number, pct: string | null, state: SpendingDifference['state']): SpendingDifference => ({
  amount: cents(amount),
  pct,
  state,
})

const period = (key: string, from: string, through: string, end: string, partial = false) => ({
  key,
  from,
  through,
  end,
  partial,
})

const DATA: SpendingReportData = {
  grain: 'month',
  today: '2026-10-03',
  period: period('2026-10', '2026-10-01', '2026-10-03', '2026-10-31', true),
  window: { from: '2026-10-01', to: '2026-10-03', date_field: 'effective' },
  periods: [
    { ...period('2026-09', '2026-09-01', '2026-09-30', '2026-09-30'), income: cents(500000), spent: cents(-300000), remaining: cents(200000) },
    { ...period('2026-10', '2026-10-01', '2026-10-03', '2026-10-31', true), income: cents(0), spent: cents(-40000), remaining: cents(-40000) },
  ],
  compare: 'prior',
  compare_options: ['same_last_year', 'prior', 'ytd_average', 'average_3', 'average_6', 'average_12', 'none'],
  comparison: {
    compare: 'prior',
    periods: [period('2026-09', '2026-09-01', '2026-09-03', '2026-09-30', true)],
    average: false,
    spent: cents(-20000),
    difference: change(20000, '100', 'change'),
  },
  summary: {
    income: cents(0),
    spent: cents(-40000),
    remaining: cents(-40000),
    savings_rate: null,
    spending_rate: null,
    rating: 'great',
    // A 5,000.00 paycheck and 1,200.00 of bills still to come by Oct 31:
    // 5,000 in, 1,600 out, 3,400 left, 68% saved.
    projection: {
      end: '2026-10-31',
      expected_income: cents(500000),
      expected_spent: cents(-120000),
      count: 2,
      income: cents(500000),
      spent: cents(-160000),
      remaining: cents(340000),
      savings_rate: '0.68',
      spending_rate: '0.32',
    },
  },
  group_by: 'category',
  rows: [
    { key: 'c-home', label: 'Household', amount: cents(-30000), comparison: cents(-15000), difference: change(15000, '100', 'change'), share: '0.75' },
    { key: 'c-food', label: 'Dining Out', amount: cents(-10000), comparison: cents(0), difference: change(10000, null, 'new_spend'), share: '0.25' },
    { key: 'c-travel', label: 'Getaways', amount: cents(0), comparison: cents(-5000), difference: change(-5000, '-100', 'no_spend'), share: null },
    { key: 'c-gifts', label: 'Presents', amount: cents(2500), comparison: cents(0), difference: change(-2500, null, 'new_spend'), share: null },
  ],
  uncategorized_count: 2,
  table: {
    periods: [
      period('2026-09', '2026-09-01', '2026-09-30', '2026-09-30'),
      period('2026-10', '2026-10-01', '2026-10-03', '2026-10-31', true),
    ],
    prior: period('2026-09', '2026-09-01', '2026-09-03', '2026-09-30', true),
    rows: [
      { key: 'c-home', label: 'Household', cells: [cents(-250000), cents(-30000)], total: cents(-280000), difference: change(15000, '100', 'change') },
    ],
  },
  flow: {
    income: [],
    credits: [{ key: 'c-gifts', label: 'Presents', amount: cents(2500), share: null }],
    spending: [{ key: 'c-home', label: 'Household', amount: cents(30000), share: null }],
    income_total: cents(0),
    spent: cents(27500),
    spent_share: null,
  },
}

const PATH = spendingReportPath({
  grain: 'month',
  period: null,
  compare: 'prior',
  groupBy: 'category',
  under: null,
  filterId: null,
})

function render(view?: string): string {
  installStorage()
  if (view) writeStored('reports.spending.view', view)
  const tab = openTab(galleryEntry('spending'))
  writeWorkspace(workspaceKey(undefined), { tabs: [tab], active: tab.id })
  return renderScreen(<ReportsPage />, { seed: [[reportKeys.spending(PATH), DATA]] })
}

describe('the Spending report', () => {
  it('heads the page with the period, its cards and the comparison', () => {
    const rendered = render()
    expect(rendered).toContain('October 2026')
    expect(rendered).toContain('Oct 1 – 3, 2026*')
    expect(rendered).toContain('Overspent')
    expect(rendered).toContain('Compare: Prior month')
    expect(rendered).toContain('Spend vs. Sep 1 – 3, 2026')
    expect(rendered).toContain('2 transactions need to be categorized.')
    expect(rendered).toContain('tab=spending&amp;uncategorized=1&amp;from=2026-10-01&amp;to=2026-10-03')
  })

  it('sets what the month in progress is expected to close at under each card', () => {
    const rendered = render()
    expect(rendered).toContain('Expected by Oct 31')
    expect(rendered).toContain('+$5,000.00')
    expect(rendered).toContain('-$1,600.00')
    expect(rendered).toContain('$3,400.00')
    expect(rendered).toContain('68.0%')
    // The rating is the projection's, beside it.
    expect(rendered).toContain('Great')
  })

  it('reads each line’s difference in words where a number would mislead', () => {
    const rendered = render('bars')
    expect(rendered).toContain('New spend')
    expect(rendered).toContain('No spend yet')
    expect(rendered).toContain('100.0%')
    // A net credit reads as money back, not as spend.
    expect(rendered).toContain('+$25.00')
  })

  it('drops the cents when asked', () => {
    installStorage()
    writeStoredFlag('reports.spending.hide-cents', true)
    const tab = openTab(galleryEntry('spending'))
    writeWorkspace(workspaceKey(undefined), { tabs: [tab], active: tab.id })
    const rendered = renderScreen(<ReportsPage />, { seed: [[reportKeys.spending(PATH), DATA]] })
    expect(rendered).toContain('$300')
    expect(rendered).not.toContain('$300.00')
  })

  it('shares the donut among the lines that were spent on', () => {
    const rendered = render('donut')
    expect(rendered).toContain('(75.0%)')
    expect(rendered).toContain('Largest spent')
  })

  it('lays the table out with its shading and its last-against-prior column', () => {
    const rendered = render('table')
    expect(rendered).toContain('Oct* vs Sep')
    expect(rendered).toContain('spend-heat--4')
    expect(rendered).toContain('More spend')
  })

  it('flows credits into what was spent', () => {
    const rendered = render('flow')
    expect(rendered).toContain('Presents (credit)')
    expect(rendered).toContain('Total spent')
  })
})
