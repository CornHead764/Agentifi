import { QueryClient, type InfiniteData } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { transaction } from '@/test/builders'

import {
  REGISTER_ROOT,
  flattenPages,
  patchRegisterCache,
  removeFromRegisterCache,
  restoreRegister,
  snapshotRegister,
} from './cache'
import { appliedSuggestion } from './suggestions'
import type { Suggestion, TransactionPage } from './types'

function suggestion(): Suggestion {
  return {
    action_id: 'p1',
    conversation_id: 'c1',
    run_id: 'r1',
    tool: 'update_transaction',
    summary: 'Costco is Groceries, as the last 11 times',
    category_id: 'groceries',
    splits: [],
    created_at: '2026-09-04T12:00:08Z',
  }
}

function seed(): { client: QueryClient; key: readonly unknown[] } {
  const page: TransactionPage = {
    items: [transaction({ id: 't1', amount: moneyFromCents(-34_567), payee: 'Harbor Coffee Usa' }), transaction({ id: 't2', amount: moneyFromCents(-2_000), payee: 'T-mobile' })],
    count: 2,
    total: moneyFromCents(-36_567),
    full_total: moneyFromCents(-36_567),
    partial_count: 0,
    padding_count: 0,
    padding_total: moneyFromCents(0),
    window: { from: null, to: null, date_field: 'posted' },
    limit: 200,
    offset: 0,
  }
  const data: InfiniteData<TransactionPage> = { pages: [page], pageParams: [0] }

  const client = new QueryClient()
  const key = [...REGISTER_ROOT, 'order=desc']
  client.setQueryData(key, data)
  return { client, key }
}

describe('optimistic register edits', () => {
  it('rewrites the row before the round trip', () => {
    const { client, key } = seed()

    patchRegisterCache(client, 't1', { payee: 'Harbor Coffee Roasters' })

    const data = client.getQueryData<InfiniteData<TransactionPage>>(key)
    expect(data?.pages[0].items[0].payee).toBe('Harbor Coffee Roasters')
  })

  it('moves the net with the amount, so the chip cannot disagree with the row', () => {
    const { client, key } = seed()

    patchRegisterCache(client, 't1', { amount: moneyFromCents(-40_000) })

    const data = client.getQueryData<InfiniteData<TransactionPage>>(key)
    expect(data?.pages[0].total).toBe(moneyFromCents(-42_000))
  })

  it('restores exactly what was there when the write fails', () => {
    const { client, key } = seed()
    const before = client.getQueryData<InfiniteData<TransactionPage>>(key)
    const snapshot = snapshotRegister(client)

    patchRegisterCache(client, 't1', {
      payee: 'Harbor Coffee Roasters',
      amount: moneyFromCents(-1),
    })
    expect(client.getQueryData<InfiniteData<TransactionPage>>(key)).not.toEqual(before)

    restoreRegister(client, snapshot)

    const after = client.getQueryData<InfiniteData<TransactionPage>>(key)
    expect(after).toEqual(before)
    expect(after?.pages[0].items[0].payee).toBe('Harbor Coffee Usa')
    expect(after?.pages[0].total).toBe(moneyFromCents(-36_567))
  })

  it('takes a partial split row out of each total by what it counted for there', () => {
    const { client, key } = seed()
    patchRegisterCache(client, 't1', { matched_amount: moneyFromCents(-4_567) })
    client.setQueryData<InfiniteData<TransactionPage>>(key, (data) =>
      data === undefined
        ? data
        : {
            ...data,
            pages: [
              {
                ...data.pages[0],
                total: moneyFromCents(-6_567),
                partial_count: 1,
              },
            ],
          },
    )

    removeFromRegisterCache(client, 't1')

    const page = client.getQueryData<InfiniteData<TransactionPage>>(key)?.pages[0]
    expect(page?.total).toBe(moneyFromCents(-2_000))
    expect(page?.full_total).toBe(moneyFromCents(-2_000))
    expect(page?.partial_count).toBe(0)
  })

  it('rolls back a removal too', () => {
    const { client, key } = seed()
    const snapshot = snapshotRegister(client)

    removeFromRegisterCache(client, 't2')
    expect(client.getQueryData<InfiniteData<TransactionPage>>(key)?.pages[0].count).toBe(1)

    restoreRegister(client, snapshot)
    expect(client.getQueryData<InfiniteData<TransactionPage>>(key)?.pages[0].count).toBe(2)
  })

  it('approves a suggestion before the round trip, and offers it again on failure', () => {
    const { client, key } = seed()
    const waiting = suggestion()
    patchRegisterCache(client, 't1', { suggestion: waiting })
    const before = client.getQueryData<InfiniteData<TransactionPage>>(key)
    const snapshot = snapshotRegister(client)

    const row = before?.pages[0].items[0]
    if (row === undefined) throw new Error('seeded row is missing')
    patchRegisterCache(client, 't1', appliedSuggestion(row))

    const applied = client.getQueryData<InfiniteData<TransactionPage>>(key)?.pages[0].items[0]
    expect(applied?.is_reviewed).toBe(true)
    expect(applied?.category_id).toBe('groceries')
    expect(applied?.suggestion).toBeNull()

    restoreRegister(client, snapshot)

    const after = client.getQueryData<InfiniteData<TransactionPage>>(key)?.pages[0].items[0]
    expect(after?.is_reviewed).toBe(false)
    expect(after?.suggestion).toEqual(waiting)
  })

  it('takes only the proposal off a discarded row', () => {
    const { client, key } = seed()
    patchRegisterCache(client, 't1', { suggestion: suggestion() })

    patchRegisterCache(client, 't1', { suggestion: null })

    const after = client.getQueryData<InfiniteData<TransactionPage>>(key)?.pages[0].items[0]
    expect(after?.suggestion).toBeNull()
    expect(after?.is_reviewed).toBe(false)
    expect(after?.category_id).toBeNull()
  })

  it('leaves an unrelated register query alone', () => {
    const { client } = seed()
    const other = [...REGISTER_ROOT, 'order=asc']
    client.setQueryData(other, undefined)

    patchRegisterCache(client, 't1', { payee: 'Changed' })

    expect(client.getQueryData(other)).toBeUndefined()
  })

  it('drops a row repeated across a page seam', () => {
    const page: TransactionPage = {
      items: [transaction({ id: 't1', amount: moneyFromCents(-100), payee: 'One' })],
      count: 2,
      total: moneyFromCents(-100),
      full_total: moneyFromCents(-100),
      partial_count: 0,
      padding_count: 0,
      padding_total: moneyFromCents(0),
      window: { from: null, to: null, date_field: 'posted' },
      limit: 1,
      offset: 0,
    }
    const rows = flattenPages({
      pages: [page, { ...page, offset: 1 }],
      pageParams: [0, 1],
    })

    expect(rows.map((row) => row.id)).toEqual(['t1'])
  })
})
