import { describe, expect, it } from 'vitest'

import { ALL_TIME_SELECTION, selectionForToken, type DateSelection } from '@/lib/dateRanges'

import { EMPTY_DRAFT, EMPTY_UNIVERSE, withoutFacets, type FilterDraft } from './filter'
import {
  builtInQuickFilters,
  isQuickFilterOn,
  quickFilterFromSaved,
  quickFilterWrite,
  reorderedPositions,
  type QuickFilter,
  type RegisterFilter,
} from './quickFilters'
import type { FilterRead } from './types'

const today = new Date(2026, 8, 3)
const universe = EMPTY_UNIVERSE

function quick(id: string): QuickFilter {
  return builtInQuickFilters(today).find((one) => one.id === id)!
}

function showing(
  panel: Partial<FilterDraft>,
  search = '',
  dates: DateSelection = ALL_TIME_SELECTION,
): RegisterFilter {
  return { panel: { ...EMPTY_DRAFT, ...panel }, search, dates }
}

/** A write as the server answers it back. */
function stored(id: string, write: ReturnType<typeof quickFilterWrite>): FilterRead {
  return {
    id,
    name: write.name,
    scope: write.scope,
    query_text: write.query_text,
    items: write.items.map((item, index) => ({ ...item, id: `item-${index}` })),
  }
}

describe('the built-in quick filters', () => {
  it('give This month its window and leave the others the register’s', () => {
    expect(quick('this-month').dates).toEqual(selectionForToken('this-month', today))
    for (const id of ['uncategorized', 'unreviewed', 'unreviewed-uncategorized', 'unreviewed-suggested']) {
      expect(quick(id).dates).toBeNull()
    }
  })

  it('are each one whole filter, not a facet added to the current one', () => {
    expect(quick('unreviewed').panel).toEqual({ ...EMPTY_DRAFT, isReviewed: false })
    expect(quick('unreviewed-uncategorized').panel).toEqual({
      ...EMPTY_DRAFT,
      isReviewed: false,
      uncategorized: true,
    })
    expect(quick('unreviewed-suggested').panel).toEqual({
      ...EMPTY_DRAFT,
      isReviewed: false,
      hasCategorySuggestion: true,
    })
  })
})

describe('isQuickFilterOn', () => {
  it('is on only while the register asks for exactly that', () => {
    expect(isQuickFilterOn(quick('uncategorized'), showing({ uncategorized: true }), universe)).toBe(true)
    expect(
      isQuickFilterOn(
        quick('uncategorized'),
        showing({ uncategorized: true, payees: { values: ['Corner Store'], negated: false } }),
        universe,
      ),
    ).toBe(false)
    expect(
      isQuickFilterOn(quick('unreviewed'), showing({ isReviewed: false, hasCategorySuggestion: true }), universe),
    ).toBe(false)
  })

  it('ignores the window for a quick filter that carries none', () => {
    const thisMonth = selectionForToken('this-month', today)!
    expect(
      isQuickFilterOn(quick('uncategorized'), showing({ uncategorized: true }, '', thisMonth), universe),
    ).toBe(true)
  })

  it('is never on for one that asks for nothing', () => {
    const empty = { ...quick('uncategorized'), id: 'empty', panel: EMPTY_DRAFT }
    expect(isQuickFilterOn(empty, showing({}), universe)).toBe(false)
  })

  it('is off once the Filter popover clears the facets it set', () => {
    for (const id of ['uncategorized', 'unreviewed', 'unreviewed-uncategorized', 'unreviewed-suggested']) {
      const chosen = quick(id)
      expect(isQuickFilterOn(chosen, showing(chosen.panel), universe)).toBe(true)
      expect(isQuickFilterOn(chosen, showing(withoutFacets(chosen.panel)), universe)).toBe(false)
    }
  })

  it('needs the window for one that carries it', () => {
    const thisMonth = selectionForToken('this-month', today)!
    expect(isQuickFilterOn(quick('this-month'), showing({}, '', thisMonth), universe)).toBe(true)
    expect(isQuickFilterOn(quick('this-month'), showing({}), universe)).toBe(false)
    expect(isQuickFilterOn(quick('this-month'), showing({ uncategorized: true }, '', thisMonth), universe)).toBe(
      false,
    )
  })
})

describe('a saved quick filter', () => {
  it('comes back as the filter it was saved from, search box included', () => {
    const current = showing({ payees: { values: ['Corner Store'], negated: false } }, 'coffee')
    const write = quickFilterWrite({ name: '  Coffee runs ', ...current, dates: null }, universe)
    expect(write).toMatchObject({ name: 'Coffee runs', scope: 'saved_view', query_text: 'coffee' })

    const back = quickFilterFromSaved(stored('a', write), universe, today)
    expect(back).toMatchObject({ id: 'a', name: 'Coffee runs', group: 'saved', search: 'coffee', dates: null })
    expect(back.panel.payees).toEqual({ values: ['Corner Store'], negated: false })
    expect(isQuickFilterOn(back, current, universe)).toBe(true)
  })

  it('keeps a preset window as the preset, resolved again on the day it is read', () => {
    const write = quickFilterWrite(
      { name: 'Month', panel: EMPTY_DRAFT, search: '', dates: selectionForToken('this-month', today) },
      universe,
    )
    const later = new Date(2026, 10, 20)
    const back = quickFilterFromSaved(stored('b', write), universe, later)
    expect(back.dates).toEqual(selectionForToken('this-month', later))
    expect(back.panel.date).toBeNull()
  })

  it('keeps a hand-picked window as its dates', () => {
    const dates = { range: { from: '2026-02-01', to: '2026-02-14' }, preset: null }
    const write = quickFilterWrite({ name: 'February', panel: EMPTY_DRAFT, search: '', dates }, universe)
    expect(quickFilterFromSaved(stored('c', write), universe, today).dates).toEqual(dates)
  })
})

describe('reorderedPositions', () => {
  const rows = [
    { id: 'a', position: 0 },
    { id: 'b', position: 1 },
    { id: 'c', position: 2 },
  ]

  it('renumbers the list in its new order and sends only what changed', () => {
    expect(reorderedPositions(rows, 2, 1)).toEqual([
      { id: 'c', position: 1 },
      { id: 'b', position: 2 },
    ])
  })

  it('numbers a list that has never been arranged, whose positions are all 0', () => {
    const fresh = rows.map((row) => ({ ...row, position: 0 }))
    expect(reorderedPositions(fresh, 0, 1)).toEqual([
      { id: 'a', position: 1 },
      { id: 'c', position: 2 },
    ])
  })

  it('does nothing past either end', () => {
    expect(reorderedPositions(rows, 0, -1)).toEqual([])
    expect(reorderedPositions(rows, 2, 3)).toEqual([])
  })
})
