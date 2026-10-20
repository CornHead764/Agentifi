/**
 * The register's paperclip and package: what rests on them is fetched when a
 * hover card opens, not when the row is drawn. Figures and names are invented.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  merchantTransactionKey,
  type MerchantMatch,
  type MerchantOrder,
} from '@/lib/clients/merchant'
import type { Document } from '@/lib/clients/documents'
import { parseMoney } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'
import { transaction } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'
import { ATTACHMENTS_KEY, AttachmentIndicator } from './AttachmentPanel'
import { MerchantOrderIndicator } from './MerchantPanel'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).hoverCardStub,
}))

const fetched = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/lib/api')>()
  return { ...real, api: { ...real.api, get: fetched } }
})

beforeEach(() => fetched.mockReset())

const ID = 't1' as Uuid

function file(over: Partial<Document>): Document {
  return {
    id: 'd1' as Uuid,
    filename: 'receipt-0001.pdf',
    content_type: 'application/pdf',
    size_bytes: 2048,
    url: '/documents/d1/content',
    source: 'upload',
    source_ref: '',
    uploaded_by_user_id: null,
    created_at: '2026-08-21T12:00:00Z',
    ...over,
  }
}

function order(): MerchantOrder {
  return {
    id: 'o1' as Uuid,
    merchant: 'amazon',
    merchant_account_id: 'm1' as Uuid,
    account_label: 'Household',
    order_number: '000-0000000-0000001',
    ordered_on: '2026-08-19',
    total: parseMoney('18.50'),
    currency: 'USD',
    status: '',
    details_url: '',
    source: 'agentifi_json',
    kind: 'online',
    location: '',
    items: [
      {
        sku: 'B000000001',
        title: 'Desk lamp',
        quantity: 1,
        unit_price: parseMoney('12.50'),
        total_owed: parseMoney('12.50'),
        shipped_on: null,
        condition: '',
        url: '',
        catalog: null,
        paid_by: null,
        fsa_hsa_eligible: false,
        shipment: null,
      },
      {
        sku: 'B000000002',
        title: 'Spare bulbs',
        quantity: 2,
        unit_price: parseMoney('3.00'),
        total_owed: parseMoney('6.00'),
        shipped_on: null,
        condition: '',
        url: '',
        catalog: null,
        paid_by: null,
        fsa_hsa_eligible: false,
        shipment: null,
      },
    ],
    refunds: [],
    matched_transaction_ids: [ID],
    matched_transactions: [],
    card_total: parseMoney('18.50'),
    gift_card_amount: null,
    tax: null,
    fsa_hsa_amount: null,
    paid_by_gift_card: false,
    cancelled: false,
    ignored: false,
  }
}

function match(): MerchantMatch {
  const one = order()
  return {
    transaction_id: ID,
    amount: parseMoney('18.50'),
    basis: 'charge',
    confidence: 1,
    order: one,
    orders: [{ amount: parseMoney('18.50'), basis: 'charge', order: one }],
    refund: null,
  }
}

describe('the register’s package', () => {
  it('is drawn only on a row matched to an order', () => {
    const on = renderScreen(
      <MerchantOrderIndicator transaction={transaction({ id: ID, has_merchant_order: true })} />,
    )
    expect(on).toContain('Matched to a purchase')
    const off = renderScreen(
      <MerchantOrderIndicator transaction={transaction({ id: ID, has_merchant_order: false })} />,
    )
    expect(off).not.toContain('Matched to a purchase')
  })

  it('lists the items bought, with the order line and amount', () => {
    const rendered = renderScreen(
      <MerchantOrderIndicator transaction={transaction({ id: ID, has_merchant_order: true })} />,
      { seed: [[merchantTransactionKey(ID), match()]] },
    )
    expect(rendered).toContain('Desk lamp')
    expect(rendered).toContain('2 × ')
    expect(rendered).toContain('Spare bulbs')
    expect(rendered).toContain('000-0000000-0000001')
    expect(rendered).toContain('18.50')
  })

  it('asks the server nothing until the card is open', () => {
    renderScreen(
      <MerchantOrderIndicator transaction={transaction({ id: ID, has_merchant_order: false })} />,
    )
    expect(fetched).not.toHaveBeenCalled()
  })
})

describe('the register’s paperclip', () => {
  it('is drawn only on a row with attachments', () => {
    expect(renderScreen(<AttachmentIndicator count={0} transactionId={ID} />)).not.toContain('attachment')
    expect(renderScreen(<AttachmentIndicator count={2} transactionId={ID} />)).toContain(
      '2 attachments',
    )
  })

  it('lists each attachment as something to open', () => {
    const rendered = renderScreen(<AttachmentIndicator count={2} transactionId={ID} />, {
      seed: [
        [
          ATTACHMENTS_KEY(ID),
          [file({}), file({ id: 'd2' as Uuid, filename: 'statement-0002.pdf' })],
        ],
      ],
    })
    expect(rendered).toContain('receipt-0001.pdf')
    expect(rendered).toContain('statement-0002.pdf')
    expect(rendered.match(/<button/g)).toHaveLength(2)
  })
})
