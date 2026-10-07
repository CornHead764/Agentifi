/**
 * What a register cell needs, without threading props through the grid. The
 * value must be stable: a virtualized row re-renders on every scroll frame.
 */

import { createContext, useContext } from 'react'

import type { RowSwipe } from '@/lib/transactions/gestures'
import type { Account, Category, Tag, Transaction, Uuid } from '@/lib/transactions/types'

export interface RegisterLookups {
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  accountName: (id: Uuid) => string
  categoryName: (id: Uuid | null) => string
  tagName: (id: Uuid) => string
  /** Derived from the rows on screen, so the picker leads with what is actually used. */
  frequentCategoryIds: readonly Uuid[]
  /** Every row whose category is being rechecked; a row leaves only when its own result lands. */
  checkingCategories?: ReadonlySet<Uuid>
}

export interface RegisterActions {
  /**
   * An in-place edit. `patch` is the request body and `optimistic` is the same
   * change in the shape the cache holds — they are separate because the wire
   * carries an amount as a string and the cache holds it as `Money`.
   */
  edit: (
    txn: Transaction,
    patch: Record<string, unknown>,
    optimistic: Partial<Transaction>,
  ) => void
  setReviewed: (txn: Transaction, reviewed: boolean) => void
  setTags: (txn: Transaction, tagIds: Uuid[]) => void
  openDetail: (txn: Transaction) => void
  /** Open the row for review: the same dialog, with the proposal on top. */
  openReview: (txn: Transaction) => void
  /**
   * Apply the suggestion waiting on a row. An omitted `categoryId` approves
   * the model's choice; a value overrides it and the server records the
   * disagreement. Applying also ticks the row reviewed.
   */
  applySuggestion: (txn: Transaction, categoryId?: Uuid | null) => void
  discardSuggestion: (txn: Transaction) => void
  /** True while a suggestion is being decided, so a cell stops taking clicks. */
  decidingSuggestion: boolean
  toggleSplits: (id: Uuid) => void
  /** Explain a refused edit — the statement name — rather than doing nothing. */
  refuse: (reason: string) => void
  /** Open the assistant run behind a row's category. */
  showRun: (runId: Uuid) => void
}

export interface RegisterSelection {
  /** The checkbox column, which the desktop register always draws. */
  enabled: boolean
  /**
   * Selection mode on a phone, where the column is drawn only once asked for.
   * `begin` is the long press: it opens the mode with the pressed row already
   * selected.
   */
  active: boolean
  ids: ReadonlySet<Uuid>
  /** `extend` is a shift-click: it carries the row's new state from the last row clicked. */
  toggle: (id: Uuid, extend?: boolean) => void
  begin: (id: Uuid) => void
  /**
   * Every row on screen, in order, after filters, search, window and closed
   * sections. What select-all acts on.
   */
  visibleIds: readonly Uuid[]
  /** Tick all of those, or clear the selection when any of them is already ticked. */
  toggleAll: () => void
}

/**
 * The group headings, as disclosures. Closed sections are held by the page:
 * they decide which rows exist (`buildRows`), which keeps a closed section out
 * of the selection.
 */
export interface RegisterSections {
  collapsed: ReadonlySet<string>
  toggle: (section: string) => void
}

/**
 * How a swipe opens a row menu that has no button. `anchorOnly` hides the
 * trigger rather than removing it: Radix positions against the trigger's box,
 * and a `display: none` trigger has none.
 */
export interface RowMenuControl {
  open: boolean
  onOpenChange: (open: boolean) => void
  anchorOnly: boolean
}

/**
 * What each swipe direction runs, named the way a finger moves: `left` is a
 * swipe from the right edge towards the left one. Both roam with the account,
 * except in the review queue, which binds its own.
 */
export interface SwipeBindings {
  left: RowSwipe
  right: RowSwipe
}

export interface RegisterView {
  lookups: RegisterLookups
  actions: RegisterActions
  selection: RegisterSelection
  sections: RegisterSections
  swipe: SwipeBindings
  /** More than one account is in view, so a running balance has no meaning. */
  multiAccount: boolean
  expanded: ReadonlySet<Uuid>
}

export const RegisterContext = createContext<RegisterView | null>(null)

export function useRegisterView(): RegisterView {
  const value = useContext(RegisterContext)
  if (!value) throw new Error('register cells must render inside <RegisterGrid>')
  return value
}
