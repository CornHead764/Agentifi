import { describe, expect, it } from 'vitest'

import type { Bill } from '@/lib/clients/bills'
import type { Series } from '@/lib/clients/upcoming'
import { parseMoney } from '@/lib/money'

import { autopayFields, latestOpenBill, reminderChoices, splitSubaccounts } from './draft'

describe('the autopay rule as fields', () => {
  it('sends only the figure its own kind uses', () => {
    expect(autopayFields('days_before_due', '5')).toEqual({
      autopay_rule: 'days_before_due',
      autopay_days: 5,
    })
    expect(autopayFields('day_of_month', '9')).toEqual({
      autopay_rule: 'day_of_month',
      autopay_day: 9,
    })
  })

  it('sends no figure for the two rules that need none', () => {
    expect(autopayFields('none', '')).toEqual({ autopay_rule: 'none' })
    expect(autopayFields('on_due_date', '9')).toEqual({ autopay_rule: 'on_due_date' })
  })

  it('refuses a day of the month that is not one', () => {
    expect(autopayFields('day_of_month', '0')).toBeNull()
    expect(autopayFields('day_of_month', '32')).toBeNull()
    expect(autopayFields('day_of_month', '')).toBeNull()
    expect(autopayFields('day_of_month', '9.5')).toBeNull()
  })

  it('refuses a lead time that is not a count of days', () => {
    expect(autopayFields('days_before_due', '-2')).toBeNull()
    expect(autopayFields('days_before_due', '90')).toBeNull()
    expect(autopayFields('days_before_due', '')).toBeNull()
  })

  it('allows the same day, which is a lead time of nothing', () => {
    expect(autopayFields('days_before_due', '0')).toEqual({
      autopay_rule: 'days_before_due',
      autopay_days: 0,
    })
  })
})

describe('which bill a subaccount shows', () => {
  function bill(over: Partial<Bill>): Bill {
    return {
      id: 'b1',
      subaccount_id: 'sub1',
      due_on: '2026-10-26',
      amount_due: parseMoney('120.00'),
      minimum_due: null,
      currency: 'USD',
      issued_on: null,
      period_start: null,
      period_end: null,
      autopay_on: null,
      pays_on: null,
      status: 'open',
      source: 'manual',
      statement_url: '',
      document_id: null,
      fetched_at: '2026-09-25T06:00:00Z',
      amended_at: null,
      ...over,
    }
  }

  it('is nothing at all when nothing is owed', () => {
    expect(latestOpenBill([])).toBeNull()
    expect(latestOpenBill([bill({ status: 'paid' })])).toBeNull()
  })

  it('is the open one furthest out, whatever order they arrived in', () => {
    const rows = [
      bill({ id: 'older', due_on: '2026-09-26' }),
      bill({ id: 'newest', due_on: '2026-11-14' }),
      bill({ id: 'middle', due_on: '2026-10-26' }),
    ]

    expect(latestOpenBill(rows)?.id).toBe('newest')
  })

  it('skips a cycle that was superseded, which has an open bill of its own', () => {
    const rows = [
      bill({ id: 'replaced', due_on: '2026-11-14', status: 'superseded' }),
      bill({ id: 'standing', due_on: '2026-10-26' }),
    ]

    expect(latestOpenBill(rows)?.id).toBe('standing')
  })
})

describe('which accounts a card lists', () => {
  const account = (id: string, selected: boolean) =>
    ({ id, label: id, is_selected: selected }) as unknown as import('@/lib/clients/bills').BillSubaccount

  it('folds the hidden ones away in their own group, in the order the provider gave', () => {
    const { shown, hidden } = splitSubaccounts([account('a', false), account('b', true), account('c', true), account('d', false)])
    expect(shown.map((one) => one.id)).toEqual(['b', 'c'])
    expect(hidden.map((one) => one.id)).toEqual(['a', 'd'])
  })

  it('has nothing to fold when every account matters', () => {
    expect(splitSubaccounts([account('a', true)]).hidden).toEqual([])
  })
})

describe('which reminders a billed account can feed', () => {
  const series = (over: Partial<Series>) =>
    ({
      id: 's1',
      kind: 'bill',
      label: 'Sample Power',
      amount: parseMoney('-64.00'),
      due_on: '2026-10-03',
      is_active: true,
      ...over,
    }) as Series

  it('leaves out a canceled twin, which would read as a duplicate of the running one', () => {
    const rows = [
      series({ id: 'old', is_active: false }),
      series({ id: 'running' }),
      series({ id: 'water', label: 'Hill Water' }),
    ]

    expect(reminderChoices(rows, null)).toEqual([
      { id: 'water', label: 'Hill Water' },
      { id: 'running', label: 'Sample Power' },
    ])
  })

  it('keeps the canceled one already linked, and says it is canceled', () => {
    const rows = [series({ id: 'old', is_active: false }), series({ id: 'running' })]

    expect(reminderChoices(rows, 'old').map((one) => one.id)).toEqual(['old', 'running'])
    expect(reminderChoices(rows, 'old')[0]?.label).toMatch(/\(canceled\)$/)
  })

  it('tells two running reminders of one name apart by amount and due date', () => {
    const rows = [
      series({ id: 'monthly', amount: parseMoney('-41.00'), due_on: '2026-10-19' }),
      series({ id: 'quarterly', amount: parseMoney('-120.00'), due_on: '2026-11-09' }),
    ]
    const labels = reminderChoices(rows, null).map((one) => one.label)

    expect(new Set(labels).size).toBe(2)
    expect(labels[0]).toContain('41.00')
    expect(labels[1]).toContain('120.00')
  })

  it('never offers income', () => {
    expect(reminderChoices([series({ kind: 'income' })], null)).toEqual([])
  })
})
