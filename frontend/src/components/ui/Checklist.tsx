import { clsx } from 'clsx'
import { useRef, useState, type ReactNode } from 'react'

import { clickRange } from '@/lib/selection'
import { toggled } from '@/lib/toggle'
import { matchesSearch } from '@/lib/search'

import { Checkbox } from './Checkbox'
import { EmptyState } from './EmptyState'
import { useArrowList } from './list-keys'
import { SearchInput } from './SearchInput'

export interface ChecklistOption {
  id: string
  label: ReactNode
}

export interface ChecklistGroup {
  id: string
  label: string
  options: readonly ChecklistOption[]
}

export interface ChecklistProps {
  /** A flat list; pass `groups` instead for one drawn under headings. */
  options?: readonly ChecklistOption[]
  groups?: readonly ChecklistGroup[]
  chosen: readonly string[]
  onChange: (ids: string[]) => void
  /** Shown in place of the list when it has no options. */
  empty: ReactNode
  /** The list's class, `option-list` unless it sits in a picker. */
  className?: string
}

const CHECKBOX = '[role="checkbox"]'

/** Several things ticked from a list. The arrow keys walk the boxes. */
export function Checklist(props: ChecklistProps) {
  const keys = useArrowList(CHECKBOX)
  return <ChecklistBody {...props} keys={keys} />
}

export interface SearchableChecklistOption extends ChecklistOption {
  /** What the search matches, for an option whose label is more than its name. */
  text: string
}

export interface SearchableChecklistProps {
  options: readonly SearchableChecklistOption[]
  chosen: readonly string[]
  onChange: (ids: string[]) => void
  /** The box's placeholder and accessible name: "Search tags". */
  searchLabel: string
  /** Shown when no option matches, or there are none. */
  empty: ReactNode
  className?: string
  /** The most options drawn at once, for a list that can run to thousands. */
  limit?: number
}

/**
 * A `Checklist` under a search box. Down from the box enters the list, and
 * typing in the list goes back to the box.
 */
export function SearchableChecklist({
  options,
  chosen,
  onChange,
  searchLabel,
  empty,
  className,
  limit,
}: SearchableChecklistProps) {
  const [search, setSearch] = useState('')
  const keys = useArrowList(CHECKBOX)
  const matching = options.filter((option) => matchesSearch(search, option.text))

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
      <ChecklistBody
        options={limit === undefined ? matching : matching.slice(0, limit)}
        chosen={chosen}
        onChange={onChange}
        empty={empty}
        className={className}
        keys={keys}
      />
    </>
  )
}

function ChecklistBody({
  options,
  groups,
  chosen,
  onChange,
  empty,
  className = 'option-list',
  keys,
}: ChecklistProps & { keys: ReturnType<typeof useArrowList> }) {
  const anchor = useRef<string | null>(null)
  const drawn = [
    ...(options ?? []).map((option) => option.id),
    ...(groups ?? []).flatMap((group) => group.options.map((option) => option.id)),
  ]
  const click = (id: string, extend: boolean) => {
    const on = !chosen.includes(id)
    const reached = clickRange(drawn, anchor.current, id, extend)
    anchor.current = id
    onChange(reached.reduce((list, one) => toggled(list, one, on), [...chosen]))
  }
  const row = (option: ChecklistOption, child: boolean) => (
    <div
      key={option.id}
      className={clsx('option-list__row', child && 'option-list__row--child')}
    >
      <Checkbox
        label={option.label}
        checked={chosen.includes(option.id)}
        onClick={(event) => click(option.id, event.shiftKey)}
      />
    </div>
  )
  const count = drawn.length

  return (
    <div className={className} {...keys.list}>
      {options?.map((option) => row(option, false))}
      {groups?.map((group) => (
        <div key={group.id}>
          <p className="option-list__heading eyebrow">{group.label}</p>
          {group.options.map((option) => row(option, true))}
        </div>
      ))}
      {count === 0 ? <EmptyState compact title={empty} /> : null}
    </div>
  )
}
