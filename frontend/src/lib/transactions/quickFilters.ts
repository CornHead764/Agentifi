/**
 * The register's quick filters. Choosing one sets the register's filter to it,
 * facets and search together, rather than adding to what is there; one that
 * carries a date window sets that too, and one without leaves the window. The
 * custom ones are `saved_view` filters (ground rule 3): written through
 * `toFilterItems` and read back through `splitSavedSearch`.
 */

import { selectionForToken, type DateSelection } from '@/lib/dateRanges'
import { splitSavedSearch } from '@/lib/reports/savedFilter'

import {
  EMPTY_DRAFT,
  mergeDrafts,
  toFilterItems,
  type FilterDraft,
  type FilterUniverse,
} from './filter'
import { parseSearch } from './search'
import type { FilterRead, FilterWrite, Uuid } from './types'

export type QuickFilterGroup = 'general' | 'review' | 'saved'

export interface QuickFilter {
  /** A built-in's own name for itself, or the saved filter's id. */
  id: string
  name: string
  group: QuickFilterGroup
  /** The facets, never a date: the window is `dates`. */
  panel: FilterDraft
  search: string
  /** The window it sets; null leaves the register's own. */
  dates: DateSelection | null
}

/** What the register is showing, as a quick filter is compared with it. */
export interface RegisterFilter {
  panel: FilterDraft
  search: string
  dates: DateSelection
}

function builtIn(
  id: string,
  name: string,
  group: QuickFilterGroup,
  panel: Partial<FilterDraft>,
  dates: DateSelection | null = null,
): QuickFilter {
  return { id, name, group, panel: { ...EMPTY_DRAFT, ...panel }, search: '', dates }
}

/** The ones every register offers. "This month" is resolved against `today`. */
export function builtInQuickFilters(today: Date = new Date()): QuickFilter[] {
  return [
    builtIn('this-month', 'This month', 'general', {}, selectionForToken('this-month', today)),
    builtIn('uncategorized', 'Uncategorized', 'general', { uncategorized: true }),
    builtIn('unreviewed', 'All unreviewed', 'review', { isReviewed: false }),
    // An undetermined row has no category, so uncategorized takes those in too.
    builtIn('unreviewed-uncategorized', 'Unreviewed, uncategorized', 'review', {
      isReviewed: false,
      uncategorized: true,
    }),
    builtIn('unreviewed-suggested', 'Unreviewed, with a suggested category', 'review', {
      isReviewed: false,
      hasCategorySuggestion: true,
    }),
  ]
}

/** A saved filter's date item is its window; a preset resolves again today. */
export function quickFilterFromSaved(
  filter: FilterRead,
  universe: FilterUniverse,
  today: Date = new Date(),
): QuickFilter {
  const { filter: draft, search } = splitSavedSearch(filter.items, filter.query_text, universe)
  let dates: DateSelection | null = null
  if (draft.date !== null) {
    const { from, to, preset } = draft.date
    dates = (preset === null ? null : selectionForToken(preset, today)) ?? {
      range: { from, to },
      preset: null,
    }
  }
  return {
    id: filter.id,
    name: filter.name ?? 'Untitled',
    group: 'saved',
    panel: { ...draft, date: null },
    search,
    dates,
  }
}

/** The body that saves a quick filter, its search included in the items as the register sends it. */
export function quickFilterWrite(
  filter: Pick<QuickFilter, 'name' | 'panel' | 'search' | 'dates'>,
  universe: FilterUniverse,
): FilterWrite {
  const date =
    filter.dates === null
      ? null
      : { from: filter.dates.range.from, to: filter.dates.range.to, preset: filter.dates.preset }
  const searched = parseSearch(filter.search, { universe }).draft
  const search = filter.search.trim()
  return {
    name: filter.name.trim(),
    scope: 'saved_view',
    query_text: search === '' ? null : search,
    items: toFilterItems(mergeDrafts({ ...filter.panel, date }, searched), universe),
  }
}

function sameSelection(a: DateSelection, b: DateSelection): boolean {
  if (a.preset !== null || b.preset !== null) return a.preset === b.preset
  return a.range.from === b.range.from && a.range.to === b.range.to
}

/** What a panel and a search ask for, comparable however each was arrived at. */
export function filterSignature(
  panel: FilterDraft,
  search: string,
  universe: FilterUniverse,
): string {
  const searched = parseSearch(search, { universe }).draft
  return JSON.stringify(toFilterItems(mergeDrafts({ ...panel, date: null }, searched), universe))
}

/**
 * On when the register asks for what it asks for, and for its window if it
 * has one. One that asks for nothing is never on: an unfiltered register is
 * not showing it.
 */
export function isQuickFilterOn(
  quick: QuickFilter,
  current: RegisterFilter,
  universe: FilterUniverse,
): boolean {
  if (quick.dates !== null && !sameSelection(quick.dates, current.dates)) return false
  const asked = filterSignature(quick.panel, quick.search, universe)
  if (quick.dates === null && asked === filterSignature(EMPTY_DRAFT, '', universe)) return false
  return asked === filterSignature(current.panel, current.search, universe)
}

/**
 * The positions to write after moving one saved quick filter: the whole list
 * renumbered from 0 in its new order, sent only where a number changed.
 */
export function reorderedPositions(
  rows: readonly { id: Uuid; position: number }[],
  from: number,
  to: number,
): { id: Uuid; position: number }[] {
  if (from === to || from < 0 || to < 0 || from >= rows.length || to >= rows.length) return []
  const order = [...rows]
  const [moved] = order.splice(from, 1)
  order.splice(to, 0, moved!)
  return order
    .map((row, position) => ({ id: row.id, position, was: row.position }))
    .filter((row) => row.position !== row.was)
    .map(({ id, position }) => ({ id, position }))
}
