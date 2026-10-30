import { describe, expect, it } from 'vitest'

import { ApiError } from '@/lib/api'
import { createQueryClient } from '@/lib/queryClient'

import { meQueryOptions, sessionStatus } from './auth'

describe('meQueryOptions', () => {
  it('keys the question on the token and asks nothing without one', () => {
    expect(meQueryOptions('t-1').queryKey).toEqual(['auth', 'me', 't-1'])
    expect(meQueryOptions('t-1').enabled).toBe(true)
    expect(meQueryOptions(null).enabled).toBe(false)
  })

  it('does not override the client retry policy', () => {
    // A `retry: false` override would drop the retries a 500 or a dropped connection gets.
    expect('retry' in meQueryOptions('t-1')).toBe(false)
  })
})

describe('the client default the me query inherits', () => {
  it('retries a 500 but not a 401', async () => {
    const client = createQueryClient()

    let flaky = 0
    await client.fetchQuery({
      queryKey: ['retry', '500'],
      queryFn: () => {
        flaky += 1
        if (flaky === 1) throw new ApiError(500, '/auth/me', null)
        return Promise.resolve({ id: 'u1' })
      },
      retryDelay: 0,
    })
    expect(flaky).toBe(2)

    let refused = 0
    await client
      .fetchQuery({
        queryKey: ['retry', '401'],
        queryFn: () => {
          refused += 1
          throw new ApiError(401, '/auth/me', null)
        },
        retryDelay: 0,
      })
      .catch(() => undefined)
    expect(refused).toBe(1)
  })
})

describe('userAfterPasswordChange', () => {
  it('keeps the session renderable across the token swap', async () => {
    const { userAfterPasswordChange } = await import('./auth')
    const before = {
      id: 'u1',
      email: 'c@example.com',
      full_name: null,
      is_active: true,
      is_superuser: false,
      is_verified: true,
      locale: 'en',
      theme: 'system',
      privacy_mode: false,
    swipe_left_action: 'menu',
    swipe_right_action: 'review',
      last_login_at: '2026-08-23T00:00:00Z',
      // Either flag left stale would bounce the user back to /set-password.
      must_change_password: true,
      has_password: false,
      has_totp: false,
      has_oidc: true,
    }
    const after = userAfterPasswordChange(before)
    expect(after.must_change_password).toBe(false)
    expect(after.has_password).toBe(true)
    expect({ ...after, must_change_password: true, has_password: false }).toEqual(before)
  })
})

describe('sessionStatus', () => {
  const user = {
    id: 'u1',
    email: 'c@example.com',
    full_name: null,
    is_active: true,
    is_superuser: false,
    is_verified: true,
    locale: 'en',
    theme: 'system',
    privacy_mode: false,
    swipe_left_action: 'menu',
    swipe_right_action: 'review',
    last_login_at: null,
    must_change_password: false,
    has_password: true,
    has_totp: false,
    has_oidc: false,
  }

  it('holds a stored token as loading only while the answer is still coming', () => {
    expect(sessionStatus('t-1', null, false)).toBe('loading')
    expect(sessionStatus('t-1', user, false)).toBe('authenticated')
    expect(sessionStatus(null, null, false)).toBe('anonymous')
  })

  it('calls a settled failure an error rather than more loading', () => {
    // A 403 or 500 is `error`, not an endless `loading`.
    expect(sessionStatus('t-1', null, true)).toBe('error')
  })

  it('keeps the login form for a token the server refused outright', () => {
    // A 401 clears the token, so it arrives as no token, not as an error.
    expect(sessionStatus(null, null, true)).toBe('anonymous')
  })
})
