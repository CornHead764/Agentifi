/**
 * The state a donut and its legend share, since they are siblings in the
 * page. `DonutGroup` provides it; a `Donut` with no provider keeps its own
 * copy. Its own module for Fast Refresh.
 */

import { clsx } from 'clsx'
import { createContext, useContext, useState, type KeyboardEvent } from 'react'

import type { Money as MoneyValue } from '@/lib/money'

export interface DonutSlice {
  key: string
  label: string
  value: MoneyValue
  color: string
  /**
   * What clicking this slice does, worded to be read out ("Show Groceries in
   * the register"). Absent means the slice is not a target, as for a folded
   * "Everything else".
   */
  action?: string
}

export interface DonutFocus {
  /** The slice the pointer, or the keyboard, is on. */
  active: string | null
  hover: (key: string | null) => void
  select?: (slice: DonutSlice) => void
}

export const DonutFocusContext = createContext<DonutFocus | null>(null)

export function useDonutFocus(): DonutFocus {
  const shared = useContext(DonutFocusContext)
  // Always called, never conditionally: a donut mounted on its own still pops
  // its slices, it just has nothing to keep in step with.
  const [own, setOwn] = useState<string | null>(null)
  return shared ?? { active: own, hover: setOwn }
}

function isTarget(focus: DonutFocus, slice: Pick<DonutSlice, 'action'>): boolean {
  return slice.action !== undefined && focus.select !== undefined
}

/**
 * One legend row's classes and handlers, for screens that draw their own
 * legend. The row is itself an ARIA button, because the rows are grid items
 * and a `<button>` between would break the columns; Enter and Space are
 * handled as the role promises.
 */
export function useSliceRowProps(slice: DonutSlice, className: string) {
  const focus = useDonutFocus()
  const target = isTarget(focus, slice)
  const activate = () => {
    if (target) focus.select?.(slice)
  }
  return {
    className: clsx(
      className,
      focus.active === slice.key && `${className}--on`,
      target && `${className}--target`,
    ),
    onMouseEnter: () => focus.hover(slice.key),
    onMouseLeave: () => focus.hover(null),
    ...(target
      ? {
          role: 'button' as const,
          tabIndex: 0,
          'aria-label': slice.action,
          onFocus: () => focus.hover(slice.key),
          onBlur: () => focus.hover(null),
          onClick: activate,
          onKeyDown: (event: KeyboardEvent<HTMLElement>) => {
            if (event.key !== 'Enter' && event.key !== ' ') return
            event.preventDefault()
            activate()
          },
        }
      : {}),
  }
}
