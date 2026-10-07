import { describe, expect, it } from 'vitest'

import {
  merchantKey,
  type MerchantMatchCandidate,
  type MerchantOrder,
} from '@/lib/clients/merchant'
import { moneyFromCents } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { BankRowCandidates } from './BankRowCandidates'

const order = {
  id: 'order-1',
  card_total: moneyFromCents(4000),
} as MerchantOrder

const row = (id: string, cents: number): MerchantMatchCandidate => ({
  id,
  account_id: 'acct-card',
  account_name: 'Amazon Card',
  date: '2026-08-03',
  amount: moneyFromCents(cents),
  payee: `Payee ${id}`,
  statement_name: 'AMAZON.COM',
  is_pending: false,
  matched_order_id: null,
  matched_order_number: '',
})

function render(candidates: MerchantMatchCandidate[]) {
  return renderScreen(
    <BankRowCandidates merchant="amazon" order={order} busy={false} onPick={() => {}} />,
    { seed: [[[...merchantKey('amazon'), 'candidates', order.id, ''], { candidates }]] },
  )
}

describe('<BankRowCandidates>', () => {
  it('marks only the rows that pay the card total to the cent, whatever their sign', () => {
    const html = render([row('a', -4000), row('b', 4000), row('c', -4001)])
    const marks = html
      .split('<li>')
      .slice(1)
      .map((one) => one.includes('to the cent'))
    expect(marks).toEqual([true, true, false])
    expect(html).toContain('-$40.01')
  })
})
