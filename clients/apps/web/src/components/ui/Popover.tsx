import { clsx } from 'clsx'
import { Popover as Radix } from 'radix-ui'
import { useCallback, useEffect, useRef, useState, type ComponentProps } from 'react'

import { POPUP_INSET } from './popup-inset'

export const Popover = Radix.Root

export const PopoverTrigger = Radix.Trigger

export interface PopoverContentProps extends ComponentProps<typeof Radix.Content> {
  /** Drop the padding, for panels that draw their own header and rows. */
  flush?: boolean
}

type Side = NonNullable<PopoverContentProps['side']>

const SIDES: readonly string[] = ['top', 'right', 'bottom', 'left'] satisfies Side[]

const isSide = (value: string | undefined): value is Side =>
  value !== undefined && SIDES.includes(value)

/** Where the panel was first placed: a side of its trigger, and its offsets from it. */
interface Placement {
  side: Side
  alignOffset: number
  sideOffset: number
}

/**
 * `undefined` while the panel is not yet drawn `sideOffset` clear of its
 * trigger, which on a loaded machine can be some frames after Radix reports
 * it placed; `null` when there is no trigger to hold it against.
 */
function measure(
  content: HTMLElement | null,
  sideOffset: number,
  inset: number,
): Placement | null | undefined {
  // The wrapper carries the position; the content itself may be mid-way
  // through its opening scale.
  const wrapper = content?.parentElement
  const side = content?.dataset.side
  const trigger =
    content && document.querySelector(`[aria-controls="${CSS.escape(content.id)}"]`)
  if (!wrapper || !trigger || !isSide(side)) return null
  const at = wrapper.getBoundingClientRect()
  const from = trigger.getBoundingClientRect()
  const clear = {
    top: from.top - at.bottom,
    bottom: at.top - from.bottom,
    left: from.left - at.right,
    right: at.left - from.right,
  }[side]
  if (Math.abs(clear - sideOffset) > 1) return undefined
  const across = side === 'top' || side === 'bottom'
  // Neither side had room for the whole panel, so it runs past the window's
  // edge on the side it took: it slides back over its trigger by as much.
  const past =
    side === 'bottom'
      ? at.bottom - (document.documentElement.clientHeight - inset)
      : side === 'top'
        ? inset - at.top
        : 0
  return {
    side,
    alignOffset: across ? at.left - from.left : at.top - from.top,
    sideOffset: sideOffset - Math.max(0, Math.ceil(past)),
  }
}

/**
 * The panel opens at its own size, on whichever side of its trigger has room
 * for it; when neither side has, it covers part of its trigger rather than
 * shrinking. Only a window shorter than the panel shortens it, and then the
 * panel's own scrolling part takes the difference.
 *
 * It is placed once, on open, clear of the screen's edges, and then
 * held there against its trigger: it follows the trigger as the page scrolls
 * under it, hides while the trigger is scrolled out of view, and keeps the
 * side it opened on however its content grows or shrinks. Only a change of
 * the window's width places it again.
 */
export function PopoverContent({
  flush = false,
  className,
  sideOffset = 6,
  align = 'end',
  side,
  alignOffset,
  avoidCollisions = true,
  collisionPadding = POPUP_INSET,
  onWheel,
  onTouchMove,
  ...props
}: PopoverContentProps) {
  const ref = useRef<HTMLDivElement>(null)
  const inset = typeof collisionPadding === 'number' ? collisionPadding : POPUP_INSET
  const [held, setHeld] = useState<Placement | null>(null)
  const [settled, setSettled] = useState(false)
  const frame = useRef(0)
  const hold = useCallback(() => {
    // Two frames on: Radix reports a placement before its last adjustment lands.
    const attempt = (tries: number) => {
      cancelAnimationFrame(frame.current)
      frame.current = requestAnimationFrame(() => {
        frame.current = requestAnimationFrame(() => {
          const placement = measure(ref.current, sideOffset, inset)
          if (placement === undefined && tries > 0) {
            attempt(tries - 1)
            return
          }
          setHeld(placement ?? null)
          setSettled(true)
        })
      })
    }
    attempt(10)
  }, [sideOffset, inset])
  // Popper's `onPlaced`, which Popover passes through but leaves out of its
  // types. It runs once, after the first placement.
  const placed = { onPlaced: hold }

  useEffect(() => {
    let width = window.innerWidth
    const onResize = () => {
      if (window.innerWidth === width) return
      width = window.innerWidth
      setHeld(null)
      hold()
    }
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('resize', onResize)
      cancelAnimationFrame(frame.current)
    }
  }, [hold])

  return (
    <Radix.Portal>
      {/* An open dialog's scroll lock listens on the document and cancels every
          wheel and touch move outside the dialog's own DOM, which a popover
          portalled to the body is. Stopping them here keeps a popover opened
          from a dialog scrollable; `.popover` contains the overscroll. */}
      <Radix.Content
        ref={ref}
        className={clsx('popover', flush && 'popover--flush', className)}
        sideOffset={held?.sideOffset ?? sideOffset}
        side={held?.side ?? side}
        align={held === null ? align : 'start'}
        alignOffset={held?.alignOffset ?? alignOffset}
        avoidCollisions={held === null && avoidCollisions}
        collisionPadding={collisionPadding}
        data-placed={settled ? '' : undefined}
        hideWhenDetached
        onWheel={(event) => {
          event.stopPropagation()
          onWheel?.(event)
        }}
        onTouchMove={(event) => {
          event.stopPropagation()
          onTouchMove?.(event)
        }}
        {...placed}
        {...props}
      />
    </Radix.Portal>
  )
}
