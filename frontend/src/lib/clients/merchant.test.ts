import { describe, expect, it, vi } from 'vitest'

import { ApiError, auditUndeclaredMoney, coerceMoney } from '@/lib/api'
import { moneyFromCents } from '@/lib/money'

import {
  ACCOUNT_SHAPE,
  MATCH_SHAPE,
  ORDER_LIST_SHAPE,
  describeMatchBasis,
  describeMerchantPullFailure,
  describeSync,
  importMerchantFile,
  merchantKey,
  merchantPullFailureScreenshot,
  paysToTheCent,
  waitForMailedMerchantCode,
  type MerchantAccount,
  type MerchantMatch,
  type MerchantOrderList,
} from './merchant'
import type { Uuid } from '@/lib/transactions/types'

const ACCOUNT = {
  connected: false,
  needs_sign_in: false,
  last_sync_status: '' as const,
  last_sync_error: '',
  has_failure_screenshot: false,
  sync_enabled: false,
  sync_days: 30,
  last_synced_at: null,
} as MerchantAccount

describe('describeMatchBasis', () => {
  it('names the shop whose charge record agreed', () => {
    expect(describeMatchBasis('amazon', 'charge')).toBe(
      "Amazon's own charge record agrees to the cent",
    )
    expect(describeMatchBasis('costco', 'charge')).toBe(
      "Costco's own charge record agrees to the cent",
    )
  })

  it('calls a record what the shop calls it', () => {
    expect(describeMatchBasis('amazon', 'order_total')).toBe('the order total agrees to the cent')
    expect(describeMatchBasis('costco', 'order_total')).toBe(
      'the purchase total agrees to the cent',
    )
  })
})

describe('a refused "Update now"', () => {
  const ID = 'c0ffee00-0000-4000-8000-000000000002'
  const PATH = `/merchants/costco/accounts/${ID}/failure-screenshot`
  const refused = (status: number, body: unknown) => new ApiError(status, `${PATH}/pull`, body)

  it('offers the page the pull stopped on when the refusal says one was kept', () => {
    const kept = refused(502, { detail: 'the order list never loaded', has_failure_screenshot: true })
    expect(merchantPullFailureScreenshot('costco', ID, kept)).toEqual({ path: PATH })
  })

  it('offers none when the refusal kept no page, or is no refusal at all', () => {
    expect(
      merchantPullFailureScreenshot('costco', ID, refused(502, { detail: 'the order list never loaded' })),
    ).toBeNull()
    expect(merchantPullFailureScreenshot('costco', ID, new TypeError('Failed to fetch'))).toBeNull()
  })

  it("says why in the connector's own words, as its card does", () => {
    expect(
      describeMerchantPullFailure('costco', refused(409, { detail: 'Costco needs a sign-in' })),
    ).toBe('Costco needs a sign-in.')
  })
})

describe('describeSync', () => {
  it('says what an account with no session can still do, in its own words', () => {
    expect(describeSync('amazon', ACCOUNT)).toBe('Not signed in: orders arrive from files only')
    // Costco takes no files: its pull reaches as far back as its site does.
    expect(describeSync('costco', ACCOUNT)).toBe('Not signed in yet')
  })

  it('says a pull is running before how the last one went', () => {
    expect(
      describeSync('costco', { ...ACCOUNT, connected: true, pulling: true, needs_sign_in: true }),
    ).toBe('Fetching purchases now…')
  })

  it('reports a session that needs signing in again', () => {
    expect(
      describeSync('costco', {
        ...ACCOUNT,
        connected: true,
        needs_sign_in: true,
        last_sync_error: 'Costco asked for a one-time code',
      }),
    ).toBe('Needs a sign-in. Costco asked for a one-time code.')
  })

  it('says a lapsed session with a kept password waits only on the next update', () => {
    const lapsed = {
      ...ACCOUNT,
      connected: true,
      needs_sign_in: true,
      has_password: true,
      sign_in_paused: '' as const,
    }
    expect(describeSync('amazon', lapsed)).toBe(
      'Session expired; the kept password signs in at the next update',
    )
    expect(
      describeSync('amazon', {
        ...lapsed,
        sign_in_paused: 'code_needed',
        last_sync_error: 'Amazon asked for a code; sign in to answer it',
      }),
    ).toBe(
      'Needs a sign-in. Amazon asked for a code. Sign in again to answer it; until then updates do not sign in on their own.',
    )
  })
})

describe('which merchant a call is for', () => {
  it('names the merchant in the path and in the cache key', async () => {
    const calls: string[] = []
    const original = globalThis.fetch
    globalThis.fetch = ((input: RequestInfo | URL) => {
      calls.push(String(input))
      return Promise.resolve(new Response('{}', { status: 200 }))
    }) as typeof fetch
    try {
      await importMerchantFile('amazon', new File(['{}'], 'orders.json'), 'acct-2' as Uuid, true)
    } finally {
      globalThis.fetch = original
    }
    expect(calls[0]).toContain('/merchants/amazon/imports')
    expect(merchantKey('costco')).toEqual(['merchant', 'costco'])
    expect(merchantKey('amazon')).not.toEqual(merchantKey('costco'))
  })
})

describe('a code answered from the mailbox', () => {
  it('asks the sign-in in progress to wait for it, with nothing in the body', async () => {
    const calls: { url: string; method: string | undefined; body: unknown }[] = []
    const original = globalThis.fetch
    globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), method: init?.method, body: init?.body })
      return Promise.resolve(
        new Response(
          JSON.stringify({
            session_id: 'sess-1',
            state: 'otp',
            prompt: '',
            image: '',
            error: '',
            mailed_code_found: false,
          }),
          { status: 200 },
        ),
      )
    }) as typeof fetch
    try {
      const result = await waitForMailedMerchantCode('costco', 'acct-2' as Uuid, 'sess-1')
      expect(result.mailed_code_found).toBe(false)
    } finally {
      globalThis.fetch = original
    }
    expect(calls[0].url).toBe('/api/merchants/costco/accounts/acct-2/sign-in/sess-1/mailed-code')
    expect(calls[0].method).toBe('POST')
    expect(calls[0].body ?? undefined).toBeUndefined()
  })
})

describe('paysToTheCent', () => {
  it('agrees whatever sign the bank row is stored with', () => {
    expect(paysToTheCent(moneyFromCents(-4000), moneyFromCents(4000))).toBe(true)
    expect(paysToTheCent(moneyFromCents(4000), moneyFromCents(4000))).toBe(true)
  })

  it('is exact to the cent', () => {
    expect(paysToTheCent(moneyFromCents(-4001), moneyFromCents(4000))).toBe(false)
    expect(paysToTheCent(moneyFromCents(-400_000), moneyFromCents(4000))).toBe(false)
  })
})

const WIRE_ORDER = {
  id: 'order-1',
  total: '40.00',
  card_total: '30.00',
  gift_card_amount: '10.00',
  tax: null,
  items: [{ sku: 'B0ONE', unit_price: '24.50', total_owed: '26.00' }],
  refunds: [{ id: 'refund-1', amount: '5.50' }],
  matched_transactions: [{ id: 'txn-1', amount: '-30.00' }],
}

describe('merchant money is coerced once, at the client', () => {
  it('turns every amount on an order list into Money', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const body = { total: 1, orders: [structuredClone(WIRE_ORDER)] }
      const list = coerceMoney<MerchantOrderList>(body, ORDER_LIST_SHAPE)
      const [order] = list.orders
      expect(order.total).toBe(moneyFromCents(4000))
      expect(order.card_total).toBe(moneyFromCents(3000))
      expect(order.gift_card_amount).toBe(moneyFromCents(1000))
      expect(order.tax).toBeNull()
      expect(order.items[0].total_owed).toBe(moneyFromCents(2600))
      expect(order.refunds[0].amount).toBe(moneyFromCents(550))
      expect(order.matched_transactions[0].amount).toBe(moneyFromCents(-3000))
      auditUndeclaredMoney(list)
      expect(warn).not.toHaveBeenCalled()
    } finally {
      warn.mockRestore()
    }
  })

  it('coerces a match, its orders and its refund', () => {
    const match = coerceMoney<MerchantMatch>(
      {
        transaction_id: 'txn-1',
        amount: '-30.00',
        order: structuredClone(WIRE_ORDER),
        orders: [{ amount: '-30.00', order: structuredClone(WIRE_ORDER) }],
        refund: { id: 'refund-1', amount: '5.50' },
      },
      MATCH_SHAPE,
    )
    expect(match.amount).toBe(moneyFromCents(-3000))
    expect(match.order.card_total).toBe(moneyFromCents(3000))
    expect(match.orders[0].amount).toBe(moneyFromCents(-3000))
    expect(match.orders[0].order.items[0].unit_price).toBe(moneyFromCents(2450))
    expect(match.refund?.amount).toBe(moneyFromCents(550))
  })

  it("coerces an account's gift card balance and leaves an unknown one null", () => {
    const [known, unknown] = coerceMoney<MerchantAccount[]>(
      [{ gift_card_balance: '40.00' }, { gift_card_balance: null }],
      ACCOUNT_SHAPE,
    )
    expect(known.gift_card_balance).toBe(moneyFromCents(4000))
    expect(unknown.gift_card_balance).toBeNull()
  })
})
