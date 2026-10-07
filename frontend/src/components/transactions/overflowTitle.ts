import type { MouseEvent } from 'react'

/**
 * Puts a cell's whole text in its title on hover, only when it is clipped.
 * Measured on pointer entry rather than on render, because a layout read per
 * cell per frame is what virtualization avoids.
 */
export function titleWhenClipped(event: MouseEvent<HTMLElement>): void {
  const element = event.currentTarget
  if (element.scrollWidth > element.clientWidth) {
    element.title = element.textContent ?? ''
  } else {
    element.removeAttribute('title')
  }
}
