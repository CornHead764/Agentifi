import { describe, expect, it } from 'vitest'

import { expandCircles, packCircles, type PackedCircle } from './packCircles'

const SIZE = 420

function overlapping(circles: readonly PackedCircle[], slack = 0.5): [PackedCircle, PackedCircle][] {
  const pairs: [PackedCircle, PackedCircle][] = []
  for (let i = 0; i < circles.length; i += 1) {
    for (let j = i + 1; j < circles.length; j += 1) {
      const a = circles[i]
      const b = circles[j]
      if (Math.hypot(a.x - b.x, a.y - b.y) < a.r + b.r - slack) pairs.push([a, b])
    }
  }
  return pairs
}

describe('expandCircles', () => {
  const values = [3719, 2210, 945, 471, 451, 117, 81, 60]
  const base = packCircles(values, SIZE)

  it('keeps the open group exactly where the packing put it', () => {
    const parent = base.find((circle) => circle.index === 0)!
    const { parents } = expandCircles(base, 0, [3047, 414, 258], 3719, SIZE)
    const after = parents.find((circle) => circle.index === 0)!

    expect(after.x).toBe(parent.x)
    expect(after.y).toBe(parent.y)
    expect(after.r).toBe(parent.r)
  })

  it('surfaces every child and leaves nothing overlapping', () => {
    const { parents, children } = expandCircles(base, 0, [3047, 414, 258], 3719, SIZE)

    expect(children).toHaveLength(3)
    expect(overlapping([...parents, ...children])).toEqual([])
  })

  it('sizes children by share of the group, floored so the smallest is pressable', () => {
    const { parents, children } = expandCircles(base, 0, [3000, 700, 1], 3719, SIZE)
    const parent = parents.find((circle) => circle.index === 0)!
    const biggest = children.find((circle) => circle.index === 0)!
    const tiny = children.find((circle) => circle.index === 2)!

    expect(biggest.r).toBeLessThan(parent.r)
    expect(biggest.r).toBeGreaterThan(tiny.r)
    expect(tiny.r).toBeGreaterThanOrEqual(14)
  })

  it('sizes a child by its share of the group, not of the child sum', () => {
    // A group can be mostly direct spend; normalized to the child sum, a small
    // child would balloon to fill its parent.
    const { parents, children } = expandCircles(base, 0, [414, 258], 3719, SIZE)
    const parent = parents.find((circle) => circle.index === 0)!
    const eleven = children.find((circle) => circle.index === 0)!

    expect(eleven.r / parent.r).toBeCloseTo(Math.sqrt(414 / 3719), 1)
  })

  it('keeps the children beside their parent, not scattered', () => {
    const { parents, children } = expandCircles(base, 1, [2210], 2210, SIZE)
    const parent = parents.find((circle) => circle.index === 1)!
    for (const child of children) {
      const distance = Math.hypot(child.x - parent.x, child.y - parent.y)
      expect(distance).toBeLessThan(parent.r + child.r * 3)
    }
  })

  it('lays the same month out the same way twice', () => {
    const once = expandCircles(base, 0, [3047, 414, 258], 3719, SIZE)
    const twice = expandCircles(base, 0, [3047, 414, 258], 3719, SIZE)
    expect(twice).toEqual(once)
  })

  it('never leaves a crowded chart overlapping', () => {
    // Whatever the month looks like, nothing settles on top of anything.
    const values = [8000, 3700, 2200, 950, 470, 450, 120, 80, 60, 45, 30, 12]
    const crowded = packCircles(values, SIZE)
    expect(overlapping(crowded)).toEqual([])

    const kids = [6000, 900, 500, 300, 200, 90, 40, 15, 8]
    const { parents, children } = expandCircles(crowded, 0, kids, 8000, SIZE)
    expect(children).toHaveLength(9)
    expect(overlapping([...parents, ...children])).toEqual([])
  })

  it('clears two dominant circles even when the spot lies outside the box', () => {
    const halves = packCircles([100, 95, 90], SIZE)
    expect(overlapping(halves)).toEqual([])
    const { parents, children } = expandCircles(halves, 0, [60, 40], 100, SIZE)
    expect(overlapping([...parents, ...children])).toEqual([])
  })

  it('treats a group with no positive children as not expandable', () => {
    const { parents, children } = expandCircles(base, 0, [0, -5], 3700, SIZE)
    expect(children).toEqual([])
    expect(parents).toEqual(base)
  })
})

describe('packCircles', () => {
  it('makes area, not radius, track the value', () => {
    const circles = packCircles([400, 100], SIZE)
    const big = circles.find((circle) => circle.index === 0)!
    const small = circles.find((circle) => circle.index === 1)!
    expect((big.r / small.r) ** 2).toBeCloseTo(4, 1)
  })

  it('never overlaps', () => {
    const circles = packCircles([3719, 2210, 945, 471, 451, 117, 81, 60], SIZE)
    expect(overlapping(circles)).toEqual([])
  })
})
