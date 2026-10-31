import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'
import { describeApiError } from '@/lib/errors'

afterEach(() => vi.restoreAllMocks())

function silenceConsole() {
  return vi.spyOn(console, 'error').mockImplementation(() => {})
}

describe('describeApiError', () => {
  it('says who cannot do what, not which status was returned', () => {
    silenceConsole()
    expect(describeApiError(new ApiError(403, '/goals', null))).toBe("You don't have access to this.")
    expect(describeApiError(new ApiError(401, '/goals', null))).toBe("You don't have access to this.")
    expect(describeApiError(new ApiError(404, '/reports/cashflow', null))).toBe('Not found.')
    expect(describeApiError(new ApiError(500, '/reports/cashflow', null))).toBe(
      'The server had a problem. Try again in a moment.',
    )
  })

  it('never leaks the path or the status into the sentence', () => {
    silenceConsole()
    for (const status of [400, 401, 403, 404, 409, 418, 422, 429, 500, 503]) {
      const sentence = describeApiError(new ApiError(status, '/reports/cashflow', null))
      expect(sentence).not.toContain('/reports/cashflow')
      expect(sentence).not.toContain(String(status))
    }
  })

  it('falls back differently for a read and a write', () => {
    silenceConsole()
    expect(describeApiError(new ApiError(418, '/x', null), 'load')).toBe(
      'Could not load this. Try again.',
    )
    expect(describeApiError(new ApiError(418, '/x', null), 'save')).toBe(
      'Something went wrong saving this.',
    )
  })

  it('shows the request id with a generic sentence, and not with the server sentence', () => {
    silenceConsole()
    expect(describeApiError(new ApiError(500, '/x', null, 'abc123'))).toBe(
      'The server had a problem. Try again in a moment. (reference abc123)',
    )
    expect(
      describeApiError(new ApiError(409, '/x', { detail: 'That category still has children' }, 'abc123')),
    ).toBe('That category still has children')
  })

  it('prefers the server sentence on a refusal and ignores a 5xx body', () => {
    silenceConsole()
    expect(describeApiError(new ApiError(400, '/x', { detail: 'Pick an account first' }))).toBe(
      'Pick an account first',
    )
    expect(
      describeApiError(new ApiError(422, '/x', { detail: [{ msg: 'must be positive', loc: ['body', 'amount'] }] })),
    ).toBe('Amount: must be positive')
    expect(describeApiError(new ApiError(500, '/x', { detail: 'pq: deadlock detected' }))).toBe(
      'The server had a problem. Try again in a moment.',
    )
  })

  it('names a refused field the way a person reads it, not the way a column is spelled', () => {
    silenceConsole()
    const refusal = (field: string) =>
      describeApiError(new ApiError(422, '/x', { detail: [{ msg: 'is required', loc: ['body', field] }] }))
    expect(refusal('amount_due')).toBe('Amount due: is required')
    // A trailing `id` names the column rather than the thing the person chose.
    expect(refusal('subaccount_id')).toBe('Subaccount: is required')
    expect(refusal('biller')).toBe('Biller: is required')
  })

  it('names a network failure as one', () => {
    expect(describeApiError(new TypeError('Failed to fetch'))).toBe(
      "Can't reach the server. Check your connection and try again.",
    )
  })

  it('logs the status, path and request id to the console exactly once', () => {
    const spy = silenceConsole()
    const error = new ApiError(500, '/reports/cashflow', null, 'abc123')
    describeApiError(error)
    describeApiError(error)
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy.mock.calls[0][0]).toBe('[api] 500 /reports/cashflow request abc123')
  })
})
