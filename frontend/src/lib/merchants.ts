/**
 * The merchants the order connector knows: one engine, and a table of what
 * differs between shops. Nothing here imports the HTTP client, so the client
 * can name this table without a cycle.
 */

import type { Transaction } from '@/lib/transactions/types'

// The browser-console script that saves eBay's purchase history, bundled so
// the import card can hand it over. vite.config.ts and the Dockerfile name
// this path too.
import ebayExporter from '../../../tools/extractors/extract-ebay.js?raw'

export type MerchantId = 'amazon' | 'costco' | 'ebay'

export type OrderKind = 'online' | 'warehouse' | 'fuel'

/** One importable file, as the import card reads it: lead-in, body, a code or link, then tail. */
export interface FileSource {
  title: string
  body?: string
  code?: string
  link?: { href: string; text: string }
  tail?: string
  /** A script the person runs in their own signed-in browser, to copy or download. */
  script?: { name: string; text: string }
}

export interface Merchant {
  id: MerchantId
  name: string
  noun: string
  nounPlural: string
  article: 'a' | 'an'
  settingsPath: string
  /** The same stems the server's matcher looks for; keep them in step. */
  wording: (txn: Pick<Transaction, 'payee' | 'statement_name'>) => boolean
  /** A gift card balance kept as its own account (a Costco Shop Card is only a tender line). */
  hasGiftCardBalance: boolean
  kinds: readonly OrderKind[]
  /**
   * Where an order's invoice document comes from: the merchant's own printable
   * page, reopened for a backfill; laid out here from the lines on file; or
   * laid out by each pull from the merchant's API, which nothing else can do.
   */
  invoices: 'page' | 'laid-out' | 'pull'
  /**
   * How far back the merchant answers, in days; null is as far as the site
   * keeps. A merchant with a reach has no history to fetch beyond what the
   * first pull already read.
   */
  reachDays: number | null
  /** How an account is connected: a sign-in typed here, or the merchant's own consent page. */
  connects: 'sign-in' | 'consent'
  signInAdvice: string
  /** Empty means no import card: the pull reaches as far back as the site does. */
  files: readonly FileSource[]
  fileHint: string
  fileAccept: string
}

const KIND_LABELS: Record<OrderKind, string> = {
  online: 'Online',
  warehouse: 'Warehouse',
  fuel: 'Gas',
}

/** The catalog's product name once found, else the merchant's own (abbreviated) words. */
export function itemLabel(item: { title: string; catalog?: { title: string } | null }): string {
  return item.catalog?.title || item.title
}

export function kindLabel(kind: OrderKind): string {
  return KIND_LABELS[kind]
}

export function aNoun(merchant: MerchantId): string {
  const { article, noun } = MERCHANTS[merchant]
  return `${article} ${noun}`
}

function wordedAs(txn: Pick<Transaction, 'payee' | 'statement_name'>, ...stems: string[]): boolean {
  const text = `${txn.statement_name} ${txn.payee}`.toLowerCase()
  return stems.some((stem) => text.includes(stem))
}

export const MERCHANTS: Record<MerchantId, Merchant> = {
  amazon: {
    id: 'amazon',
    name: 'Amazon',
    noun: 'order',
    nounPlural: 'orders',
    article: 'an',
    settingsPath: '/settings/merchants/amazon',
    wording: (txn) => wordedAs(txn, 'amazon', 'amzn'),
    hasGiftCardBalance: true,
    kinds: ['online'],
    invoices: 'page',
    connects: 'sign-in',
    reachDays: null,
    signInAdvice:
      'Amazon may ask for a code it sends to your phone or email, or a picture to read; each is asked here. The session and the password are kept, so the daily update can sign in again on its own when Amazon asks; Forget password in the account menu deletes the password.',
    files: [
      {
        title: "Amazon's own export, the complete history.",
        body: 'Amazon → Your Account → Manage your data → Request your data → Your Orders. The email arrives within a day or so; upload each ',
        code: 'Retail.OrderHistory.N.csv',
        tail: ' from the ZIP. Only this file says what each line cost and when it shipped, which is how an order charged in parts is matched.',
      },
      {
        title: 'A browser extension, the last few months.',
        link: {
          href: 'https://github.com/xenolphthalein/order-history-exporter-for-amazon',
          text: 'Order History Exporter for Amazon',
        },
        tail: ' exports the orders page as JSON with every item. Run it signed in to each account.',
      },
    ],
    fileHint: "A CSV from Amazon's export, or a JSON file from the extension.",
    fileAccept: '.csv,.json,text/csv,application/json',
  },
  costco: {
    id: 'costco',
    name: 'Costco',
    noun: 'purchase',
    nounPlural: 'purchases',
    article: 'a',
    settingsPath: '/settings/merchants/costco',
    wording: (txn) => wordedAs(txn, 'costco'),
    hasGiftCardBalance: false,
    kinds: ['online', 'warehouse', 'fuel'],
    invoices: 'laid-out',
    connects: 'sign-in',
    reachDays: null,
    signInAdvice:
      'Costco may ask for a code it sends to your phone or email; it is asked here. The session is kept and every update renews it, and the password is kept with it, so an update can sign in again on its own when the session expires; Forget password in the account menu deletes the password.',
    files: [],
    fileHint: '',
    fileAccept: '',
  },
  ebay: {
    id: 'ebay',
    name: 'eBay',
    noun: 'order',
    nounPlural: 'orders',
    article: 'an',
    settingsPath: '/settings/merchants/ebay',
    wording: (txn) => wordedAs(txn, 'ebay'),
    hasGiftCardBalance: false,
    kinds: ['online'],
    invoices: 'pull',
    connects: 'consent',
    reachDays: 90,
    signInAdvice:
      "eBay is connected on eBay's own page, where you grant this server read access to your purchases; no password is kept here. eBay's API reaches back 90 days, which the first update reads in full; older purchases come in through the import below. The access lasts 18 months before eBay asks again.",
    files: [
      {
        title: 'Your purchases page, about three years back.',
        body: "Signed in to eBay in your own browser, open My eBay → Purchases, then the browser's developer tools (F12) → Console. Paste this script and press Enter; it reads the purchases list a page at a time and saves ",
        code: 'ebay-purchases-<date>.json',
        tail: '. It keeps no seller names. Tax, shipping and the payment method are not on the list, so those orders match by their totals.',
        script: { name: 'extract-ebay.js', text: ebayExporter },
      },
    ],
    fileHint: 'The ebay-purchases JSON file the script saved.',
    fileAccept: '.json,application/json',
  },
}

export const ALL_MERCHANTS: readonly MerchantId[] = ['amazon', 'costco', 'ebay']

export function readMerchantId(value: string | undefined): MerchantId | null {
  return ALL_MERCHANTS.find((id) => id === value) ?? null
}

export function merchantsFor(txn: Pick<Transaction, 'payee' | 'statement_name'>): MerchantId[] {
  return ALL_MERCHANTS.filter((id) => MERCHANTS[id].wording(txn))
}

/**
 * Where a tap on a record goes. Amazon's export names no URL, so the
 * order-details page is built from the number as a plain https link (Amazon's
 * app claims those on phones).
 */
export function orderUrl(
  merchant: MerchantId,
  order: { order_number: string; details_url: string; kind?: OrderKind },
): string | null {
  if (order.details_url) return order.details_url
  if (!order.order_number) return null
  if (merchant === 'amazon') {
    return `https://www.amazon.com/gp/your-account/order-details?orderID=${encodeURIComponent(
      order.order_number,
    )}`
  }
  if (merchant === 'costco') {
    // A receipt has no known deep link; this page lists receipts under In-Warehouse.
    if (order.kind && order.kind !== 'online') return COSTCO_PURCHASES_URL
    return `https://www.costco.com/OrderStatusDetailsCmd?orderNumber=${encodeURIComponent(order.order_number)}`
  }
  if (merchant === 'ebay') {
    // One seller order's page shows its whole checkout.
    return `https://order.ebay.com/ord/show?orderId=${encodeURIComponent(order.order_number)}`
  }
  return null
}

// Costco's Orders & Purchases page, a hash route whose client id is the fixed WCS one.
const COSTCO_WCS_CLIENT_ID = '4900eb1f-0c10-4bd9-99c3-c59e6c1ecebf'
const COSTCO_PURCHASES_URL = `https://www.costco.com/myaccount/#/app/${COSTCO_WCS_CLIENT_ID}/ordersandpurchases`

/** An order's number; a receipt's twenty-digit barcode nobody reads, so it says what it opens. */
export function orderLinkText(order: { order_number: string; kind?: OrderKind }): string {
  return order.kind && order.kind !== 'online' ? 'View the receipt' : order.order_number
}
