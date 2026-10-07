/**
 * The Recent Transactions widget asks both its queries, the rows and the
 * "spent from … – Today" figure, for the accounts it was pointed at (trap 5).
 */

import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import type { AccountKind, AccountWithBalances } from '@/lib/transactions/types'
import { account } from '@/test/builders'
import { renderScreen, testQueryClient } from '@/test/renderScreen'

import { RecentTransactionsPanel } from './RecentTransactionsPanel'

function accountOf(id: string, kind: AccountKind, type: string): AccountWithBalances {
  return account({ id, name: id, kind, type })
}

const LEDGER = [
  accountOf('checking', 'cash', 'checking'),
  accountOf('card', 'credit_card', 'credit_card'),
  accountOf('brokerage', 'investment', 'brokerage'),
  accountOf('house', 'asset', 'real_estate'),
]

/** The account ids each of the widget's queries was keyed with. */
function scopes(client: QueryClient, kind: 'recent' | 'recent-spend'): unknown[] {
  return client
    .getQueryCache()
    .getAll()
    .map((query) => query.queryKey)
    .filter((key): key is unknown[] => Array.isArray(key) && key[1] === kind)
    .map((key) => key[key.length - 1])
}

function render(accounts?: string[]): QueryClient {
  const client = testQueryClient()
  renderScreen(<RecentTransactionsPanel accounts={accounts} />, {
    client,
    seed: [[ACCOUNTS_KEY, LEDGER]],
  })
  return client
}

describe('the accounts the Recent Transactions widget asks for', () => {
  it('scopes the rows and the spend figure to the same set', () => {
    const client = render(['checking'])
    expect(scopes(client, 'recent')).toEqual([['checking']])
    expect(scopes(client, 'recent-spend')).toEqual([['checking']])
  })

  it('falls back to the everyday accounts where nobody has chosen', () => {
    const client = render()
    expect(scopes(client, 'recent')).toEqual([['checking', 'card']])
    expect(scopes(client, 'recent-spend')).toEqual([['checking', 'card']])
  })

  it('passes a chosen investment account straight through', () => {
    expect(scopes(render(['brokerage']), 'recent')).toEqual([['brokerage']])
  })
})
