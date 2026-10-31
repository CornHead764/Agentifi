import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  readStored,
  readStoredFlag,
  readStoredJson,
  writeStored,
  writeStoredFlag,
  writeStoredJson,
} from './storage'

afterEach(() => {
  vi.unstubAllGlobals()
})

function fakeStorage(initial: Record<string, string> = {}) {
  const stored = new Map(Object.entries(initial))
  vi.stubGlobal('window', {
    localStorage: {
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => stored.set(key, value),
    },
  })
  return stored
}

describe('stored preferences', () => {
  it('writes flags as true and false', () => {
    const stored = fakeStorage()
    writeStoredFlag('rail.collapsed', true)
    expect(stored.get('agentifi.rail.collapsed')).toBe('true')
    expect(readStoredFlag('rail.collapsed', false)).toBe(true)
  })

  it('reads a flag stored as 1 or 0', () => {
    fakeStorage({ 'agentifi.on': '1', 'agentifi.off': '0' })
    expect(readStoredFlag('on', false)).toBe(true)
    expect(readStoredFlag('off', true)).toBe(false)
  })

  it('falls back on a flag that is absent or not a flag', () => {
    fakeStorage({ 'agentifi.odd': 'yes' })
    expect(readStoredFlag('odd', true)).toBe(true)
    expect(readStoredFlag('absent', false)).toBe(false)
  })

  it('round-trips JSON and reads a value that does not parse as nothing', () => {
    fakeStorage({ 'agentifi.broken': '{not json' })
    writeStoredJson('layout', [{ id: 'budget', on: true }])
    expect(readStoredJson('layout')).toEqual([{ id: 'budget', on: true }])
    expect(readStoredJson('broken')).toBeNull()
    expect(readStoredJson('absent')).toBeNull()
  })

  it('reads nothing and writes nowhere when the browser refuses storage', () => {
    vi.stubGlobal('window', {
      localStorage: {
        getItem: () => {
          throw new Error('blocked')
        },
        setItem: () => {
          throw new Error('quota')
        },
      },
    })
    expect(readStored('anything')).toBeNull()
    expect(readStoredJson('anything')).toBeNull()
    expect(readStoredFlag('anything', true)).toBe(true)
    expect(() => writeStored('anything', 'x')).not.toThrow()
  })
})
