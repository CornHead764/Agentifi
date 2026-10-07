import { MutationObserver, QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'

import { PENDING_KEY } from '@/lib/clients/automations'
import { transaction } from '@/test/builders'

import { ACCOUNTS_KEY, AGGREGATE_ROOT, REGISTER_ROOT } from './cache'
import { decidedSuggestionKeys, editTransactionOptions } from './queries'
import type { Suggestion, TransactionPage, Uuid } from './types'

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  updateTransaction: (id: Uuid, body: { category_id?: Uuid | null }) =>
    Promise.resolve(transaction({ id, category_id: body.category_id ?? null })),
}))

function invalidatedBy(id: string, keys: readonly (readonly unknown[])[]): Set<string> {
  const client = new QueryClient()
  for (const key of keys) client.setQueryData(key, 'held')
  for (const key of decidedSuggestionKeys(id)) void client.invalidateQueries({ queryKey: key })
  return new Set(
    client
      .getQueryCache()
      .getAll()
      .filter((query) => query.state.isInvalidated)
      .map((query) => JSON.stringify(query.queryKey)),
  )
}

describe('the screens a decided suggestion refetches', () => {
  it('covers the register, the figures over it and the account balances', () => {
    const stale = invalidatedBy('t1', [
      [...REGISTER_ROOT, 'order=desc'],
      [...AGGREGATE_ROOT, 'spending', 'category', 'order=desc'],
      ACCOUNTS_KEY,
    ])

    expect(stale.size).toBe(3)
  })

  it('covers the row itself, the assistant queue and the dashboard review tiles', () => {
    const stale = invalidatedBy('t1', [
      ['transaction', 't1'],
      [...PENDING_KEY],
      ['dashboard', 'review', 'all', '2026-09-01', '2026-09-30'],
    ])

    expect(stale.size).toBe(3)
  })

  it('leaves another row, read through its own key, alone', () => {
    const stale = invalidatedBy('t1', [
      ['transaction', 't1'],
      ['transaction', 't2'],
    ])

    expect(stale).toEqual(new Set([JSON.stringify(['transaction', 't1'])]))
  })

  it('leaves the screens a decision cannot have changed alone', () => {
    const stale = invalidatedBy('t1', [['rules'], ['connections'], ['security', 'passkeys']])

    expect(stale.size).toBe(0)
  })
})

const SUGGESTION: Suggestion = {
  action_id: 'p1' as Uuid,
  conversation_id: 'c1' as Uuid,
  run_id: null,
  tool: 'update_transaction',
  summary: '',
  category_id: 'groceries' as Uuid,
  splits: [],
  created_at: '2026-09-04T12:00:08Z',
}

describe('saving a row from its edit form', () => {
  async function save(patch: Record<string, unknown>) {
    const client = new QueryClient()
    const page = { items: [transaction({ suggestion: SUGGESTION })], total: 1 } as unknown as TransactionPage
    client.setQueryData([...REGISTER_ROOT, 'order=desc'], { pages: [page], pageParams: [0] })
    client.setQueryData([...PENDING_KEY], 'held')
    const observer = new MutationObserver(client, editTransactionOptions(client))
    await observer.mutate({ id: 't1' as Uuid, patch, optimistic: patch })
    const pending = client.getQueryCache().find({ queryKey: [...PENDING_KEY] })
    const register = client.getQueryData<{ pages: TransactionPage[] }>([...REGISTER_ROOT, 'order=desc'])
    return { pendingStale: pending?.state.isInvalidated, row: register?.pages[0]?.items[0] }
  }

  it('drops a waiting suggestion once a category is saved, and refetches the queue', async () => {
    const { pendingStale, row } = await save({ category_id: 'pet' })
    expect(row?.suggestion).toBeNull()
    expect(pendingStale).toBe(true)
  })

  it('leaves the queue alone when the edit names no category', async () => {
    const { pendingStale } = await save({ memo: 'Birthday' })
    expect(pendingStale).toBe(false)
  })
})
