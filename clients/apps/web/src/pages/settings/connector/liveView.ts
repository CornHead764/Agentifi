/** Where a click on the drawn page lands on the page itself, in the page's own pixels. */
export function pagePixels(
  drawn: { left: number; top: number; width: number; height: number },
  page: { width: number; height: number },
  click: { x: number; y: number },
): { x: number; y: number } | null {
  if (drawn.width === 0 || drawn.height === 0 || page.width === 0 || page.height === 0) return null
  return {
    x: Math.round(((click.x - drawn.left) * page.width) / drawn.width),
    y: Math.round(((click.y - drawn.top) * page.height) / drawn.height),
  }
}
