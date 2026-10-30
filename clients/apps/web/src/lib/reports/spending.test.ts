import { describe, expect, it } from 'vitest'

import type {
  SpendingComparison,
  SpendingFlow,
  SpendingRow,
  SpendingTableRow,
} from '@/lib/clients/reports'
import { moneyFromCents as cents } from '@/lib/money'

import {
  barPercent,
  barScale,
  chartBars,
  compareLabel,
  comparisonCaption,
  comparisonSentence,
  dayRange,
  heatLevel,
  heatScale,
  layoutFlow,
  periodTick,
  periodTitle,
  searchRows,
  sortByDifference,
  sortFlow,
  sortRows,
  sortTable,
  sparkPoints,
} from './spending'

function row(label: string, amount: number, comparison = 0, difference = 0): SpendingRow {
  return {
    key: label.toLowerCase(),
    label,
    amount: cents(amount),
    comparison: cents(comparison),
    difference: { amount: cents(difference), pct: null, state: 'change' },
    share: null,
  }
}

describe('compareLabel', () => {
  it('names each option in its grain’s words', () => {
    expect(compareLabel('prior', 'month')).toBe('Prior month')
    expect(compareLabel('prior', 'quarter')).toBe('Prior quarter')
    expect(compareLabel('prior', 'year')).toBe('Prior year')
    expect(compareLabel('same_last_year', 'month')).toBe('Same period last year')
    expect(compareLabel('same_last_year', 'quarter')).toBe('Same quarter last year')
    expect(compareLabel('ytd_average', 'month')).toBe('Year to date average')
    expect(compareLabel('average_12', 'month')).toBe('12-month average')
    expect(compareLabel('average_2', 'quarter')).toBe('2-quarter average')
    expect(compareLabel('average_3', 'year')).toBe('3-year average')
    expect(compareLabel('none', 'year')).toBe('Don’t compare')
  })
})

describe('period names', () => {
  it('titles the selected period', () => {
    expect(periodTitle({ from: '2026-10-01' }, 'month')).toBe('October 2026')
    expect(periodTitle({ from: '2026-10-01' }, 'quarter')).toBe('Q4 2026')
    expect(periodTitle({ from: '2026-01-01' }, 'year')).toBe('2026')
  })

  it('stars a bar still running and gives January its year', () => {
    expect(periodTick({ from: '2026-10-01', partial: true }, 'month')).toBe('Oct*')
    expect(periodTick({ from: '2026-01-01', partial: false }, 'month')).toBe('Jan 2026')
    expect(periodTick({ from: '2026-04-01', partial: false }, 'quarter')).toBe('Q2 2026')
    expect(periodTick({ from: '2025-01-01', partial: false }, 'year')).toBe('2025')
  })

  it('writes a range as briefly as it reads', () => {
    expect(dayRange('2026-09-01', '2026-09-03')).toBe('Sep 1 – 3, 2026')
    expect(dayRange('2026-01-01', '2026-10-03')).toBe('Jan 1 – Oct 3, 2026')
    expect(dayRange('2025-12-01', '2026-01-03')).toBe('Dec 1, 2025 – Jan 3, 2026')
    expect(dayRange('2026-09-01', '2026-09-01')).toBe('Sep 1, 2026')
  })

  it('captions a comparison by its dates, or an average by its name', () => {
    const prior: SpendingComparison = {
      compare: 'prior',
      periods: [{ key: '2026-09', from: '2026-09-01', through: '2026-09-03', end: '2026-09-30', partial: true }],
      average: false,
      spent: cents(-1000),
      difference: { amount: cents(0), pct: '0', state: 'change' },
    }
    expect(comparisonCaption(prior, 'month')).toBe('Sep 1 – 3, 2026')
    expect(comparisonSentence({ from: '2026-10-01', through: '2026-10-03' }, prior)).toBe(
      'Comparing Oct 1 – 3, 2026 to Sep 1 – 3, 2026',
    )
    expect(comparisonCaption({ ...prior, compare: 'average_3', average: true }, 'month')).toBe(
      '3-month average',
    )
  })
})

describe('ordering lines', () => {
  const rows = [row('Travel', 0, -4000), row('Home', -38000), row('Gifts', 2500), row('Food', -3300)]

  it('puts the most spent first, credits last', () => {
    expect(sortRows(rows, 'largest').map((one) => one.label)).toEqual(['Home', 'Food', 'Travel', 'Gifts'])
    expect(sortRows(rows, 'smallest').map((one) => one.label)).toEqual(['Gifts', 'Travel', 'Food', 'Home'])
    expect(sortRows(rows, 'az').map((one) => one.label)).toEqual(['Food', 'Gifts', 'Home', 'Travel'])
    expect(sortRows(rows, 'za').map((one) => one.label)).toEqual(['Travel', 'Home', 'Gifts', 'Food'])
  })

  it('sorts by the change in spend', () => {
    const changed = [row('A', 0, 0, 500), row('B', 0, 0, -900), row('C', 0, 0, 1200)]
    expect(sortByDifference(changed, 'desc').map((one) => one.label)).toEqual(['C', 'A', 'B'])
    expect(sortByDifference(changed, 'asc').map((one) => one.label)).toEqual(['B', 'A', 'C'])
  })

  it('searches the names, ignoring case and accents', () => {
    expect(searchRows([row('Café Lune', -500), row('Home', -100)], 'cafe').map((one) => one.label)).toEqual([
      'Café Lune',
    ])
  })
})

describe('bars', () => {
  it('scales every bar to the largest net on either side', () => {
    const rows = [row('Home', -20000, -40000), row('Gifts', 1500, -1000)]
    expect(barScale(rows)).toBe(40000)
    expect(barScale([row('Home', -20000), row('Refund', 50000)])).toBe(50000)
    expect(barPercent(cents(-20000), cents(40000))).toBe(50)
    expect(barPercent(cents(10000), cents(40000))).toBe(25)
    expect(barPercent(cents(0), cents(40000))).toBe(0)
    expect(barPercent(cents(-100), cents(0))).toBe(0)
  })

  it('plots the chart as net spend, a net credit below the axis', () => {
    const bars = chartBars(
      [
        { key: '2026-09', from: '2026-09-01', through: '2026-09-30', end: '2026-09-30', partial: false, income: cents(0), spent: cents(-12345), remaining: cents(-12345) },
        { key: '2026-10', from: '2026-10-01', through: '2026-10-03', end: '2026-10-31', partial: true, income: cents(0), spent: cents(800), remaining: cents(800) },
      ],
      'month',
    )
    expect(bars.map((bar) => [bar.label, bar.plot, bar.credit])).toEqual([
      ['Sep', 123.45, false],
      ['Oct*', -8, true],
    ])
  })
})

describe('the table', () => {
  const table: SpendingTableRow[] = [
    {
      key: 'home',
      label: 'Home',
      cells: [cents(-10000), cents(-40000)],
      total: cents(-50000),
      difference: { amount: cents(30000), pct: '300', state: 'change' },
    },
    {
      key: 'gifts',
      label: 'Gifts',
      cells: [cents(-20000), cents(500)],
      total: cents(-19500),
      difference: { amount: cents(-20500), pct: '-102.5', state: 'change' },
    },
  ]

  it('shades by quarters of the largest cell', () => {
    expect(heatScale(table)).toBe(40000)
    expect(heatLevel(cents(-40000), cents(40000))).toBe(4)
    expect(heatLevel(cents(-20000), cents(40000))).toBe(2)
    expect(heatLevel(cents(-10000), cents(40000))).toBe(1)
    expect(heatLevel(cents(-1), cents(40000))).toBe(1)
    expect(heatLevel(cents(500), cents(40000))).toBe(0)
    expect(heatLevel(cents(0), cents(40000))).toBe(0)
  })

  it('sorts by any column', () => {
    expect(sortTable(table, 0, 'desc').map((one) => one.label)).toEqual(['Gifts', 'Home'])
    expect(sortTable(table, 1, 'desc').map((one) => one.label)).toEqual(['Home', 'Gifts'])
    expect(sortTable(table, 'total', 'asc').map((one) => one.label)).toEqual(['Gifts', 'Home'])
    expect(sortTable(table, 'difference', 'asc').map((one) => one.label)).toEqual(['Gifts', 'Home'])
    expect(sortTable(table, 'name', 'asc').map((one) => one.label)).toEqual(['Gifts', 'Home'])
  })

  it('draws a sparkline with spend upward', () => {
    expect(sparkPoints([cents(-1000), cents(-3000), cents(-2000)], 40, 10)).toBe('0,10 20,0 40,5')
    expect(sparkPoints([cents(-1000), cents(-1000)], 40, 10)).toBe('0,5 40,5')
  })
})

describe('layoutFlow', () => {
  const flow: SpendingFlow = {
    income: [
      { key: 'salary', label: 'Salary', amount: cents(150000), share: '0.75' },
      { key: 'bonus', label: 'Bonus', amount: cents(50000), share: '0.25' },
    ],
    credits: [{ key: 'shopping', label: 'Shopping', amount: cents(10000), share: '0.05' }],
    spending: [
      { key: 'home', label: 'Home', amount: cents(100000), share: '0.5' },
      { key: 'food', label: 'Food', amount: cents(80000), share: '0.4' },
    ],
    income_total: cents(200000),
    spent: cents(170000),
    spent_share: '0.85',
  }

  it('gives every band one scale and lands each where it belongs', () => {
    const layout = layoutFlow(flow, cents(30000), 800)
    const box = (key: string) => layout.boxes.find((one) => one.key === key)!
    // The tallest column is the $2,100 of income and credits on the left.
    expect(box('salary').height / box('bonus').height).toBeCloseTo(3)
    expect(box('total-income').height).toBeCloseTo(box('salary').height + box('bonus').height)
    expect(box('home').height / box('food').height).toBeCloseTo(1.25)
    // Income pays for $1,700 of the spending; the other $300 is left.
    expect(box('left').amount).toBe(30000)
    expect(layout.links.map((one) => one.key)).toEqual([
      'salary>total-income',
      'bonus>total-income',
      'total-income>total-spent',
      'shopping>total-spent',
      'total-income>left',
      'total-spent>home',
      'total-spent>food',
    ])
    expect(box('home').x).toBe(792)
  })

  it('orders the bands by size or name', () => {
    expect(sortFlow(flow, 'smallest').spending.map((one) => one.label)).toEqual(['Food', 'Home'])
    expect(sortFlow(flow, 'largest').income.map((one) => one.label)).toEqual(['Salary', 'Bonus'])
    expect(sortFlow(flow, 'az').income.map((one) => one.label)).toEqual(['Bonus', 'Salary'])
  })

  it('leaves nothing unspent when spending ran over', () => {
    const layout = layoutFlow({ ...flow, income_total: cents(100000) }, cents(-70000), 800)
    expect(layout.boxes.some((one) => one.key === 'left')).toBe(false)
  })
})
