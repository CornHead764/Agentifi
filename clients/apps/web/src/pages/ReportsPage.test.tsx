/**
 * The reports workspace, rendered against a seeded cache. Opening a tab is a
 * click, which a static render cannot make, so this asserts the resting
 * state: the gallery's eight report types, the snapshot badge, and the saved
 * rail.
 */

import { afterEach, describe, expect, it } from 'vitest'

import { CURRENT_SPACE_KEY, type Space } from '@/lib/clients/spaces'
import { EMPTY_DRAFT } from '@/lib/transactions/filter'
import { openTab, workspaceKey, writeWorkspace } from '@/lib/reports/tabs'
import { renderScreen, type RenderScreenOptions } from '@/test/renderScreen'
import { ReportsPage } from './ReportsPage'

/** `lib/storage` reads `window.localStorage`, and this suite runs under node. */
function installStorage(): Map<string, string> {
  const backing = new Map<string, string>()
  Reflect.set(globalThis, 'window', {
    localStorage: {
      getItem: (key: string) => backing.get(key) ?? null,
      setItem: (key: string, value: string) => void backing.set(key, value),
      removeItem: (key: string) => void backing.delete(key),
    },
  })
  return backing
}

afterEach(() => Reflect.deleteProperty(globalThis, 'window'))

const SPENDING_ENTRY = {
  id: 'spending',
  name: 'Spending',
  blurb: 'See where your money goes',
  series: 1,
  range: 'this-month',
  config: {
    preset: 'spending',
    mode: 'transaction',
    rows: 'category',
    columns: 'time',
    time_grain: 'month',
    sign: 'expenses',
  },
  servedBy: null,
} as const

const INCOME_ENTRY = {
  ...SPENDING_ENTRY,
  id: 'income',
  name: 'Income',
  config: { ...SPENDING_ENTRY.config, preset: 'income', sign: 'income' },
} as const

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

const SAVED = {
  id: '00000000-0000-0000-0000-0000000000r1',
  name: 'August groceries',
  config: {
    preset: 'spending',
    mode: 'transaction',
    rows: 'category',
    columns: '',
    time_grain: '',
    sign: 'expenses',
  },
  filter: { id: 'f1', scope: 'report', name: 'August groceries', items: [] },
}

function render(seed?: RenderScreenOptions['seed']): string {
  return renderScreen(<ReportsPage />, { seed })
}

const EIGHT = [
  'Spending',
  'Income',
  'Income Summary',
  'Income &amp; Expense',
  'Net Worth',
  'Taxes',
  'Savings',
  'Monthly Summary',
]

describe('the reports workspace', () => {
  it('offers all eight report types, with the snapshot badged as one', () => {
    const rendered = render()
    for (const name of EIGHT) expect(rendered).toContain(name)
    expect(rendered).not.toContain('Spending Summary')
    expect(rendered).toContain('Snapshot')
  })

  it('lists the saved reports the server has', () => {
    const rendered = render([
      [
        ['reports', 'saved'],
        [
          {
            id: '00000000-0000-0000-0000-0000000000r1',
            name: 'August groceries',
            config: {
              preset: 'spending',
              mode: 'transaction',
              rows: 'category',
              columns: '',
              time_grain: '',
              sign: 'expenses',
            },
            filter: { id: 'f1', scope: 'report', name: 'August groceries', items: [] },
          },
        ],
      ],
    ])
    expect(rendered).toContain('August groceries')
    expect(rendered).not.toContain('No saved reports yet')
  })

  it('reopens the tabs that were open before the page was left', () => {
    installStorage()
    const tab = openTab(SPENDING_ENTRY)
    writeWorkspace(workspaceKey(undefined), { tabs: [tab], active: tab.id })

    const rendered = render()
    expect(rendered).toContain('Close Spending')
    // The restored tab is the active one: its workspace is on screen, not the
    // gallery this page opens on from cold.
    expect(rendered).not.toContain('Save customized reports')
  })

    // The space's default range is where a *new* tab starts; a restored tab
    // keeps the window it was left on.
  it('leaves a restored tab on the range it was left on', () => {
    installStorage()
    const tab = openTab(INCOME_ENTRY, {
      range: { range: { from: '2026-02-01', to: '2026-02-14' }, preset: null },
      config: INCOME_ENTRY.config,
      filter: EMPTY_DRAFT,
      search: '',
    })
    writeWorkspace(workspaceKey(undefined), { tabs: [tab], active: tab.id })

    const rendered = render([[CURRENT_SPACE_KEY, { ...SPACE, default_date_range: '6M' }]])
    expect(rendered).toContain('Feb 1, 2026 – Feb 14, 2026')
    expect(rendered).not.toContain('Recent 6 months')
  })

  it('opens Spending on its period length rather than a date range', () => {
    installStorage()
    const tab = openTab(SPENDING_ENTRY)
    writeWorkspace(workspaceKey(undefined), { tabs: [tab], active: tab.id })

    const rendered = render()
    expect(rendered).toContain('View spend by')
    expect(rendered).toContain('Quarter')
    expect(rendered).not.toContain('Search payees and notes')
  })

  it('does not reopen a tab whose saved report is gone', () => {
    installStorage()
    const tab = { ...openTab(SPENDING_ENTRY), savedId: 'deleted' }
    writeWorkspace(workspaceKey(undefined), { tabs: [tab], active: tab.id })

    const rendered = render([[['reports', 'saved'], [SAVED]]])
    expect(rendered).not.toContain('Close Spending')
  })
})
