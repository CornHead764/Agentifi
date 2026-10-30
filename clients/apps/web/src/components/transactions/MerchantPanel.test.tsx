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

function order(matched: MerchantMatchedTransaction[]): MerchantOrder {
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
    items: [],
    refunds: [],
    matched_transaction_ids: matched.map((row) => row.id),
    matched_transactions: matched,
    card_total: parseMoney('30.00'),
    gift_card_amount: parseMoney('20.00'),
    tax: null,
    paid_by_gift_card: false,
    cancelled: false,
    ignored: false,
  }
}

function render(id: string, amount: string, matched: MerchantMatchedTransaction[]): string {
  const txn = transaction({ id: id as Uuid, amount: parseMoney(amount), payee: 'Amazon' })
  const one = order(matched)
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
