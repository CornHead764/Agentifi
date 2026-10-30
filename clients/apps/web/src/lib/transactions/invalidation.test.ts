import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import {
  ACCOUNTS_KEY,
  ADJUSTMENTS_ROOT,
  AGGREGATE_ROOT,
  REGISTER_ROOT,
  invalidateTransactions,
} from './cache'

const SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

describe('a write that changed transactions', () => {
  it('leaves the register, its totals, the adjustments and the balances stale', () => {
    const client = new QueryClient()
    const held = [
      [...REGISTER_ROOT, 'order=desc'],
      [...AGGREGATE_ROOT, 'spending', 'category', 'order=desc'],
      [...ADJUSTMENTS_ROOT, 'a1'],
      [...ACCOUNTS_KEY, 'a1', 'summary'],
      ['rules'],
    ]
    for (const key of held) client.setQueryData(key, 'held')

    invalidateTransactions(client)

    const stale = client
      .getQueryCache()
      .getAll()
      .filter((query) => query.state.isInvalidated)
      .map((query) => query.queryKey[0] === 'rules')
    expect(stale).toEqual([false, false, false, false])
  })

  it('is invalidated through the shared set, never through the register key alone', () => {
    const offenders = Object.entries(SOURCES)
      .filter(([path]) => !path.includes('.test.'))
      .filter(([, source]) => /invalidateQueries\(\{\s*queryKey:\s*REGISTER_ROOT\s*\}\)/.test(source))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })
})
