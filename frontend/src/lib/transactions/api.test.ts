import { describe, expect, it } from 'vitest'

import { DEFAULT_QUERY, aggregateParams, registerParams } from './api'

describe('registerParams order', () => {
  it('sends the newest rows first unless the header asked otherwise', () => {
    expect(registerParams(DEFAULT_QUERY)).toContain('order=desc')
    expect(registerParams({ ...DEFAULT_QUERY, order: 'asc' })).toContain('order=asc')
  })

  it('makes the two directions different queries, so paging cannot mix them', () => {
    // The cache key is these parameters.
    expect(registerParams({ ...DEFAULT_QUERY, order: 'asc' })).not.toBe(
      registerParams(DEFAULT_QUERY),
    )
  })
})

describe('aggregateParams', () => {
  const query = {
    ...DEFAULT_QUERY,
    accountIds: ['acct-1'],
    from: '2026-08-01',
    to: '2026-08-31',
    dateField: 'effective' as const,
    filterId: 'filter-1',
  }

  it('narrows the aggregate exactly as the list is narrowed', () => {
    // Trap 5.
    const params = new URLSearchParams(aggregateParams(query, 'spending', 'category'))
    for (const [name, value] of new URLSearchParams(registerParams(query))) {
      expect(params.getAll(name)).toContain(value)
    }
  })

  it('carries the two knobs the register query has no room for', () => {
    const params = new URLSearchParams(aggregateParams(query, 'income', 'tag'))
    expect(params.get('direction')).toBe('income')
    expect(params.get('group_by')).toBe('tag')
  })

  it('names the category a drilled chart is under, and nothing at the top', () => {
    expect(new URLSearchParams(aggregateParams(query, 'spending', 'category')).has('under')).toBe(
      false,
    )
    const params = new URLSearchParams(aggregateParams(query, 'spending', 'category', 'cat-1'))
    expect(params.get('under')).toBe('cat-1')
  })

  it('makes each tab and each grouping its own cache entry', () => {
    expect(aggregateParams(query, 'income', 'tag')).not.toBe(
      aggregateParams(query, 'spending', 'tag'),
    )
    expect(aggregateParams(query, 'spending', 'payee')).not.toBe(
      aggregateParams(query, 'spending', 'category'),
    )
  })
})
