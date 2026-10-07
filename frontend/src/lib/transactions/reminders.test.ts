import { describe, expect, it } from 'vitest'

import { parseMoney } from '@/lib/money'
import type { Occurrence, OccurrenceStatus } from '@/lib/clients/upcoming'

import {
  REMINDER_DAYS_AHEAD,
  REMINDER_DAYS_BACK,
  pastDueCount,
  reminderWindow,
  stripReminders,
} from './reminders'

function slot(
  due_on: string,
  status: OccurrenceStatus,
  account_id = 'acct-1',
): Occurrence {
  return {
    series_id: `series-${due_on}`,
    account_id,
    category_id: null,
    kind: 'bill',
    label: `Bill due ${due_on}`,
    due_on,
    amount: parseMoney('-20.00'),
    status,
    pays_on: null,
    bill: null,
    bill_link: null,
    transaction_id: null,
  }
}

describe('the window the strip asks for', () => {
  it('reaches back far enough to see what is late', () => {
    const { from, to } = reminderWindow(new Date(2026, 7, 29))
    expect(from).toBe('2026-06-30')
    expect(to).toBe('2026-09-28')
  })

  it('looks further back than forward', () => {
    expect(REMINDER_DAYS_BACK).toBeGreaterThan(REMINDER_DAYS_AHEAD)
  })
})

describe('the slots the strip draws', () => {
  const items = [
    slot('2026-09-10', 'upcoming'),
    slot('2026-07-04', 'past_due'),
    slot('2026-08-20', 'paid'),
    slot('2026-08-22', 'skipped'),
    slot('2026-09-01', 'upcoming', 'acct-2'),
  ]

  it('leads with what is late', () => {
    expect(stripReminders(items, null).map((one) => one.due_on)).toEqual([
      '2026-07-04',
      '2026-09-01',
      '2026-09-10',
    ])
  })

  it('drops what has already been settled', () => {
    const drawn = stripReminders(items, null)
    expect(drawn.some((one) => one.status === 'paid' || one.status === 'skipped')).toBe(false)
  })

  it('follows the register into one account', () => {
    expect(stripReminders(items, ['acct-2']).map((one) => one.due_on)).toEqual(['2026-09-01'])
  })

  it('shows nothing for a scope that resolved to no accounts', () => {
    expect(stripReminders(items, [])).toEqual([])
  })

  it('counts what is late for the strip to say so', () => {
    expect(pastDueCount(stripReminders(items, null))).toBe(1)
    expect(pastDueCount([])).toBe(0)
  })
})
