import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { SavedReport } from '@/lib/clients/reports'
import { writeStored } from '@/lib/storage'
import { EMPTY_DRAFT } from '@/lib/transactions/filter'

import {
  HOME_TAB,
  isDirty,
  openTab,
  pruneDeleted,
  readWorkspace,
  workspaceKey,
  writeWorkspace,
  type ReportTab,
} from './tabs'
import type { GalleryEntry } from './presets'

/** `lib/storage` reads `window.localStorage`, and this suite runs under node. */
function installStorage(): Map<string, string> {
  const backing = new Map<string, string>()
  const storage = {
    getItem: (key: string) => backing.get(key) ?? null,
    setItem: (key: string, value: string) => void backing.set(key, value),
    removeItem: (key: string) => void backing.delete(key),
  }
  Reflect.set(globalThis, 'window', { localStorage: storage })
  return backing
}

let backing: Map<string, string>

beforeEach(() => {
  backing = installStorage()
})

afterEach(() => {
  Reflect.deleteProperty(globalThis, 'window')
})

const ENTRY: GalleryEntry = {
  id: 'spending',
  name: 'Spending',
  blurb: 'Where it went',
  series: 1,
  range: 'this-month',
  config: {
    preset: 'spending',
    mode: 'transaction',
    rows: 'category',
    columns: 'time',
    time_grain: 'month',
    sign: 'expenses',
  },
  servedBy: null,
}

function savedReport(id: string, name = 'August groceries'): SavedReport {
  return {
    id,
    name,
    config: ENTRY.config,
    filter: { items: [] },
  } as unknown as SavedReport
}

describe('the reports workspace', () => {
  it('gives an empty workspace when nothing was stored', () => {
    expect(readWorkspace(workspaceKey('u1'))).toEqual({ tabs: [], active: HOME_TAB })
  })

  it('restores the open tabs, their edits and their custom range', () => {
    const key = workspaceKey('u1')
    const tab = openTab(ENTRY)
    tab.state = {
      ...tab.state,
      range: { range: { from: '2026-01-01', to: '2026-03-31' }, preset: null },
      config: { ...tab.state.config, mode: 'summary' },
      filter: { ...EMPTY_DRAFT, texts: ['coffee'] },
    }
    writeWorkspace(key, { tabs: [tab], active: tab.id })

    const restored = readWorkspace(key)
    expect(restored.active).toBe(tab.id)
    expect(restored.tabs).toHaveLength(1)
    expect(restored.tabs[0].state.range).toEqual({
      range: { from: '2026-01-01', to: '2026-03-31' },
      preset: null,
    })
    expect(restored.tabs[0].state.config.mode).toBe('summary')
    expect(restored.tabs[0].state.filter.texts).toEqual(['coffee'])
  })

  it('reads a range stored as a bare token or a two-day window as the same selection', () => {
    const key = workspaceKey('u1')
    const token = openTab(ENTRY)
    const custom = openTab(ENTRY)
    const stored = (tab: ReportTab, range: unknown) => ({
      ...tab,
      state: { ...tab.state, range },
      baseline: { ...tab.baseline, range },
    })
    writeStored(
      key,
      JSON.stringify({
        tabs: [
          stored(token, 'year-to-date'),
          stored(custom, { from: '2026-01-01', to: '2026-03-31' }),
        ],
        active: HOME_TAB,
      }),
    )

    const restored = readWorkspace(key)
    expect(restored.tabs.map((tab) => tab.state.range.preset)).toEqual(['this-year', null])
    expect(restored.tabs[1].state.range.range).toEqual({ from: '2026-01-01', to: '2026-03-31' })
    expect(isDirty(restored.tabs[0])).toBe(false)
  })

  it('keeps the unsaved dot across the round trip', () => {
    const key = workspaceKey('u1')
    const clean = openTab(ENTRY)
    const dirty = openTab(ENTRY)
    dirty.state = { ...dirty.state, config: { ...dirty.state.config, mode: 'summary' } }
    writeWorkspace(key, { tabs: [clean, dirty], active: dirty.id })

    const restored = readWorkspace(key)
    expect(isDirty(restored.tabs[0])).toBe(false)
    expect(isDirty(restored.tabs[1])).toBe(true)
  })

  it('drops a tab whose report type the gallery no longer has', () => {
    const key = workspaceKey('u1')
    const kept = openTab(ENTRY)
    const gone = { ...openTab(ENTRY), presetId: 'retired_report' }
    writeWorkspace(key, { tabs: [kept, gone], active: gone.id })

    const restored = readWorkspace(key)
    expect(restored.tabs.map((tab) => tab.id)).toEqual([kept.id])
    expect(restored.active).toBe(HOME_TAB)
  })

  it('gives each user their own drawer', () => {
    writeWorkspace(workspaceKey('u1'), { tabs: [openTab(ENTRY)], active: HOME_TAB })
    expect(readWorkspace(workspaceKey('u2')).tabs).toHaveLength(0)
  })

  it('never hands back a tab id that is already open', () => {
    const key = workspaceKey('u1')
    writeWorkspace(key, { tabs: [openTab(ENTRY), openTab(ENTRY)], active: HOME_TAB })
    const restored = readWorkspace(key)
    const fresh = openTab(ENTRY)
    expect(restored.tabs.map((tab) => tab.id)).not.toContain(fresh.id)
  })

  it('drops a stored tab that no longer parses, and keeps the rest', () => {
    const key = workspaceKey('u1')
    const good = openTab(ENTRY)
    backing.set(
      `agentifi.${key}`,
      JSON.stringify({ tabs: [good, { id: 'tab-9' }, null, 'nonsense'], active: good.id }),
    )
    expect(readWorkspace(key).tabs.map((tab) => tab.id)).toEqual([good.id])
  })

  it('falls back to home rather than throwing on a corrupt payload', () => {
    const key = workspaceKey('u1')
    backing.set(`agentifi.${key}`, '{not json')
    expect(readWorkspace(key)).toEqual({ tabs: [], active: HOME_TAB })
  })

  it('falls back to home when the active tab is not one of the restored ones', () => {
    const key = workspaceKey('u1')
    writeWorkspace(key, { tabs: [openTab(ENTRY)], active: 'tab-gone' })
    expect(readWorkspace(key).active).toBe(HOME_TAB)
  })

  it('closes a tab whose saved report was deleted, and keeps unsaved ones', () => {
    const unsaved = openTab(ENTRY)
    const live = { ...openTab(ENTRY), savedId: 'r1' } as ReportTab
    const gone = { ...openTab(ENTRY), savedId: 'r2' } as ReportTab

    const kept = pruneDeleted([unsaved, live, gone], [savedReport('r1')])
    expect(kept.map((tab) => tab.savedId)).toEqual([null, 'r1'])
  })

  it('keeps every tab while the saved list has not answered', () => {
    const tabs = [{ ...openTab(ENTRY), savedId: 'r1' } as ReportTab]
    expect(pruneDeleted(tabs, null)).toBe(tabs)
  })

  it('hands back the same array when nothing was dropped', () => {
    const tabs = [{ ...openTab(ENTRY), savedId: 'r1' } as ReportTab]
    expect(pruneDeleted(tabs, [savedReport('r1')])).toBe(tabs)
  })
})
