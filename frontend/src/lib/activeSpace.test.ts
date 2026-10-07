import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  activeSpaceId,
  activeSpaceStorageKey,
  adoptUser,
  forgetActiveSpace,
  onActiveSpaceChange,
  resetActiveSpace,
  setActiveSpace,
} from './activeSpace'

/** The choice of space is stored per user id, since a browser profile can be shared. */
function fakeWindow() {
  const entries = new Map<string, string>()
  vi.stubGlobal('window', {
    localStorage: {
      getItem: (key: string) => entries.get(key) ?? null,
      setItem: (key: string, value: string) => {
        entries.set(key, value)
      },
      removeItem: (key: string) => {
        entries.delete(key)
      },
    },
  })
  return entries
}

afterEach(() => {
  resetActiveSpace()
  vi.unstubAllGlobals()
})

describe('the active space', () => {
  it('is nobody’s until an account is signed in', () => {
    fakeWindow()
    expect(activeSpaceId()).toBeNull()
    expect(adoptUser('u-ada')).toBeNull()
  })

  it('is remembered for the account that chose it, and not for another', () => {
    const entries = fakeWindow()

    adoptUser('u-ada')
    setActiveSpace('s-cabin')
    expect(activeSpaceId()).toBe('s-cabin')
    expect(entries.get('agentifi.' + activeSpaceStorageKey('u-ada'))).toBe('s-cabin')

    // Signing in as somebody else must not inherit it.
    expect(adoptUser('u-bob')).toBeNull()
    expect(activeSpaceId()).toBeNull()

    expect(adoptUser('u-ada')).toBe('s-cabin')
  })

  it('is forgotten rather than stored empty when it is given up', () => {
    const entries = fakeWindow()

    adoptUser('u-ada')
    setActiveSpace('s-cabin')
    forgetActiveSpace()

    expect(activeSpaceId()).toBeNull()
    expect(entries.has('agentifi.' + activeSpaceStorageKey('u-ada'))).toBe(false)
  })

  it('tells its subscribers, which is what empties the cache of the old space', () => {
    fakeWindow()
    let switches = 0
    const unsubscribe = onActiveSpaceChange(() => {
      switches += 1
    })

    adoptUser('u-ada')
    setActiveSpace('s-cabin')
    // The same space twice is not a switch.
    setActiveSpace('s-cabin')
    forgetActiveSpace()
    unsubscribe()
    setActiveSpace('s-cabin')

    expect(switches).toBe(2)
  })

  it('signs out to the server’s default rather than to the last space read', () => {
    fakeWindow()
    adoptUser('u-ada')
    setActiveSpace('s-cabin')

    expect(adoptUser(null)).toBeNull()
    expect(activeSpaceId()).toBeNull()
  })
})
