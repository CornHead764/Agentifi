import { describe, expect, it } from 'vitest'

import { moneyFromCents, ZERO_MONEY } from '@agentifi/core/money'
import { renderScreen } from '@/test/renderScreen'

import { AccountsDrawer } from './AccountsDrawer'
import type { AccountNode } from './accountTree'

const LONG = 'Neighbourhood Credit Union Everyday Checking With A Very Long Name'

const TREE: AccountNode[] = [
  {
    id: 'class:banking',
    name: 'Banking',
    kind: 'class',
    children: [
      {
        id: 'group:cash',
        name: 'Cash & Checking',
        kind: 'group',
        children: [
          {
            id: 'long',
            name: LONG,
            kind: 'account',
            balance: moneyFromCents(123_400),
            held: { reported: ZERO_MONEY, at: '2026-06-14T17:00:00Z' },
          },
          {
            id: 'drifting',
            name: 'Joint Checking',
            kind: 'account',
            balance: moneyFromCents(50_000),
            drift: { amount: moneyFromCents(-7_500), since: '2026-06-14T17:00:00Z', suspect: null },
          },
        ],
      },
    ],
  },
]

describe('<AccountsDrawer>', () => {
  it('carries each name whole in its title, for the row that ends it in an ellipsis', () => {
    const html = renderScreen(<AccountsDrawer tree={TREE} />)
    expect(html).toContain(`<span class="acct-row__name" title="${LONG}">${LONG}</span>`)
  })

  it('names the held balance in the row link, so a screen reader hears the warning', () => {
    const html = renderScreen(<AccountsDrawer tree={TREE} />)
    expect(html).toMatch(/role="img" aria-label="Balance held: SimpleFIN reported \$0\.00 on Jun 14, 2026/)
  })

  it('names a ledger drift in the row link', () => {
    const html = renderScreen(<AccountsDrawer tree={TREE} />)
    expect(html).toContain(
      'role="img" aria-label="Transactions don&#x27;t add up. Since Jun 14, 2026, this account&#x27;s ' +
        'transactions come to $75.00 less than the bank&#x27;s balance."',
    )
  })
})
