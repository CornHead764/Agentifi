/**
 * Picking several categories, stored as one `category IN (…)` item on a
 * `Filter` (ground rule 3).
 *
 * A category whose parent is not in the offered set reads as top level, or it
 * would never render. Selecting a parent does not select its children: the
 * server expands a parent where the surface means that.
 */

import { useMemo, useRef, useState } from 'react'

import { Checkbox, EmptyState, SearchInput, useArrowList } from '@/components/ui'
import { categoryIndentClass, categoryRows, type CategoryChoice } from '@/lib/categoryTree'
import { clickRange } from '@/lib/selection'
import { toggled } from '@/lib/toggle'

/** A row that is not a category (Uncategorized), drawn above the tree. */
export interface ChecklistRow {
  id: string
  label: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  depth?: number
}

export interface CategoryChecklistProps {
  categories: readonly CategoryChoice[]
  selected: readonly string[]
  onChange: (ids: string[]) => void
  extraRows?: readonly ChecklistRow[]
  /** Shown when there is nothing to check, rather than when a search misses. */
  emptyLabel?: string
  searchLabel?: string
}

export function CategoryChecklist({
  categories,
  selected,
  onChange,
  extraRows = [],
  emptyLabel = 'No categories yet.',
  searchLabel = 'Search categories',
}: CategoryChecklistProps) {
  const [search, setSearch] = useState('')
  const keys = useArrowList('[role="checkbox"]')

  const rows = useMemo(() => categoryRows(categories, search), [categories, search])
  const anchor = useRef<string | null>(null)

  if (categories.length === 0 && extraRows.length === 0) {
    return <EmptyState compact title={emptyLabel} />
  }

  const click = (id: string, extend: boolean) => {
    const on = !selected.includes(id)
    const drawn = rows.map(({ category }) => category.id)
    const reached = clickRange(drawn, anchor.current, id, extend)
    anchor.current = id
    onChange(reached.reduce((list, one) => toggled(list, one, on), [...selected]))
  }

  return (
    <>
      <SearchInput
        {...keys.search}
        value={search}
        onChange={setSearch}
        placeholder={searchLabel}
        aria-label={searchLabel}
        onKeyDown={keys.onSearchKeyDown}
      />
      <div className="option-list" {...keys.list}>
        {extraRows.map((row) => (
          <div key={row.id} className={categoryIndentClass('option-list__row', row.depth ?? 0)}>
            <Checkbox
              label={row.label}
              checked={row.checked}
              onCheckedChange={(checked) => row.onCheckedChange(checked === true)}
            />
          </div>
        ))}
        {rows.map(({ category, depth }) => (
          <div key={category.id} className={categoryIndentClass('option-list__row', depth)}>
            <Checkbox
              label={category.name}
              checked={selected.includes(category.id)}
              onClick={(event) => click(category.id, event.shiftKey)}
            />
          </div>
        ))}
        {rows.length === 0 && categories.length > 0 ? (
          <EmptyState compact title="No matching category." />
        ) : null}
      </div>
    </>
  )
}
