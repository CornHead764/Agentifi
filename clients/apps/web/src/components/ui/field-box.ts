import type { MouseEvent } from 'react'

/* What already answers a press of its own: the field itself, and any control
   sitting in the box beside it — a clear button, a Shortcuts popover. */
const OWN_CONTROLS = 'input, textarea, select, a, button, [role="button"]'

/**
 * Clicking the box focuses the field inside it: the wrapper draws most of what
 * reads as the box, and a press on the icon or padding otherwise leaves the
 * keyboard elsewhere.
 *
 * `onMouseDown` with the default prevented, rather than `onClick`: by click the
 * browser has already moved focus, and the caret would land at the end of the
 * text rather than where the press was.
 */
export function focusFieldOnPress(event: MouseEvent<HTMLElement>) {
  const pressed = event.target
  if (!(pressed instanceof Element)) return
  if (pressed.closest(OWN_CONTROLS) !== null) return
  const field = event.currentTarget.querySelector<HTMLElement>('input, textarea')
  if (field === null) return
  event.preventDefault()
  field.focus()
}
