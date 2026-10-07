/**
 * Opens the context menu enclosing `element`, anchored to its corner.
 *
 * Radix's context-menu trigger keeps the pointer position in state of its own
 * and takes no prop for it, so a button that wants to open the menu dispatches
 * the event the trigger already listens for. Bubbling carries it: the button
 * sits inside the trigger, and React's listener above both.
 *
 * This is how a region whose actions live on a context menu still answers the
 * keyboard — the platform's own context-menu key fires `contextmenu` on the
 * focused element, and Enter on the button comes through here.
 */
export function openContextMenuAt(element: HTMLElement | null) {
  if (element === null) return
  const box = element.getBoundingClientRect()
  element.dispatchEvent(
    new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
      clientX: box.right,
      clientY: box.bottom,
    }),
  )
}
