import { describe, expect, it } from 'vitest'

import {
  HORIZON_DAYS,
  customHorizon,
  horizonLabel,
  horizonOf,
  isCompleteRange,
} from './horizon'

describe('the horizon Bills & Income looks over', () => {
  it('starts today and runs forward', () => {
    const horizon = horizonOf(30, new Date(2026, 7, 29))
    expect(horizon.from).toBe('2026-08-29')
    expect(horizon.to).toBe('2026-09-28')
    expect(horizon.days).toBe(30)
  })

  it('crosses a year end without arithmetic of its own', () => {
    expect(horizonOf(60, new Date(2026, 11, 15)).to).toBe('2027-02-13')
  })

  it('offers the four Simplifi offers', () => {
    expect([...HORIZON_DAYS]).toEqual([30, 60, 90, 180])
  })
})

describe('a range the user picked', () => {
  it('carries no day count, which is what marks it custom', () => {
    expect(customHorizon('2026-09-01', '2026-09-30').days).toBeNull()
    expect(horizonLabel(customHorizon('2026-09-01', '2026-09-30'))).toBe('Selected dates')
    expect(horizonLabel(horizonOf(90, new Date(2026, 7, 29)))).toBe('Next 90 days')
  })

  it('swaps a pair given back to front', () => {
    expect(customHorizon('2026-09-30', '2026-09-01')).toEqual({
      from: '2026-09-01',
      to: '2026-09-30',
      days: null,
    })
  })

  it('may look backwards, which a horizon may not', () => {
    expect(customHorizon('2026-01-01', '2026-02-01').from).toBe('2026-01-01')
  })

  it('is not applied until both halves are there', () => {
    expect(isCompleteRange('2026-09-01', '')).toBe(false)
    expect(isCompleteRange('  ', '2026-09-30')).toBe(false)
    expect(isCompleteRange('2026-09-01', '2026-09-30')).toBe(true)
  })
})
