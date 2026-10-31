/**
 * Move one entry of an ordered list by sending the whole order back: the
 * server refuses a partial order. Returns nothing when the move would fall off
 * either end, re-checking what the disabled arrow already says.
 */
export function movedOrder(
  entries: readonly { id: string }[],
  index: number,
  by: number,
): string[] | null {
  const target = index + by
  if (target < 0 || target >= entries.length) return null
  const next = [...entries]
  const [moved] = next.splice(index, 1)
  next.splice(target, 0, moved)
  return next.map((entry) => entry.id)
}
