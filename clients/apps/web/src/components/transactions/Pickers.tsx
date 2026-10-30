import { Check, X } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'

import {
  EmptyState,
  IconButton,
  Popover,
  PopoverContent,
  PopoverTrigger,
  SearchableChecklist,
  SearchInput,
  useArrowList,
} from '@/components/ui'
import { smallBalancesLabel } from '@/components/shell/accountTree'
import { pickerAccounts } from '@/lib/accounts'
import { categoryCreateOffer } from '@/lib/categoryNames'
import { categoryIndentClass, categoryRows } from '@/lib/categoryTree'
import { inlineCategoryCreate, useCreateCategory } from '@/lib/clients/categories'
import type { AccountWithBalances, Category, Tag, Uuid } from '@/lib/transactions/types'

import { pickableCategories } from './pickableCategories'

/**
 * The category picker: most used (counted from the rows on screen; there is
 * no stored ranking), then the whole tree, searched as every category list is.
 *
 * A name that matches nothing offers to become a top-level expense category.
 * Both exclusions are sent as false explicitly, so a new category cannot
 * silently drop rows from reports or the plan. A blank name or one an existing
 * category carries (case and accents folded) creates nothing.
 */
export function CategoryPicker({
  value,
  categories,
  frequentIds,
  trigger,
  noneLabel = 'Uncategorized',
  onChange,
  open: openProp,
  onOpenChange,
}: {
  value: Uuid | null
  categories: readonly Category[]
  frequentIds: readonly Uuid[]
  trigger: ReactNode
  /** What choosing no category means here. */
  noneLabel?: string
  onChange: (id: Uuid | null) => void
  /** For a picker opened by something other than its trigger, such as a swipe. */
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const [ownOpen, setOwnOpen] = useState(false)
  const open = openProp ?? ownOpen
  const setOpen = onOpenChange ?? setOwnOpen
  const [search, setSearch] = useState('')
  const create = useCreateCategory()

  const assignable = useMemo(() => pickableCategories(categories, value), [categories, value])

  const rows = useMemo(() => categoryRows(assignable, search), [assignable, search])

  const frequent = useMemo(
    () =>
      frequentIds
        .map((id) => assignable.find((category) => category.id === id))
        .filter((category): category is Category => category !== undefined)
        .slice(0, 5),
    [assignable, frequentIds],
  )

  const choose = (id: Uuid | null) => {
    onChange(id)
    setOpen(false)
    setSearch('')
  }

  // The collision check reads every category, not the assignable ones: a name
  // already taken by one the picker does not offer is still taken.
  const offer = categoryCreateOffer(categories, search, rows.length)

  const createIt = () => {
    if (!offer.canCreate || create.isPending) return
    create.mutate(inlineCategoryCreate(offer.name), {
      onSuccess: (category) => choose(category.id),
    })
  }

  const keys = useArrowList('.picker__option')

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent
        align="start"
        className="picker"
        onEscapeKeyDown={(event) => {
          // With the prompt up, Escape answers the prompt. Radix would close
          // the whole picker, which is not what "no, don't create it" means.
          if (!offer.canCreate) return
          event.preventDefault()
          setSearch('')
        }}
      >
        <SearchInput
          autoFocus
          {...keys.search}
          value={search}
          placeholder="Search categories"
          aria-label="Search categories"
          onChange={setSearch}
          onKeyDown={(event) => {
            if (keys.onSearchKeyDown(event)) return
            if (event.key !== 'Enter') return
            // Enter takes what is on offer. Creating is what it means only when
            // the search matches nothing to take.
            const first = keys.items()[0]
            if (first) {
              event.preventDefault()
              first.click()
              return
            }
            if (!offer.canCreate) return
            event.preventDefault()
            createIt()
          }}
        />
        <div className="picker__list" {...keys.list}>
          <Option label={noneLabel} selected={value === null} onSelect={() => choose(null)} />
          {search === '' && frequent.length > 0 ? (
            <>
              <p className="menu__label eyebrow">Most used</p>
              {frequent.map((category) => (
                <Option
                  key={`frequent-${category.id}`}
                  label={category.name}
                  selected={value === category.id}
                  onSelect={() => choose(category.id)}
                />
              ))}
              <p className="menu__label eyebrow">All categories</p>
            </>
          ) : null}
          {rows.map(({ category, depth }) => (
            <Option
              key={category.id}
              label={category.name}
              depth={depth}
              selected={value === category.id}
              onSelect={() => choose(category.id)}
            />
          ))}
          {rows.length === 0 && !offer.canCreate ? (
            <EmptyState
              compact
              title={
                offer.taken === undefined
                  ? 'No matching category.'
                  : `“${offer.name}” is already a category, spelled “${offer.taken.name}”.`
              }
            />
          ) : null}
          {offer.canCreate ? (
            <div className="picker__create">
              {/* The sentence is the live region, not the row: a region holding
                  the two buttons would read their labels out with it. */}
              <span role="status">No category called “{offer.name}”. Create it?</span>
              <span className="picker__create-actions">
                <IconButton
                  label={`Create the category “${offer.name}”`}
                  variant="ghost"
                  size="sm"
                  disabled={create.isPending}
                  onClick={createIt}
                >
                  <Check size={14} />
                </IconButton>
                <IconButton
                  label="Do not create it"
                  variant="ghost"
                  size="sm"
                  onClick={() => setSearch('')}
                >
                  <X size={14} />
                </IconButton>
              </span>
            </div>
          ) : null}
        </div>
      </PopoverContent>
    </Popover>
  )
}

/** The dot beside a tag's name; nothing for an uncoloured tag. */
export function TagSwatch({ color }: { color: string | null }) {
  if (color === null || color === '') return null
  return <span className="tag-swatch" style={{ background: color }} aria-hidden="true" />
}

export function TagPicker({
  value,
  tags,
  trigger,
  onChange,
}: {
  value: readonly Uuid[]
  tags: readonly Tag[]
  trigger: ReactNode
  onChange: (ids: Uuid[]) => void
}) {
  const options = tags.map((tag) => ({
    id: tag.id,
    text: tag.name,
    label: (
      <span className="row row--wrap txn-actions">
        <TagSwatch color={tag.color} /> {tag.name}
      </span>
    ),
  }))

  return (
    <Popover>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent align="start" className="picker">
        <SearchableChecklist
          options={options}
          chosen={value}
          onChange={onChange}
          searchLabel="Search tags"
          empty="No matching tag."
          className="picker__list"
        />
      </PopoverContent>
    </Popover>
  )
}

/**
 * The account a row is filed in. An account flagged `hidden_small_balance` is
 * left out behind the "N small balances hidden" line, as the account lists
 * leave it out; the row's own account is always listed.
 */
export function AccountPicker({
  value,
  accounts,
  trigger,
  onChange,
}: {
  value: Uuid
  accounts: readonly Pick<AccountWithBalances, 'id' | 'name' | 'hidden_small_balance'>[]
  trigger: ReactNode
  onChange: (id: Uuid) => void
}) {
  const [open, setOpen] = useState(false)
  const [revealed, setRevealed] = useState(false)
  const { shown, hidden } = pickerAccounts(accounts, value, revealed)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent align="start" className="picker">
        <div className="picker__list">
          {shown.map((account) => (
            <Option
              key={account.id}
              label={account.name}
              selected={account.id === value}
              onSelect={() => {
                onChange(account.id)
                setOpen(false)
              }}
            />
          ))}
          {hidden === 0 ? null : (
            <button
              type="button"
              className="picker__option picker__option--reveal"
              aria-expanded={revealed}
              onClick={() => setRevealed(!revealed)}
            >
              {smallBalancesLabel(hidden, revealed)}
            </button>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function Option({
  label,
  selected,
  depth = 0,
  onSelect,
}: {
  label: string
  selected: boolean
  depth?: number
  onSelect: () => void
}) {
  return (
    <button
      type="button"
      className={categoryIndentClass('picker__option', depth)}
      aria-pressed={selected}
      data-selected={selected}
      onClick={onSelect}
    >
      {label}
      {selected ? <Check size={14} aria-hidden="true" /> : null}
    </button>
  )
}
