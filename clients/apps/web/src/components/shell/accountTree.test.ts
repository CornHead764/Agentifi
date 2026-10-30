import { describe, expect, it } from 'vitest'

import { ZERO_MONEY, formatMoney, moneyFromCents } from '@/lib/money'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { account } from '@/test/builders'

import {
  accountIdsFor,
  buildAccountTree,
  classIdFor,
  collectIds,
  groupIdFor,
  heldBalanceText,
  nodeTotal,
  shownChildren,
  smallBalancesLabel,
  treeTotal,
  treeTotals,
  withoutSmallBalances,
  type AccountNode,
} from './accountTree'

const cents = moneyFromCents

/** An account drawer, trimmed to the shapes that matter. */

const CASH: AccountNode = {
  id: 'cash',
  name: 'Cash & Checking',
  kind: 'group',
  children: [
    { id: 'apple-cash', name: 'Apple Cash', kind: 'account', balance: cents(2_500) },
    { id: 'joint', name: 'Everyday Checking', kind: 'account', balance: cents(512_345) },
    {
      id: 'hsa',
      name: 'Health Savings',
      kind: 'account',
      balance: cents(640_000),
      connectionError: true,
    },
  ],
}

/** A savings account whose goals reserve money inside it. */
const RAINY_DAY_SAVINGS: AccountNode = {
  id: 'rainy-day-savings',
  name: 'Rainy Day Savings',
  kind: 'account',
  balance: cents(1_800_000),
  children: [
    { id: 'goals', name: 'Savings Goals', kind: 'reserved', balance: cents(1_000_000) },
    { id: 'available', name: 'Available Balance', kind: 'available', balance: cents(800_000) },
  ],
}

const SAVINGS: AccountNode = {
  id: 'savings',
  name: 'Savings',
  kind: 'group',
  children: [RAINY_DAY_SAVINGS],
}

const BANKING: AccountNode = {
  id: 'banking',
  name: 'Banking',
  kind: 'class',
  children: [CASH, SAVINGS],
}

const LIABILITIES: AccountNode = {
  id: 'liabilities',
  name: 'Liabilities',
  kind: 'class',
  children: [
    {
      id: 'home-loan',
      name: 'Home Loan',
      kind: 'group',
      children: [
        { id: 'mortgage', name: 'Mortgage', kind: 'account', balance: cents(-25_000_000) },
      ],
    },
  ],
}

const TREE = [BANKING, LIABILITIES]

describe('nodeTotal', () => {
  it('sums the leaves under a group', () => {
    expect(nodeTotal(CASH)).toBe(cents(2_500 + 512_345 + 640_000))
  })

  it('rolls groups up into their class', () => {
    expect(nodeTotal(BANKING)).toBe(cents(2_500 + 512_345 + 640_000 + 1_800_000))
  })

  it('does not double count money reserved by savings goals', () => {
    // The two children partition the balance; they do not add to it.
    expect(nodeTotal(RAINY_DAY_SAVINGS)).toBe(cents(1_800_000))
    expect(nodeTotal(SAVINGS)).toBe(cents(1_800_000))
  })

  it('keeps debts negative through the roll-up', () => {
    expect(nodeTotal(LIABILITIES)).toBe(cents(-25_000_000))
  })

  it('treats an empty group as zero rather than as missing', () => {
    expect(nodeTotal({ id: 'empty', name: 'Vehicle', kind: 'group', children: [] })).toBe(cents(0))
  })
})

describe('treeTotal', () => {
  it('nets assets against liabilities across the whole tree', () => {
    expect(treeTotal(TREE)).toBe(cents(2_500 + 512_345 + 640_000 + 1_800_000 - 25_000_000))
  })

  it('is zero for no accounts', () => {
    expect(treeTotal([])).toBe(cents(0))
  })
})

describe('collectIds', () => {
  it('walks every level, reserved children included', () => {
    const ids = collectIds(TREE)

    expect(ids).toContain('available')
    expect(ids).toHaveLength(12)
  })
})

/** One row of `GET /accounts`, with only the fields the tree reads set. */
function balanced(
  id: string,
  amount: number,
  over: Partial<AccountWithBalances> = {},
): AccountWithBalances {
  return account({
    id,
    name: id,
    ...over,
    balances: {
      balance: cents(amount),
      balance_with_pending: cents(amount),
      available_balance: cents(amount),
      credit_used_pct: null,
    },
  })
}

describe('a financed asset', () => {
  const HOUSE = '55555555-5555-4555-8555-555555555555'
  const MORTGAGE = '66666666-6666-4666-8666-666666666666'
  const accounts = [
    balanced(HOUSE, 42_000_000, {
      name: 'Maple Street',
      kind: 'asset',
      type: 'real_estate',
      equity: {
        value: cents(42_000_000),
        owed: cents(28_400_000),
        equity: cents(13_600_000),
        loan_to_value: '0.6762',
        loan_ids: [MORTGAGE],
      },
    }),
    balanced(MORTGAGE, -28_400_000, {
      name: 'Maple Street Mortgage',
      kind: 'loan',
      type: 'mortgage',
      secured_by_account_id: HOUSE,
    }),
  ]
  const tree = buildAccountTree(accounts)
  const house = tree
    .flatMap((klass) => klass.children ?? [])
    .flatMap((group) => group.children ?? [])
    .find((node) => node.id === HOUSE)

  it('hangs its equity under it as a footnote', () => {
    expect(house?.children?.map((node) => node.name)).toEqual(['Equity'])
    expect(house?.children?.[0].balance).toEqual(cents(13_600_000))
  })

  it('does not let the equity reach the total above it', () => {
    // An account's own balance wins over its children, or equity would take
    // the mortgage off net worth twice.
    expect(nodeTotal(house as AccountNode)).toEqual(cents(42_000_000))
    expect(treeTotal(tree)).toEqual(cents(13_600_000))
  })
})

describe('groupIdFor', () => {
  it('splits cash between Cash & Checking and Savings on the picker label', () => {
    expect(groupIdFor({ kind: 'cash', type: 'checking' })).toBe('group:cash')
    expect(groupIdFor({ kind: 'cash', type: 'hsa' })).toBe('group:cash')
    expect(groupIdFor({ kind: 'cash', type: 'money_market' })).toBe('group:savings')
  })

  it('splits investments between Retirement and Other Investments', () => {
    expect(groupIdFor({ kind: 'investment', type: 'roth_ira' })).toBe('group:retirement')
    expect(groupIdFor({ kind: 'investment', type: 'brokerage' })).toBe('group:other-investments')
    expect(groupIdFor({ kind: 'investment', type: '529_plan' })).toBe('group:other-investments')
  })

  it('gives crypto and life insurance cash value headings of their own under Investments', () => {
    expect(groupIdFor({ kind: 'investment', type: 'crypto' })).toBe('group:crypto')
    expect(groupIdFor({ kind: 'investment', type: 'life_insurance' })).toBe('group:life-insurance')
    expect(classIdFor({ kind: 'investment', type: 'crypto' })).toBe('class:investments')
    expect(classIdFor({ kind: 'investment', type: 'life_insurance' })).toBe('class:investments')
  })

  it('files every secured home loan under Home Loan', () => {
    expect(groupIdFor({ kind: 'loan', type: 'mortgage' })).toBe('group:home-loan')
    expect(groupIdFor({ kind: 'loan', type: 'home_equity_loan' })).toBe('group:home-loan')
    expect(groupIdFor({ kind: 'loan', type: 'vehicle_loan' })).toBe('group:vehicle-loan')
    expect(groupIdFor({ kind: 'loan', type: 'student_loan' })).toBe('group:personal-loan')
  })

  it('keeps a type it has never been taught in the drawer', () => {
    // Dropping it would drop it from the total at the top of the drawer too.
    expect(groupIdFor({ kind: 'asset', type: 'timeshare' })).toBe('group:other-assets')
  })
})

describe('buildAccountTree', () => {
  const ACCOUNTS = [
    balanced('11111111-1111-4111-8111-111111111111', 512_345, {
      name: 'Everyday Checking',
      sort_order: 1,
    }),
    balanced('22222222-2222-4222-8222-222222222222', 1_800_000, {
      name: 'Rainy Day Savings',
      type: 'savings',
      sort_order: 2,
      goal_balance: cents(1_000_000),
    }),
    balanced('33333333-3333-4333-8333-333333333333', -25_000_000, {
      name: 'Mortgage',
      kind: 'loan',
      type: 'mortgage',
      sort_order: 3,
    }),
    balanced('44444444-4444-4444-8444-444444444444', 12_500, {
      name: 'Old Card',
      kind: 'credit_card',
      type: 'credit_card',
      sort_order: 4,
      is_closed: true,
    }),
  ]

  const built = buildAccountTree(ACCOUNTS)

  it('files each account under its group and its class', () => {
    expect(built.map((node) => node.name)).toEqual(['Banking', 'Liabilities'])
    expect(built[0].children?.map((node) => node.name)).toEqual(['Cash & Checking', 'Savings'])
  })

  it('leaves a group nobody has an account in out of the tree', () => {
    expect(collectIds(built)).not.toContain('group:credit')
  })

  it('drops closed accounts, which the register header also leaves out', () => {
    expect(collectIds(built)).not.toContain('44444444-4444-4444-8444-444444444444')
  })

  it('splits a savings account into its reserve and what is left', () => {
    const savings = built[0].children?.[1].children?.[0]

    expect(savings?.children?.map((child) => child.name)).toEqual([
      'Savings Goals',
      'Available Balance',
    ])
    expect(savings?.children?.[1].balance).toBe(cents(800_000))
    // The children partition the balance; the account still totals its own.
    expect(nodeTotal(savings as AccountNode)).toBe(cents(1_800_000))
  })

  it('nets the whole tree the way the drawer prints it', () => {
    expect(treeTotal(built)).toBe(cents(512_345 + 1_800_000 - 25_000_000))
  })
})

describe('accountIdsFor', () => {
  const built = buildAccountTree([
    balanced('11111111-1111-4111-8111-111111111111', 100, { type: 'checking' }),
    balanced('22222222-2222-4222-8222-222222222222', 200, { type: 'savings' }),
  ])

  it('reads the root row and a missing parameter as every account', () => {
    expect(accountIdsFor(built, null)).toBeNull()
    expect(accountIdsFor(built, 'all')).toBeNull()
  })

  it('scopes a group to its members rather than sending the group id', () => {
    expect(accountIdsFor(built, 'group:savings')).toEqual([
      '22222222-2222-4222-8222-222222222222',
    ])
  })

  it('scopes a class to every account under it', () => {
    expect(accountIdsFor(built, 'class:banking')).toEqual([
      '11111111-1111-4111-8111-111111111111',
      '22222222-2222-4222-8222-222222222222',
    ])
  })

  it('resolves an account id without the tree, so a deep link does not wait', () => {
    expect(accountIdsFor([], '11111111-1111-4111-8111-111111111111')).toEqual([
      '11111111-1111-4111-8111-111111111111',
    ])
  })

  it('selects nothing for a node the tree does not have', () => {
    // Not every account: an unknown node is a question with no answer, and
    // answering it with the whole ledger is answering a different one.
    expect(accountIdsFor(built, 'group:vehicle')).toEqual([])
  })
})

describe('a held balance', () => {
  const HELD = '77777777-7777-4777-8777-777777777777'

  it('flags the node when the sync withheld a suspicious zero', () => {
    const tree = buildAccountTree([
      balanced(HELD, 4_200_000, {
        withheld_balance: ZERO_MONEY,
        withheld_balance_at: '2026-09-09T17:00:00Z',
      }),
    ])
    const node = tree
      .flatMap((klass) => klass.children ?? [])
      .flatMap((group) => group.children ?? [])
      .find((account) => account.id === HELD)
    expect(node?.held).toEqual({ reported: ZERO_MONEY, at: '2026-09-09T17:00:00Z' })
  })

  it('says what the feed reported and when', () => {
    const text = heldBalanceText(
      { reported: ZERO_MONEY, at: '2026-09-09T17:00:00Z' },
      (value) => formatMoney(value),
    )
    expect(text).toBe(
      "Balance held: SimpleFIN reported $0.00 on Sep 9, 2026, which the account's history doesn't explain. " +
        'The last good balance is shown until you confirm or keep it in Settings › Accounts.',
    )
  })

  it('leaves the node unflagged when nothing is held', () => {
    const tree = buildAccountTree([balanced(HELD, 4_200_000)])
    const node = tree
      .flatMap((klass) => klass.children ?? [])
      .flatMap((group) => group.children ?? [])
      .find((account) => account.id === HELD)
    expect(node?.held).toBeUndefined()
  })
})

describe('treeTotals', () => {
  it('rolls each currency up on its own rather than adding euros to dollars', () => {
    const tree: AccountNode[] = [
      { id: 'a', name: 'Checking', kind: 'account', balance: cents(10_000), currency: 'USD' },
      { id: 'b', name: 'Girokonto', kind: 'account', balance: cents(20_000), currency: 'EUR' },
      { id: 'c', name: 'Savings', kind: 'account', balance: cents(5_000), currency: 'USD' },
    ]
    expect(treeTotals(tree)).toEqual([
      { currency: 'USD', amount: cents(15_000) },
      { currency: 'EUR', amount: cents(20_000) },
    ])
    expect(() => treeTotal(tree)).toThrow()
  })
})

describe('small balances the server hid', () => {
  const COIN_A = '77777777-7777-4777-8777-777777777771'
  const COIN_B = '77777777-7777-4777-8777-777777777772'
  const COIN_C = '77777777-7777-4777-8777-777777777773'
  const coin = (id: string, amount: number, hidden: boolean) =>
    balanced(id, amount, { kind: 'investment', type: 'crypto', hidden_small_balance: hidden })
  const tree = buildAccountTree([
    coin(COIN_A, 0, true),
    coin(COIN_B, 40, true),
    coin(COIN_C, 2_500, false),
  ])
  const group = tree[0].children![0]

  it('stays in the tree, so the group total and its scope still count it', () => {
    expect(group.children).toHaveLength(3)
    expect(nodeTotal(group)).toBe(cents(2_540))
    expect(accountIdsFor(tree, group.id)).toEqual([COIN_A, COIN_B, COIN_C])
  })

  it('is left out of the rows drawn, and counted, until revealed', () => {
    expect(shownChildren(group, false)).toEqual({ shown: [group.children![2]], hidden: 2 })
    expect(shownChildren(group, true)).toEqual({ shown: group.children, hidden: 2 })
  })

  it('is named in the singular and the plural, hidden or revealed', () => {
    expect(smallBalancesLabel(1, false)).toBe('1 small balance hidden')
    expect(smallBalancesLabel(2, false)).toBe('2 small balances hidden')
    expect(smallBalancesLabel(2, true)).toBe('Hide 2 small balances')
  })
})

describe('withoutSmallBalances', () => {
  const rows = [
    { id: 'a', small: true },
    { id: 'b', small: false },
    { id: 'c', small: true },
  ]
  const isSmall = (row: { small: boolean }) => row.small

  it('leaves the small rows out and counts them', () => {
    expect(withoutSmallBalances(rows, isSmall, false)).toEqual({ shown: [rows[1]], hidden: 2 })
  })

  it('draws every row once revealed, still counting what it would hide', () => {
    expect(withoutSmallBalances(rows, isSmall, true)).toEqual({ shown: rows, hidden: 2 })
  })

  it('hides nothing from a list with no small balances', () => {
    expect(withoutSmallBalances([rows[1]], isSmall, false)).toEqual({ shown: [rows[1]], hidden: 0 })
  })
})
