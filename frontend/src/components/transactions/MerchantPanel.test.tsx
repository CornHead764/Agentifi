/**
 * The purchase in a row's detail when one order was paid by more than one
 * row, part from a gift card balance and part by card. Figures and names are
 * invented.
 */

import { describe, expect, it } from 'vitest'

import {
  merchantTransactionKey,
  type MerchantMatch,
  type MerchantMatchedTransaction,
  type MerchantOrder,
  type MerchantOrderItem,
} from '@/lib/clients/merchant'
import { parseMoney } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'
import { transaction } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'
import { MerchantPanel } from './MerchantPanel'

function paid(over: Partial<MerchantMatchedTransaction>): MerchantMatchedTransaction {
  return {
    id: 'card-row' as Uuid,
    account_id: 'card' as Uuid,
    date: '2026-05-12',
    amount: parseMoney('-30.00'),
    payee: 'Amazon',
    statement_name: 'AMZN MKTP',
    account_name: 'Everyday Card',
    basis: 'charge',
    confidence: 1,
    ...over,
  }
}

function order(matched: MerchantMatchedTransaction[], items: MerchantOrderItem[]): MerchantOrder {
  return {
    id: 'o1' as Uuid,
    merchant: 'amazon',
    merchant_account_id: 'm1' as Uuid,
    account_label: 'Household',
    order_number: '000-0000000-0000001',
    ordered_on: '2026-05-10',
    total: parseMoney('50.00'),
    currency: 'USD',
    status: '',
    details_url: '',
    source: 'agentifi_json',
    kind: 'online',
    location: '',
    items,
    refunds: [],
    matched_transaction_ids: matched.map((row) => row.id),
    matched_transactions: matched,
    card_total: parseMoney('30.00'),
    gift_card_amount: parseMoney('20.00'),
    tax: null,
    fsa_hsa_amount: null,
    paid_by_gift_card: false,
    cancelled: false,
    ignored: false,
  }
}

function render(
  id: string,
  amount: string,
  matched: MerchantMatchedTransaction[],
  items: MerchantOrderItem[] = [],
): string {
  const txn = transaction({ id: id as Uuid, amount: parseMoney(amount), payee: 'Amazon' })
  const one = order(matched, items)
  const match: MerchantMatch = {
    transaction_id: txn.id,
    amount: parseMoney(amount),
    basis: 'charge',
    confidence: 1,
    order: one,
    orders: [{ amount: parseMoney(amount), basis: 'charge', order: one }],
    refund: null,
  }
  return renderScreen(<MerchantPanel transaction={txn} onPick={() => {}} />, {
    seed: [[merchantTransactionKey(txn.id), match]],
  })
}

const giftCardRow = paid({
  id: 'gift-row' as Uuid,
  account_id: 'gift' as Uuid,
  date: '2026-05-10',
  amount: parseMoney('-20.00'),
  account_name: 'Gift card balance',
})

describe('an order paid by more than one row', () => {
  it('shows the whole total and links the gift card part from the card part', () => {
    const rendered = render('card-row', '-30.00', [paid({}), giftCardRow])
    expect(rendered).toContain('50.00')
    expect(rendered).toContain('Gift card balance')
    expect(rendered).toContain('20.00')
    expect(rendered).toContain('edit=gift-row')
    expect(rendered).not.toContain('edit=card-row')
  })

  it('shows the whole total, not the card part, on the gift card row', () => {
    const rendered = render('gift-row', '-20.00', [paid({}), giftCardRow])
    expect(rendered).toContain('50.00')
    expect(rendered).toContain('edit=card-row')
    expect(rendered).not.toContain('edit=gift-row')
  })

  it('lists no payments when one row paid the order', () => {
    const rendered = render('card-row', '-30.00', [paid({})])
    expect(rendered).toContain('30.00')
    expect(rendered).not.toContain('Paid from')
  })
})

function line(title: string, paidBy: string | null): MerchantOrderItem {
  return {
    sku: title,
    title,
    quantity: 1,
    unit_price: parseMoney('10.00'),
    total_owed: null,
    shipped_on: null,
    condition: '',
    url: '',
    catalog: null,
    paid_by: paidBy as Uuid | null,
    fsa_hsa_eligible: false,
    shipment: null,
  }
}

describe('an order charged per shipment', () => {
  const rows = [
    paid({ id: 'first-row' as Uuid, date: '2026-10-11', amount: parseMoney('-16.00') }),
    paid({ id: 'second-row' as Uuid, date: '2026-10-14', amount: parseMoney('-12.00') }),
  ]

  it('lists only the items this row paid for and says where the rest went', () => {
    const rendered = render('first-row', '-16.00', rows, [
      line('Desk lamp', 'first-row'),
      line('Monitor stand', 'second-row'),
    ])
    expect(rendered).toContain('Desk lamp')
    expect(rendered).not.toContain('Monitor stand')
    expect(rendered).toContain('+1 more item paid by the Oct 14 charge')
  })

  it('says an item is paid separately when no row has paid it yet', () => {
    const rendered = render('second-row', '-12.00', rows, [
      line('Desk lamp', null),
      line('Monitor stand', 'second-row'),
    ])
    expect(rendered).not.toContain('Desk lamp')
    expect(rendered).toContain('+1 more item paid separately')
  })

  it('lists every item when the charges could not be told apart', () => {
    const rendered = render('first-row', '-16.00', rows, [
      line('Desk lamp', null),
      line('Monitor stand', null),
    ])
    expect(rendered).toContain('Desk lamp')
    expect(rendered).toContain('Monitor stand')
    expect(rendered).not.toContain('more item')
    expect(rendered).toContain('Paid in 2 charges; Amazon didn&#x27;t say which items each covered.')
  })

  it('does not say so when the items were told apart or one row paid', () => {
    const divided = render('first-row', '-16.00', rows, [
      line('Desk lamp', 'first-row'),
      line('Monitor stand', 'second-row'),
    ])
    expect(divided).not.toContain('which items each covered')
    const single = render('first-row', '-16.00', rows.slice(0, 1), [
      line('Desk lamp', null),
      line('Monitor stand', null),
    ])
    expect(single).not.toContain('which items each covered')
  })

  it('marks an item the invoice calls FSA/HSA eligible', () => {
    const eligible = { ...line('Cold remedy', 'first-row'), fsa_hsa_eligible: true }
    const rendered = render('first-row', '-16.00', rows, [eligible, line('Desk lamp', 'second-row')])
    expect(rendered).toMatch(/Cold remedy[\s\S]*>FSA\/HSA</)
  })
})
