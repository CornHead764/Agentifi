import { afterEach, describe, expect, it, vi } from 'vitest'

import { railStartsCollapsed, storeRailCollapsed } from './railCollapse'

afterEach(() => {
  vi.unstubAllGlobals()
})

function fakeStorage(stored: string | null) {
  const writes: string[] = []
  vi.stubGlobal('window', {
    localStorage: { getItem: () => stored, setItem: (_k: string, v: string) => writes.push(v) },
  })
  return writes
}

describe('the rail’s collapsed state', () => {
  // Expanded is the default: a rail of unlabelled glyphs is a state somebody
  // should choose, not the one they arrive in.
  it('starts expanded when nothing has been stored', () => {
    fakeStorage(null)
    expect(railStartsCollapsed()).toBe(false)
  })

  it('stays collapsed once somebody has collapsed it', () => {
    fakeStorage('1')
    expect(railStartsCollapsed()).toBe(true)
  })

  it('remembers both directions', () => {
    const writes = fakeStorage(null)
    storeRailCollapsed(true)
    storeRailCollapsed(false)
    expect(writes).toEqual(['true', 'false'])
  })

  it('stays expanded rather than throwing when storage is refused', () => {
    vi.stubGlobal('window', {
      get localStorage(): never {
        throw new Error('blocked')
      },
    })
    expect(railStartsCollapsed()).toBe(false)
    expect(() => storeRailCollapsed(true)).not.toThrow()
  })
})
