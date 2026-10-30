import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Connection } from '@/lib/clients/connections'
import { IGNORED_ACCOUNTS_KEY, type IgnoredLocalAccount } from '@/lib/clients/ignoredAccounts'
import { parseMoney } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { IgnoredAccountsCard, IgnoredList } from './IgnoredAccountsCard'
import { groupIgnored, remoteIgnoredRows, type IgnoredRow } from './ignoredRows'

const OWN: IgnoredLocalAccount = {
  id: 'acct-9',
  name: 'Old Coin Wallet',
  type: 'crypto',
  kind: 'investment',
  masked_number: '4321',
  connection_id: null,
  institution_id: 'inst-x',
  institution: 'Example Exchange',
  balance: parseMoney('12.50'),
  ignored_at: '2026-09-01T12:00:00Z',
}

const CONNECTION: Connection = {
  id: 'conn-1',
  name: 'Family bank',
  status: 'active',
  status_detail: null,
  needs_setup_token: false,
  bank_warnings: [],
  ignored: [
    {
      id: 'ign-1',
      external_id: 'ACT-77',
      name: 'Duplicate Savings',
      institution: 'Example Bank',
      masked_number: '8765',
      ignored_at: '2026-09-02T12:00:00Z',
    },
    {
      id: 'ign-2',
      external_id: 'ACT-78',
      name: 'Grant Loan Grant Loan',
      institution: 'Example Bank',
      masked_number: '',
      ignored_at: '2026-09-02T12:00:00Z',
    },
    {
      id: 'ign-3',
      external_id: 'ACT-79',
      name: 'Dust Wallet (0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9)',
      institution: 'Example Exchange',
      masked_number: '',
      ignored_at: '2026-09-02T12:00:00Z',
    },
  ],
  last_sync_at: null,
  last_successful_sync_at: null,
  retry_not_before: null,
  created_at: '2026-01-01T00:00:00Z',
  sync: null,
}

function render(own: IgnoredLocalAccount[], connections: Connection[]): string {
  return renderScreen(<IgnoredAccountsCard connections={connections} />, {
    seed: [[IGNORED_ACCOUNTS_KEY, own]],
  })
}

function opened(stored: string) {
  vi.stubGlobal('window', { localStorage: { getItem: () => stored } })
}

function row(key: string, institution: string): IgnoredRow {
  return { key, name: key, institution, details: [], restore: () => Promise.resolve() }
}

describe('the ignored accounts card', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('starts folded, with how many accounts are ignored in its title', () => {
    const markup = render([OWN], [CONNECTION])
    expect(markup).toContain('Ignored accounts')
    expect(markup).toMatch(/badge--count[^>]*>4</)
    expect(markup).not.toContain('Old Coin Wallet')
  })

  it('lists the household’s and each connection’s ignored accounts by institution once opened', () => {
    opened('0')
    const markup = render([OWN], [CONNECTION])
    expect(markup).toContain('Example Bank')
    expect(markup).toContain('2 accounts')
    expect(markup).toContain('Stop ignoring all')
    expect(markup).toContain('Example Exchange')
    // An institution's accounts stay folded under its row until it is opened.
    expect(markup).not.toContain('Duplicate Savings')
  })

  it('says nothing is ignored only when neither list has anything', () => {
    opened('0')
    expect(render([], [])).toContain('No accounts are ignored')
    expect(render([], [CONNECTION])).not.toContain('No accounts are ignored')
  })
})

describe('an ignored list', () => {
  it('shows an institution’s only account as one row, with the institution beside it', () => {
    const rows = remoteIgnoredRows(
      { id: 'conn-1', ignored: CONNECTION.ignored.slice(0, 1) },
      () => Promise.resolve(),
      'Not synced from SimpleFIN',
    )
    const markup = renderScreen(<IgnoredList rows={rows} />)
    expect(markup).toContain('Duplicate Savings')
    expect(markup).toContain('Example Bank · ····8765 · Not synced from SimpleFIN')
    expect(markup).toContain('Stop ignoring')
    expect(markup).not.toContain('Stop ignoring all')
  })
})

describe('ignored rows', () => {
  it('name an account as it reads, without the feed’s id or a doubled name', () => {
    const rows = remoteIgnoredRows(CONNECTION, () => Promise.resolve(), null)
    expect(rows.map((one) => one.name)).toEqual(['Duplicate Savings', 'Grant Loan', 'Dust Wallet'])
  })

  it('group by institution, A to Z, with the ones naming none last', () => {
    const groups = groupIgnored([row('a', 'Zeta'), row('b', ''), row('c', 'Alpha'), row('d', 'Zeta')])
    expect(groups.map((group) => [group.institution, group.rows.map((one) => one.key)])).toEqual([
      ['Alpha', ['c']],
      ['Zeta', ['a', 'd']],
      ['', ['b']],
    ])
  })
})
