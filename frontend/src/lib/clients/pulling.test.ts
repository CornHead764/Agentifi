import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { anyPulling, startPulling } from './pulling'

const KEY = ['merchant', 'amazon', 'accounts'] as const

describe('a pull started from a list', () => {
  it('marks only its own row as pulling, in the cache every mount of the list reads', async () => {
    const client = new QueryClient()
    client.setQueryData(KEY, [
      { id: 'a1', name: 'Household', pulling: false },
      { id: 'a2', name: 'Work', pulling: false },
    ])

    await startPulling(client, KEY, 'a2')

    expect(client.getQueryData(KEY)).toEqual([
      { id: 'a1', name: 'Household', pulling: false },
      { id: 'a2', name: 'Work', pulling: true },
    ])
    expect(anyPulling(client.getQueryData(KEY))).toBe(true)
  })

  it('leaves a list nobody has fetched alone', async () => {
    const client = new QueryClient()
    await startPulling(client, KEY, 'a1')
    expect(client.getQueryData(KEY)).toBeUndefined()
    expect(anyPulling(undefined)).toBe(false)
  })

  it('is polled for only while a row says it is pulling', () => {
    expect(anyPulling([{ id: 'a1', pulling: false }])).toBe(false)
    expect(anyPulling([{ id: 'a1', pulling: false }, { id: 'a2', pulling: true }])).toBe(true)
  })
})
