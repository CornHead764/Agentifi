import { describe, expect, it } from 'vitest'

import { transaction } from '@/test/builders'

import {
  NO_CHECKS,
  categoryWhyRunId,
  checkReducer,
  checkingIds,
  isUndetermined,
} from './categoryCheck'
import type { Transaction } from './types'

function row(id: string, overrides: Partial<Transaction> = {}): Transaction {
  return transaction({ id, ...overrides })
}

describe('checkReducer', () => {
  it('keeps the first row pending when a second is asked for', () => {
    const one = checkReducer(NO_CHECKS, { kind: 'checking', ids: ['a'] })
    const two = checkReducer(one, { kind: 'checking', ids: ['b'] })

    expect([...two].sort()).toEqual(['a', 'b'])
  })

  it('takes a row out only on its own result', () => {
    const started = checkReducer(NO_CHECKS, { kind: 'checking', ids: ['a', 'b'] })
    const settled = checkReducer(started, { kind: 'settled', ids: ['b'] })

    expect([...settled]).toEqual(['a'])
  })

  it('changes nothing for a row it already holds', () => {
    const started = checkReducer(NO_CHECKS, { kind: 'checking', ids: ['a', 'b'] })
    const settled = checkReducer(started, { kind: 'settled', ids: ['a'] })
    const again = checkReducer(settled, { kind: 'checking', ids: ['b'] })

    expect(again).toBe(settled)
  })

  it('changes nothing when a row it does not hold settles', () => {
    expect(checkReducer(NO_CHECKS, { kind: 'settled', ids: ['a'] })).toBe(NO_CHECKS)
  })
})

describe('isUndetermined', () => {
  it('is a checked row with no category', () => {
    expect(isUndetermined(row('a', { category_checked_at: '2026-09-16T10:00:00Z' }))).toBe(true)
  })

  it('is not a row nobody has checked', () => {
    expect(isUndetermined(row('a'))).toBe(false)
  })

  it('is not a row the check filed', () => {
    const filed = row('a', { category_checked_at: '2026-09-16T10:00:00Z', category_id: 'c1' })
    expect(isUndetermined(filed)).toBe(false)
  })

  it('is not a row with a suggestion waiting on it', () => {
    const proposed = row('a', {
      category_checked_at: '2026-09-16T10:00:00Z',
      suggestion: { action_id: 's1' } as Transaction['suggestion'],
    })
    expect(isUndetermined(proposed)).toBe(false)
  })

  it('is not a split row, whose categories are on its parts', () => {
    const split = row('a', {
      category_checked_at: '2026-09-16T10:00:00Z',
      splits: [{ id: 's' }] as Transaction['splits'],
    })
    expect(isUndetermined(split)).toBe(false)
  })

  it('is not a paired transfer leg, which carries no category by design', () => {
    const leg = row('a', { category_checked_at: '2026-09-16T10:00:00Z', transfer_pair_id: 'p1' })
    expect(isUndetermined(leg)).toBe(false)
  })
})

describe('checkingIds', () => {
  it('reads the busy rows off the server’s own answer', () => {
    const rows = [row('a', { checking_category: true }), row('b')]
    expect(checkingIds(rows)).toEqual(['a'])
  })
})

describe('categoryWhyRunId', () => {
  it('offers the run a checked row points at', () => {
    expect(categoryWhyRunId(row('a', { category_check_run_id: 'run-1' }))).toBe('run-1')
  })

  it('offers nothing on a row nobody checked', () => {
    expect(categoryWhyRunId(row('a', { category_check_run_id: null }))).toBeNull()
  })

  it('offers nothing while a check is still in flight', () => {
    expect(
      categoryWhyRunId(row('a', { category_check_run_id: 'run-1', checking_category: true })),
    ).toBeNull()
  })

  it('offers the run whether the row was placed or left undetermined', () => {
    const placed = row('a', { category_id: 'groceries', category_check_run_id: 'run-1' })
    const undetermined = row('b', {
      category_checked_at: '2026-09-16T00:00:00Z',
      category_check_run_id: 'run-2',
    })
    expect(categoryWhyRunId(placed)).toBe('run-1')
    expect(categoryWhyRunId(undetermined)).toBe('run-2')
  })
})
