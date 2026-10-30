/**
 * Open report tabs, persisted per user to storage on every change and read
 * back on mount. Everything stored is plain JSON, so `JSON.parse` and a shape
 * check are enough.
 */

import { ALL_TIME_SELECTION, selectionForToken, type DateSelection } from '@/lib/dateRanges'
import { readStoredJson, writeStoredJson } from '@/lib/storage'
import type { ReportConfig, SavedReport } from '@/lib/clients/reports'
import type { FilterDraft } from '@/lib/transactions/filter'
import { EMPTY_DRAFT } from '@/lib/transactions/filter'
import { isRecord } from '@/lib/typeGuards'

import { isGalleryPreset, type GalleryEntry } from './presets'

/** A tab's mutable state, and what "unsaved" is measured against. */
export interface TabState {
  range: DateSelection
  config: ReportConfig
  filter: FilterDraft
  /** The free-text box; stored as `query_text`, sent to the engine as a text item. */
  search: string
}

export interface ReportTab {
  id: string
  presetId: string
  title: string
  /** Set once the report exists on the server; the same id `Save` updates. */
  savedId: string | null
  state: TabState
  /** The state as last saved, or as the preset opened. The dot compares to this. */
  baseline: TabState
}

export interface Workspace {
  tabs: ReportTab[]
  active: string
}

export const HOME_TAB = 'home'

let nextTabId = 0

export function openTab(entry: GalleryEntry, state?: TabState, saved?: SavedReport): ReportTab {
  nextTabId += 1
  const opened: TabState = state ?? {
    range: selectionForToken(entry.range) ?? ALL_TIME_SELECTION,
    config: entry.config,
    filter: EMPTY_DRAFT,
    search: '',
  }
  return {
    id: `tab-${nextTabId}`,
    presetId: entry.id,
    title: saved?.name ?? entry.name,
    savedId: saved?.id ?? null,
    state: opened,
    baseline: opened,
  }
}

/**
 * Compared against the saved state, not the preset, so the
 * `toFilterItems`/`fromFilterItems` round trip must invert cleanly or the dot
 * lights on every open.
 */
export function isDirty(tab: ReportTab): boolean {
  return JSON.stringify(tab.state) !== JSON.stringify(tab.baseline)
}

/** Anonymous is its own drawer, not a shared one. */
export function workspaceKey(userId: string | null | undefined): string {
  return `reports.workspace.${userId ?? 'anon'}`
}

function isTabState(value: unknown): value is TabState {
  if (!isRecord(value)) return false
  return storedRange(value.range) !== null && isRecord(value.config) && isRecord(value.filter)
}

/** Names a stored tab's range may carry for the windows `lib/dateRanges.ts` calls otherwise. */
const STORED_TOKENS: Record<string, string> = {
  'month-to-date': 'this-month',
  'last-3-months': '-3m',
  'last-6-months': '-6m',
  'year-to-date': 'this-year',
}

/** A stored range as a selection: a token, a two-ended custom window, or a selection itself. */
function storedRange(value: unknown): DateSelection | null {
  if (typeof value === 'string') return selectionForToken(STORED_TOKENS[value] ?? value)
  if (!isRecord(value)) return null
  if (typeof value.from === 'string' && typeof value.to === 'string') {
    return { range: { from: value.from, to: value.to }, preset: null }
  }
  const range = value.range
  const preset = value.preset
  if (!isRecord(range) || !(preset === null || typeof preset === 'string')) return null
  const from = range.from
  const to = range.to
  if (!(from === null || typeof from === 'string') || !(to === null || typeof to === 'string')) {
    return null
  }
  return { range: { from, to }, preset }
}

function isTab(value: unknown): value is ReportTab {
  if (!isRecord(value)) return false
  return (
    typeof value.id === 'string' &&
    typeof value.presetId === 'string' &&
    typeof value.title === 'string' &&
    (value.savedId === null || typeof value.savedId === 'string') &&
    isTabState(value.state) &&
    isTabState(value.baseline)
  )
}

/**
 * Read the workspace back, dropping any tab that no longer parses or whose
 * report type the gallery no longer has. The id counter moves past restored
 * ids, or two tabs could share one.
 */
export function readWorkspace(key: string): Workspace {
  const parsed = readStoredJson(key)
  if (!isRecord(parsed) || !Array.isArray(parsed.tabs)) return { tabs: [], active: HOME_TAB }

  const tabs = parsed.tabs
    .filter(isTab)
    .filter((tab) => isGalleryPreset(tab.presetId))
    .map(withSearch)
  for (const tab of tabs) {
    const counter = Number(tab.id.replace('tab-', ''))
    if (Number.isFinite(counter) && counter > nextTabId) nextTabId = counter
  }

  const active =
    typeof parsed.active === 'string' && tabs.some((tab) => tab.id === parsed.active)
      ? parsed.active
      : HOME_TAB
  return { tabs, active }
}

/** An absent `search` or facet is empty, not a dropped tab or a lit unsaved dot. */
function withSearch(tab: ReportTab): ReportTab {
  const fill = (state: TabState): TabState => ({
    ...state,
    range: storedRange(state.range) ?? ALL_TIME_SELECTION,
    filter: { ...EMPTY_DRAFT, ...state.filter },
    search: typeof state.search === 'string' ? state.search : '',
  })
  return { ...tab, state: fill(tab.state), baseline: fill(tab.baseline) }
}

export function writeWorkspace(key: string, workspace: Workspace): void {
  writeStoredJson(key, workspace)
}

/**
 * Drop the tabs whose saved report is gone; never-saved tabs always survive.
 * `saved === null` means the list has not answered yet.
 */
export function pruneDeleted(
  tabs: ReportTab[],
  saved: readonly SavedReport[] | null,
): ReportTab[] {
  if (saved === null) return tabs
  const live = new Set(saved.map((report) => report.id))
  const kept = tabs.filter((tab) => tab.savedId === null || live.has(tab.savedId))
  // The same array back when nothing was dropped: the caller memoizes on it.
  return kept.length === tabs.length ? tabs : kept
}
