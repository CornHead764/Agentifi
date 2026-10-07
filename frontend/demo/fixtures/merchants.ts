/**
 * The Amazon order and the Costco receipt behind two card charges, and the
 * receipts on the HSA's purchases. Invented items and order numbers.
 */

import { accountName, id } from './core'
import { SHOWCASE, TRANSACTIONS, byNumber, money } from './ledger'
import { state } from './state'

interface ItemSeed {
  title: string
  price: string
  quantity?: number
}

function items(seeds: ItemSeed[], shipped: string) {
  return seeds.map((seed, index) => ({
    sku: `DEMO${String(index + 1).padStart(6, '0')}`,
    title: seed.title,
    quantity: seed.quantity ?? 1,
    unit_price: seed.price,
    total_owed: money(Number(seed.price) * (seed.quantity ?? 1)),
    shipped_on: shipped,
    condition: 'New',
    url: '',
    catalog: null,
  }))
}

function order(n: number, merchant: 'amazon' | 'costco', txnNumber: number, seeds: ItemSeed[]) {
  const txn = byNumber(txnNumber)
  const total = txn.amount.slice(1)
  const warehouse = merchant === 'costco'
  return {
    id: id('ordr', n),
    merchant,
    merchant_account_id: id('macct', n),
    account_label: warehouse ? 'Membership ending 0000' : 'Sam Sample',
    order_number: warehouse ? '0000-0000-0001' : '123-0000000-0000001',
    ordered_on: txn.date,
    total,
    currency: 'USD',
    status: warehouse ? 'Completed' : 'Delivered',
    details_url: '',
    source: 'agentifi_json',
    kind: warehouse ? 'warehouse' : 'online',
    location: warehouse ? 'Springfield' : '',
    items: items(seeds, txn.date),
    refunds: [],
    matched_transaction_ids: [txn.id],
    matched_transactions: [
      {
        id: txn.id,
        account_id: txn.account_id,
        date: txn.date,
        amount: txn.amount,
        payee: txn.payee,
        statement_name: txn.statement_name,
        account_name: accountName(txn.account_id),
        basis: 'order_total',
        confidence: 1,
      },
    ],
    card_total: total,
    gift_card_amount: null,
    tax: null,
    paid_by_gift_card: false,
    cancelled: false,
    ignored: false,
  }
}

const AMAZON_ORDER = order(1, 'amazon', SHOWCASE.amazon, [
  { title: 'USB-C Charging Cable, 6 ft, 2 pack', price: '13.00' },
  { title: 'Wireless Mouse, Quiet Click', price: '25.00' },
  { title: 'The Sample Novel (Paperback)', price: '15.00' },
  { title: 'Kitchen Sponges, 24 count', price: '9.00' },
])

const COSTCO_ORDER = order(2, 'costco', SHOWCASE.costco, [
  { title: 'Organic Strawberries, 2 lb', price: '9.00' },
  { title: 'Large Eggs, 24 count', price: '7.00' },
  { title: 'Rotisserie Chicken', price: '5.00' },
  { title: 'Salmon Fillets, 3 lb', price: '22.00' },
  { title: 'Almond Milk, 3 pack', price: '9.00' },
  { title: 'Whole Bean Coffee, 2.5 lb', price: '18.00' },
  { title: 'Mixed Nuts, 2.5 lb', price: '16.00' },
  { title: 'Avocados, bag of 6', price: '7.00' },
  { title: 'Greek Yogurt, 2 pack', price: '13.00' },
  { title: 'Sourdough Bread, 2 loaves', price: '6.00' },
  { title: 'Baby Spinach, 1 lb', price: '4.00' },
  { title: 'Sharp Cheddar, 2 lb', price: '9.00' },
  { title: 'Paper Towels, 12 rolls', price: '23.00' },
  { title: 'Laundry Detergent, 150 loads', price: '20.00' },
  { title: 'Trash Bags, 200 count', price: '16.00' },
  { title: 'Toothpaste, 5 pack', price: '14.00' },
  { title: 'Sunscreen SPF 50, 2 pack', price: '18.00' },
  { title: 'Washable Markers, 40 count', price: '13.00' },
  { title: 'Construction Paper, 500 sheets', price: '10.00' },
])

function merchantMatch(_query: URLSearchParams, _body: unknown, path: string) {
  const txnId = path.split('/')[3]
  const found = [AMAZON_ORDER, COSTCO_ORDER].find((one) => one.matched_transaction_ids.includes(txnId))
  if (!found) return { status: 404, body: { detail: 'No purchase' } }
  return {
    transaction_id: txnId,
    amount: found.card_total,
    basis: 'order_total',
    confidence: 1,
    order: found,
    orders: [{ amount: found.card_total, basis: 'order_total', order: found }],
    refund: null,
  }
}

/* ---- Receipts ------------------------------------------------------------ */

function receipt(txnId: string, n: number) {
  const docId = id('doc', n)
  return {
    id: docId,
    filename: n >= 900 ? 'receipt-photo.png' : `receipt-${String(n).padStart(3, '0')}.png`,
    content_type: 'image/png',
    size_bytes: 184_000,
    url: `/documents/${docId}/content`,
    source: n >= 900 ? 'receipt_scan' : 'upload',
    source_ref: '',
    via: 'transaction',
    uploaded_by_user_id: null,
    created_at: '2026-06-15T15:00:00Z',
    transaction_id: txnId,
  }
}

function documentsFor(query: URLSearchParams) {
  const txnId = query.get('transaction_id')
  if (!txnId) return []
  const uploaded = state.uploads.get(txnId)
  if (uploaded) return [receipt(txnId, 900 + Number(uploaded))]
  const txn = TRANSACTIONS.find((one) => one.id === txnId)
  if (txn?.receipt_status === 'on_file') return [receipt(txnId, Number(txnId.slice(-12)))]
  return []
}

function upload(_query: URLSearchParams, body: unknown) {
  const text = typeof body === 'string' ? body : ''
  const target = /name="target_id"\r?\n\r?\n([0-9a-f-]{36})/.exec(text)?.[1] ?? byNumber(SHOWCASE.pharmacy).id
  state.uploads.set(target, String(state.uploads.size + 1))
  return receipt(target, 900 + state.uploads.size)
}

function content() {
  if (!state.receiptImage) return { status: 404, body: { detail: 'No receipt image' } }
  return { status: 200, body: null, binary: { contentType: 'image/png', bytes: state.receiptImage } }
}

export const MERCHANTS = {
  'GET /merchants/transactions/:id': merchantMatch,
  'GET /merchants/transactions/:id/candidates': { candidates: [AMAZON_ORDER, COSTCO_ORDER] },
  'GET /merchants/:merchant/agent': { configured: true, reachable: true, detail: '', unavailable: '' },
  'GET /documents': documentsFor,
  'POST /documents': upload,
  'GET /documents/:id/content': content,
}
