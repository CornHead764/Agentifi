import type { QueryClient } from '@tanstack/react-query'
import { Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import {
  merchantKey,
  type MerchantAccount,
  type MerchantOrderList,
  type MerchantSummary,
} from '@/lib/clients/merchant'
import { registerLinkFor } from '@/lib/clients/merchant'
import { moneyFromCents } from '@/lib/money'
import { ALL_MERCHANTS, MERCHANTS, type MerchantId } from '@/lib/merchants'
import { renderScreen, testQueryClient } from '@/test/renderScreen'

import { MerchantsSettings } from './MerchantsSettings'
import { MerchantSettings } from './MerchantSettings'

/** The screen off a seeded cache, for each merchant, in that merchant's own words. */
function render(merchant: MerchantId, seed?: (client: QueryClient) => void) {
  const client = testQueryClient()
  seed?.(client)
  return renderScreen(<MerchantSettings merchant={merchant} />, { client })
}

const bare = {
  merchant: 'amazon' as MerchantId,
  email: '',
  connected: false,
  signed_in_at: null,
  has_password: false,
  has_totp: false,
  second_factor: '' as const,
  sign_in_paused: '' as const,
  sync_enabled: false,
  sync_days: 30,
  last_synced_at: null,
  last_sync_status: '' as const,
  last_sync_error: '',
  has_failure_screenshot: false,
  needs_sign_in: false,
  pulling: false,
  backfill: null,
  gift_card_account_id: null,
  gift_card_balance: null,
  gift_card_balance_at: null,
  created_at: '2026-09-01T00:00:00Z',
}

const accounts: MerchantAccount[] = [
  {
    ...bare,
    id: 'acct-alex',
    label: 'Alex',
    name: 'Alex',
    orders: 12,
    email: 'alex@example.com',
    connected: true,
    signed_in_at: '2026-09-04T00:00:00Z',
    sync_enabled: true,
    last_synced_at: '2026-09-05T04:00:00Z',
    last_sync_status: 'ok',
    gift_card_account_id: 'acct-gc',
    gift_card_balance: moneyFromCents(4500),
    gift_card_balance_at: '2026-09-05T04:00:00Z',
  },
  {
    ...bare,
    id: 'acct-casey',
    label: '',
    name: 'casey@example.com',
    orders: 3,
    email: 'casey@example.com',
    connected: true,
    needs_sign_in: true,
    last_sync_status: 'needs_sign_in',
    last_sync_error: 'Amazon asked for a one-time code',
  },
]

const agent = { configured: true, reachable: true, detail: '', unavailable: '' }

const summary: MerchantSummary = {
  accounts: 2,
  orders: 15,
  items: 31,
  charges: 0,
  merchant_transactions: 140,
  matched_transactions: 14,
  newest_order: '2026-08-30',
  oldest_order: '2025-01-04',
}

const orders: MerchantOrderList = {
  total: 1,
  orders: [
    {
      id: 'order-1',
      merchant: 'amazon',
      merchant_account_id: 'acct-casey',
      account_label: 'Casey',
      order_number: '113-1234567-0000001',
      ordered_on: '2026-08-01',
      total: moneyFromCents(4000),
      currency: 'USD',
      status: 'Closed',
      details_url:
        'https://www.amazon.com/gp/your-account/order-details?orderID=113-1234567-0000001',
      source: 'amazon_csv',
      kind: 'online',
      location: '',
      items: [
        {
          sku: 'B0STORAGE',
          title: 'Glass Storage Set, 10pc',
          quantity: 1,
          unit_price: moneyFromCents(2450),
          total_owed: moneyFromCents(2600),
          shipped_on: '2026-08-02',
          condition: 'New',
          url: 'https://www.amazon.com/dp/B0STORAGE',
          catalog: null,
        },
      ],
      refunds: [],
      matched_transaction_ids: ['txn-1'],
      matched_transactions: [
        {
          id: 'txn-1',
          account_id: 'acct-card',
          date: '2026-08-03',
          amount: moneyFromCents(-4000),
          payee: 'Amazon',
          statement_name: 'AMAZON.COM*2K4D1R6Q3',
          account_name: 'Amazon Card',
          basis: 'charge',
          confidence: 0.98,
        },
      ],
      card_total: moneyFromCents(4000),
      gift_card_amount: moneyFromCents(0),
      tax: moneyFromCents(350),
      paid_by_gift_card: false,
      cancelled: false,
      ignored: false,
    },
  ],
}

/** The orders list is paged, so the cache holds pages rather than one list. */
function onePage(list: MerchantOrderList) {
  return { pages: [list], pageParams: [0] }
}

function seedAmazon(client: QueryClient, list: MerchantOrderList = orders) {
  client.setQueryData([...merchantKey('amazon'), 'accounts'], accounts)
  client.setQueryData([...merchantKey('amazon'), 'agent'], agent)
  client.setQueryData([...merchantKey('amazon'), 'summary'], summary)
  client.setQueryData([...merchantKey('amazon'), 'orders', '?limit=25'], onePage(list))
}

describe('Settings → Amazon', () => {
  it('lists the accounts and the orders, with the coverage in the orders header', () => {
    const html = render('amazon', seedAmazon)
    expect(html).toContain('Amazon accounts')
    expect(html).toContain('Alex')
    expect(html).toContain('12 orders')
    expect(html).toContain('Updating daily, the last 30 days')
    expect(html).toContain('Needs a sign-in')
    expect(html).toContain('Amazon asked for a one-time code.')
    expect(html).toContain('Sign in again')
    expect(html).toContain('Update now')
    expect(html).toContain('aria-label="Actions for Alex"')
    expect(html).not.toContain('Fetch history')
    expect(html).toContain('Gift card balance')
    expect(html).toContain('45.00')
    expect(html).toContain('displayNode=acct-gc')
    expect(html).toContain('Orders on file')
    expect(html).toContain('15 orders, ')
    expect(html).toContain('31 items')
    expect(html).toContain('14 of 140 Amazon rows matched, 126 without an order')
    expect(html).not.toContain('Coverage')
    expect(html).toContain('Match now')
    expect(html).toContain('Suggest categories')
    expect(html).toContain('Glass Storage Set, 10pc')
    // Casey's login has no name and is called by its email.
    expect(html).toContain('casey@example.com')
    expect(html).toContain('Amazon Card')
    // The bank row link opens the account's register two weeks either side of
    // the row, with the row itself marked.
    expect(html).toContain(
      '/transactions?displayNode=acct-card&amp;from=2026-07-20&amp;to=2026-08-17&amp;highlight=txn-1',
    )
    expect(html).toContain('113-1234567-0000001')
    expect(html).toContain('Actions for order 113-1234567-0000001')
    // One way in: adding an account is signing in to it.
    expect(html).toContain('Add account')
  })

  it("shows a running backfill's progress on the row, with Update now held back", () => {
    const html = render('amazon', (client) => {
      seedAmazon(client)
      client.setQueryData(
        [...merchantKey('amazon'), 'accounts'],
        [
          {
            ...accounts[0],
            backfill: {
              running: true,
              total: 9,
              done: 4,
              filed: 3,
              left: 0,
              stopped: '',
              finished_at: null,
            },
          },
        ],
      )
    })
    expect(html).toContain('Backfilling invoices: 4 of 9…')
    expect(html).not.toContain('Last invoice backfill')
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>(?:(?!<\/button>).)*Update now/)
  })

  it('says how the last backfill ended, and why it stopped early', () => {
    const html = render('amazon', (client) => {
      seedAmazon(client)
      client.setQueryData(
        [...merchantKey('amazon'), 'accounts'],
        [
          {
            ...accounts[0],
            backfill: {
              running: false,
              total: 9,
              done: 5,
              filed: 5,
              left: 4,
              stopped:
                'Amazon asked to sign in again. Sign in or press Update now, then backfill again to go on',
              finished_at: '2026-09-05T05:00:00Z',
            },
          },
        ],
      )
    })
    expect(html).toContain('Last invoice backfill, ')
    expect(html).toContain(
      '5 invoices filed, 4 orders still without one. It stopped early: Amazon asked to sign in again. Sign in or press Update now, then backfill again to go on.',
    )
    expect(html).not.toContain('Backfilling invoices')
  })

  it('shows an order a person ignored as ignored rather than waiting', () => {
    const html = render('amazon', (client) =>
      seedAmazon(client, {
        total: 1,
        orders: [
          {
            ...orders.orders[0],
            matched_transaction_ids: [],
            matched_transactions: [],
            ignored: true,
          },
        ],
      }),
    )
    expect(html).toContain('ignored')
    expect(html).not.toContain('none yet')
  })

  it('says what a gift card left for the card, formatted, and nothing when it paid none', () => {
    expect(render('amazon', seedAmazon)).not.toContain('to the card')
    const html = render('amazon', (client) =>
      seedAmazon(client, {
        total: 1,
        orders: [
          {
            ...orders.orders[0],
            gift_card_amount: moneyFromCents(1000),
            card_total: moneyFromCents(3000),
          },
        ],
      }),
    )
    expect(html).toMatch(/\$30\.00<\/span> to the card/)
  })

  it('tells a lapsed session a kept password signs in from a password Amazon turned away', () => {
    const html = render('amazon', (client) => {
      seedAmazon(client)
      client.setQueryData(
        [...merchantKey('amazon'), 'accounts'],
        [
          { ...accounts[1], id: 'acct-lapsed', name: 'Lapsed', has_password: true },
          {
            ...accounts[1],
            id: 'acct-refused',
            name: 'Refused',
            has_password: true,
            sign_in_paused: 'password_refused',
            last_sync_error:
              'Amazon did not accept the kept password (Your password is incorrect). The password is still kept.',
          },
          {
            ...accounts[1],
            id: 'acct-code',
            name: 'Coded',
            has_password: true,
            has_totp: false,
            sign_in_paused: 'code_needed',
            last_sync_error: 'Amazon asked for a code; sign in to answer it.',
          },
        ],
      )
    })
    // The lapsed one waits on nothing but the next update.
    expect(html).toContain('Session expired')
    expect(html).toContain('Session expired; the kept password signs in at the next update.')
    expect(html).toContain('Update now')
    // The refused one is a warning, and a sign-in.
    expect(html).toContain('Password refused')
    expect(html).toContain('Amazon did not accept the kept password.')
    expect(html).toContain('Sign in again')
    // The one Amazon asked a code of says so.
    expect(html).toContain('Code needed')
    expect(html).toContain('Amazon asked for a code.')
  })

  it('offers the page a stopped pull ended on beside its failure, and only when one was kept', () => {
    const shown = render('amazon', (client) => {
      seedAmazon(client)
      client.setQueryData(
        [...merchantKey('amazon'), 'accounts'],
        [{ ...accounts[1], has_failure_screenshot: true }],
      )
    })
    expect(shown).toContain('Amazon asked for a one-time code')
    expect(shown).toContain('>Show screenshot</button>')

    const none = render('amazon', seedAmazon)
    expect(none).toContain('Amazon asked for a one-time code')
    expect(none).not.toContain('Show screenshot')
  })

  it("links a bank row to its account's register, two weeks either side, marked", () => {
    expect(
      registerLinkFor({
        id: 'txn-9',
        account_id: 'acct-2',
        date: '2026-01-05',
      }),
    ).toBe('/transactions?displayNode=acct-2&from=2025-12-22&to=2026-01-19&highlight=txn-9')
  })

  it('says where the files come from and offers one Import button', () => {
    const html = render('amazon', (client) => {
      client.setQueryData([...merchantKey('amazon'), 'accounts'], [])
      client.setQueryData([...merchantKey('amazon'), 'agent'], {
        configured: false,
        reachable: false,
        detail: '',
        unavailable: '',
      })
      client.setQueryData([...merchantKey('amazon'), 'summary'], {
        ...summary,
        orders: 0,
        items: 0,
      })
      client.setQueryData(
        [...merchantKey('amazon'), 'orders', '?limit=25'],
        onePage({ total: 0, orders: [] }),
      )
    })
    expect(html).toContain('Request your data')
    expect(html).toContain('Order History Exporter for Amazon')
    expect(html).not.toContain('Preview')
    expect(html).toContain('> Import</button>')
    expect(html).toContain('No Amazon accounts yet')
    expect(html).toContain('No orders yet')
    expect(html).toContain('This build carries no browser engine, so signing in is off.')
    expect(html).toContain('Amazon orders can still be imported from files below.')
  })
})

const costcoAccounts: MerchantAccount[] = [
  {
    ...bare,
    merchant: 'costco',
    id: 'acct-alex',
    label: '',
    name: 'alex@example.com',
    orders: 3,
    email: 'alex@example.com',
    connected: true,
    sync_enabled: true,
    last_synced_at: '2026-09-05T04:00:00Z',
    last_sync_status: 'ok',
  },
  {
    ...bare,
    merchant: 'costco',
    id: 'acct-casey',
    label: '',
    name: 'Costco account',
    orders: 0,
  },
]

const costcoOrders: MerchantOrderList = {
  total: 1,
  orders: [
    {
      ...orders.orders[0],
      id: 'order-c1',
      merchant_account_id: 'acct-alex',
      account_label: 'Alex',
      order_number: '21100123456789012345',
      ordered_on: '2026-09-05',
      total: moneyFromCents(18_800),
      status: '',
      details_url: '',
      source: 'agentifi_json',
      kind: 'warehouse',
      location: 'Costco Springfield #0123',
      items: [
        {
          sku: '1234567',
          title: 'KS ORGANIC EGGS',
          quantity: 2,
          unit_price: moneyFromCents(800),
          total_owed: moneyFromCents(1600),
          shipped_on: null,
          condition: '',
          url: '',
          catalog: null,
        },
      ],
      matched_transaction_ids: [],
      matched_transactions: [],
      card_total: moneyFromCents(18_800),
      gift_card_amount: null,
    },
  ],
}

describe('Settings → Costco', () => {
  it('calls a record a purchase, says which warehouse wrote it, and has no gift card', () => {
    const html = render('costco', (client) => {
      client.setQueryData([...merchantKey('costco'), 'accounts'], costcoAccounts)
      client.setQueryData([...merchantKey('costco'), 'agent'], agent)
      client.setQueryData([...merchantKey('costco'), 'summary'], {
        ...summary,
        merchant_transactions: 140,
      })
      client.setQueryData([...merchantKey('costco'), 'orders', '?limit=25'], onePage(costcoOrders))
    })
    expect(html).toContain('Costco accounts')
    expect(html).toContain('3 purchases')
    expect(html).toContain('Purchases on file')
    expect(html).toContain('14 of 140 Costco rows matched, 126 without a purchase')
    expect(html).toContain('Warehouse')
    expect(html).toContain('Costco Springfield #0123')
    expect(html).toContain('KS ORGANIC EGGS')
    expect(html).toContain('Actions for purchase 21100123456789012345')
    // A receipt has no page of its own that is known, so the link opens the
    // Orders & Purchases page.
    expect(html).toContain(
      'href="https://www.costco.com/myaccount/#/app/4900eb1f-0c10-4bd9-99c3-c59e6c1ecebf/ordersandpurchases"',
    )
    expect(html).toContain('View the receipt')
    expect(html).not.toContain('>21100123456789012345<')
    // The Shop Card is a tender line on a receipt, not a balance Agentifi
    // keeps an account for.
    expect(html).not.toContain('Gift card balance')
  })

  it('offers one way in: sign in, and nothing about bridges or bookmarklets', () => {
    const html = render('costco', (client) => {
      client.setQueryData([...merchantKey('costco'), 'accounts'], costcoAccounts)
      client.setQueryData([...merchantKey('costco'), 'agent'], agent)
      client.setQueryData([...merchantKey('costco'), 'summary'], summary)
      client.setQueryData([...merchantKey('costco'), 'orders', '?limit=25'], onePage(costcoOrders))
    })
    // Neither login was named: one is called by its email, the other, never
    // signed in, by the shop's own name.
    expect(html).toContain('alex@example.com')
    expect(html).toContain('Costco account')
    expect(html).toContain('Update now')
    expect(html).toContain('</svg> Sign in</button>')
    expect(html).toContain('Add account')
    expect(html).not.toContain('bridge')
    expect(html).not.toContain('Connect…')
  })

  it('offers no sign-in without Camoufox, and says why', () => {
    const why =
      'Costco runs only in Camoufox, and this server has no CAMOUFOX_URL set, so signing in to Costco is off.'
    const html = render('costco', (client) => {
      client.setQueryData([...merchantKey('costco'), 'accounts'], costcoAccounts)
      client.setQueryData([...merchantKey('costco'), 'agent'], {
        ...agent,
        reachable: false,
        unavailable: why,
      })
      client.setQueryData([...merchantKey('costco'), 'summary'], summary)
      client.setQueryData([...merchantKey('costco'), 'orders', '?limit=25'], onePage(costcoOrders))
    })
    expect(html).toContain(why)
    expect(html).not.toContain('</svg> Sign in</button>')
    expect(html).toContain('Update now')
  })

  it('reads nothing an Amazon cache holds: the two merchants are separate keys', () => {
    const html = render('costco', (client) => {
      seedAmazon(client)
      client.setQueryData([...merchantKey('costco'), 'accounts'], [])
      client.setQueryData(
        [...merchantKey('costco'), 'orders', '?limit=25'],
        onePage({ total: 0, orders: [] }),
      )
    })
    expect(html).not.toContain('Glass Storage Set, 10pc')
    expect(html).toContain('No Costco accounts yet')
    expect(html).toContain('No purchases yet')
  })
})

/** The section the two shops share; which shop is being configured is the URL. */
function renderSection(path: string) {
  return renderScreen(
    <Routes>
      <Route path="/settings/merchants" element={<MerchantsSettings />} />
      <Route path="/settings/merchants/:merchant" element={<MerchantsSettings />} />
    </Routes>,
    { route: path },
  )
}

describe('Settings → Merchants', () => {
  it('offers every merchant in the table as a tab', () => {
    const html = renderSection('/settings/merchants/amazon')
    for (const id of ALL_MERCHANTS) {
      expect(html).toContain(MERCHANTS[id].name)
    }
  })

  it('shows the merchant the URL names, and only that one', () => {
    // Radix draws the selected tab's panel alone, so the other shop's screen
    // makes no queries and renders nothing at all.
    const amazon = renderSection('/settings/merchants/amazon')
    expect(amazon).toContain('Amazon accounts')
    expect(amazon).not.toContain('Costco accounts')

    const costco = renderSection('/settings/merchants/costco')
    expect(costco).toContain('Costco accounts')
    expect(costco).not.toContain('Amazon accounts')
  })
})
