/**
 * Which window the register opens on: the space's "default date range",
 * asserted through the Date button's label, the only place on screen that says
 * which window is being asked for.
 */

import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { PENDING_KEY } from '@/lib/clients/automations'
import { CURRENT_SPACE_KEY, type Space } from '@/lib/clients/spaces'
import { ACCOUNTS_KEY, CATEGORIES_KEY } from '@/lib/transactions/cache'
import type { AccountWithBalances, Uuid } from '@/lib/transactions/types'
import { account, category } from '@/test/builders'
import { renderScreen, testQueryClient } from '@/test/renderScreen'
import { TransactionsPage } from './TransactionsPage'

const SPACE: Space = {
  id: 'space-1',
  name: 'Household',
  primary_currency: 'USD',
  timezone: 'UTC',
  default_date_range: '',
  sidebar_account_types: null,
  role: 'owner',
  can_write: true,
  is_owner: true,
  joined_at: '2026-01-01T00:00:00Z',
}

function renderWith(
  options: {
    range?: string
    at?: string
    accounts?: AccountWithBalances[]
    waiting?: number
  } = {},
): { markup: string; client: QueryClient } {
  const client = testQueryClient()
  if (options.range !== undefined) {
    client.setQueryData(CURRENT_SPACE_KEY, { ...SPACE, default_date_range: options.range })
  }
  if (options.accounts !== undefined) {
    client.setQueryData(ACCOUNTS_KEY, options.accounts)
  }
  if (options.waiting !== undefined) {
    client.setQueryData(
      PENDING_KEY,
      Array.from({ length: options.waiting }, (_, index) => ({ id: `p${index}` })),
    )
  }
  const markup = renderScreen(<TransactionsPage />, {
    client,
    route: options.at ?? '/transactions',
  })
  return { markup, client }
}

function render(options: Parameters<typeof renderWith>[0] = {}): string {
  return renderWith(options).markup
}

/** The aggregate queries this render asked for, as their request strings. */
function aggregateQueries(client: QueryClient): string[] {
  return client
    .getQueryCache()
    .getAll()
    .map((query) => query.queryKey)
    .filter((key): key is string[] => Array.isArray(key) && key[1] === 'aggregate')
    .map((key) => key.slice(2).join(' '))
}

describe('the window the register opens on', () => {
  it('opens on all time where the space has no preference', () => {
    expect(render()).toContain('All time')
    expect(render({ range: '' })).toContain('All time')
  })

  it('opens on the space default range', () => {
    expect(render({ range: '3M' })).toContain('Recent 3 months')
    expect(render({ range: 'YTD' })).toContain('Year to date')
  })

  // Every chip the setting offers has a window here, including the two the
  // picker does not list as rows of its own.
  it('names a window the picker has no row for rather than showing its token', () => {
    expect(render({ range: '5Y' })).toContain('Recent 5 years')
    expect(render({ range: '5Y' })).not.toContain('-5y')
  })

  // A link that names a window is somebody's explicit ask; the preference is
  // only where the page starts when nobody said.
  it('lets a link outrank the preference', () => {
    const rendered = render({ range: '3M', at: '/transactions?datePreset=this-month' })
    expect(rendered).toContain('Month to date')
    expect(rendered).not.toContain('Recent 3 months')
  })

  it('lets a link name a window of its own', () => {
    const rendered = render({ range: '3M', at: '/transactions?from=2026-01-01&to=2026-03-31' })
    expect(rendered).toContain('Jan 1, 2026 – Mar 31, 2026')
  })

  // A preference the build does not know is no preference at all, not a
  // window nobody can name.
  it('ignores a range token from an older build', () => {
    expect(render({ range: 'LAST_QUARTER' })).toContain('All time')
  })
})

describe('which accounts the register opens on', () => {
  const ledger = [
    account({ id: 'a1' as Uuid, name: 'Everyday Checking', kind: 'cash', type: 'checking' }),
    account({ id: 'b1' as Uuid, name: 'Brokerage', kind: 'investment', type: 'brokerage' }),
  ]

  it('opens on Banking rather than on every account', () => {
    // Every investment account's dividends, reinvestments and monthly
    // revaluations would bury the groceries somebody opened the page to see.
    const html = render({ accounts: ledger })
    expect(html).toContain('Banking')
    expect(html).not.toContain('All Accounts')
  })

  it('lets an explicit All Accounts stand', () => {
    // The default is what happens when nobody said. `?displayNode=all` is
    // somebody saying, and it is not the same as never having chosen.
    const html = render({ accounts: ledger, at: '/transactions?displayNode=all' })
    expect(html).toContain('All Accounts')
  })

  it('names the one account a link scoped it to', () => {
    const html = render({ accounts: ledger, at: '/transactions?displayNode=b1' })
    expect(html).toContain('Brokerage')
  })
})

describe('which tab an account’s register opens on', () => {
  const spendingFirst = account({
    id: 'a1' as Uuid,
    name: 'Everyday Checking',
    kind: 'cash',
    type: 'checking',
    default_register_tab: 'spending',
  })

  it('opens on the tab the account was set to', () => {
    const html = render({ accounts: [spendingFirst], at: '/transactions?displayNode=a1' })
    expect(html).toContain('Total expenses')
  })

  it('lets a link that names the rows outrank it', () => {
    const html = render({ accounts: [spendingFirst], at: '/transactions?displayNode=a1&tab=all' })
    expect(html).not.toContain('Total expenses')
  })

  it('opens every other account on the rows', () => {
    const html = render({ accounts: [account({ id: 'a1' as Uuid })], at: '/transactions?displayNode=a1' })
    expect(html).not.toContain('Total expenses')
  })
})

describe('where a new transaction can be filed', () => {
  const manual = account()
  const gift = account({ id: 'gc' as Uuid, name: 'Amazon Gift Card', type: 'gift_card' })
  const synced = account({
    id: 'sy' as Uuid,
    name: 'Synced Savings',
    connection_id: 'c1' as Uuid,
  })

  it('offers New while at least one account can take a hand-entered row', () => {
    expect(render({ accounts: [manual, gift, synced] })).toContain('New')
  })

  it('hides New entirely when no account can take one', () => {
    // A button that opens a form with nowhere to file the row is a button
    // whose only outcome is an error.
    const html = render({ accounts: [gift, synced] })

    expect(html).not.toMatch(/>\s*New\s*<\/button>/)
  })

  it('hides New on a register scoped to an account that cannot take a row', () => {
    const html = render({ accounts: [manual, gift], at: '/transactions?displayNode=gc' })

    expect(html).not.toMatch(/>\s*New\s*<\/button>/)
  })
})

/**
 * Selection mode opens on a long press, which static markup cannot make. What
 * is pinned is the mode-off half: the toolbar does not carry the mode's
 * controls, which live in the header bar (see SelectionBar).
 */
describe('what the register toolbar keeps once selection has its own bar', () => {
  it('offers no Done: leaving the mode is the X on the bar', () => {
    expect(render({ accounts: [account()] })).not.toMatch(/>\s*Done\s*</)
  })

  it('still offers the whole query on the unreviewed view, where no mode is on', () => {
    expect(render({ accounts: [account()], at: '/transactions?isReviewed=0' })).toContain(
      'as reviewed',
    )
  })
})

describe('the suggestions waiting on the unreviewed view', () => {
  it('counts them where they are decided', () => {
    const html = render({
      accounts: [account()],
      waiting: 3,
      at: '/transactions?isReviewed=0',
    })

    expect(html).toContain('3 suggestions waiting')
  })

  it('says nothing on the unreviewed view when nothing is waiting', () => {
    const html = render({ accounts: [account()], waiting: 0, at: '/transactions?isReviewed=0' })

    expect(html).not.toContain('waiting')
  })

  it('says nothing on the ordinary register, where it is not the errand', () => {
    const html = render({ accounts: [account()], waiting: 3 })

    expect(html).not.toContain('suggestions waiting')
  })
})

/** The Spending and Income tabs ask the server for their totals rather than summing loaded pages. */
describe('the aggregate the Spending and Income tabs ask for', () => {
  it('draws no chart at all on the register tab', () => {
    // The hook is still mounted there — hooks cannot be conditional — but it
    // is switched off, and nothing on screen reads it.
    expect(render()).not.toContain('Total expenses')
  })

  it('asks for the tab it is on, over the window on screen', () => {
    const { client } = renderWith({ at: '/transactions?tab=spending&from=2026-08-01&to=2026-08-31' })
    const asked = aggregateQueries(client)
    expect(asked).toHaveLength(1)
    expect(asked[0]).toContain('spending')
    expect(asked[0]).toContain('category')
    expect(asked[0]).toContain('from=2026-08-01')
    expect(asked[0]).toContain('to=2026-08-31')
    // Reports read the effective date; the register shows the posted one, and
    // conflating them shifts a whole month of credit-card spending.
    expect(asked[0]).toContain('date_field=effective')
  })

  it('charts the children of the category the URL drilled into, with the way back on screen', () => {
    const food = 'bbbbbbbb-0000-4000-8000-000000000001' as Uuid
    const employee = 'bbbbbbbb-0000-4000-8000-000000000002' as Uuid
    const client = testQueryClient()
    client.setQueryData(CATEGORIES_KEY, [
      category(food, 'Food & Dining'),
      category(employee, 'Lunch', food),
    ])
    const markup = renderScreen(<TransactionsPage />, {
      client,
      route: `/transactions?tab=spending&drill=category:${food}&drill=category:${employee}`,
    })
    const asked = aggregateQueries(client)
    expect(asked).toHaveLength(1)
    expect(asked[0]).toContain(`category ${employee}`)
    // The breadcrumb, and a removable chip per step on the register's toolbar.
    expect(markup).toContain('aria-label="Chart breakdown"')
    expect(markup).toContain('Remove Food &amp; Dining from the filter')
    expect(markup).toContain('Remove Lunch from the filter')
  })

  it('asks the income side on the Income tab', () => {
    const asked = aggregateQueries(renderWith({ at: '/transactions?tab=income' }).client)
    expect(asked).toHaveLength(1)
    expect(asked[0]).toContain('income')
  })
})

describe('review mode', () => {
  it('offers the toggle, off by default, with no banner', () => {
    const markup = render()
    expect(markup).toContain('Review mode')
    expect(markup).toMatch(/aria-pressed="false"[^>]*>.*?Review mode/)
    expect(markup).not.toContain('click ✓')
  })
})
