import { afterEach, describe, expect, it, vi } from 'vitest'

import { groupCollapsed, toggleGroupCollapsed } from './groupCollapse'

afterEach(() => {
  vi.unstubAllGlobals()
})

function fakeStorage(stored: Record<string, string>) {
  const writes: [string, string][] = []
  vi.stubGlobal('window', {
    localStorage: {
      getItem: (key: string) => stored[key] ?? null,
      setItem: (key: string, value: string) => writes.push([key, value]),
    },
  })
  return writes
}

describe('an accounts-drawer group’s collapsed state', () => {
  it('starts open when nothing has been stored', () => {
    fakeStorage({})
    expect(groupCollapsed(new Map(), 'group:savings')).toBe(false)
  })

  it('reads each group’s own stored state', () => {
    fakeStorage({ 'agentifi.accounts.collapsed.group:savings': 'true' })
    expect(groupCollapsed(new Map(), 'group:savings')).toBe(true)
    expect(groupCollapsed(new Map(), 'group:cash')).toBe(false)
  })

  it('stores a toggle under the group, and the session’s toggle wins over storage', () => {
    const writes = fakeStorage({ 'agentifi.accounts.collapsed.group:savings': 'true' })
    const opened = toggleGroupCollapsed(new Map(), 'group:savings')
    expect(groupCollapsed(opened, 'group:savings')).toBe(false)
    expect(writes).toEqual([['agentifi.accounts.collapsed.group:savings', 'false']])

    const closed = toggleGroupCollapsed(opened, 'group:savings')
    expect(groupCollapsed(closed, 'group:savings')).toBe(true)
  })

  it('still toggles when storage refuses', () => {
    vi.stubGlobal('window', {
      localStorage: {
        getItem: () => {
          throw new Error('blocked')
        },
        setItem: () => {
          throw new Error('blocked')
        },
      },
    })
    expect(groupCollapsed(toggleGroupCollapsed(new Map(), 'group:cash'), 'group:cash')).toBe(true)
  })
})
