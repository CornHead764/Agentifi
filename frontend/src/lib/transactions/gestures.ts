/**
 * The arithmetic behind a phone row's press and swipe, kept out of the
 * component because the suite renders static markup with no pointer events.
 */

import { suggestedCategoryId } from './suggestions'
import type { Transaction } from './types'

export const LONG_PRESS_MS = 500
/** A held thumb wanders a few pixels; past this it is a drag. */
const LONG_PRESS_SLOP_PX = 8
const SWIPE_START_PX = 10
const SWIPE_COMMIT_FRACTION = 0.4
/** px per ms over the whole gesture: a fast flick commits short of the distance. */
const SWIPE_FLICK_VELOCITY = 0.4
const SWIPE_MAX_PX = 132

export interface Drag {
  /** Positive is rightwards. */
  dx: number
  dy: number
  elapsedMs: number
}

export type SwipeDirection = 'left' | 'right'

export function cancelsLongPress(drag: Drag, slop = LONG_PRESS_SLOP_PX): boolean {
  return Math.abs(drag.dx) > slop || Math.abs(drag.dy) > slop
}

/** Vertical wins ties, so a diagonal flick scrolls the page rather than snagging on a row. */
export function isScrollGesture(drag: Drag, slop = SWIPE_START_PX): boolean {
  return Math.abs(drag.dy) > slop && Math.abs(drag.dy) >= Math.abs(drag.dx)
}

export function beginsSwipe(drag: Drag, slop = SWIPE_START_PX): boolean {
  return Math.abs(drag.dx) > slop && !isScrollGesture(drag, slop)
}

/** Clamped so the row never drags clear of the action label behind it. */
export function swipeOffset(dx: number, max = SWIPE_MAX_PX): number {
  if (dx > max) return max
  if (dx < -max) return -max
  return dx
}

export function swipeDirection(dx: number): SwipeDirection | null {
  if (dx > 0) return 'right'
  if (dx < 0) return 'left'
  return null
}

/** The direction whose action to run on release, or null to snap back. Distance is relative to the row's width. */
export function swipeDecision(
  drag: Drag,
  width: number,
  fraction = SWIPE_COMMIT_FRACTION,
): SwipeDirection | null {
  if (isScrollGesture(drag)) return null
  const distance = Math.abs(drag.dx)
  if (distance <= SWIPE_START_PX) return null
  const far = width > 0 && distance >= width * fraction
  const fast = drag.elapsedMs > 0 && distance / drag.elapsedMs >= SWIPE_FLICK_VELOCITY
  return far || fast ? swipeDirection(drag.dx) : null
}

// --- What a direction does ---------------------------------------------------

/** Wire values of `swipe_left_action` / `swipe_right_action`, validated server-side against this set. */
export const SWIPE_ACTIONS = ['menu', 'review', 'flag', 'exclude', 'edit', 'none'] as const

export type SwipeAction = (typeof SWIPE_ACTIONS)[number]

export const DEFAULT_SWIPE_LEFT: SwipeAction = 'menu'
export const DEFAULT_SWIPE_RIGHT: SwipeAction = 'review'

export const SWIPE_ACTION_LABELS: Record<SwipeAction, string> = {
  menu: 'Open menu',
  review: 'Mark reviewed / approve',
  flag: 'Flag',
  exclude: 'Exclude from reports',
  edit: 'Edit',
  none: 'Nothing',
}

/** A row swipe: a preference, or one the register binds itself and Settings does not offer. */
export type RowSwipe = SwipeAction | 'category'

/**
 * The review queue's swipes, whatever the preference: a finger moving right
 * accepts the row, one moving left files it somewhere else.
 */
export const REVIEW_QUEUE_SWIPE: { left: RowSwipe; right: RowSwipe } = {
  left: 'category',
  right: 'review',
}

export function asSwipeAction(value: string | null | undefined, fallback: SwipeAction): SwipeAction {
  for (const action of SWIPE_ACTIONS) {
    if (action === value) return action
  }
  return fallback
}

export interface SwipeIntent {
  action: RowSwipe
  /** Empty when nothing happens. */
  label: string
  /** `positive` is green, for accepting; undoing one is `neutral`, or "Unreview" would read as approval. */
  tone: 'positive' | 'neutral'
}

/** What this direction would do to this row: the lane is the only warning before an irreversible approve. */
export function swipeIntent(action: RowSwipe, txn: Transaction): SwipeIntent {
  switch (action) {
    case 'menu':
      return { action, label: 'Menu', tone: 'neutral' }
    case 'review':
      if (txn.suggestion) return { action, label: 'Approve', tone: 'positive' }
      return txn.is_reviewed
        ? { action, label: 'Unreview', tone: 'neutral' }
        : { action, label: 'Reviewed', tone: 'positive' }
    case 'flag':
      return { action, label: txn.user_flag === null ? 'Flag' : 'Unflag', tone: 'neutral' }
    case 'exclude':
      return {
        action,
        label: txn.excluded_from_reports ? 'Include' : 'Exclude',
        tone: 'neutral',
      }
    case 'edit':
      return { action, label: 'Edit', tone: 'neutral' }
    case 'category':
      return { action, label: 'Category', tone: 'neutral' }
    case 'none':
      return { action, label: '', tone: 'neutral' }
  }
}

/** Structural rather than `RegisterActions`, so this module stays out of the component tree. */
export interface SwipeHandlers {
  openMenu: (txn: Transaction) => void
  openDetail: (txn: Transaction) => void
  openReview: (txn: Transaction) => void
  /** One category for the row: the proposed one when something is waiting, otherwise its own. */
  pickCategory: (txn: Transaction) => void
  setReviewed: (txn: Transaction, reviewed: boolean) => void
  applySuggestion: (txn: Transaction) => void
  edit: (
    txn: Transaction,
    patch: Record<string, unknown>,
    optimistic: Partial<Transaction>,
  ) => void
}

export function runSwipeAction(
  action: RowSwipe,
  txn: Transaction,
  handlers: SwipeHandlers,
): void {
  switch (action) {
    case 'menu':
      handlers.openMenu(txn)
      return
    case 'review':
      // Approving a waiting proposal also marks the row reviewed on the server.
      if (txn.suggestion) handlers.applySuggestion(txn)
      else handlers.setReviewed(txn, !txn.is_reviewed)
      return
    case 'flag': {
      const next = txn.user_flag === null ? 'flagged' : null
      handlers.edit(txn, { user_flag: next }, { user_flag: next })
      return
    }
    case 'exclude': {
      const next = !txn.excluded_from_reports
      handlers.edit(txn, { excluded_from_reports: next }, { excluded_from_reports: next })
      return
    }
    case 'edit':
      handlers.openDetail(txn)
      return
    // A proposed split and a row already split are more than one category, so
    // they open where every part can be seen.
    case 'category':
      if (txn.suggestion) {
        if (suggestedCategoryId(txn.suggestion) === undefined) handlers.openReview(txn)
        else handlers.pickCategory(txn)
      } else if (txn.splits.length > 0) handlers.openDetail(txn)
      else handlers.pickCategory(txn)
      return
    case 'none':
  }
}
