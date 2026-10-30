import { describe, expect, it } from 'vitest'

import { billEngine, engineSentence, engineSignsIn, merchantEngine } from './engine'

describe('whether the server can sign in to anything', () => {
  it('reads the bill and shop engines into one answer', () => {
    expect(billEngine(undefined)).toEqual({ kind: 'checking' })
    expect(billEngine({ configured: false, healthy: false, error: '', providers: [] })).toEqual({
      kind: 'absent',
    })
    expect(
      billEngine({ configured: true, healthy: false, error: 'connection refused', providers: [] }),
    ).toEqual({ kind: 'down', detail: 'connection refused' })
    expect(merchantEngine({ configured: true, reachable: false, detail: 'timed out', unavailable: '' })).toEqual({
      kind: 'down',
      detail: 'timed out',
    })
    expect(merchantEngine({ configured: true, reachable: true, detail: '', unavailable: '' })).toEqual({
      kind: 'ready',
    })
  })

  it('refuses signing in to a merchant this server cannot reach, and says why', () => {
    const why = 'Costco runs only in Camoufox, and this server has no CAMOUFOX_URL set.'
    const state = merchantEngine({ configured: true, reachable: false, detail: '', unavailable: why })
    expect(state).toEqual({ kind: 'unavailable', detail: why })
    expect(engineSentence(state)).toBe(why)
    expect(engineSignsIn(state)).toBe(false)
    expect(engineSignsIn({ kind: 'absent' })).toBe(false)
    expect(engineSignsIn({ kind: 'down', detail: 'timed out' })).toBe(true)
    expect(engineSignsIn({ kind: 'ready' })).toBe(true)
  })

  it('says nothing while it is still asking, or once the engine answers', () => {
    expect(engineSentence({ kind: 'checking' })).toBeNull()
    expect(engineSentence({ kind: 'ready' })).toBeNull()
  })

  it('names what would have to change', () => {
    expect(engineSentence({ kind: 'absent' })).toBe(
      'This build carries no browser engine, so signing in is off.',
    )
    expect(engineSentence({ kind: 'down', detail: '' })).toBe(
      'The browser engine is here but not answering.',
    )
    expect(engineSentence({ kind: 'down', detail: 'timed out' })).toBe(
      'The browser engine is here but not answering: timed out',
    )
  })
})
