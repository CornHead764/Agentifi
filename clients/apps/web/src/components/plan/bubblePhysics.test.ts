import { describe, expect, it } from 'vitest'

import { bodyAtHome, stepBubbles, type BubbleBody } from './bubblePhysics'

/** A body sitting somewhere other than its home — mid-transition. */
function displaced(x: number, y: number, homeX: number, homeY: number, r: number): BubbleBody {
  return { homeX, homeY, r, x, y, vx: 0, vy: 0 }
}

/** Run the simulation until it reports itself at rest, or give up. */
function settle(bodies: BubbleBody[], maxFrames = 2000) {
  let current = bodies
  for (let frame = 0; frame < maxFrames; frame += 1) {
    const result = stepBubbles(current, 1 / 60)
    current = result.bodies
    if (!result.moving) return { bodies: current, frames: frame + 1 }
  }
  return { bodies: current, frames: maxFrames }
}

describe('bubble physics', () => {
  it('leaves a cluster nobody touched exactly where the layout put it', () => {
    const bodies = [bodyAtHome(100, 100, 40), bodyAtHome(180, 100, 30)]
    const { bodies: after, moving } = stepBubbles(bodies, 1 / 60)

    expect(moving).toBe(false)
    expect(after[0].x).toBe(100)
    expect(after[1].y).toBe(100)
  })

  it('glides a re-homed bubble to its new home and stops', () => {
    // The failure this exists to catch: bubbles that drift off and never
    // arrive, or a loop that never reports itself finished.
    const bodies = [displaced(200, 100, 100, 100, 40)]
    const { bodies: after, frames } = settle(bodies)

    expect(after[0].x).toBeCloseTo(100, 1)
    expect(after[0].y).toBeCloseTo(100, 1)
    expect(frames).toBeLessThan(2000)
  })

  it('arrives with a soft catch, not a visible bounce', () => {
    // Simplifi's chart rearranges itself; it is not flicked. Just under
    // critical damping means any overshoot stays too small to read as one.
    let bodies: BubbleBody[] = [displaced(200, 100, 100, 100, 40)]
    let lowest = 200
    for (let frame = 0; frame < 600; frame += 1) {
      bodies = stepBubbles(bodies, 1 / 60).bodies
      lowest = Math.min(lowest, bodies[0].x)
    }
    expect(lowest).toBeGreaterThan(100 - 4)
  })

  it('slides a child born on its parent apart instead of dividing by zero', () => {
    // A child starts at the parent's exact centre, where the normal between
    // them is undefined; one NaN empties the chart.
    const parent = bodyAtHome(100, 100, 40)
    const child = displaced(100, 100, 190, 100, 25)
    const { bodies } = settle([parent, child])

    for (const body of bodies) {
      expect(Number.isFinite(body.x)).toBe(true)
      expect(Number.isFinite(body.y)).toBe(true)
    }
    expect(bodies[1].x).toBeCloseTo(190, 0)
  })

  it('lets a bubble pass a neighbour without passing through it, and both go home', () => {
    // A bubble launched at an occupied spot pushes the tenant aside, never
    // crossing it, and the cluster must not ratchet outward.
    const traveller = displaced(173, 100, 100, 100, 40)
    const tenant = bodyAtHome(173, 100, 30)
    let current = [traveller, tenant]
    for (let frame = 0; frame < 120; frame += 1) {
      current = stepBubbles(current, 1 / 60).bodies
      expect(current[1].x).toBeGreaterThan(current[0].x)
    }
    const { bodies } = settle(current)
    expect(bodies[0].x).toBeCloseTo(100, 0)
    expect(bodies[1].x).toBeCloseTo(173, 0)
  })

  it('treats a long gap as time the user did not watch', () => {
    // A backgrounded tab hands back seconds; an unclamped spring gains energy
    // over that and throws the bubbles off the canvas.
    const bodies = [displaced(200, 100, 100, 100, 20)]
    const { bodies: after } = stepBubbles(bodies, 45)

    expect(Math.abs(after[0].x - 100)).toBeLessThan(200)
    expect(Number.isFinite(after[0].x)).toBe(true)
  })
})
