/**
 * The interface scales from `html { font-size }`, but the chart library sizes
 * in pixels from JavaScript; `scaled` converts a design-scale pixel size.
 */

/** Read once: the root size cannot change without a reload. */
let factor: number | null = null

function rootScale(): number {
  // Not remembered, so the first real measurement in a browser still counts.
  if (typeof document === 'undefined') return 1
  if (factor === null) {
    const root = parseFloat(getComputedStyle(document.documentElement).fontSize)
    factor = Number.isFinite(root) && root > 0 ? root / 16 : 1
  }
  return factor
}

export function scaled(px: number): number {
  return Math.round(px * rootScale())
}

/** `2.125rem` at a root of `rootPx`, in pixels; 0 for anything that is not rem. */
export function remToPx(value: string, rootPx: number): number {
  const rem = /^(\d+(?:\.\d+)?)rem$/.exec(value.trim())
  return rem ? Math.round(Number.parseFloat(rem[1]) * rootPx) : 0
}

/**
 * A rem length token from `styles/tokens.css`, in pixels at the root size in
 * force now. 0 where no stylesheet is loaded: a server render.
 */
export function tokenPx(name: string): number {
  if (typeof document === 'undefined') return 0
  const root = getComputedStyle(document.documentElement)
  return remToPx(root.getPropertyValue(name), Number.parseFloat(root.fontSize) || 16)
}
