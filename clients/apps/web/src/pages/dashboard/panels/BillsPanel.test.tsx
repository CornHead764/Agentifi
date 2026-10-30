/**
 * The Bills & Income widget carries the biller's own date when the money
 * leaves before the due date, so the dashboard agrees with the reminder strip
 * and the list. Only then: a bill paid on its due date is already described.
 */

import { describe, expect, it } from 'vitest'

import type { Occurrence, OccurrenceList } from '@/lib/clients/upcoming'
import { formatDate, toIsoDate } from '@/lib/format'
import { parseMoney, ZERO_MONEY } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { BillsPanel } from './BillsPanel'

function occurrence(over: Partial<Occurrence> = {}): Occurrence {
  return {
    series_id: 'ser-1',
    account_id: 'acct-1',
    category_id: null,
    kind: 'bill',
    label: 'Fibre broadband',
    due_on: '2026-10-26',
    amount: parseMoney('-64.00'),
    status: 'upcoming',
    pays_on: null,
    bill: null,
    bill_link: null,
    transaction_id: null,
    ...over,
  }
}

function list(items: Occurrence[]): OccurrenceList {
  return {
    window: { from: null, to: null, date_field: 'effective' },
    items,
    summary: {
      income: ZERO_MONEY,
      expenses: ZERO_MONEY,
      net: ZERO_MONEY,
      count: items.length,
      past_due: 0,
    },
  }
}

/**
 * The window the panel asks for, computed the way the panel computes it: its
 * bounds are today's date, so the key cannot be written down as a literal.
 */
function windowKey(): [string, string, string, boolean] {
  const from = new Date()
  const to = new Date(from)
  to.setDate(to.getDate() + 30)
  return ['occurrences', toIsoDate(from), toIsoDate(to), false]
}

/** A clinic's newest unpaid statement, which no autopay takes. Invented. */
const PAY_MANUALLY: Occurrence = {
  series_id: null,
  account_id: null,
  category_id: null,
  kind: 'bill',
  label: 'Example Health',
  due_on: '2026-10-28',
  amount: parseMoney('-90.00'),
  status: 'upcoming',
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

function render(items: Occurrence[]): string {
  return renderScreen(<BillsPanel />, { seed: [[windowKey(), list(items)]] })
}

describe('the autopay note on a dashboard tile', () => {
  it('names the day the money leaves when that is before the due date', () => {
    const markup = render([occurrence({ due_on: '2026-10-26', pays_on: '2026-10-21' })])

    expect(markup).toContain(`autopays ${formatDate('2026-10-21', 'short')}`)
  })

  it('says nothing when the bill pays on the day it is due', () => {
    const markup = render([occurrence({ due_on: '2026-10-26', pays_on: '2026-10-26' })])

    expect(markup).not.toContain('autopays')
  })

  it('says nothing when no provider has said when it pays', () => {
    const markup = render([occurrence({ pays_on: null })])

    expect(markup).not.toContain('autopays')
  })
})

describe('a pay-manually reminder on a dashboard tile', () => {
  it('says to pay it by hand, with the provider, the account, the amount and the statement', () => {
    const markup = render([PAY_MANUALLY])

    expect(markup).toContain('pay manually')
    expect(markup).toContain('Example Health')
    expect(markup).toContain('Guarantor account ****5678')
    expect(markup).toContain('90.00')
    expect(markup).toContain('Statement')
  })

  it('says the same of a linked reminder whose connection has no autopay', () => {
    const markup = render([
      occurrence({ bill_link: { ...PAY_MANUALLY.bill_link!, autopay: false } }),
    ])

    expect(markup).toContain('pay manually')
    // A series is paid from its own account, so the tile names no billed one.
    expect(markup).not.toContain('Guarantor account')
  })
})
