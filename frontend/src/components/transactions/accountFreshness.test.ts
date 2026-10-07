import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AccountWithBalances } from '@/lib/transactions/types'

import { accountFreshnessBadge } from './accountFreshness'

type Fields = Pick<AccountWithBalances, 'provider_balance_at' | 'valuation_source' | 'valued_at'>

function fields(overrides: Partial<Fields>): Fields {
  return { provider_balance_at: null, valuation_source: null, valued_at: null, ...overrides }
}

describe('accountFreshnessBadge', () => {
  beforeEach(() => {
    vi.setSystemTime(new Date('2026-08-24T00:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('reports a SimpleFIN sync stamp as synced, not priced', () => {
    const badge = accountFreshnessBadge(
      fields({
        provider_balance_at: '2026-08-21T00:00:00Z',
        // A synced account can still carry a stale valuation_source from
        // before it was linked — the sync stamp wins.
        valuation_source: 'zillow',
        valued_at: '2020-01-01T00:00:00Z',
      }),
    )
    expect(badge.tone).toBe('income')
    expect(badge.label).toBe('Updated 3 days ago')
  })

  it('reports a Zillow-priced asset as linked, with the source and its age', () => {
    const badge = accountFreshnessBadge(
      fields({ valuation_source: 'zillow', valued_at: '2026-08-21T00:00:00Z' }),
    )
    expect(badge.tone).toBe('accent')
    expect(badge.label).toBe('Priced by Zillow · 3 days ago')
  })

  it('names an unrecognized valuation source verbatim rather than hiding it', () => {
    const badge = accountFreshnessBadge(
      fields({ valuation_source: 'some-new-provider', valued_at: null }),
    )
    expect(badge.tone).toBe('accent')
    expect(badge.label).toBe('Priced by some-new-provider')
  })

  it('calls a plain hand-kept account Manual, not "Not linked"', () => {
    const badge = accountFreshnessBadge(fields({}))
    expect(badge.tone).toBe('neutral')
    expect(badge.label).toBe('Manual')
  })
})
