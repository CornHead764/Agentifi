import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { spendingPlanKeys } from '@/lib/spendingPlan/api'

import { cashFlowKeys, invalidateOccurrenceWrites, occurrenceKeys, seriesKeys } from './keys'

describe('invalidateOccurrenceWrites', () => {
  it('marks every read an occurrence write moves as stale', () => {
    const client = new QueryClient()
    const keys = [
      seriesKeys.list('bills', ''),
      occurrenceKeys.range('2026-09-01', '2026-09-30', false),
      cashFlowKeys.range('2026-09-01', '2026-12-31', 'all', '0'),
      spendingPlanKeys.month('2026-09'),
    ]
    for (const key of keys) client.setQueryData(key, 'fresh')

    invalidateOccurrenceWrites(client)

    for (const key of keys) {
      expect(client.getQueryState(key)?.isInvalidated, JSON.stringify(key)).toBe(true)
    }
  })
})
