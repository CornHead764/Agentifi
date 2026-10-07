import { CircleCheck, EyeOff, Flag, FolderInput, MoreHorizontal, Pencil, Sparkles } from 'lucide-react'
import { useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'

import {
  LONG_PRESS_MS,
  beginsSwipe,
  cancelsLongPress,
  isScrollGesture,
  runSwipeAction,
  swipeDecision,
  swipeDirection,
  swipeIntent,
  swipeOffset,
  type Drag,
  type RowSwipe,
  type SwipeDirection,
  type SwipeIntent,
} from '@/lib/transactions/gestures'
import { suggestedCategoryId } from '@/lib/transactions/suggestions'
import type { Transaction } from '@/lib/transactions/types'

import { CategoryPicker } from './Pickers'
import { TwoLineRow } from './RegisterCells'
import { useRegisterView, type RowMenuControl, type SwipeBindings } from './register-context'

/** What was recorded when the finger went down, and what it has become since. */
interface Press {
  id: number
  x: number
  y: number
  at: number
  /** Set once the finger has committed sideways: the row is moving now. */
  swiping: boolean
  /** Set by either gesture. The click that follows the release is not an open. */
  handled: boolean
  timer: ReturnType<typeof setTimeout> | null
}

/**
 * A register row on a phone: a tap opens it, a long press starts selection,
 * and a swipe each way runs the action the user chose in Settings, Display.
 * Pointer events, so a mouse drives the same code. The decisions are in
 * lib/transactions/gestures.
 */
export function PhoneRow({
  txn,
  menu,
}: {
  txn: Transaction
  /** The row's menu, rendered against an anchor inside the row — see RowMenuControl. */
  menu?: (control: RowMenuControl) => ReactNode
}) {
  const { actions, lookups, selection, swipe } = useRegisterView()
  const [offset, setOffset] = useState(0)
  // Kept apart from the offset so the lane is painted from the first pixel and
  // does not flicker as the offset passes through zero.
  const [sliding, setSliding] = useState<SwipeDirection | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const [pickingCategory, setPickingCategory] = useState(false)

  const press = useRef<Press | null>(null)
  // The row is a button: without this the click after a swipe would also open
  // the transaction.
  const swallowClick = useRef(false)

  function clearTimer() {
    const active = press.current
    if (active?.timer) {
      clearTimeout(active.timer)
      active.timer = null
    }
  }

  function settle() {
    setOffset(0)
    setSliding(null)
  }

  function end(handled: boolean) {
    clearTimer()
    press.current = null
    swallowClick.current = handled
    settle()
  }

  function perform(which: SwipeDirection) {
    runSwipeAction(swipe[which], txn, {
      openMenu: () => setMenuOpen(true),
      openDetail: actions.openDetail,
      openReview: actions.openReview,
      pickCategory: () => setPickingCategory(true),
      setReviewed: actions.setReviewed,
      applySuggestion: (row) => actions.applySuggestion(row),
      edit: actions.edit,
    })
  }

  function onPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
    // A second finger landing mid-gesture would drag the row sideways on its
    // way past; the first one owns the row until it lifts.
    if (event.button !== 0 || press.current !== null) return
    swallowClick.current = false
    const timer = setTimeout(() => {
      const active = press.current
      if (active === null || active.swiping) return
      active.timer = null
      active.handled = true
      selection.begin(txn.id)
    }, LONG_PRESS_MS)
    press.current = {
      id: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      at: Date.now(),
      swiping: false,
      handled: false,
      timer,
    }
  }

  function onPointerMove(event: ReactPointerEvent<HTMLDivElement>) {
    const active = press.current
    if (active === null || active.id !== event.pointerId) return
    const drag = dragFrom(active, event)

    if (!active.swiping) {
      if (cancelsLongPress(drag)) clearTimer()
      // The page is scrolling under the finger. The row lets go of it rather
      // than competing, which is what `touch-action: pan-y` asks for.
      if (isScrollGesture(drag)) {
        end(false)
        return
      }
      if (!beginsSwipe(drag)) return
      // Nothing bound this way: leave the row alone rather than sliding it
      // aside to reveal an empty lane.
      if (boundAction(swipe, drag) === 'none') return
      active.swiping = true
      active.handled = true
      // From here the finger belongs to this row even once it leaves it, which
      // is what stops a fast swipe stranding the row half open.
      event.currentTarget.setPointerCapture(event.pointerId)
    }

    setSliding(swipeDirection(drag.dx))
    setOffset(swipeOffset(drag.dx))
  }

  function onPointerUp(event: ReactPointerEvent<HTMLDivElement>) {
    const active = press.current
    if (active === null || active.id !== event.pointerId) return
    if (active.swiping) {
      const drag = dragFrom(active, event)
      const decided = swipeDecision(drag, event.currentTarget.getBoundingClientRect().width)
      if (decided !== null) perform(decided)
    }
    end(active.handled)
  }

  const intent = sliding === null ? null : swipeIntent(swipe[sliding], txn)

  return (
    <div
      className="txn-swipe"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={() => end(false)}
      // A long press on a touch screen raises the browser's own selection
      // handles and context menu over the top of the one being opened.
      onContextMenu={(event) => {
        if (press.current !== null) event.preventDefault()
      }}
      onClickCapture={(event) => {
        if (swallowClick.current) {
          swallowClick.current = false
          event.preventDefault()
          event.stopPropagation()
          return
        }
        // While selection is on, the row is the checkbox.
        if (selection.active) {
          event.preventDefault()
          event.stopPropagation()
          selection.toggle(txn.id)
        }
      }}
    >
      {intent === null ? null : (
        <span
          className="txn-swipe__lane"
          data-side={sliding ?? undefined}
          data-tone={intent.tone}
          aria-hidden="true"
        >
          {swipeGlyph(intent, txn)}
          {intent.label}
        </span>
      )}
      <div
        className="txn-swipe__surface"
        data-held={offset === 0 ? undefined : true}
        style={offset === 0 ? undefined : { transform: `translateX(${offset}px)` }}
      >
        <TwoLineRow txn={txn} />
      </div>
      {menu?.({ open: menuOpen, onOpenChange: setMenuOpen, anchorOnly: true })}
      {pickingCategory ? (
        <CategoryPicker
          open
          onOpenChange={setPickingCategory}
          value={txn.suggestion ? (suggestedCategoryId(txn.suggestion) ?? null) : txn.category_id}
          categories={lookups.categories}
          frequentIds={lookups.frequentCategoryIds}
          // Choosing for a row with a proposal approves it with that category,
          // as the register's suggestion cell does.
          onChange={(next) =>
            txn.suggestion
              ? actions.applySuggestion(txn, next)
              : actions.edit(txn, { category_id: next }, { category_id: next })
          }
          trigger={<span className="row-menu__anchor" aria-hidden="true" />}
        />
      ) : null}
    </div>
  )
}

/** The icon the lane shows, which is the action's — except when approving. */
function swipeGlyph(intent: SwipeIntent, txn: Transaction): ReactNode {
  if (intent.action === 'review' && txn.suggestion) return <Sparkles size={16} />
  switch (intent.action) {
    case 'menu':
      return <MoreHorizontal size={16} />
    case 'review':
      return <CircleCheck size={16} />
    case 'flag':
      return <Flag size={16} />
    case 'exclude':
      return <EyeOff size={16} />
    case 'edit':
      return <Pencil size={16} />
    case 'category':
      return <FolderInput size={16} />
    case 'none':
      return null
  }
}

function dragFrom(start: Press, event: { clientX: number; clientY: number }): Drag {
  return {
    dx: event.clientX - start.x,
    dy: event.clientY - start.y,
    elapsedMs: Date.now() - start.at,
  }
}

function boundAction(swipe: SwipeBindings, drag: Drag): RowSwipe {
  const which = swipeDirection(drag.dx)
  return which === null ? 'none' : swipe[which]
}
