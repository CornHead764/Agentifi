import { afterEach, describe, expect, it, vi } from 'vitest'

import { parseMoney } from '@/lib/money'

import {
  freshnessOf,
  revalueAllAssets,
  revalueAsset,
  syncsEnded,
  syncsSeen,
  type Connection,
  type SyncRun,
} from './connections'

const NOW = new Date('2026-03-20T12:00:00Z')

function connection(overrides: Partial<Connection> = {}): Connection {
  return {
    id: 'c1',
    name: 'SimpleFIN',
    status: 'active',
    status_detail: null,
    needs_setup_token: false,
    bank_warnings: [],
    ignored: [],
    last_sync_at: '2026-03-20T11:30:00Z',
    last_successful_sync_at: '2026-03-20T11:30:00Z',
    retry_not_before: null,
    created_at: '2026-01-01T00:00:00Z',
    sync: null,
    ...overrides,
  }
}

describe('freshness', () => {
  it('reads the last successful sync, not the last attempt', () => {
    // A connection that has been failing for a week has a recent last_sync_at
    // and week-old balances. Reading the attempt would call that fresh.
    expect(
      freshnessOf(
        connection({
          last_sync_at: '2026-03-20T11:59:00Z',
          last_successful_sync_at: '2026-03-10T09:00:00Z',
        }),
        NOW,
      ),
    ).toBe('stale')
  })

  it('is fresh within a day of a successful sync', () => {
    expect(freshnessOf(connection(), NOW)).toBe('fresh')
  })

  it('has never synced when there is no successful run', () => {
    expect(freshnessOf(connection({ last_successful_sync_at: null }), NOW)).toBe('never')
  })

  it('tells a dead credential apart from a throttle', () => {
    // The two SimpleFIN failures. Only one of them is the user's to fix by
    // pasting a fresh token.
    expect(
      freshnessOf(
        connection({ needs_setup_token: true, status: 'credentials_expired' }),
        NOW,
      ),
    ).toBe('failing')
    expect(freshnessOf(connection({ status: 'rate_limited' }), NOW)).toBe('parked')
  })

  it('calls a bank warning behind a working connection fresh', () => {
    // One bank needing reauthorization is not this connection failing, and the
    // chip must not say it is.
    expect(
      freshnessOf(
        connection({ bank_warnings: [{ institution: 'Big Bank', message: 'x', at: '' }] }),
        NOW,
      ),
    ).toBe('fresh')
  })
})

describe('re-pricing', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function respond(body: unknown) {
    return vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => null },
      text: () => Promise.resolve(JSON.stringify(body)),
    })
  }

  const priced = {
    results: [
      {
        account_id: 'a1',
        name: 'Car 1',
        skipped: null,
        source: 'kbb',
        estimate: '17500.00',
        adjustment: '-2500.00',
        mileage_used: 41000,
        priced_as: {
          year: '2019',
          make: 'Examplemotors',
          model: 'Roadster',
          trim: null,
          mileage: 41000,
          typical_mileage: false,
          address: null,
          low: '16100.00',
          high: '18900.00',
        },
      },
    ],
  }

  it('re-prices every asset when asked now, due or not', async () => {
    const fetcher = respond(priced)
    vi.stubGlobal('fetch', fetcher)
    await revalueAllAssets()
    expect(fetcher.mock.calls[0][0]).toBe('/api/accounts/revalue?force=1')
  })

  it('reads the estimate and the adjustment as money', async () => {
    vi.stubGlobal('fetch', respond(priced))
    const { results } = await revalueAsset('a1')
    expect(results[0].estimate).toBe(parseMoney('17500.00'))
    expect(results[0].adjustment).toBe(parseMoney('-2500.00'))
  })

  it("reads the source's range as money", async () => {
    vi.stubGlobal('fetch', respond(priced))
    const { results } = await revalueAsset('a1')
    expect(results[0].priced_as?.low).toBe(parseMoney('16100.00'))
    expect(results[0].priced_as?.high).toBe(parseMoney('18900.00'))
  })
})

describe('which syncs have ended', () => {
  const started: SyncRun = {
    state: 'running',
    phase: 'importing',
    account: 1,
    accounts: 2,
    account_name: 'Everyday Checking',
    transactions_imported: 0,
    transactions_updated: 0,
    accounts_created: 0,
    warnings: 0,
    balances_held: 0,
    message: null,
    started_at: '2026-03-20T12:00:00Z',
    finished_at: null,
  }
  const running = connection({ sync: started })
  const done = connection({
    sync: { ...started, state: 'succeeded', phase: null, finished_at: '2026-03-20T12:03:00Z' },
  })

  it('reports none on the first read, so a run that ended before the page opened is not announced', () => {
    expect(syncsEnded(null, [done])).toEqual([])
  })

  it('reports a run seen running that has since ended', () => {
    expect(syncsEnded(syncsSeen([running]), [done])).toEqual([done])
  })

  it('reports a run that started and ended between two reads', () => {
    expect(syncsEnded(syncsSeen([connection()]), [done])).toEqual([done])
  })

  it('reports an ended run once', () => {
    expect(syncsEnded(syncsSeen([done]), [done])).toEqual([])
  })
})
