/**
 * `calculations.md` §11: switching the rendering changes one parameter and no
 * others, and only `/reports/monthly-summary` excludes bills and subscriptions.
 */

import { describe, expect, it } from 'vitest'

import {
  monthlySummaryPath,
  reportParams,
  reportRunPath,
  type ReportConfig,
  type ReportQuery,
} from './reports'

const CONFIG: ReportConfig = {
  preset: 'spending',
  mode: 'transaction',
  rows: 'category',
  columns: 'time',
  time_grain: 'month',
  sign: 'expenses',
}

const QUERY: ReportQuery = {
  from: '2026-08-01',
  to: '2026-08-21',
  config: CONFIG,
  filterId: null,
}

describe('one query, two renderings', () => {
  it('changes exactly one parameter between the drill-down and the pivot', () => {
    const drill = reportParams(QUERY)
    const pivot = reportParams({ ...QUERY, config: { ...CONFIG, mode: 'summary' } })

    const differing = Object.keys({ ...drill, ...pivot }).filter(
      (key) => drill[key] !== pivot[key],
    )
    expect(differing).toEqual(['mode'])
    expect(drill.mode).toBe('transaction')
    expect(pivot.mode).toBe('summary')
  })

  it('sends the same window and sign to both, so their totals cannot diverge', () => {
    const drill = reportParams(QUERY)
    const pivot = reportParams({ ...QUERY, config: { ...CONFIG, mode: 'summary' } })

    expect(drill.from).toBe(pivot.from)
    expect(drill.to).toBe(pivot.to)
    expect(drill.sign).toBe(pivot.sign)
    expect(drill.rows).toBe(pivot.rows)
  })

  it('files rows under the date they hit cash flow, not the date the register shows', () => {
    expect(reportParams(QUERY).date_field).toBe('effective')
  })

  it('omits an unbounded start rather than sending an invented one', () => {
    expect(reportParams({ ...QUERY, from: null }).from).toBeUndefined()
    expect(reportRunPath({ ...QUERY, from: null })).not.toContain('from=')
  })

  it('runs the unsaved route so an unapplied filter still narrows the report', () => {
    const path = reportRunPath({ ...QUERY, filterId: 'f1' })
    expect(path.startsWith('/reports/run?')).toBe(true)
    expect(path).toContain('filter_id=f1')
  })
})

describe('the Monthly Summary request', () => {
  /** The engine route would include the rent in Top Categories. */
  it('goes to the endpoint that excludes bills and subscriptions', () => {
    const path = monthlySummaryPath('2026-07')
    expect(path).toBe('/reports/monthly-summary?month=2026-07')
    expect(path).not.toContain('/reports/run')
  })
})
