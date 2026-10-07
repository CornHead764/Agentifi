import type { FormEvent } from 'react'

/**
 * A dialog's submit is its own. React bubbles a submit through the portal to
 * whatever dialog opened this one, so without the stop a dialog opened from
 * inside another form — a new account from the goal editor — would save that
 * form as well.
 */
export function submitOwnForm<E extends Pick<FormEvent, 'preventDefault' | 'stopPropagation'>>(
  event: E,
  onSubmit: (event: E) => void,
): void {
  event.preventDefault()
  event.stopPropagation()
  onSubmit(event)
}
