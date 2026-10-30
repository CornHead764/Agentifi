import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  loadCollapsedSections,
  parseCollapsedSections,
  saveCollapsedSections,
  toggleCollapsedSection,
} from './sections'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the sections the reader has closed', () => {
  it('survives the round trip', () => {
    const stored = new Map<string, string>()
    vi.stubGlobal('window', {
      localStorage: {
        getItem: (key: string) => stored.get(key) ?? null,
        setItem: (key: string, value: string) => stored.set(key, value),
      },
    })
    saveCollapsedSections(new Set(['pending', '2026-07']))
    expect([...loadCollapsedSections()]).toEqual(['pending', '2026-07'])
  })

  it('opens everything where nothing is stored', () => {
    expect(parseCollapsedSections(null).size).toBe(0)
  })

  it('opens everything where the stored value is not a list of sections', () => {
    expect(parseCollapsedSections({ pending: true }).size).toBe(0)
    expect([...parseCollapsedSections(['2026-07', 7, null])]).toEqual(['2026-07'])
  })

  it('closes a section and opens it again, in a set of its own each time', () => {
    const open: ReadonlySet<string> = new Set()
    const closed = toggleCollapsedSection(open, 'pending')
    expect(closed.has('pending')).toBe(true)
    expect(open.size).toBe(0)
    expect(toggleCollapsedSection(closed, 'pending').has('pending')).toBe(false)
  })
})
