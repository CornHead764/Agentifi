import { describe, expect, it } from 'vitest'

import { chosenPasswordReady, passwordLongEnough } from './passwords'

describe('passwordLongEnough', () => {
  it('needs at least ten characters, as auth.MinPasswordLength does', () => {
    expect(passwordLongEnough('ninechars')).toBe(false)
    expect(passwordLongEnough('tenchars!!')).toBe(true)
  })

  it('counts characters, not UTF-16 units', () => {
    expect(passwordLongEnough('🔑🔑🔑🔑🔑')).toBe(false)
  })
})

describe('chosenPasswordReady', () => {
  it('refuses a typed password under ten characters', () => {
    expect(chosenPasswordReady(false, 'short')).toBe(false)
    expect(chosenPasswordReady(false, '')).toBe(false)
    expect(chosenPasswordReady(false, 'long enough now')).toBe(true)
  })

  it('needs nothing typed when the server generates one', () => {
    expect(chosenPasswordReady(true, '')).toBe(true)
  })
})
