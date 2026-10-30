/**
 * Range selection over a list of checkboxes. A click on a box sets the anchor;
 * a shift-click carries the clicked box's new state to every box from the
 * anchor to it, inclusive, in the order the list is drawn.
 */

/**
 * What one click reaches: the items from `anchor` to `item` in `order` when
 * `extend` and both are drawn, and `[item]` otherwise, so a shift-click with no
 * anchor on screen is a plain click.
 */
export function clickRange<T>(
  order: readonly T[],
  anchor: T | null,
  item: T,
  extend: boolean,
): T[] {
  const to = order.indexOf(item)
  const from = extend && anchor !== null ? order.indexOf(anchor) : -1
  if (from < 0 || to < 0) return [item]
  return order.slice(Math.min(from, to), Math.max(from, to) + 1)
}

/** `selected` after a click on `item`: its range set to the state `item` goes to. */
export function rangeToggled<T>(
  order: readonly T[],
  selected: ReadonlySet<T>,
  anchor: T | null,
  item: T,
  extend: boolean,
): Set<T> {
  const on = !selected.has(item)
  const next = new Set(selected)
  for (const one of clickRange(order, anchor, item, extend)) {
    if (on) next.add(one)
    else next.delete(one)
  }
  return next
}
