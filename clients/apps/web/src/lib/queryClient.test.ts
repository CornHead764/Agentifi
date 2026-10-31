import { describe, expect, it } from 'vitest'

import { ApiError } from './api'
import { createQueryClient, invalidate } from './queryClient'
import { ACCOUNTS_KEY, AGGREGATE_ROOT, REGISTER_ROOT, TRANSACTIONS_KEY } from './transactions/cache'

const ME_KEY = ['auth', 'me', 'token-1']

function refusal(code?: string): ApiError {
  return new ApiError(403, '/accounts', {
    detail: 'password change required',
    ...(code ? { code } : {}),
  })
}

describe('createQueryClient', () => {
  it('refreshes /auth/me when a query is refused pending a password change', async () => {
    const client = createQueryClient()
    client.setQueryData(ME_KEY, { id: 'u1', must_change_password: false })

    await expect(
      client.fetchQuery({
        queryKey: ['accounts'],
        queryFn: () => Promise.reject(refusal('password_change_required')),
        retry: false,
      }),
    ).rejects.toBeInstanceOf(ApiError)
    await Promise.resolve()

    expect(client.getQueryState(ME_KEY)?.isInvalidated).toBe(true)
  })

  it('refreshes /auth/me when a mutation is refused the same way', async () => {
    const client = createQueryClient()
    client.setQueryData(ME_KEY, { id: 'u1', must_change_password: false })

    const mutation = client
      .getMutationCache()
      .build(client, { mutationFn: () => Promise.reject(refusal('password_change_required')) })
    await mutation.execute(undefined).catch(() => undefined)
    await Promise.resolve()

    expect(client.getQueryState(ME_KEY)?.isInvalidated).toBe(true)
  })

  it('leaves /auth/me alone for an ordinary 403', async () => {
    const client = createQueryClient()
    client.setQueryData(ME_KEY, { id: 'u1', must_change_password: false })

    await expect(
      client.fetchQuery({
        queryKey: ['accounts'],
        queryFn: () => Promise.reject(refusal()),
        retry: false,
      }),
    ).rejects.toBeInstanceOf(ApiError)
    await Promise.resolve()

    expect(client.getQueryState(ME_KEY)?.isInvalidated).toBe(false)
  })
})

describe('invalidate', () => {
  it('expands the transactions key to the register, its totals and the balances', () => {
    const client = createQueryClient()
    const held = [[...REGISTER_ROOT, 'q'], [...AGGREGATE_ROOT, 'q'], ACCOUNTS_KEY, ['goals'], ['rules']]
    for (const key of held) client.setQueryData(key, 'held')

    invalidate(client, [['goals'], TRANSACTIONS_KEY])

    const stale = held.map((key) => client.getQueryState(key)?.isInvalidated)
    expect(stale).toEqual([true, true, true, true, false])
  })
})
