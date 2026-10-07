import { describe, expect, it } from 'vitest'

import {
  ALL_MERCHANTS,
  itemLabel,
  kindLabel,
  MERCHANTS,
  merchantsFor,
  orderLinkText,
  orderUrl,
  readMerchantId,
} from './merchants'

const ORDER = { order_number: '113-1234567-0000001', details_url: '' }

describe('which merchant a row is', () => {
  it('spots Amazon by either stem, in either name, in any case', () => {
    expect(
      merchantsFor({ payee: '', statement_name: 'AMAZON.COM*2K4D1R6Q3 AMZN.COM/BILL' }),
    ).toEqual(['amazon'])
    expect(merchantsFor({ payee: '', statement_name: 'AMZN Mktp US*1A2B3C' })).toEqual(['amazon'])
    expect(merchantsFor({ payee: 'Amazon', statement_name: '' })).toEqual(['amazon'])
  })

  it('spots Costco however the warehouse worded it', () => {
    expect(merchantsFor({ payee: '', statement_name: 'COSTCO WHSE #0123' })).toEqual(['costco'])
    expect(merchantsFor({ payee: 'Costco Gas', statement_name: 'COSTCO GAS #0123' })).toEqual([
      'costco',
    ])
  })

  it('names no merchant for a row that is neither', () => {
    expect(merchantsFor({ payee: 'Coffee', statement_name: 'POS DEBIT EXAMPLEMOBILE*AUTO' })).toEqual([])
  })
})

describe('orderUrl', () => {
  it('prefers the URL the file carried', () => {
    const url = 'https://www.amazon.com/gp/css/order-details?orderID=D01-9'
    expect(orderUrl('amazon', { ...ORDER, details_url: url })).toBe(url)
  })

  it('builds the order-details page from the number when the file named none', () => {
    expect(orderUrl('amazon', ORDER)).toBe(
      'https://www.amazon.com/gp/your-account/order-details?orderID=113-1234567-0000001',
    )
  })

  it('has nowhere to send a row with no order number', () => {
    expect(orderUrl('amazon', { ...ORDER, order_number: '' })).toBeNull()
  })

  it('sends a Costco order where its file said, or to its order page; a receipt to the purchases page', () => {
    const url = 'https://www.costco.com/OrderStatusDetailsCmd?orderNumber=1234567890'
    expect(orderUrl('costco', { order_number: '1234567890', details_url: url })).toBe(url)
    expect(orderUrl('costco', { order_number: '1234567890', details_url: '', kind: 'online' })).toBe(url)
    // A warehouse receipt's barcode has no page of its own that is known; the
    // Orders & Purchases page lists every receipt.
    const receipt = { order_number: '21100123456789012345', details_url: '', kind: 'warehouse' as const }
    const purchases = 'https://www.costco.com/myaccount/#/app/4900eb1f-0c10-4bd9-99c3-c59e6c1ecebf/ordersandpurchases'
    expect(orderUrl('costco', receipt)).toBe(purchases)
    expect(orderUrl('costco', { ...receipt, kind: 'fuel' })).toBe(purchases)
    expect(orderUrl('costco', { ...receipt, order_number: '' })).toBeNull()
    expect(orderLinkText(receipt)).toBe('View the receipt')
    expect(orderLinkText({ order_number: '1234567890', kind: 'online' })).toBe('1234567890')
    expect(orderLinkText({ order_number: '113-1' })).toBe('113-1')
  })
})

describe('the words each merchant uses', () => {
  it('says what kind of purchase a Costco record is', () => {
    expect(kindLabel('online')).toBe('Online')
    expect(kindLabel('warehouse')).toBe('Warehouse')
    expect(kindLabel('fuel')).toBe('Gas')
  })
})

describe('itemLabel', () => {
  it('reads the catalog name once the number was found, else the receipt', () => {
    expect(itemLabel({ title: 'KS ORG EGGS', catalog: null })).toBe('KS ORG EGGS')
    expect(
      itemLabel({ title: 'KS ORG EGGS', catalog: { title: 'Kirkland Signature Organic Eggs, 24-count' } }),
    ).toBe('Kirkland Signature Organic Eggs, 24-count')
    expect(itemLabel({ title: 'REGULAR', catalog: { title: '' } })).toBe('REGULAR')
  })
})

describe('where a merchant is configured', () => {
  it('is one section with the merchant in the path', () => {
    for (const id of ALL_MERCHANTS) {
      expect(MERCHANTS[id].settingsPath).toBe(`/settings/merchants/${id}`)
    }
  })

  it('reads a path segment back, and nothing else', () => {
    expect(readMerchantId('costco')).toBe('costco')
    expect(readMerchantId('target')).toBeNull()
    expect(readMerchantId(undefined)).toBeNull()
  })
})
