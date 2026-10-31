import { describe, expect, it } from 'vitest'

import { parseValueHistory } from '@/lib/valueHistory'

describe('parseValueHistory', () => {
  it('reads a headed file by its column names, in any order', () => {
    const { points, rejected } = parseValueHistory('Value,Date\n310000.00,2024-01-01')
    expect(rejected).toEqual([])
    expect(points).toEqual([{ on: '2024-01-01', value: '310000.00' }])
  })

  it('reads a headless file positionally, date then value', () => {
    const { points } = parseValueHistory('2024-01-01,310000.00\n2025-01-01,325000.00')
    expect(points).toHaveLength(2)
    expect(points[1]).toEqual({ on: '2025-01-01', value: '325000.00' })
  })

  it('strips a currency symbol and separators without going through a float', () => {
    const { points } = parseValueHistory('2024-01-01,"$310,000.55"')
    expect(points[0].value).toBe('310000.55')
  })

  it('reads a parenthesised figure as negative', () => {
    const { points } = parseValueHistory('2024-01-01,(1200.00)')
    expect(points[0].value).toBe('-1200.00')
  })

  it('reports a row it cannot read rather than dropping it silently', () => {
    const { points, rejected } = parseValueHistory('2024-01-01,310000\n03/04/2025,9\n\n2026-01-01,5')
    expect(points).toHaveLength(2)
    expect(rejected).toEqual([2])
  })

  it('refuses an ambiguous date rather than guessing the month', () => {
    expect(parseValueHistory('03/04/2025,9').points).toEqual([])
  })

  it('refuses a day the calendar does not have', () => {
    const { points, rejected } = parseValueHistory('2026-02-31,9')
    expect(points).toEqual([])
    expect(rejected).toEqual([1])
  })
})
