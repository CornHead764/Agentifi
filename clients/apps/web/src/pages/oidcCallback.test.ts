import { describe, expect, it } from 'vitest'

import { callbackTarget } from './oidcCallback'

describe('callbackTarget', () => {
  it('sends a fragment with no token straight to login', () => {
    expect(callbackTarget(null, 'anonymous', false)).toBe('/login')
  })

  it('waits while the adopted token is being confirmed', () => {
    expect(callbackTarget('t', 'anonymous', false)).toBeNull()
    expect(callbackTarget('t', 'loading', true)).toBeNull()
  })

  it('goes home once the session is confirmed', () => {
    expect(callbackTarget('t', 'authenticated', true)).toBe('/')
  })

  it('returns to login when the adopted token is refused, instead of spinning forever', () => {
    // /auth/me answered 401 and the client cleared the token, but the memoized
    // fragment token is still non-null.
    expect(callbackTarget('t', 'anonymous', true)).toBe('/login')
  })
})
