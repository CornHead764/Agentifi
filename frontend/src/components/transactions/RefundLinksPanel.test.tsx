/**
 * Both ends of a refund link in a row's detail: the credit names the purchase
 * it gives back, the purchase names its credits and whether they are all of
 * it. Figures and names are invented.
 */

import { describe, expect, it } from 'vitest'

import { refundLinksKey, type RefundCharge, type RefundLinks } from '@/lib/clients/refunds'
import { parseMoney } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'
import { RefundLinksPanel } from './RefundLinksPanel'

function row(over: Partial<RefundCharge>): RefundCharge {
  return {
    id: 'r1' as Uuid,
    account_id: 'a1' as Uuid,
    account_name: 'Everyday Card',
    date: '2026-06-01',
    amount: parseMoney('-54.00'),
    payee: 'Amazon',
    statement_name: 'AMZN MKTP US',
    category_id: null,
    category_name: null,
    ...over,
  }
}

function render(links: Partial<RefundLinks>): string {
  return renderScreen(<RefundLinksPanel transactionId={'t1' as Uuid} />, {
    seed: [
      [
        refundLinksKey('t1' as Uuid),
        { can_be_a_refund: false, refunds: [], refunded_by: [], refund_state: '', ...links },
      ],
    ],
  })
}

describe('the refund links in a row’s detail', () => {
  it('links a credit to the purchase it gives back, opening that row', () => {
    const rendered = render({ refunds: [row({ id: 'p1' as Uuid })] })
    expect(rendered).toContain('Refund of')
    expect(rendered).toContain('Everyday Card')
    expect(rendered).toContain('highlight=p1')
    expect(rendered).toContain('edit=p1')
  })

  it('says a purchase was refunded in part, and links each credit', () => {
    const rendered = render({
      refunded_by: [
        row({ id: 'c1' as Uuid, amount: parseMoney('27.00'), account_name: 'Gift card balance' }),
      ],
      refund_state: 'partial',
    })
    expect(rendered).toContain('Partially refunded')
    expect(rendered).toContain('edit=c1')
    expect(rendered).toContain('Gift card balance')
  })

  it('says a purchase given back whole was fully refunded', () => {
    const rendered = render({
      refunded_by: [row({ id: 'c1' as Uuid }), row({ id: 'c2' as Uuid })],
      refund_state: 'full',
    })
    expect(rendered).toContain('Fully refunded')
    expect(rendered).toContain('edit=c2')
  })

  it('shows nothing for a row with no links', () => {
    expect(render({})).not.toContain('txn-refunds')
  })
})
