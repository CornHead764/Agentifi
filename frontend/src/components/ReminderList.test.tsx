import { describe, expect, it } from 'vitest'

import type { Occurrence } from '@/lib/clients/upcoming'
import { parseMoney } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { ReminderList } from './ReminderList'

/** A clinic's newest unpaid statement, which no autopay takes. Invented. */
const statement: Occurrence = {
  series_id: null,
  account_id: null,
  category_id: null,
  kind: 'bill',
  label: 'Example Health',
  due_on: '2026-10-28',
  amount: parseMoney('-90.00'),
  status: 'past_due',
  pays_on: null,
  bill: {
    id: 'bill-oct',
    amount_due: parseMoney('90.00'),
    due_on: '2026-10-28',
    status: 'open',
    source: 'provider',
    fetched_at: '2026-10-04T06:00:00Z',
    document_id: 'doc-oct',
  },
  bill_link: {
    connection_id: 'conn-clinic',
    biller: 'mychart',
    connection_label: 'Example Health',
    subaccount_label: 'Guarantor account ****5678',
    health: 'ok',
    autopay: false,
  },
  transaction_id: null,
}

const series: Occurrence = {
  ...statement,
  series_id: 'ser-power',
  account_id: 'acct-1',
  label: 'Power',
  bill: null,
  bill_link: null,
}

describe('a pay-manually reminder in the list', () => {
  it('says to pay it by hand, names the billed account and opens the statement', () => {
    const html = renderScreen(<ReminderList occurrences={[statement]} />)

    expect(html).toContain('Example Health')
    expect(html).toContain('Guarantor account ****5678')
    expect(html).toContain('pay manually')
    expect(html).toContain('Statement')
    expect(html).toContain('90.00')
  })

  it('is marked paid, and offers no slot to skip or pay at another amount', () => {
    const html = renderScreen(<ReminderList occurrences={[statement]} />)

    expect(html).toContain('aria-label="Mark Example Health paid"')
    expect(html).not.toContain('Skip Example Health')
    expect(html).not.toContain('for a different amount')
  })

  it('leaves a series its slot actions', () => {
    const html = renderScreen(<ReminderList occurrences={[series]} />)

    expect(html).toContain('aria-label="Skip Power"')
    expect(html).toContain('Mark Power paid for a different amount')
  })
})
