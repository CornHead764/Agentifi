import { afterEach, describe, expect, it, vi } from 'vitest'

import { parseMoney } from '@agentifi/core/money'

import { acceptOccurrence, unacceptOccurrence } from './api'
import type { Occurrence } from './types'

const POWER: Occurrence = {
  series_id: 'ser-power',
  account_id: 'acct-1',
  category_id: null,
  kind: 'bill',
  label: 'Power',
  due_on: '2026-10-15',
  amount: parseMoney('-80.00'),
  status: 'upcoming',
  pays_on: null,
  bill: null,
  bill_link: null,
  transaction_id: null,
  marked_paid: false,
  accepted_by_hand: false,
}

function stubFetch() {
  const fetcher = vi.fn().mockResolvedValue({
    ok: true,
    status: 204,
    headers: { get: () => null },
    text: () => Promise.resolve(''),
  })
  vi.stubGlobal('fetch', fetcher)
  return fetcher
}

function sent(fetcher: ReturnType<typeof stubFetch>): { path: unknown; body: unknown } {
  const [path, init] = fetcher.mock.calls[0] as [unknown, RequestInit]
  return { path, body: JSON.parse(String(init.body)) }
}

describe('marking a slot paid', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('names only the slot, since it writes no transaction', async () => {
    const fetcher = stubFetch()

    await acceptOccurrence(POWER)

    expect(sent(fetcher)).toEqual({
      path: '/api/occurrences/accept',
      body: { series_id: 'ser-power', due_on: '2026-10-15' },
    })
  })

  it('is taken back by the same slot', async () => {
    const fetcher = stubFetch()

    await unacceptOccurrence({ ...POWER, status: 'paid', marked_paid: true })

    expect(sent(fetcher)).toEqual({
      path: '/api/occurrences/unaccept',
      body: { series_id: 'ser-power', due_on: '2026-10-15' },
    })
  })
})
