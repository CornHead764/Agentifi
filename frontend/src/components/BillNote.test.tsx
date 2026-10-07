import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { Occurrence } from '@/lib/clients/upcoming'
import { formatDate } from '@/lib/format'
import { parseMoney } from '@/lib/money'

import { BillNote } from './BillNote'

const SLOT: Occurrence = {
  series_id: 'ser-power',
  account_id: 'acct-1',
  category_id: null,
  kind: 'bill',
  label: 'Power',
  due_on: '2026-08-03',
  amount: parseMoney('-90.00'),
  status: 'past_due',
  pays_on: '2026-08-01',
  bill: {
    id: 'bill-1',
    amount_due: parseMoney('90.00'),
    due_on: '2026-08-03',
    status: 'paid',
    source: 'provider',
    fetched_at: '2026-08-04T06:00:00Z',
    document_id: null,
  },
  bill_link: null,
  transaction_id: null,
}

const AUTOPAYS = `autopays ${formatDate('2026-08-01', 'short')}`

describe('<BillNote>', () => {
  it('continues a row’s sub line, each note after a separator', () => {
    const html = renderToStaticMarkup(<BillNote occurrence={SLOT} inline />)
    expect(html).toBe(`<span> · ${AUTOPAYS}</span><span> · biller reports paid</span>`)
  })

  it('stands as its own line under a card’s amount', () => {
    const html = renderToStaticMarkup(<BillNote occurrence={SLOT} />)
    expect(html).toBe(
      `<p class="reminder-card__bill"><span>${AUTOPAYS}</span><span>biller reports paid</span></p>`,
    )
  })

  it('renders nothing when the biller has said nothing about the slot', () => {
    const quiet = { ...SLOT, status: 'upcoming' as const, pays_on: null }
    expect(renderToStaticMarkup(<BillNote occurrence={quiet} />)).toBe('')
    expect(renderToStaticMarkup(<BillNote occurrence={quiet} inline />)).toBe('')
  })
})
