import { describe, expect, it } from 'vitest'

import { parseMoney } from '@/lib/money'
import { recurrenceFor } from '@/lib/recurrence'
import type { Series } from '@/lib/clients/upcoming'

import { dueState, nextDueText, sortSeries } from './rows'

// Every date below is read against this day, so the suite does not start
// failing the morning the fixtures fall into the past.
const TODAY = '2026-08-20'

function series(fields: Partial<Series> & { id: string }): Series {
  return {
    account_id: 'acct-1',
    category_id: null,
    kind: 'bill',
    description: 'ACME UTILITIES',
    display_name: null,
    label: fields.id,
    amount: parseMoney('-40.00'),
    currency: 'USD',
    recurrence: recurrenceFor('EVERY_MONTH', new Date(2026, 7, 1)),
    start_on: '2026-08-01',
    end_on: null,
    next_due_on: '2026-09-01',
    due_on: '2026-09-01',
    override_next_due_on: null,
    override_next_amount: null,
    auto_adjust_due_on: false,
    reminder_days: 3,
    match_criteria: 'auto',
    match_amount_min: null,
    match_amount_max: null,
    tag_ids: [],
    splits: [],
    is_active: true,
    annualized_amount: parseMoney('-480.00'),
    occurrences_per_year: 12,
    ...fields,
  }
}

describe('sortSeries', () => {
  it('leads with what falls due soonest and sinks the paused', () => {
    const rows = [
      series({ id: 'later', next_due_on: '2026-10-01' }),
      series({ id: 'paused', next_due_on: '2026-08-25', is_active: false }),
      series({ id: 'soonest', next_due_on: '2026-08-30' }),
    ]

    expect(sortSeries(rows, TODAY).map((one) => one.id)).toEqual(['soonest', 'later', 'paused'])
  })

  it('puts what is still to come above what went by, and the freshest miss first', () => {
    // A Simplifi import brings years of unmatched one-time reminders; the bill
    // due soon must come first.
    const rows = [
      series({ id: 'missed-2022', next_due_on: '2022-11-03' }),
      series({ id: 'missed-last-week', next_due_on: '2026-08-13' }),
      series({ id: 'friday', next_due_on: '2026-08-21' }),
      series({ id: 'done', next_due_on: null }),
    ]

    expect(sortSeries(rows, TODAY).map((one) => one.id)).toEqual([
      'friday',
      'missed-last-week',
      'missed-2022',
      'done',
    ])
  })

  it('puts a series with nothing left after the dated ones, not before them', () => {
    // A string comparison against null is where "no further occurrences" ends
    // up at the top of the table.
    const rows = [series({ id: 'done', next_due_on: null }), series({ id: 'due' })]

    expect(sortSeries(rows, TODAY).map((one) => one.id)).toEqual(['due', 'done'])
  })

  it('breaks a tie on the label a person reads', () => {
    const rows = [
      series({ id: 'b', label: 'Water' }),
      series({ id: 'a', label: 'Electricity' }),
    ]

    expect(sortSeries(rows, TODAY).map((one) => one.label)).toEqual(['Electricity', 'Water'])
  })

  it('leaves the list it was given alone', () => {
    const rows = [series({ id: 'later', next_due_on: '2026-10-01' }), series({ id: 'sooner' })]
    sortSeries(rows, TODAY)

    expect(rows.map((one) => one.id)).toEqual(['later', 'sooner'])
  })
})

describe('nextDueText', () => {
  it('says a series has run out rather than printing nothing', () => {
    expect(nextDueText(series({ id: 'one-time', next_due_on: null }))).toBe(
      'No further occurrences',
    )
  })

  it('prints the date otherwise', () => {
    expect(nextDueText(series({ id: 'due' }), TODAY)).toContain('2026')
    expect(nextDueText(series({ id: 'due' }), TODAY)).not.toContain('Past due')
  })

  it('calls a date that has gone by past due rather than next', () => {
    expect(nextDueText(series({ id: 'missed', next_due_on: '2022-11-03' }), TODAY)).toMatch(
      /^Past due .*2022/,
    )
  })
})

describe('dueState', () => {
  it('reads today as still to come', () => {
    expect(dueState(series({ id: 'today', next_due_on: TODAY }), TODAY)).toBe('upcoming')
    expect(dueState(series({ id: 'yesterday', next_due_on: '2026-08-19' }), TODAY)).toBe('overdue')
    expect(dueState(series({ id: 'done', next_due_on: null }), TODAY)).toBe('done')
  })
})
