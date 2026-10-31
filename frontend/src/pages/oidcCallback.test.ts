import { describe, expect, it } from 'vitest'

import { callbackTarget } from './oidcCallback'

describe('callbackTarget', () => {
  it('waits while the cookie is being confirmed', () => {
    expect(callbackTarget('anonymous', false)).toBeNull()
    expect(callbackTarget('loading', true)).toBeNull()
  })

  it('goes home once the session is confirmed', () => {
    expect(callbackTarget('authenticated', true)).toBe('/')
  })

  it('returns to login when the cookie is refused, instead of spinning forever', () => {
    expect(callbackTarget('anonymous', true)).toBe('/login')
    expect(callbackTarget('error', true)).toBe('/login')
  })
})
