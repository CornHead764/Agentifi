import { describe, expect, it } from 'vitest'

import {
  describeMonths,
  describeRecurrence,
  optionFor,
  previewOccurrences,
  reanchorRecurrence,
  recurrenceFor,
  recurrenceFromWire,
  shortLabel,
} from './recurrence'

/** A one-time rule as the Go response spells it: the frequency zero value. */
const WIRE_ONE_TIME = {
  alias: 'ONE_TIME',
  frequency: '',
  interval: 1,
  by_month_day: [],
  by_day: [],
  by_month: [],
} as const

describe('recurrenceFromWire', () => {
  it('reads the empty frequency as the one-time rule it is', () => {
    expect(recurrenceFromWire(WIRE_ONE_TIME).frequency).toBeNull()
  })

  it('leaves a repeating rule alone', () => {
    expect(
      recurrenceFromWire({ ...WIRE_ONE_TIME, alias: 'EVERY_MONTH', frequency: 'MONTHLY' })
        .frequency,
    ).toBe('MONTHLY')
  })

  it('stops the preview inventing dates for something that happens once', () => {
    const on = new Date(2026, 7, 21)

    expect(previewOccurrences(recurrenceFromWire(WIRE_ONE_TIME), on)).toEqual([on])
    expect(optionFor(recurrenceFromWire(WIRE_ONE_TIME))).toBe('ONE_TIME')
  })
})

describe('optionFor', () => {
  it('reads a one-time alias on a repeating rule from the rule, not the alias', () => {
    // Simplifi imports stored monthly rules under the default alias.
    const spectrum = recurrenceFromWire({
      alias: 'ONE_TIME',
      frequency: 'MONTHLY',
      interval: 1,
      by_month_day: [14],
      by_day: [],
      by_month: [],
    })

    expect(optionFor(spectrum)).toBe('EVERY_MONTH')
    expect(optionFor({ ...spectrum, by_month_day: [1, 15] })).toBe('TWICE_A_MONTH')
    expect(optionFor({ ...spectrum, interval: 3 })).toBe('EVERY_QUARTER')
    expect(optionFor({ ...spectrum, interval: 12 })).toBe('EVERY_X_MONTHS')
    expect(optionFor({ ...spectrum, frequency: 'WEEKLY', interval: 2 })).toBe('EVERY_X_WEEKS')
  })

  it('still trusts an alias that agrees with its rule', () => {
    expect(optionFor(recurrenceFromWire({ ...WIRE_ONE_TIME, alias: 'EVERY_QUARTER', frequency: 'MONTHLY', interval: 3 }))).toBe('EVERY_QUARTER')
  })
})

describe('reanchorRecurrence', () => {
  const jan1 = new Date('2026-01-01T00:00:00')
  const jan15 = new Date('2026-01-15T00:00:00')

  it('moves a month day that came from the start date', () => {
    const monthly = recurrenceFor('EVERY_MONTH', jan1)
    expect(reanchorRecurrence(monthly, jan1, jan15).by_month_day).toEqual([15])
  })

  it('moves a weekday that came from the start date', () => {
    const weekly = recurrenceFor('EVERY_WEEK', jan1)
    expect(weekly.by_day).toEqual(['TH'])
    expect(reanchorRecurrence(weekly, jan1, jan15).by_day).toEqual(['TH'])
    const jan16 = new Date('2026-01-16T00:00:00')
    expect(reanchorRecurrence(weekly, jan1, jan16).by_day).toEqual(['FR'])
  })

  it('leaves a day the person picked by hand alone', () => {
    const twice = recurrenceFor('TWICE_A_MONTH', jan1, { byMonthDay: [1, 15] })
    expect(reanchorRecurrence(twice, jan1, jan15)).toBe(twice)
    const chosen = recurrenceFor('EVERY_MONTH', jan1, { byMonthDay: [20] })
    expect(reanchorRecurrence(chosen, jan1, jan15)).toBe(chosen)
  })
})

describe('active months', () => {
  const april12 = new Date('2026-04-12T00:00:00')
  const lawn = recurrenceFor('EVERY_MONTH', april12, { byMonth: [10, 4, 5, 6, 7, 8, 9] })

  it('keeps a seasonal rule sorted and reads every month as none', () => {
    expect(lawn.by_month).toEqual([4, 5, 6, 7, 8, 9, 10])
    const all = recurrenceFor('EVERY_MONTH', april12, {
      byMonth: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12],
    })
    expect(all.by_month).toEqual([])
  })

  it('drops the months on a yearly or one-time rule', () => {
    expect(recurrenceFor('EVERY_YEAR', april12, { byMonth: [4] }).by_month).toEqual([])
    expect(recurrenceFor('ONE_TIME', april12, { byMonth: [4] }).by_month).toEqual([])
    expect(recurrenceFor('EVERY_X_WEEKS', april12, { byMonth: [4] }).by_month).toEqual([4])
  })

  it('names a run of months and a run across the new year', () => {
    expect(describeMonths([4, 5, 6, 7, 8, 9, 10])).toBe('April to October')
    expect(describeMonths([11, 12, 1, 2])).toBe('November to February')
    expect(describeMonths([1, 6, 7])).toBe('January, June and July')
    expect(describeMonths([4, 5, 6, 7, 8, 9, 10], 'short')).toBe('Apr to Oct')
  })

  it('says the season in the description and the short label', () => {
    expect(describeRecurrence(lawn)).toBe(
      'Repeats every month on the 12th. Only in April to October.',
    )
    expect(shortLabel(lawn)).toBe('Every month, Apr to Oct')
  })

  it('previews across the off season rather than into it', () => {
    const october12 = new Date('2026-10-12T00:00:00')
    expect(previewOccurrences(lawn, october12).map((on) => on.toDateString())).toEqual([
      new Date('2026-10-12T00:00:00').toDateString(),
      new Date('2027-04-12T00:00:00').toDateString(),
      new Date('2027-05-12T00:00:00').toDateString(),
    ])
  })
})
