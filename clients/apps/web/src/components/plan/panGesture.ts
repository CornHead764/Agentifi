/** How far a pointer may travel and still count as a tap, in CSS pixels. */
export const TAP_SLOP = 6

/**
 * Whether a pointer that has moved (dx, dy) from where it went down is still
 * a tap or has become a pan. Only horizontal travel pans: the chart scrolls
 * sideways, and a vertical drag belongs to the page.
 */
export function classifyPointer(dx: number, dy: number, slop = TAP_SLOP): 'tap' | 'pan' {
  return Math.abs(dx) > slop && Math.abs(dx) >= Math.abs(dy) ? 'pan' : 'tap'
}

/**
 * The scroll position that puts `focus` (an x in the canvas, in pixels) in the
 * middle of the viewport, held to what the canvas can scroll.
 */
export function centredScroll(focus: number, viewport: number, canvas: number): number {
  const most = Math.max(0, canvas - viewport)
  return Math.min(most, Math.max(0, focus - viewport / 2))
}

/**
 * The canvas width: the viewport's, unless the bubbles would shrink below
 * `minScale` of their drawn size to fit it, in which case they keep that scale
 * and the overflow is panned.
 */
export function canvasWidth(viewport: number, frameWidth: number, minScale: number): number {
  return Math.max(viewport, frameWidth * minScale)
}
