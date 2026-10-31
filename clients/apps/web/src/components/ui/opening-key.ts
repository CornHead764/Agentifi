import { useState } from 'react'

/**
 * How many times a dialog has opened, for keying the form inside it: each
 * opening starts from its props as they are then, while closing keeps the
 * same form so the close animation has something to animate.
 */
export interface Openings {
  open: boolean
  count: number
}

export const NEVER_OPENED: Openings = { open: false, count: 0 }

export function nextOpenings(previous: Openings, open: boolean): Openings {
  if (open === previous.open) return previous
  return { open, count: open ? previous.count + 1 : previous.count }
}

/**
 * A key that changes each time `open` turns true, and only then. Zero until
 * the first opening; a dialog mounted open is on its first.
 */
export function useOpeningKey(open: boolean): number {
  const [openings, setOpenings] = useState(NEVER_OPENED)
  const next = nextOpenings(openings, open)
  if (next !== openings) setOpenings(next)
  return next.count
}

/**
 * The last non-null value, for a dialog opened on a target and closed by
 * clearing it: the content keeps drawing the target while it animates out.
 */
export function useHeld<T>(value: T | null): T | null {
  const [held, setHeld] = useState(value)
  if (value !== null && value !== held) setHeld(value)
  return value ?? held
}
