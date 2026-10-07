/**
 * A window with no balance history offers the import on the spot, for the
 * account it is about, instead of pointing at the accounts screen.
 */

import { describe, expect, it } from 'vitest'

import { windowFor } from '@/lib/dateRanges'
import { netWorthKeys, type NetWorth, type NetWorthPoint } from '@/lib/clients/networth'
import { ZERO_MONEY } from '@/lib/money'
import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { NetWorthPage } from './NetWorthPage'

const POINT: NetWorthPoint = {
  on: '2026-01-01',
  assets: ZERO_MONEY,
  debt: ZERO_MONEY,
  net: ZERO_MONEY,
  by_kind: [],
  equity: ZERO_MONEY,
}

const EMPTY: NetWorth = {
  window: { from: null, to: null, date_field: 'date' },
  granularity: 'month',
  points: [],
  start: POINT,
  end: POINT,
  change: ZERO_MONEY,
  change_pct: null,
  debt_to_asset: null,
  groups: [],
  included_accounts: 1,
  total_accounts: 1,
  unconverted_currencies: [],
}

function render(accounts: Partial<AccountWithBalances>[]): string {
  const bounds = windowFor('6M')
  return renderScreen(<NetWorthPage />, {
    seed: [
      [netWorthKeys.window(bounds.from, bounds.to), EMPTY],
      [ACCOUNTS_KEY, accounts],
    ],
  })
}

describe('NetWorthPage with no history in the window', () => {
  it('offers to import balance history here', () => {
    const html = render([{ id: 'a1', name: 'Everyday Checking', is_closed: false }])
    expect(html).toContain('No balance history in this range')
    expect(html).toContain('Import balance history')
    expect(html).not.toContain('Settings → Accounts')
  })

  it('offers no import when there is no account to import into', () => {
    expect(render([])).not.toContain('Import balance history')
  })
})

describe('the Net worth toolbar', () => {
  it('leaves the chart detail to the server and adding accounts to the Accounts screens', () => {
    const html = render([{ id: 'a1', name: 'Everyday Checking', is_closed: false }])
    expect(html).toContain('1 / 1 accounts included')
    expect(html).not.toContain('Chart detail')
    expect(html).not.toContain('New account')
  })
})
