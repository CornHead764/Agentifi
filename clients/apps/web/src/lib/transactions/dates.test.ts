import { describe, expect, it } from 'vitest'

import { pickRangeDay } from './dates'

describe('pickRangeDay', () => {
  const range = (from: string | null, to: string | null) => ({ from, to })

  it('opens a range on the first pick', () => {
    expect(pickRangeDay(range(null, null), '2026-08-10')).toEqual(range('2026-08-10', null))
  })

  it('closes it on a later pick', () => {
    expect(pickRangeDay(range('2026-08-10', null), '2026-08-20')).toEqual(
      range('2026-08-10', '2026-08-20'),
    )
  })

  it('moves the opening day on an earlier pick', () => {
    expect(pickRangeDay(range('2026-08-10', null), '2026-08-05')).toEqual(
      range('2026-08-05', null),
    )
  })

  it('starts over once a range is complete', () => {
    expect(pickRangeDay(range('2026-08-01', '2026-08-31'), '2026-09-04')).toEqual(
      range('2026-09-04', null),
    )
  })

  it('accepts a one-day range', () => {
    expect(pickRangeDay(range('2026-08-10', null), '2026-08-10')).toEqual(
      range('2026-08-10', '2026-08-10'),
    )
  })
})
