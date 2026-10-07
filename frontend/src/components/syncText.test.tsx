import { describe, expect, it } from 'vitest'

import type { Connection, SyncRun } from '@/lib/clients/connections'
import { renderScreen } from '@/test/renderScreen'

import { SyncProgress } from './SyncProgress'
import {
  syncEndedToast,
  syncProgressLine,
  syncProgressShort,
  syncShare,
  syncSummary,
} from './syncText'

function run(over: Partial<SyncRun> = {}): SyncRun {
  return {
    state: 'running',
    phase: 'importing',
    account: 3,
    accounts: 8,
    account_name: 'Everyday Checking',
    transactions_imported: 1250,
    transactions_updated: 0,
    accounts_created: 0,
    warnings: 0,
    balances_held: 0,
    message: null,
    started_at: '2026-03-20T12:00:00Z',
    finished_at: null,
    ...over,
  }
}

function connection(sync: SyncRun | null): Connection {
  return {
    id: 'c1',
    name: 'Household bridge',
    status: 'active',
    status_detail: null,
    needs_setup_token: false,
    bank_warnings: [],
    ignored: [],
    last_sync_at: null,
    last_successful_sync_at: null,
    retry_not_before: null,
    created_at: '2026-01-01T00:00:00Z',
    sync,
  }
}

describe('a running sync', () => {
  it('names the account it is reading and counts what has arrived', () => {
    expect(syncProgressLine(run())).toBe(
      'Importing account 3 of 8: Everyday Checking · 1,250 new transactions so far',
    )
    expect(syncProgressShort(run())).toBe('Importing 3 of 8')
  })

  it('says it is asking the Bridge before any account is known', () => {
    expect(syncProgressLine(run({ phase: 'fetching', account: 0, accounts: 0 }))).toBe(
      'Asking SimpleFIN for your accounts',
    )
  })

  it('says what it is doing once every account has been read', () => {
    expect(syncProgressLine(run({ phase: 'settling' }))).toBe(
      'Pairing transfers and applying rules to 1,250 new transactions',
    )
  })

  it('fills its bar by the accounts already read', () => {
    expect(syncShare(run({ account: 1, accounts: 4 }))).toBe(0)
    expect(syncShare(run({ account: 3, accounts: 4 }))).toBe(50)
    expect(syncShare(run({ phase: 'settling' }))).toBe(100)
  })

  it('renders its line and a labelled bar', () => {
    const markup = renderScreen(<SyncProgress run={run()} />)
    expect(markup).toContain('Importing account 3 of 8')
    expect(markup).toContain('aria-label="Sync progress"')
  })
})

describe('a sync that has ended', () => {
  const ended = { state: 'succeeded' as const, phase: null, finished_at: '2026-03-20T12:04:00Z' }

  it('sums up the accounts, the new rows and anything to look at', () => {
    expect(syncSummary(run({ ...ended, accounts: 80 }))).toBe('80 accounts, 1,250 new transactions')
    expect(syncSummary(run({ ...ended, accounts: 1, transactions_imported: 1, warnings: 1, balances_held: 1 }))).toBe(
      '1 account, 1 new transaction, 2 warnings',
    )
  })

  it('reports a success, pointing at the warnings when there are some', () => {
    const quiet = syncEndedToast(connection(run({ ...ended, accounts: 2 })), () => {})
    expect(quiet.title).toBe('Synced Household bridge')
    expect(quiet.tone).toBe('success')
    expect(quiet.action).toBeUndefined()

    const warned = syncEndedToast(connection(run({ ...ended, warnings: 1 })), () => {})
    expect(warned.action?.label).toBe('See why')
  })

  it('reports a failure with its reason and a way to it', () => {
    const toast = syncEndedToast(
      connection(run({ ...ended, state: 'failed', message: 'the Bridge did not answer' })),
      () => {},
    )
    expect(toast.tone).toBe('error')
    expect(toast.description).toBe('The Bridge did not answer.')
    expect(toast.action?.label).toBe('See why')
  })

  it('reports a skipped run as nothing read, not as a failure', () => {
    const toast = syncEndedToast(
      connection(run({ ...ended, state: 'skipped', message: 'SimpleFIN is rate limiting this connection.' })),
      () => {},
    )
    expect(toast.title).toBe('Household bridge was not synced')
    expect(toast.tone).toBe('neutral')
  })
})
