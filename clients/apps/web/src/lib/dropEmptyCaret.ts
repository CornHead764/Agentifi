/**
 * Clears the collapsed selection a click on non-editable text leaves, which
 * caret browsing paints as a caret that then eats every key. Real ranges are
 * untouched. On `mouseup`, not `click`: by the time a label's click forwards
 * to its control, the caret is already drawn.
 */

const EDITABLE = 'input, textarea, select, [contenteditable]'

export interface CaretPress {
  /** Shift-click extends from a caret somebody placed deliberately. */
  shiftKey: boolean
  inEditable: boolean
  collapsed: boolean
  hasRange: boolean
}

export function keepsItsCaret(press: CaretPress): boolean {
  if (press.shiftKey || press.inEditable) return true
  return !press.hasRange || !press.collapsed
}

function dropEmptyCaret(event: MouseEvent): void {
  const selection = window.getSelection()
  if (selection === null) return

  const target = event.target
  const inEditable =
    target !== null &&
    typeof target === 'object' &&
    'closest' in target &&
    typeof target.closest === 'function' &&
    target.closest(EDITABLE) !== null

  const press: CaretPress = {
    shiftKey: event.shiftKey,
    inEditable,
    collapsed: selection.isCollapsed,
    hasRange: selection.rangeCount > 0,
  }
  if (keepsItsCaret(press)) return
  selection.removeAllRanges()
}

export function watchForEmptyCarets(): () => void {
  document.addEventListener('mouseup', dropEmptyCaret)
  return () => document.removeEventListener('mouseup', dropEmptyCaret)
}
