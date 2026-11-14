/**
 * Where the dashboard's review links land. The assistant count is over every
 * account, so its link names All Accounts and the register lets that outrank
 * the remembered pick.
 *
 * Its own file because the remembered pick is module state: set here, it
 * would leak into the register suite's "nobody has chosen" cases.
 */

import { describe, expect, it } from 'vitest'

import { rememberScopeNode } from '@/lib/accountScope'
import { SUMMARY_KEY } from '@/lib/clients/review'
import { dashboardKeys } from '@/lib/clients/dashboard'
import { dayWindow } from '@agentifi/core/dateRanges'
import { moneyFromCents } from '@agentifi/core/money'
import { toIsoDate } from '@agentifi/core/format'
import { ACCOUNTS_KEY, adHocFilterKey } from '@/lib/transactions/cache'
import { EMPTY_DRAFT, toFilterItems } from '@/lib/transactions/filter'
import {
  missingReceiptsLink,
  registerLinkForWindow,
  reviewQueueLink,
} from '@/lib/transactions/links'
import type { AccountKind, AccountWithBalances } from '@/lib/transactions/types'
import { TransactionsPage } from '@/pages/TransactionsPage'
import { account } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { ReviewPanel } from './ReviewPanel'

function accountOf(id: string, name: string, kind: AccountKind, type: string): AccountWithBalances {
  return account({ id, name, kind, type })
}

const LEDGER = [
  accountOf('a1', 'Everyday Checking', 'cash', 'checking'),
  accountOf('b1', 'Sample Brokerage', 'investment', 'brokerage'),
]

function hrefs(markup: string): string[] {
  return [...markup.matchAll(/href="([^"]*)"/g)].map((match) =>
    match[1].replaceAll('&amp;', '&'),
  )
}

function renderPanel(accounts: AccountWithBalances[] = LEDGER): string {
  const window = dayWindow(30, 0)
  const receiptItems = toFilterItems(
    { ...EMPTY_DRAFT, missingReceipt: true },
    { categories: [], tags: [] },
  )
  return renderScreen(<ReviewPanel />, {
    seed: [
      [
        dashboardKeys.review(null, window.from, window.to),
        { rows: [], count: 4, total: moneyFromCents(-1234) },
      ],
      [
        SUMMARY_KEY,
        {
          act_now: 1,
          money_at_stake: 2,
          tidy_up: 0,
          ideas: 0,
          new_rows: 9,
          needs_a_look: 3,
          badge: 6,
          kinds: [],
          oldest_open_day: null,
        },
      ],
      [ACCOUNTS_KEY, accounts],
      [adHocFilterKey(JSON.stringify(receiptItems)), 'f-receipts'],
      [
        dashboardKeys.review('f-receipts', null, toIsoDate(new Date())),
        { rows: [], count: 3, total: moneyFromCents(-20400) },
      ],
    ],
  })
}

const WITH_HSA = [...LEDGER, { ...accountOf('h1', 'Health Savings', 'cash', 'hsa'), requires_receipts: true }]

function renderRegisterAt(at: string): string {
  return renderScreen(<TransactionsPage />, { seed: [[ACCOUNTS_KEY, LEDGER]], route: at })
}

describe('the review queue link', () => {
  it('is the unreviewed register over every account', () => {
    expect(reviewQueueLink()).toBe('/transactions?displayNode=all&isReviewed=0')
    expect(reviewQueueLink({ uncategorized: true })).toBe(
      '/transactions?displayNode=all&isReviewed=0&uncategorized=1',
    )
    expect(reviewQueueLink({ isBill: true })).toBe(
      '/transactions?displayNode=all&isReviewed=0&isBill=1',
    )
  })

  it('carries the account scope on a donut slice too', () => {
    // The top-categories report behind the wedge covers every account.
    expect(registerLinkForWindow({ from: '2026-09-01', to: '2026-09-26' }, { categoryId: 'c1' })).toBe(
      '/transactions?displayNode=all&category=c1&from=2026-09-01&to=2026-09-26',
    )
  })

  it('opens a report’s period on the register tab that reads its dates', () => {
    expect(
      registerLinkForWindow({ from: '2026-09-01', to: '2026-09-26' }, { categoryId: '', tab: 'spending' }),
    ).toBe('/transactions?displayNode=all&tab=spending&uncategorized=1&from=2026-09-01&to=2026-09-26')
    expect(registerLinkForWindow({ from: '2026-09-01', to: '2026-09-26' }, { tab: 'income' })).toBe(
      '/transactions?displayNode=all&tab=income&from=2026-09-01&to=2026-09-26',
    )
  })
})

describe('where the dashboard review panel sends people', () => {
  it('sends the review count to Review', () => {
    const markup = renderPanel()
    const summary = /class="review-summary"[\s\S]*?<\/a>/.exec(markup)?.[0] ?? ''
    expect(summary).toContain('>6<')
    expect(summary).toContain('1 to act on now, 2 with money at stake and 3 rows that need a look.')
    expect(hrefs(markup)[0]).toBe('/review')
  })

  it('sends every tile to the queue over every account', () => {
    const links = hrefs(renderPanel()).slice(1)
    expect(links).toEqual([
      reviewQueueLink(),
      reviewQueueLink({ uncategorized: true }),
      reviewQueueLink({ isBill: true }),
    ])
  })

  it('counts the rows missing a receipt over all time when an account requires them', () => {
    const markup = renderPanel(WITH_HSA)
    expect(markup).toContain('Missing receipts')
    expect(markup).toContain('3 transactions')
    expect(hrefs(markup)).toContain(missingReceiptsLink())
    expect(missingReceiptsLink()).toBe(
      '/transactions?displayNode=all&missingReceipt=1&datePreset=all-time',
    )
  })

  it('has no receipts tile while no account requires them', () => {
    expect(renderPanel()).not.toContain('Missing receipts')
  })

  it('lands on All Accounts even when one account was picked before', () => {
    rememberScopeNode('b1')
    expect(renderRegisterAt('/transactions')).toContain('Sample Brokerage')
    const markup = renderRegisterAt(reviewQueueLink())
    expect(markup).toContain('All Accounts')
  })
})
