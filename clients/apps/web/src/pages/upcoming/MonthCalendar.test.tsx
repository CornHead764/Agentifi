import { describe, expect, it } from 'vitest'

import type { Occurrence } from '@/lib/clients/upcoming'
import { parseMoney } from '@agentifi/core/money'
import { renderScreen } from '@/test/renderScreen'

import { MonthCalendar } from './MonthCalendar'

const RENT: Occurrence = {
  series_id: 'ser-rent',
  account_id: 'acct-1',
  category_id: null,
  kind: 'bill',
  label: 'Rent',
  due_on: '2026-10-01',
  amount: parseMoney('-1200.00'),
  status: 'upcoming',
  pays_on: null,
  bill: null,
  bill_link: null,
  transaction_id: null,
  marked_paid: false,
  accepted_by_hand: false,
}

function render(occurrences: Occurrence[]): string {
  return renderScreen(
    <MonthCalendar
      month={new Date(2026, 9, 1)}
      onMonth={() => undefined}
      occurrences={occurrences}
      today={new Date(2026, 9, 10)}
    />,
  )
}

describe('a calendar day marked paid with no transaction linked', () => {
  it('draws its dot hollow, names it in the dot\'s title and adds it to the key', () => {
    const html = render([{ ...RENT, status: 'paid', marked_paid: true }])

    expect(html).toContain('calendar__dot--unlinked')
    expect(html).toContain('Marked paid, not linked to a transaction')
    expect(html).toContain('marked paid, not linked')
  })

  it('leaves a month with no mark its plain dots and key', () => {
    const html = render([RENT])

    expect(html).not.toContain('calendar__dot--unlinked')
    expect(html).not.toContain('not linked')
  })
})
