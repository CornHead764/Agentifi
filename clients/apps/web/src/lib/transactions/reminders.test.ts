import { describe, expect, it } from 'vitest'

import { formatDate } from '@agentifi/core/format'
import { parseMoney } from '@agentifi/core/money'
import type { Occurrence, OccurrenceStatus } from '@/lib/clients/upcoming'

import {
  REMINDER_DAYS_AHEAD,
  REMINDER_DAYS_BACK,
  pastDueCount,
  reminderWindow,
  seriesPath,
  skipPrompt,
  stripReminders,
} from './reminders'

function slot(
  due_on: string,
  status: OccurrenceStatus,
  account_id = 'acct-1',
  kind: Occurrence['kind'] = 'bill',
): Occurrence {
  return {
    series_id: `series-${due_on}`,
    account_id,
    category_id: null,
    kind,
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

const TODAY = '2026-08-29'

describe('the slots the strip draws', () => {
  const items = [
    slot('2026-09-10', 'upcoming'),
    slot('2026-07-04', 'past_due'),
    slot('2026-08-20', 'paid'),
    slot('2026-08-22', 'skipped'),
    slot('2026-09-01', 'upcoming', 'acct-2'),
  ]

  it('leads with what is late', () => {
    expect(stripReminders(items, null, TODAY).map((one) => one.due_on)).toEqual([
      '2026-07-04',
      '2026-09-01',
      '2026-09-10',
    ])
  })

  it('drops what has already been settled', () => {
    const drawn = stripReminders(items, null, TODAY)
    expect(drawn.some((one) => one.status === 'paid' || one.status === 'skipped')).toBe(false)
  })

  it('follows the register into one account', () => {
    expect(stripReminders(items, ['acct-2'], TODAY).map((one) => one.due_on)).toEqual(['2026-09-01'])
  })

  it('shows nothing for a scope that resolved to no accounts', () => {
    expect(stripReminders(items, [], TODAY)).toEqual([])
  })

  it('counts what is late for the strip to say so', () => {
    expect(pastDueCount(stripReminders(items, null, TODAY))).toBe(1)
    expect(pastDueCount([])).toBe(0)
  })
})

describe('a reminder whose day has come', () => {
  it('goes, unless it is past due', () => {
    const items = [
      slot('2026-08-20', 'past_due'),
      slot('2026-08-27', 'upcoming', 'acct-1', 'income'),
      slot('2026-08-29', 'upcoming', 'acct-1', 'income'),
      slot('2026-09-03', 'upcoming', 'acct-1', 'income'),
    ]
    expect(stripReminders(items, null, TODAY).map((one) => one.due_on)).toEqual([
      '2026-08-20',
      '2026-09-03',
    ])
  })

  it('keeps a bill on the day it is due, rather than bringing it back past due tomorrow', () => {
    expect(stripReminders([slot('2026-08-29', 'upcoming')], null, TODAY)).toHaveLength(1)
  })
})

describe('a reminder skip', () => {
  it('asks about the bill and the date being skipped, and says only that one goes', () => {
    const prompt = skipPrompt({ label: 'Power', due_on: '2026-10-28' })

    expect(prompt.title).toBe(`Skip Power on ${formatDate('2026-10-28')}?`)
    expect(prompt.description).toContain('Only this occurrence')
    expect(prompt.description).toContain('spending plan stops expecting it')
    expect(prompt.description).toContain('next one carries on')
  })
})

describe('the link from a reminder to its recurring item', () => {
  it('opens the Recurring tab on that series', () => {
    expect(seriesPath('ser-1')).toBe('/upcoming/recurring?series=ser-1')
  })
})
