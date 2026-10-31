/**
 * `list` with `item` in it when `on`, and without it otherwise; `on` left out
 * flips it. The order is kept and nothing is listed twice.
 */
export function toggled<T>(list: readonly T[], item: T, on: boolean = !list.includes(item)): T[] {
  if (!on) return list.filter((one) => one !== item)
  return list.includes(item) ? [...list] : [...list, item]
}

/** `toggled` for a set, returning a new one. */
export function toggledSet<T>(set: ReadonlySet<T>, item: T, on: boolean = !set.has(item)): Set<T> {
  const next = new Set(set)
  if (on) next.add(item)
  else next.delete(item)
  return next
}
