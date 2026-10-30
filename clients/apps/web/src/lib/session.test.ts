import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, api } from './api'
import { currentSession, markSignedIn, markSignedOut, onSessionChange } from './session'

/** The session is a cookie the page never sees; asserted through `api`, where a leak or a missed sign-out shows. */

function respond(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  })
}

function initOf(fetcher: ReturnType<typeof respond>): RequestInit & { headers: Record<string, string> } {
  return fetcher.mock.calls[0]?.[1] as RequestInit & { headers: Record<string, string> }
}

afterEach(() => {
  markSignedOut()
  vi.unstubAllGlobals()
})

describe('the session', () => {
  it('rides in the cookie, with no token in a header', async () => {
    const fetcher = respond(200, { status: 'ok' })
    vi.stubGlobal('fetch', fetcher)

    markSignedIn()
    await api.get('/health')

    expect(initOf(fetcher).credentials).toBe('same-origin')
    expect(initOf(fetcher).headers.Authorization).toBeUndefined()
  })

  it('is signed out when the server refuses it', async () => {
    vi.stubGlobal('fetch', respond(401, { detail: 'Could not validate credentials' }))
    markSignedIn()

    await expect(api.get('/accounts')).rejects.toBeInstanceOf(ApiError)
    expect(currentSession()).toBeNull()
  })

  it('survives a refusal of a request sent outside it', async () => {
    // A failed sign-in is a 401 too, and must not clear the session.
    vi.stubGlobal('fetch', respond(401, { detail: 'Could not validate credentials' }))
    markSignedIn()
    const before = currentSession()

    await expect(api.form('/auth/token', { username: 'a', password: 'b' })).rejects.toBeInstanceOf(
      ApiError,
    )
    expect(currentSession()).toBe(before)
  })

  it('tells its subscribers, so the app can re-render as somebody else', () => {
    const seen: (string | null)[] = []
    const stop = onSessionChange((next) => seen.push(next))

    markSignedIn()
    markSignedIn()
    markSignedOut()
    markSignedOut()
    stop()
    markSignedIn()

    // Each sign-in is a new session, even in the same browser; signing out
    // twice is one change, which must not empty the query cache again.
    expect(seen).toHaveLength(3)
    expect(seen[0]).not.toBe(seen[1])
    expect(seen[2]).toBeNull()
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
