import { describe, expect, it } from 'vitest'

import {
  ALL_TIME_SELECTION,
  RANGE_PRESETS,
  asRangePreset,
  calendarGrid,
  dayWindow,
  labelForSelection,
  selectionForRangePreset,
  shortLabelForSelection,
  selectionForToken,
  tokenForRangePreset,
  windowFor,
  windowOf,
} from './dateRanges'

describe('dayWindow', () => {
  it('reaches back and ahead by calendar days, both ends included', () => {
    expect(dayWindow(30, 0, new Date(2026, 8, 26))).toEqual({ from: '2026-08-27', to: '2026-09-26' })
    expect(dayWindow(0, 60, new Date(2026, 11, 15))).toEqual({ from: '2026-12-15', to: '2027-02-13' })
    expect(dayWindow(60, 30, new Date(2026, 7, 29))).toEqual({ from: '2026-06-30', to: '2026-09-28' })
  })

  it('keeps its ends across a daylight-saving change', () => {
    expect(dayWindow(7, 7, new Date(2026, 10, 3))).toEqual({ from: '2026-10-27', to: '2026-11-10' })
  })
})

describe('calendarGrid', () => {
  it('starts on the Sunday on or before the first and runs six weeks', () => {
    const grid = calendarGrid(new Date(2026, 8, 17))
    expect(grid).toHaveLength(42)
    expect(grid[0]).toEqual(new Date(2026, 7, 30))
    expect(grid[41]).toEqual(new Date(2026, 9, 10))
  })

  it('starts on the first itself when the month opens on a Sunday', () => {
    expect(calendarGrid(new Date(2026, 1, 10))[0]).toEqual(new Date(2026, 1, 1))
  })
})

describe('windowFor', () => {
  it('resolves the calendar-anchored presets', () => {
    const today = new Date(2026, 7, 23) // 2026-08-23
    expect(windowFor('QTD', today)).toEqual({ from: '2026-07-01', to: '2026-08-23' })
    expect(windowFor('YTD', today)).toEqual({ from: '2026-01-01', to: '2026-08-23' })
    expect(windowFor('ALL', today)).toEqual({ from: null, to: '2026-08-23' })
  })

  it('reads a year as the last twelve months, since the year so far is YTD', () => {
    const today = new Date(2026, 7, 23)
    expect(windowFor('1Y', today)).toEqual({ from: '2025-08-23', to: '2026-08-23' })
    expect(windowFor('5Y', today)).toEqual({ from: '2021-08-23', to: '2026-08-23' })
  })

  it('steps whole months back from a mid-month day', () => {
    const today = new Date(2026, 7, 23)
    expect(windowFor('1M', today)).toEqual({ from: '2026-07-23', to: '2026-08-23' })
    expect(windowFor('6M', today)).toEqual({ from: '2026-02-23', to: '2026-08-23' })
  })

  it('clamps a month-end day instead of letting "Sep 31" overflow into October', () => {
    expect(windowFor('1M', new Date(2026, 9, 31))).toEqual({ from: '2026-09-30', to: '2026-10-31' })
  })

  it('clamps into February instead of landing on March 3rd', () => {
    expect(windowFor('3M', new Date(2026, 4, 31))).toEqual({ from: '2026-02-28', to: '2026-05-31' })
  })

  it('clamps a leap day a year back instead of landing on March 1st', () => {
    expect(windowFor('1Y', new Date(2028, 1, 29))).toEqual({ from: '2027-02-28', to: '2028-02-29' })
  })
})

/**
 * One space default must open the register, the reports and the charts on the
 * same window, so each preset is one token everywhere.
 */
describe('a space default range', () => {
  const today = new Date(2026, 8, 3)

  it('opens on nothing in particular when the space has no preference', () => {
    expect(selectionForRangePreset(null, today)).toBeNull()
  })

  it('pairs each preset with the token that means it', () => {
    expect(RANGE_PRESETS.map(tokenForRangePreset)).toEqual([
      '-1m',
      '-3m',
      '-6m',
      '-12m',
      '-5y',
      'this-quarter',
      'this-year',
      'all-time',
    ])
  })

  it('gives the charts the window the register and the reports are given', () => {
    for (const preset of RANGE_PRESETS) {
      const selection = selectionForRangePreset(preset, today)!
      expect(windowOf(selection, today)).toEqual(windowFor(preset, today))
    }
  })

  it('resolves the token to the window the endpoints are sent', () => {
    expect(selectionForRangePreset('YTD', today)?.range).toEqual({
      from: '2026-01-01',
      to: '2026-09-03',
    })
    expect(selectionForRangePreset('1Y', today)?.range).toEqual({
      from: '2025-09-03',
      to: '2026-09-03',
    })
  })

  it('opens all time with no token at all', () => {
    expect(selectionForRangePreset('ALL', today)).toBe(ALL_TIME_SELECTION)
  })

  it('accepts only a preset the client knows', () => {
    expect(asRangePreset('YTD')).toBe('YTD')
    expect(asRangePreset('')).toBeNull()
    expect(asRangePreset(null)).toBeNull()
    expect(asRangePreset(undefined)).toBeNull()
    expect(asRangePreset('LAST_QUARTER')).toBeNull()
  })
})

describe('windowOf', () => {
  it('resolves a token on the day it is asked, not the day it was stored', () => {
    const stored = selectionForToken('this-month', new Date(2026, 6, 10))!
    expect(windowOf(stored, new Date(2026, 7, 21))).toEqual({ from: '2026-08-01', to: '2026-08-21' })
  })

  it('passes a custom window through, and ends an open one today', () => {
    const today = new Date(2026, 7, 21)
    expect(
      windowOf({ range: { from: '2026-02-01', to: '2026-02-14' }, preset: null }, today),
    ).toEqual({ from: '2026-02-01', to: '2026-02-14' })
    expect(windowOf({ range: { from: '2026-02-01', to: null }, preset: null }, today)).toEqual({
      from: '2026-02-01',
      to: '2026-08-21',
    })
    expect(windowOf(ALL_TIME_SELECTION, today)).toEqual({ from: null, to: '2026-08-21' })
  })

  it('runs the recent-months windows from the same day N months ago', () => {
    expect(windowOf(selectionForToken('-3m')!, new Date(2026, 7, 21))).toEqual({
      from: '2026-05-21',
      to: '2026-08-21',
    })
  })
})

describe('labelForSelection', () => {
  const today = new Date(2026, 8, 3)

  it('names the windows the picker does not list rather than showing the token', () => {
    expect(labelForSelection(selectionForRangePreset('1M', today)!)).toBe('Recent 1 month')
    expect(labelForSelection(selectionForRangePreset('5Y', today)!)).toBe('Recent 5 years')
    expect(labelForSelection(selectionForRangePreset('1Y', today)!)).toBe('Recent 12 months')
  })

  it("keeps the picker's wording for the tokens the picker offers", () => {
    expect(labelForSelection(selectionForToken('-3m', today)!)).toBe('Recent 3 months')
    expect(labelForSelection(selectionForToken('this-year', today)!)).toBe('Year to date')
    expect(labelForSelection(ALL_TIME_SELECTION)).toBe('All time')
  })

  it('writes a custom window as dates a reader recognises', () => {
    expect(
      labelForSelection({ range: { from: '2026-02-01', to: '2026-02-14' }, preset: null }),
    ).toBe('Feb 1, 2026 – Feb 14, 2026')
    expect(labelForSelection({ range: { from: '2026-02-01', to: null }, preset: null })).toBe(
      'From Feb 1, 2026',
    )
  })
})

describe('shortLabelForSelection', () => {
  const today = new Date(2026, 8, 3)

  it('keeps a preset and all time as they read in full', () => {
    expect(shortLabelForSelection(selectionForToken('this-year', today)!)).toBe('Year to date')
    expect(shortLabelForSelection(ALL_TIME_SELECTION)).toBe('All time')
  })

  it('names a custom window without its dates', () => {
    expect(
      shortLabelForSelection({ range: { from: '2026-02-01', to: '2026-02-14' }, preset: null }),
    ).toBe('Custom dates')
    expect(shortLabelForSelection({ range: { from: null, to: '2026-02-14' }, preset: null })).toBe(
      'Custom dates',
    )
  })
})
