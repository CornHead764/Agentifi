import { describe, expect, it, vi } from 'vitest'

import { transaction } from '@/test/builders'

import {
  REVIEW_QUEUE_SWIPE,
  asSwipeAction,
  beginsSwipe,
  cancelsLongPress,
  isScrollGesture,
  runSwipeAction,
  swipeDecision,
  swipeIntent,
  swipeOffset,
  type SwipeHandlers,
} from './gestures'

function handlers(): SwipeHandlers & Record<keyof SwipeHandlers, ReturnType<typeof vi.fn>> {
  return {
    openMenu: vi.fn(),
    openDetail: vi.fn(),
    openReview: vi.fn(),
    pickCategory: vi.fn(),
    setReviewed: vi.fn(),
    applySuggestion: vi.fn(),
    edit: vi.fn(),
  } as never
}

describe('the long press', () => {
  it('survives the wander of a thumb being held still', () => {
    expect(cancelsLongPress({ dx: 3, dy: -4, elapsedMs: 300 })).toBe(false)
  })

  it('gives up as soon as the finger commits to going somewhere', () => {
    expect(cancelsLongPress({ dx: 20, dy: 0, elapsedMs: 120 })).toBe(true)
    expect(cancelsLongPress({ dx: 0, dy: -30, elapsedMs: 120 })).toBe(true)
  })
})

describe('telling a swipe from a scroll', () => {
  // The vertical axis wins ties: a diagonal is the page scrolling.
  it('treats a mostly-vertical drag as the page moving', () => {
    expect(isScrollGesture({ dx: 12, dy: 40, elapsedMs: 100 })).toBe(true)
    expect(beginsSwipe({ dx: 12, dy: 40, elapsedMs: 100 })).toBe(false)
  })

  it('starts a swipe once the finger is clearly sideways', () => {
    expect(beginsSwipe({ dx: -24, dy: 3, elapsedMs: 100 })).toBe(true)
  })

  it('moves nothing under a tap', () => {
    expect(beginsSwipe({ dx: 4, dy: 2, elapsedMs: 60 })).toBe(false)
  })
})

describe('deciding what a release does', () => {
  const WIDTH = 360

  it('performs the action past a share of the row, whichever way', () => {
    expect(swipeDecision({ dx: -160, dy: 4, elapsedMs: 900 }, WIDTH)).toBe('left')
    expect(swipeDecision({ dx: 160, dy: 4, elapsedMs: 900 }, WIDTH)).toBe('right')
  })

  it('snaps back from a short, slow drag', () => {
    expect(swipeDecision({ dx: -60, dy: 4, elapsedMs: 900 }, WIDTH)).toBeNull()
  })

  it('takes a fast flick that never travelled far', () => {
    expect(swipeDecision({ dx: -60, dy: 4, elapsedMs: 90 }, WIDTH)).toBe('left')
  })

  it('refuses a drag that was really a scroll', () => {
    expect(swipeDecision({ dx: -160, dy: 300, elapsedMs: 200 }, WIDTH)).toBeNull()
  })

  it('scales with the row, so the same gesture means the same thing', () => {
    expect(swipeDecision({ dx: -120, dy: 0, elapsedMs: 900 }, 360)).toBeNull()
    expect(swipeDecision({ dx: -120, dy: 0, elapsedMs: 900 }, 280)).toBe('left')
  })

  it('stops drawing the row past the lane the label sits in', () => {
    expect(swipeOffset(400)).toBe(132)
    expect(swipeOffset(-400)).toBe(-132)
    expect(swipeOffset(-40)).toBe(-40)
  })
})

describe('what a direction says it will do to this row', () => {
  it('offers to approve a waiting proposal rather than tick past it', () => {
    expect(swipeIntent('review', transaction({ suggestion: { action_id: 'p1' } as never }))).toEqual({
      action: 'review',
      label: 'Approve',
      tone: 'positive',
    })
  })

  it('turns into an undo on a row that is already reviewed, and stops being green', () => {
    expect(swipeIntent('review', transaction({ is_reviewed: true }))).toEqual({
      action: 'review',
      label: 'Unreview',
      tone: 'neutral',
    })
    expect(swipeIntent('review', transaction())).toEqual({
      action: 'review',
      label: 'Reviewed',
      tone: 'positive',
    })
  })

  it('names the toggles by what they would do next', () => {
    expect(swipeIntent('flag', transaction()).label).toBe('Flag')
    expect(swipeIntent('flag', transaction({ user_flag: 'flagged' })).label).toBe('Unflag')
    expect(swipeIntent('exclude', transaction()).label).toBe('Exclude')
    expect(swipeIntent('exclude', transaction({ excluded_from_reports: true })).label).toBe('Include')
    expect(swipeIntent('none', transaction()).label).toBe('')
  })
})

describe('the preference to the action it runs', () => {
  it('approves a proposal and ticks a row without one', () => {
    const spies = handlers()
    const waiting = transaction({ suggestion: { action_id: 'p1' } as never })
    runSwipeAction('review', waiting, spies)
    expect(spies.applySuggestion).toHaveBeenCalledWith(waiting)
    expect(spies.setReviewed).not.toHaveBeenCalled()

    const plain = transaction()
    runSwipeAction('review', plain, spies)
    expect(spies.setReviewed).toHaveBeenCalledWith(plain, true)
  })

  it('unticks a reviewed row rather than writing the flag again', () => {
    const spies = handlers()
    const row = transaction({ is_reviewed: true })
    runSwipeAction('review', row, spies)
    expect(spies.setReviewed).toHaveBeenCalledWith(row, false)
  })

  it('toggles the two flags through the ordinary edit path', () => {
    const spies = handlers()
    const row = transaction()
    runSwipeAction('flag', row, spies)
    expect(spies.edit).toHaveBeenCalledWith(row, { user_flag: 'flagged' }, { user_flag: 'flagged' })

    runSwipeAction('exclude', row, spies)
    expect(spies.edit).toHaveBeenCalledWith(
      row,
      { excluded_from_reports: true },
      { excluded_from_reports: true },
    )

    const excluded = transaction({ excluded_from_reports: true, user_flag: 'flagged' })
    runSwipeAction('flag', excluded, spies)
    expect(spies.edit).toHaveBeenCalledWith(excluded, { user_flag: null }, { user_flag: null })
    runSwipeAction('exclude', excluded, spies)
    expect(spies.edit).toHaveBeenCalledWith(
      excluded,
      { excluded_from_reports: false },
      { excluded_from_reports: false },
    )
  })

  it('opens the menu, opens the row, and does nothing at all', () => {
    const spies = handlers()
    const row = transaction()
    runSwipeAction('menu', row, spies)
    expect(spies.openMenu).toHaveBeenCalledWith(row)

    runSwipeAction('edit', row, spies)
    expect(spies.openDetail).toHaveBeenCalledWith(row)

    runSwipeAction('none', row, spies)
    expect(spies.openMenu).toHaveBeenCalledTimes(1)
    expect(spies.openDetail).toHaveBeenCalledTimes(1)
    expect(spies.edit).not.toHaveBeenCalled()
  })

  it('falls back to the default for a name this register has no branch for', () => {
    expect(asSwipeAction('flag', 'menu')).toBe('flag')
    expect(asSwipeAction('explode', 'menu')).toBe('menu')
    expect(asSwipeAction(null, 'review')).toBe('review')
    expect(asSwipeAction(undefined, 'review')).toBe('review')
  })
})

describe('the review queue', () => {
  it('accepts the row moving right and files it elsewhere moving left', () => {
    expect(REVIEW_QUEUE_SWIPE).toEqual({ left: 'category', right: 'review' })
    expect(swipeIntent('category', transaction())).toEqual({
      action: 'category',
      label: 'Category',
      tone: 'neutral',
    })
  })

  it('picks one category for a plain row and for a row proposing one', () => {
    const spies = handlers()
    const plain = transaction()
    runSwipeAction('category', plain, spies)
    expect(spies.pickCategory).toHaveBeenCalledWith(plain)

    const proposing = transaction({
      suggestion: { action_id: 'p1', tool: 'update_transaction', category_id: 'c2' } as never,
    })
    runSwipeAction('category', proposing, spies)
    expect(spies.pickCategory).toHaveBeenCalledWith(proposing)
    expect(spies.applySuggestion).not.toHaveBeenCalled()
  })

  it('opens a proposed split for review and a split row in full', () => {
    const spies = handlers()
    const proposedSplit = transaction({ suggestion: { action_id: 'p1', tool: 'split_transaction' } as never })
    runSwipeAction('category', proposedSplit, spies)
    expect(spies.openReview).toHaveBeenCalledWith(proposedSplit)

    const split = transaction({ splits: [{} as never, {} as never] })
    runSwipeAction('category', split, spies)
    expect(spies.openDetail).toHaveBeenCalledWith(split)
    expect(spies.pickCategory).not.toHaveBeenCalled()
  })
})
