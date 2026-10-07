/** A bulk envelope run finishes past a refusal, and its count is honest. */

import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { describeBulk, runInOrder } from './bulkEnvelope'

afterEach(() => vi.restoreAllMocks())

interface Envelope {
  id: string
  name: string
}

const ENVELOPES: Envelope[] = [
  { id: 'a', name: 'Groceries' },
  { id: 'b', name: 'Fuel' },
  { id: 'c', name: 'Dining' },
]

describe('runInOrder', () => {
  it('writes one at a time, in order', async () => {
    const seen: string[] = []
    await runInOrder(ENVELOPES, async (envelope) => {
      seen.push(`start ${envelope.id}`)
      await Promise.resolve()
      seen.push(`end ${envelope.id}`)
    })
    expect(seen).toEqual([
      'start a',
      'end a',
      'start b',
      'end b',
      'start c',
      'end c',
    ])
  })

  it('keeps going past a failure and reports both halves', async () => {
    const result = await runInOrder(ENVELOPES, (envelope) =>
      envelope.id === 'b'
        ? Promise.reject(new ApiError(409, '/plan', { detail: 'Month is closed' }))
        : Promise.resolve(),
    )
    expect(result.done.map((one) => one.id)).toEqual(['a', 'c'])
    expect(result.failed.map((one) => one.item.id)).toEqual(['b'])
  })

  it('never rejects, even when every write fails', async () => {
    const result = await runInOrder(ENVELOPES, () => Promise.reject(new Error('nope')))
    expect(result.done).toHaveLength(0)
    expect(result.failed).toHaveLength(3)
  })
})

describe('describeBulk', () => {
  const label = (envelope: Envelope) => envelope.name

  it('counts how many of how many, even when nothing failed', async () => {
    const result = await runInOrder(ENVELOPES, () => Promise.resolve())
    expect(describeBulk(result, label, 'Auto-release on')).toEqual({
      title: 'Auto-release on for 3 of 3',
      tone: 'success',
    })
  })

  it('names what failed and why, and does not claim the whole run failed', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const result = await runInOrder(ENVELOPES, (envelope) =>
      envelope.id === 'a'
        ? Promise.reject(new ApiError(409, '/plan', { detail: 'Month is closed' }))
        : Promise.resolve(),
    )
    const toast = describeBulk(result, label, 'Auto-release on')
    expect(toast.title).toBe('Auto-release on for 2 of 3')
    expect(toast.description).toContain('Groceries: Month is closed')
    expect(toast.tone).toBe('error')
  })

  it('names at most three failures and counts the rest', async () => {
    const many = Array.from({ length: 6 }, (_, index) => ({
      id: String(index),
      name: `Envelope ${index}`,
    }))
    const result = await runInOrder(many, () => Promise.reject(new Error('nope')))
    const toast = describeBulk(result, label, 'Auto-release on')
    expect(toast.title).toBe('Auto-release on for 0 of 6')
    expect(toast.description).toContain('And 3 more.')
    expect(toast.description).not.toContain('Envelope 4')
  })
})
