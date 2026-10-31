/* Where the browser's own menu is what the person came for: a link to open,
   a field to paste into. */
const NATIVE_MENU_TARGETS = 'a, input, textarea, select, [contenteditable]:not([contenteditable="false"])'

export interface PointerMenuEvent {
  target: unknown
  clientX: number
  clientY: number
  preventDefault: () => void
}

export interface PointerMenuHost {
  selectionIsCollapsed: () => boolean
}

function hasClosest(target: unknown): target is { closest: (selector: string) => unknown } {
  return typeof target === 'object' && target !== null && 'closest' in target && typeof target.closest === 'function'
}

const BROWSER: PointerMenuHost = {
  selectionIsCollapsed: () => window.getSelection()?.isCollapsed !== false,
}

/**
 * Answer a right-click on a register row by opening the row's menu at the
 * pointer, unless the browser's menu is what the click was for (a field, a
 * link, selected text). Returns whether the menu was taken over.
 */
export function openRowMenuAtPointer(
  event: PointerMenuEvent,
  open: (point: { x: number; y: number }) => void,
  host: PointerMenuHost = BROWSER,
): boolean {
  const pressed = event.target
  if (!hasClosest(pressed)) return false
  if (pressed.closest(NATIVE_MENU_TARGETS) !== null) return false
  if (!host.selectionIsCollapsed()) return false
  event.preventDefault()
  open({ x: event.clientX, y: event.clientY })
  return true
}
