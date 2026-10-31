import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, api } from './api'
import { accessToken, clearAccessToken, onAccessTokenChange, setAccessToken } from './session'

/** The token is read on every request and dropped on a 401; asserted through `api`, where either mistake shows. */

function respond(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  })
}

function headersOf(fetcher: ReturnType<typeof respond>): Record<string, string> {
  const init: unknown = fetcher.mock.calls[0]?.[1]
  return (init as { headers: Record<string, string> }).headers
}

afterEach(() => {
  clearAccessToken()
  vi.unstubAllGlobals()
})

describe('the session token', () => {
  it('is attached to every request once it is set', async () => {
    const fetcher = respond(200, { status: 'ok' })
    vi.stubGlobal('fetch', fetcher)

    setAccessToken('a-signed-token')
    await api.get('/health')

    expect(headersOf(fetcher).Authorization).toBe('Bearer a-signed-token')
  })

  it('is absent, rather than empty, before anyone signs in', async () => {
    const fetcher = respond(200, {})
    vi.stubGlobal('fetch', fetcher)

    await api.get('/auth/oidc/config')

    expect(headersOf(fetcher).Authorization).toBeUndefined()
  })

  it('is dropped when the server refuses it', async () => {
    vi.stubGlobal('fetch', respond(401, { detail: 'Could not validate credentials' }))
    setAccessToken('expired')

    await expect(api.get('/accounts')).rejects.toBeInstanceOf(ApiError)
    expect(accessToken()).toBeNull()
  })

  it('survives a refusal of a request that never carried it', async () => {
    // A failed sign-in is a 401 too, and must not clear the session.
    vi.stubGlobal('fetch', respond(401, { detail: 'Could not validate credentials' }))
    setAccessToken('still-good')

    await expect(api.form('/auth/token', { username: 'a', password: 'b' })).rejects.toBeInstanceOf(
      ApiError,
    )
    expect(accessToken()).toBe('still-good')
  })

  it('tells its subscribers, so the app can re-render as somebody else', () => {
    const seen: (string | null)[] = []
    const stop = onAccessTokenChange((next) => seen.push(next))

    setAccessToken('first')
    setAccessToken('first')
    setAccessToken('second')
    clearAccessToken()
    stop()
    setAccessToken('after-unsubscribing')

    // 'first' twice is one change: a token that did not change must not empty
    // the query cache.
    expect(seen).toEqual(['first', 'second', null])
  })
})

describe('an error body', () => {
  it('yields the server sentence', () => {
    const error = new ApiError(400, '/auth/password', { detail: 'The current password is incorrect' })
    expect(error.detail).toBe('The current password is incorrect')
  })

  it('yields the first field message for a 422', () => {
    const error = new ApiError(422, '/transactions', {
      detail: [{ loc: ['body', 'amount'], msg: 'send money as a string', type: 'money_type' }],
    })
    expect(error.detail).toBe('send money as a string')
  })

  it('exposes the code a client has to route on', () => {
    const error = new ApiError(403, '/accounts', {
      detail: 'Set a new password before continuing',
      code: 'password_change_required',
    })
    expect(error.code).toBe('password_change_required')
  })
})
