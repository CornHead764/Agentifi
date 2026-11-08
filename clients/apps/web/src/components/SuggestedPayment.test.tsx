import { describe, expect, it, vi } from 'vitest'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { MarkedSlot, SuggestedPayment } from '@/lib/clients/upcoming'
import { parseMoney } from '@agentifi/core/money'
import { renderScreen } from '@/test/renderScreen'

import { IsThisItDialog, SuggestedPaymentLine } from './SuggestedPayment'

/** An invented monthly rent marked paid, and two rows that could be it. */
const RENT: MarkedSlot = { series_id: 'ser-rent', due_on: '2026-09-01', label: 'Rent', income: false }

const NEAR: SuggestedPayment = {
  id: 'txn-near',
  account_id: 'acct-1',
  date: '2026-09-02',
  amount: parseMoney('-1500.00'),
  statement_name: 'EXAMPLE PROPERTY MGMT WEB PMT',
  payee: 'Example Property',
}

const FAR: SuggestedPayment = {
  ...NEAR,
  id: 'txn-far',
  date: '2026-09-05',
  statement_name: 'ONLINE TRANSFER 0001',
  payee: '',
}

const suggestedKey = ['occurrences', 'suggested-payments', 'ser-rent', '2026-09-01']

describe('"Is this it?" as a slot is marked', () => {
  it('shows each row by its date, amount and statement name, with a link for each', () => {
    const html = renderScreen(<IsThisItDialog slot={RENT} candidates={[NEAR, FAR]} onClose={() => {}} />)

    expect(html).toContain('Is this it?')
    expect(html).toContain('EXAMPLE PROPERTY MGMT WEB PMT')
    expect(html).toContain('ONLINE TRANSFER 0001')
    expect(html).toContain('Sep 2')
    expect(html).toContain('1,500.00')
    expect(html.match(/Link this/g)).toHaveLength(2)
    expect(html).toContain('Mark paid anyway')
  })

  it('says received for income', () => {
    const html = renderScreen(
      <IsThisItDialog slot={{ ...RENT, income: true }} candidates={[NEAR]} onClose={() => {}} />,
    )

    expect(html).toContain('Mark received anyway')
  })

  it('asks nothing when no row could be the payment', () => {
    const html = renderScreen(<IsThisItDialog slot={RENT} candidates={[]} onClose={() => {}} />)

    expect(html).not.toContain('Is this it?')
  })
})

describe('the suggestion on a slot already marked paid', () => {
  it('is one line naming the nearest row, with its link and a way to the rest', () => {
    const html = renderScreen(<SuggestedPaymentLine slot={RENT} />, {
      seed: [[suggestedKey, [NEAR, FAR]]],
    })

    expect(html).toContain('Is this it?')
    expect(html).toContain('EXAMPLE PROPERTY MGMT WEB PMT')
    expect(html).not.toContain('ONLINE TRANSFER 0001')
    expect(html).toContain('aria-label="Link EXAMPLE PROPERTY MGMT WEB PMT on Sep 2')
    expect(html).toContain('1 more')
    expect(html).not.toContain('callout')
  })

  it('is absent when no row could be the payment, leaving what stands in for it', () => {
    const html = renderScreen(<SuggestedPaymentLine slot={RENT} inline fallback="In 3 days" />, {
      seed: [[suggestedKey, []]],
    })

    expect(html).not.toContain('Is this it?')
    expect(html).toContain('In 3 days')
  })
})
