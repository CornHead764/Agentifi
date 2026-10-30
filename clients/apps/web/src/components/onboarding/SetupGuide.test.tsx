/**
 * The setup guide leads a new space through the Simplifi import before
 * SimpleFIN, checks steps off from what the server reads in the ledger, and
 * steps aside once hidden or finished.
 */

import { describe, expect, it } from 'vitest'

import {
  SETUP_GUIDE_KEY,
  withSkipped,
  type SetupGuide,
  type SetupStepId,
  type SetupStepState,
} from '@/lib/clients/setupGuide'
import { CURRENT_SPACE_KEY } from '@/lib/clients/spaces'
import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import { DashboardPage } from '@/pages/DashboardPage'
import { renderScreen } from '@/test/renderScreen'

function guide(
  states: Partial<Record<SetupStepId, SetupStepState>> = {},
  overrides: Partial<SetupGuide> = {},
): SetupGuide {
  const ids: SetupStepId[] = ['import', 'connect', 'match', 'assistant', 'bills']
  return {
    dismissed: false,
    complete: false,
    skipped: [],
    steps: ids.map((id) => ({
      id,
      state: states[id] ?? 'open',
      optional: id === 'assistant' || id === 'bills',
    })),
    ...overrides,
  }
}

const OWNER = { id: 's1', name: 'Household', is_owner: true, can_write: true, role: 'owner' }

function dashboard(seeded: SetupGuide, { accounts = [] as unknown[], space = OWNER } = {}) {
  return renderScreen(<DashboardPage />, {
    seed: [
      [ACCOUNTS_KEY, accounts],
      [CURRENT_SPACE_KEY, space],
      [SETUP_GUIDE_KEY, seeded],
    ],
  })
}

describe('the setup guide on an empty space', () => {
  it('starts with the Simplifi import, before connecting SimpleFIN', () => {
    const html = dashboard(guide())
    const importAt = html.indexOf('Coming from Quicken Simplifi? Import your history first')
    const connectAt = html.indexOf('Connect your banks with SimpleFIN')
    expect(importAt).toBeGreaterThan(-1)
    expect(connectAt).toBeGreaterThan(importAt)
    expect(html).toContain('Import from Simplifi')
    expect(html).toContain('I’m not coming from Simplifi')
    expect(html).toContain('href="/settings/accounts#connections"')
    expect(html).toContain('Other ways in')
  })

  it('checks off what the server says is done', () => {
    const html = dashboard(guide({ import: 'done', connect: 'done' }))
    expect(html).toContain('2 of 5 done')
    expect(html).toContain('data-state="done"')
    expect(html).toContain('Match accounts')
  })

  it('tells a member who is not the owner who runs the import', () => {
    const html = dashboard(guide(), {
      space: { ...OWNER, is_owner: false, role: 'member' },
    })
    expect(html).not.toContain('Import from Simplifi')
    expect(html).toContain('owner or an admin can run the import')
  })

  it('gives way to the plain welcome once hidden', () => {
    const html = dashboard(guide({}, { dismissed: true }))
    expect(html).not.toContain('Set up this space')
    expect(html).toContain('Welcome to Agentifi')
  })

  it('is not shown to a viewer, who could act on none of it', () => {
    const html = dashboard(guide(), {
      space: { ...OWNER, is_owner: false, can_write: false, role: 'viewer' },
    })
    expect(html).not.toContain('Set up this space')
  })
})

describe('the setup guide once accounts exist', () => {
  const account = { id: 'a1', name: 'Checking', type: 'checking', is_deleted: false }

  it('sits above the dashboard until it is finished', () => {
    const html = dashboard(guide({ import: 'done', connect: 'done' }), { accounts: [account] })
    expect(html).toContain('Set up this space')
    expect(html).toContain('class="dash"')
  })

  it('is gone once every step is settled', () => {
    const html = dashboard(guide({}, { complete: true }), { accounts: [account] })
    expect(html).not.toContain('Set up this space')
  })
})

describe('skipping a step', () => {
  it('adds to and takes from the list somebody chose, nothing else', () => {
    const seeded = guide({ import: 'skipped', bills: 'skipped' }, { skipped: ['bills'] })
    expect(withSkipped(seeded, 'connect', true)).toEqual(['bills', 'connect'])
    expect(withSkipped(seeded, 'bills', false)).toEqual([])
  })
})
