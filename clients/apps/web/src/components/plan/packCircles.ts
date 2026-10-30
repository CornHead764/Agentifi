export interface PackedCircle {
  /** Index into the input array, so the caller keeps its own labels and colours. */
  index: number
  x: number
  y: number
  r: number
}

/**
 * Lay circles out so none overlap, largest at the middle. Area, not radius, is
 * proportional to the value. Placement is a deterministic outward spiral, so
 * the same month renders the same way twice.
 */
export function packCircles(values: number[], size: number, gap = 3): PackedCircle[] {
  const total = values.reduce((sum, value) => sum + Math.max(value, 0), 0)
  if (total <= 0) return []

  // Leave a margin: a circle tangent to the viewBox edge is clipped by the
  // stroke, and the labels sit inside the circles.
  const budget = Math.PI * (size / 2.6) ** 2
  const order = values
    .map((value, index) => ({ index, value: Math.max(value, 0) }))
    .filter((entry) => entry.value > 0)
    .sort((a, b) => b.value - a.value)

  const placed: PackedCircle[] = []
  const centre = size / 2

  for (const entry of order) {
    const r = Math.sqrt(((entry.value / total) * budget) / Math.PI)
    const spot = findSpot(placed, r, centre, size, gap)
    placed.push({ index: entry.index, x: spot.x, y: spot.y, r })
  }

  return placed
}

function findSpot(
  placed: PackedCircle[],
  r: number,
  centre: number,
  size: number,
  gap: number,
): { x: number; y: number } {
  if (placed.length === 0) return { x: centre, y: centre }

  // Two passes: inside the box, then anywhere. A circle past the edge is a
  // rendering detail (the chart draws with overflow visible); one dropped onto
  // another is wrong data.
  for (const bounded of [true, false]) {
    const step = Math.max(r / 4, 2)
    // The unbounded pass may search past the box: a spot that must clear two
    // dominant circles can sit further from the centre than the box is wide.
    const reach = bounded ? size : size * 2
    for (let ring = 1; ring * step < reach; ring += 1) {
      const radius = ring * step
      // More samples on a wider ring, so the angular gap between candidates
      // stays roughly constant and a small circle cannot fall through it.
      const samples = Math.max(8, Math.ceil((2 * Math.PI * radius) / step))
      for (let sample = 0; sample < samples; sample += 1) {
        const angle = (sample / samples) * 2 * Math.PI
        const x = centre + radius * Math.cos(angle)
        const y = centre + radius * Math.sin(angle)
        if (bounded && (x - r < 0 || x + r > size || y - r < 0 || y + r > size)) continue
        if (placed.every((other) => clears(x, y, r, other, gap))) return { x, y }
      }
    }
  }
  return { x: centre, y: centre }
}

function clears(x: number, y: number, r: number, other: PackedCircle, gap: number): boolean {
  const dx = x - other.x
  const dy = y - other.y
  return Math.hypot(dx, dy) >= r + other.r + gap
}

export interface ExpandedCircles {
  /** The top-level circles, siblings displaced to make room. */
  parents: PackedCircle[]
  /** The open group's children; `index` points into the child values. */
  children: PackedCircle[]
}

/**
 * The layout after a group opens: the group stays where it was, its children
 * surface in a ring beside it, and the other groups yield.
 *
 * Children are sized by share of the group's own value, not of the children's
 * sum, which is smaller when most of the group was spent directly on it. A
 * floor keeps the smallest pressable.
 *
 * Placement is deterministic: each child takes the nearest clear spot around
 * the parent (clear of the parent and earlier children, not of siblings), then
 * a relaxation pass with the parent pinned makes the siblings slide outward.
 */
export function expandCircles(
  parents: readonly PackedCircle[],
  openIndex: number,
  childValues: number[],
  parentValue: number,
  size: number,
  gap = 3,
): ExpandedCircles {
  const parent = parents.find((circle) => circle.index === openIndex)
  const total = childValues.reduce((sum, value) => sum + Math.max(value, 0), 0)
  if (!parent || total <= 0) {
    return { parents: parents.map((circle) => ({ ...circle })), children: [] }
  }
  const whole = parentValue > 0 ? Math.max(parentValue, total) : total

  const order = childValues
    .map((value, index) => ({ index, value: Math.max(value, 0) }))
    .filter((entry) => entry.value > 0)
    .sort((a, b) => b.value - a.value)

  // Spawn on the parent's outward side, away from the middle of the cluster,
  // where the chart has room to grow.
  const outward = Math.atan2(parent.y - size / 2, parent.x - size / 2)

  const children: PackedCircle[] = []
  for (const entry of order) {
    const r = Math.max(parent.r * Math.sqrt(entry.value / whole), 14)
    const spot = orbit(parent, children, r, gap, outward)
    children.push({ index: entry.index, x: spot.x, y: spot.y, r })
  }

  const moved = parents.map((circle) => ({ ...circle }))
  relax(moved, children, openIndex, gap)
  return { parents: moved, children }
}

/** The first spot on a widening orbit of the parent that clears the children already placed. */
function orbit(
  parent: PackedCircle,
  placed: PackedCircle[],
  r: number,
  gap: number,
  startAngle: number,
): { x: number; y: number } {
  for (let ring = 0; ring < 40; ring += 1) {
    const radius = parent.r + r + gap + ring * Math.max(r / 2, 6)
    const samples = Math.max(10, Math.ceil((2 * Math.PI * radius) / Math.max(r, 8)))
    for (let sample = 0; sample < samples; sample += 1) {
      const angle = startAngle + (sample / samples) * 2 * Math.PI
      const x = parent.x + radius * Math.cos(angle)
      const y = parent.y + radius * Math.sin(angle)
      if (placed.every((other) => clears(x, y, r, other, gap))) return { x, y }
    }
  }
  return { x: parent.x, y: parent.y + parent.r + r + gap }
}

/**
 * Resolve overlaps by weight: the open parent never moves, children barely do,
 * siblings yield. Each pair splits its correction by weight; the loop stops at
 * the first pass with no overlap, bounded in case it never converges.
 */
function relax(parents: PackedCircle[], children: PackedCircle[], openIndex: number, gap: number) {
  const all = [
    ...parents.map((circle) => ({ circle, weight: circle.index === openIndex ? 0 : 1 })),
    ...children.map((circle) => ({ circle, weight: 0.15 })),
  ]

  for (let pass = 0; pass < 400; pass += 1) {
    // Late passes free the children too: a pile-up the gentle weights cannot
    // untangle beats a chart that settles overlapping.
    if (pass === 200) for (const one of all) if (one.weight > 0) one.weight = 1
    let overlapped = false
    for (let i = 0; i < all.length; i += 1) {
      for (let j = i + 1; j < all.length; j += 1) {
        const a = all[i]
        const b = all[j]
        const share = a.weight + b.weight
        if (share === 0) continue
        let dx = b.circle.x - a.circle.x
        let dy = b.circle.y - a.circle.y
        let distance = Math.hypot(dx, dy)
        if (distance === 0) {
          dx = 1
          dy = 0
          distance = 1
        }
        const overlap = a.circle.r + b.circle.r + gap - distance
        if (overlap <= 0) continue
        overlapped = true
        const nx = dx / distance
        const ny = dy / distance
        a.circle.x -= nx * overlap * (a.weight / share)
        a.circle.y -= ny * overlap * (a.weight / share)
        b.circle.x += nx * overlap * (b.weight / share)
        b.circle.y += ny * overlap * (b.weight / share)
      }
    }
    if (!overlapped) break
  }
}

/** Room between the outermost disc and the frame's edge, in viewBox units. */
const FRAME_PAD = 6

/**
 * The frame the chart draws in: the packed box, grown to take in every bubble
 * that has left it, so opened children scale the chart down rather than
 * leaving the card. Never below the base box; read off the live bodies, so it
 * follows the glide.
 */
export function fitFrame(
  bodies: readonly { x: number; y: number; r: number }[],
  size: number,
): { x: number; y: number; width: number; height: number } {
  let minX = 0
  let minY = 0
  let maxX = size
  let maxY = size
  for (const body of bodies) {
    minX = Math.min(minX, body.x - body.r - FRAME_PAD)
    minY = Math.min(minY, body.y - body.r - FRAME_PAD)
    maxX = Math.max(maxX, body.x + body.r + FRAME_PAD)
    maxY = Math.max(maxY, body.y + body.r + FRAME_PAD)
  }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY }
}
