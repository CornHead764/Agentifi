import { describe, expect, it } from 'vitest'

import {
  asSentence,
  axisLabels,
  blankToNull,
  capitalize,
  changeFraction,
  EM_DASH,
  formatDate,
  formatPercent,
  formatPercentUnits,
  fractionToPercentUnits,
  initial,
  parseIsoDate,
  parseIsoDay,
  parseRate,
  plural,
  pluralWord,
  rateAsPercentText,
  shareOf,
  formatTimestamp,
  timeAgo,
  daysInMonth,
  formatMonthKey,
  monthBounds,
} from './format'
import { moneyFromCents } from './money'

const ZERO = moneyFromCents(0)
const START = moneyFromCents(41_000_000)
const END = moneyFromCents(50_000_000)

describe('percentage changes', () => {
  it('renders an em dash when the window opened at zero', () => {
    // `calculations.md`: a delta against nothing is undefined, shown as a dash.
    expect(changeFraction(ZERO, END)).toBeNull()
    expect(formatPercent(changeFraction(ZERO, END))).toBe(EM_DASH)
    expect(formatPercent(changeFraction(ZERO, END))).not.toContain('∞')
    expect(formatPercent(changeFraction(ZERO, END))).not.toContain('0%')
  })

  it('renders the dash even when nothing moved either', () => {
    expect(formatPercent(changeFraction(ZERO, ZERO))).toBe(EM_DASH)
  })

  it('divides by the magnitude, so climbing out of debt reads positive', () => {
    const debt = moneyFromCents(-100_000)
    const clear = moneyFromCents(-50_000)
    expect(changeFraction(debt, clear)).toBeCloseTo(0.5, 6)
  })

  it('prints an ordinary change with two places and an optional plus', () => {
    expect(formatPercent(changeFraction(START, END), { showPlus: true })).toBe('+21.95%')
    expect(formatPercent(changeFraction(END, START))).toBe('-18.00%')
  })

  it('refuses a non-finite rate rather than printing Infinity', () => {
    expect(formatPercent(Number.POSITIVE_INFINITY)).toBe(EM_DASH)
    expect(formatPercent(Number.NaN)).toBe(EM_DASH)
  })

  it('shares of a zero total are undefined too', () => {
    expect(shareOf(END, ZERO)).toBeNull()
    expect(formatPercent(shareOf(END, ZERO))).toBe(EM_DASH)
  })
})

describe('parseRate', () => {
  it('accepts the string form a Decimal serializes to, and a plain number', () => {
    expect(parseRate('0.217300')).toBeCloseTo(0.2173, 6)
    expect(parseRate(0.2173)).toBeCloseTo(0.2173, 6)
  })

  it('treats an absent or unparseable rate as absent, never as zero', () => {
    expect(parseRate(null)).toBeNull()
    expect(parseRate(undefined)).toBeNull()
    expect(parseRate('not a rate')).toBeNull()
  })
})

describe('fractionToPercentUnits', () => {
  it('converts a computed fraction to the units PercentText and ChangeBadge take', () => {
    // The raw fraction must not reach a percent-unit renderer (21.73% as "0.22%").
    expect(fractionToPercentUnits(0.2173)).toBeCloseTo(21.73)
    expect(formatPercentUnits(fractionToPercentUnits(0.2173), { digits: 2 })).toBe('21.73%')
    expect(formatPercentUnits(fractionToPercentUnits(0.35), { digits: 1 })).toBe('35.0%')
  })

  it('keeps the no-baseline dash', () => {
    expect(fractionToPercentUnits(null)).toBeNull()
    expect(formatPercentUnits(fractionToPercentUnits(null))).toBe('—')
  })
})

describe('formatDate', () => {
  it('reads only the calendar day off a full timestamp, like valued_at', () => {
    // `valued_at` is a `*time.Time` on the backend and crosses the wire as a
    // full RFC3339 instant, not a date-only string.
    expect(formatDate('2026-08-25T03:12:00Z')).toBe(formatDate('2026-08-25'))
  })

  it('returns the raw input instead of throwing on a value that will not parse', () => {
    expect(formatDate('not a date')).toBe('not a date')
  })
})

describe('parseIsoDate', () => {
  it('parses a timestamp the same as its leading date', () => {
    expect(parseIsoDate('2026-08-25T03:12:00Z')).toEqual(parseIsoDate('2026-08-25'))
  })
})

describe('axisLabels', () => {
  it('keeps day-month labels when they are unique across the series', () => {
    expect(axisLabels(['2026-01-31', '2026-02-28', '2026-03-31'])).toEqual([
      'Jan 31',
      'Feb 28',
      'Mar 31',
    ])
  })

  it('widens every label to include the year the moment any collide', () => {
    // Monthly samples over a multi-year window repeat "Jan 31" per year; a
    // category axis resolves hover through these values, so one collision
    // disqualifies the whole axis from year-less labels.
    const labels = axisLabels([
      '2024-01-31',
      '2025-01-31',
      '2025-12-31',
      '2026-01-31',
    ])
    expect(labels).toEqual(['Jan 2024', 'Jan 2025', 'Dec 2025', 'Jan 2026'])
  })

  it('falls all the way to the full date when the month itself collides', () => {
    // A 5Y or All window samples the range's first day and that month's last
    // day, so "Aug 2021" collides; only full dates are unique here.
    const labels = axisLabels([
      '2021-08-24',
      '2021-08-31',
      '2021-09-30',
      '2022-01-31',
      '2023-01-31',
      '2026-07-31',
      '2026-08-23',
    ])
    expect(labels).toEqual([
      'Aug 24, 2021',
      'Aug 31, 2021',
      'Sep 30, 2021',
      'Jan 31, 2022',
      'Jan 31, 2023',
      'Jul 31, 2026',
      'Aug 23, 2026',
    ])
  })

  it('returns labels as unique as the dates they came from', () => {
    // Every sample shape the backend produces: daily, weekly, and monthly with ragged ends.
    for (const isos of [
      ['2026-07-25', '2026-07-26', '2026-08-23'],
      ['2025-09-01', '2025-09-08', '2026-08-24'],
      ['2021-08-24', '2021-08-31', '2026-08-23'],
    ]) {
      const labels = axisLabels(isos)
      expect(new Set(labels).size).toBe(labels.length)
    }
  })
})

describe('how long ago a story was published', () => {
  const now = new Date('2026-08-29T12:00:00Z')

  it('reads in the coarsest unit that is still true', () => {
    expect(timeAgo('2026-08-29T11:59:40Z', 'short', now)).toBe('just now')
    expect(timeAgo('2026-08-29T11:30:00Z', 'short', now)).toBe('30m ago')
    expect(timeAgo('2026-08-29T07:00:00Z', 'short', now)).toBe('5h ago')
    expect(timeAgo('2026-08-26T12:00:00Z', 'short', now)).toBe('3d ago')
  })

  it('falls back to a date once the story is old', () => {
    expect(timeAgo('2026-07-01T12:00:00Z', 'short', now)).toBe(
      formatTimestamp('2026-07-01T12:00:00Z', 'date'),
    )
  })

  // A story dated slightly ahead of us is clock skew.
  it('does not report a negative age', () => {
    expect(timeAgo('2026-08-29T12:00:30Z', 'short', now)).toBe('just now')
  })

  it('says nothing for a story with no usable date', () => {
    expect(timeAgo(null, 'short', now)).toBe('')
    expect(timeAgo('not a date', 'short', now)).toBe('')
  })
})

describe('rateAsPercentText', () => {
  it('reads a stored rate as the percentage a person types', () => {
    expect(rateAsPercentText('0.2499')).toBe('24.99')
    expect(rateAsPercentText('0.1999')).toBe('19.99')
    expect(rateAsPercentText('0.05')).toBe('5')
    expect(rateAsPercentText('1')).toBe('100')
  })

  // 0.2499 * 100 is 24.990000000000002, so the point is moved in text.
  it('never lands on a float artefact', () => {
    expect(rateAsPercentText('0.2499')).not.toContain('000000')
    expect((0.2499 * 100).toString()).toContain('000000')
  })

  it('keeps a rate that carries more places than a percentage needs', () => {
    expect(rateAsPercentText('0.249925')).toBe('24.9925')
  })

  it('is blank for a card with no rate on file, and zero for a 0% one', () => {
    expect(rateAsPercentText(null)).toBe('')
    expect(rateAsPercentText('0')).toBe('0')
  })
})

describe('month keys', () => {
  it('names a month without moving it across a time zone', () => {
    expect(formatMonthKey('2026-08', 'monthLong', 'en-US')).toBe('August 2026')
    expect(formatMonthKey('2026-01', 'month', 'en-US')).toBe('Jan 2026')
  })

  it('knows how long a month is, leap years included', () => {
    expect(daysInMonth('2026-02')).toBe(28)
    expect(daysInMonth('2028-02')).toBe(29)
    expect(monthBounds('2026-09')).toEqual({ from: '2026-09-01', to: '2026-09-30' })
  })
})

describe('timeAgo', () => {
  const now = new Date('2026-03-20T12:00:00Z')

  it('answers in the units the question was asked in, in either style', () => {
    expect(timeAgo('2026-03-20T11:59:30Z', 'long', now)).toBe('just now')
    expect(timeAgo('2026-03-20T11:30:00Z', 'long', now)).toBe('30 minutes ago')
    expect(timeAgo('2026-03-20T09:00:00Z', 'long', now)).toBe('3 hours ago')
    expect(timeAgo('2026-03-19T12:00:00Z', 'long', now)).toBe('1 day ago')
    expect(timeAgo('2026-03-17T12:00:00Z', 'short', now)).toBe('3d ago')
  })

  it('says when something is still to come', () => {
    expect(timeAgo('2026-03-20T15:00:00Z', 'long', now)).toBe('in 3 hours')
  })

  it('falls back to a date after thirty days, whatever the style', () => {
    expect(timeAgo('2026-01-01T12:00:00Z', 'short', now)).toBe(timeAgo('2026-01-01T12:00:00Z', 'long', now))
    expect(timeAgo('2026-01-01T12:00:00Z', 'long', now)).not.toContain('ago')
  })

  it('says never in prose rather than printing a missing timestamp', () => {
    expect(timeAgo(null, 'long', now)).toBe('never')
    expect(timeAgo(null, 'short', now)).toBe('')
  })
})

describe('plural', () => {
  it('picks the form by count and groups the digits', () => {
    expect(plural(1, 'transaction')).toBe('1 transaction')
    expect(plural(0, 'transaction')).toBe('0 transactions')
    expect(plural(2, 'person', 'people')).toBe('2 people')
    expect(plural(1234, 'row')).toBe('1,234 rows')
  })
})

describe('pluralWord', () => {
  it('is just the word plural prefixes with the count', () => {
    expect(pluralWord(1, 'month')).toBe('month')
    expect(pluralWord(3, 'month')).toBe('months')
    expect(pluralWord(2, 'person', 'people')).toBe('people')
  })
})

describe('parseIsoDay', () => {
  it('reads a well-formed day', () => {
    expect(parseIsoDay('2026-08-25')).toEqual(new Date(2026, 7, 25))
  })

  it('refuses a day a calendar does not have, rather than rolling it into the next month', () => {
    expect(parseIsoDay('2026-02-31')).toBeNull()
    expect(parseIsoDay('2026-02-30')).toBeNull()
    expect(parseIsoDay('2026-13-01')).toBeNull()
  })

  it('accepts Feb 29 on a leap year and refuses it otherwise', () => {
    expect(parseIsoDay('2024-02-29')).not.toBeNull()
    expect(parseIsoDay('2026-02-29')).toBeNull()
  })

  it('refuses a form that is not YYYY-MM-DD', () => {
    expect(parseIsoDay('03/04/2025')).toBeNull()
    expect(parseIsoDay('2026-08-25T00:00:00Z')).toBeNull()
    expect(parseIsoDay('not a date')).toBeNull()
  })
})

describe('capitalize, asSentence and initial', () => {
  it('upper-cases the first letter only', () => {
    expect(capitalize('orders on file')).toBe('Orders on file')
    expect(capitalize('')).toBe('')
  })

  it('makes a sentence of a fragment', () => {
    expect(asSentence('  the bank said no ')).toBe('The bank said no.')
    expect(asSentence('Done!')).toBe('Done!')
    expect(asSentence('  ')).toBe('')
  })

  it('draws the first letter of a label', () => {
    expect(initial(' netflix')).toBe('N')
    expect(initial('')).toBe('')
  })
})

describe('blankToNull', () => {
  it('trims before deciding, so stray whitespace clears a field too', () => {
    expect(blankToNull('  ')).toBeNull()
    expect(blankToNull('')).toBeNull()
    expect(blankToNull('  Ada  ')).toBe('Ada')
  })
})
