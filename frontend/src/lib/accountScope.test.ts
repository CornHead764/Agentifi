import { describe, expect, it } from 'vitest'

import type { AccountKind, AccountWithBalances } from '@/lib/transactions/types'

import {
  ALL_ACCOUNTS_ID,
  BANKING_CLASS_ID,
  buildAccountTree,
} from '@/components/shell/accountTree'
import { account } from '@/test/builders'

import { defaultScopeNode, everydayAccounts, recentAccountIds } from './accountScope'

function accountOf(
  id: string,
  kind: AccountKind,
  type: string,
  overrides: Partial<AccountWithBalances> = {},
): AccountWithBalances {
  return account({ id, name: id, kind, type, ...overrides })
}

const LEDGER: AccountWithBalances[] = [
  accountOf('checking', 'cash', 'checking'),
  accountOf('savings', 'cash', 'savings'),
  accountOf('card', 'credit_card', 'credit_card'),
  accountOf('mortgage', 'loan', 'mortgage'),
  accountOf('brokerage', 'investment', 'brokerage'),
  accountOf('roth', 'investment', 'roth_ira'),
  accountOf('house', 'asset', 'real_estate'),
  accountOf('amazon-gc', 'cash', 'gift_card'),
  accountOf('old-card', 'credit_card', 'credit_card', { is_closed: true }),
]

describe('the accounts Recent Transactions reads by default', () => {
  it('keeps the accounts somebody actually spends from', () => {
    expect(everydayAccounts(LEDGER)).toEqual([
      'checking',
      'savings',
      'card',
      'mortgage',
    ])
  })

  it('leaves out investments and assets', () => {
    const kept = everydayAccounts(LEDGER)
    expect(kept).not.toContain('brokerage')
    expect(kept).not.toContain('roth')
    expect(kept).not.toContain('house')
  })

  it('leaves out the Amazon gift-card balances', () => {
    expect(everydayAccounts(LEDGER)).not.toContain('amazon-gc')
  })

  it('leaves out closed accounts', () => {
    expect(everydayAccounts(LEDGER)).not.toContain('old-card')
  })
})

describe('resolving the widget scope', () => {
  it('falls back to the rule when nobody has chosen', () => {
    expect(recentAccountIds(undefined, LEDGER)).toEqual(everydayAccounts(LEDGER))
    expect(recentAccountIds(null, LEDGER)).toEqual(everydayAccounts(LEDGER))
  })

  it('uses an explicit choice as it stands, investments included', () => {
    expect(recentAccountIds(['brokerage', 'checking'], LEDGER)).toEqual(['brokerage', 'checking'])
  })

  it('drops an account that no longer exists', () => {
    // An id the server no longer knows would narrow the query to nothing.
    expect(recentAccountIds(['checking', 'deleted-one'], LEDGER)).toEqual(['checking'])
    expect(recentAccountIds(['checking', 'old-card'], LEDGER)).toEqual(['checking'])
  })

  it('keeps an empty choice empty, which is not the same as no choice', () => {
    expect(recentAccountIds([], LEDGER)).toEqual([])
  })
})

describe('the drawer node the register opens on', () => {
  it('opens on Banking, where the spending is', () => {
    expect(defaultScopeNode(buildAccountTree(LEDGER))).toBe(BANKING_CLASS_ID)
  })

  it('opens on every account where there is no Banking to open on', () => {
    // The tree drops empty classes, so there may be no Banking to scope to.
    const tree = buildAccountTree([
      accountOf('brokerage', 'investment', 'brokerage'),
      accountOf('house', 'asset', 'real_estate'),
    ])
    expect(defaultScopeNode(tree)).toBe(ALL_ACCOUNTS_ID)
  })

  it('says every account while the tree is still coming', () => {
    // An empty tree has no Banking class either, so callers wait for the accounts.
    expect(defaultScopeNode([])).toBe(ALL_ACCOUNTS_ID)
  })

  it('shares its taxonomy with the widget rule', () => {
    // The widget adds loans and drops gift cards; nothing else separates the two defaults.
    const banking = new Set(
      buildAccountTree(LEDGER)
        .find((node) => node.id === BANKING_CLASS_ID)!
        .children!.flatMap((group) => (group.children ?? []).map((one) => one.id)),
    )
    const everyday = new Set(everydayAccounts(LEDGER))
    for (const id of banking) {
      if (id === 'amazon-gc') continue
      expect(everyday.has(id)).toBe(true)
    }
    expect(banking.has('mortgage')).toBe(false)
    expect(everyday.has('mortgage')).toBe(true)
  })
})
