/**
 * *Suggest a reminder* opens the series editor on what the bills describe,
 * with the billed account chosen to link on save; a reminder the payments
 * already belong to is offered first. The figures are invented.
 */

import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { BillReminderSuggestion, BillSubaccount } from '@/lib/clients/bills'
import type { Series } from '@/lib/clients/upcoming'
import { parseMoney } from '@/lib/money'
import { recurrenceFor } from '@/lib/recurrence'
import { renderScreen, testQueryClient } from '@/test/renderScreen'

import { suggestionNotice } from './draft'
import { SuggestedReminder } from './SuggestedReminder'

const yard: BillSubaccount = {
  id: 'sub-yard',
  connection_id: 'conn-lawn',
  biller: 'trugreen',
  external_id: 'yard-1',
  label: 'Front yard',
  masked_number: null,
  is_selected: true,
  series_id: null,
  account_id: null,
}

const lawn: BillReminderSuggestion = {
  subaccount_id: 'sub-yard',
  connection_id: 'conn-lawn',
  account_id: 'acct-checking',
  category_id: null,
  kind: 'bill',
  description: 'GREEN LAWN CO 5651',
  display_name: 'Lawn service',
  amount: parseMoney('-45.00'),
  amount_varies: false,
  match_criteria: 'exact',
  match_amount_min: null,
  match_amount_max: null,
  recurrence: recurrenceFor('EVERY_MONTH', new Date('2026-04-08T00:00:00'), {
    byMonth: [4, 5, 6, 7, 8, 9, 10],
  }),
  start_on: '2026-10-08',
  confident: true,
  due_dates: 14,
  first_due: '2025-04-08',
  last_due: '2026-10-07',
  payment_ids: ['p-1', 'p-2'],
  paid_by_series_id: null,
}

function render(seed: (client: QueryClient) => void) {
  const client = testQueryClient()
  seed(client)
  return renderScreen(<SuggestedReminder subaccount={yard} onClose={() => {}} />, { client })
}

const SUGGESTED = ['bills', 'suggested-reminder', 'sub-yard']

describe('suggestionNotice', () => {
  it('says what the reminder was read from, and the season', () => {
    expect(suggestionNotice(lawn)).toContain('Read from 14 due dates')
    expect(suggestionNotice(lawn)).toContain('The bills come only in April to October.')
    expect(suggestionNotice(lawn)).toContain('2 payments in the bank history set the account')
    expect(suggestionNotice(lawn)).not.toContain('check the schedule')
  })

  it('asks for a check when the rhythm is a guess, the amount varies or no payment was found', () => {
    const notice = suggestionNotice({
      ...lawn,
      confident: false,
      amount_varies: true,
      payment_ids: [],
      account_id: null,
    })
    expect(notice).toContain('check the schedule')
    expect(notice).toContain('each bill sets its own')
    expect(notice).toContain('choose the account it is paid from')
  })
})

describe('<SuggestedReminder>', () => {
  it('opens the series editor filled in from the bills', () => {
    const markup = render((client) => {
      client.setQueryData(SUGGESTED, lawn)
      client.setQueryData(['series', 'all', ''], [])
    })
    expect(markup).toContain('New recurring item')
    expect(markup).toContain('value="Lawn service"')
    expect(markup).toContain('value="GREEN LAWN CO 5651"')
    expect(markup).toContain('The bills come only in April to October.')
  })

  it('starts blank, still linked, when the bills keep no schedule', () => {
    const markup = render((client) => {
      client.setQueryData(SUGGESTED, null)
      client.setQueryData(['series', 'all', ''], [])
    })
    expect(markup).toContain('New recurring item')
    expect(markup).toContain('keep no schedule to read one from')
  })

  it('offers the reminder the payments already belong to before a new one', () => {
    const earlier = { id: 'series-yard', label: 'Yard care' } as Series
    const markup = render((client) => {
      client.setQueryData(SUGGESTED, { ...lawn, paid_by_series_id: 'series-yard' })
      client.setQueryData(['series', 'all', ''], [earlier])
    })
    expect(markup).toContain('Link “Yard care”')
    expect(markup).toContain('Suggest a new one')
    expect(markup).not.toContain('New recurring item')
  })
})
