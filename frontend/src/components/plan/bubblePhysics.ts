/**
 * The glide the bubbles make when the layout changes under them: children
 * well up out of an opened group and neighbours give way.
 *
 * A plain function over an array, so a pure step is testable. Resting
 * positions come from `packCircles`, in the same viewBox units, so moving a
 * bubble means giving its body a new home; the spring does the rest.
 */

export interface BubbleBody {
  /** Where the layout put it. The spring always pulls toward here. */
  homeX: number
  homeY: number
  r: number
  x: number
  y: number
  vx: number
  vy: number
}

/**
 * How hard the spring pulls a bubble home, and how fast the motion dies. Tuned
 * together, just under critical damping (about 12.6 for this stiffness), so the
 * glide ends with a soft catch rather than a bounce.
 */
const STIFFNESS = 40
const DAMPING = 10

/** How firmly overlapping bubbles push each other apart, per second. */
const SEPARATION = 14

/**
 * The fixed inner timestep, in seconds.
 *
 * A spring integrated at whatever interval the browser happened to deliver
 * gains energy on a long frame and flies apart. Stepping at a fixed size and
 * running however many of those fit the real elapsed time keeps the same
 * motion on a 60Hz screen, a 144Hz one, and a tab that was backgrounded.
 */
const FIXED_STEP = 1 / 120

/** Below this speed and this displacement, nothing visible is still moving. */
const AT_REST_SPEED = 0.4
const AT_REST_OFFSET = 0.05

/** A body sitting exactly where the layout put it, at rest. */
export function bodyAtHome(x: number, y: number, r: number): BubbleBody {
  return { homeX: x, homeY: y, r, x, y, vx: 0, vy: 0 }
}

/**
 * Advance the simulation by `elapsed` seconds. Returns a new array and whether
 * anything is still moving, so the loop knows to stop.
 */
export function stepBubbles(
  bodies: readonly BubbleBody[],
  elapsed: number,
): { bodies: BubbleBody[]; moving: boolean } {
  // Clamped: a backgrounded tab's gap of seconds would be catch-up nobody
  // watched.
  const capped = Math.min(Math.max(elapsed, 0), 0.25)
  const steps = Math.ceil(capped / FIXED_STEP)
  const next = bodies.map((body) => ({ ...body }))

  for (let step = 0; step < steps; step += 1) {
    integrate(next, FIXED_STEP)
    separate(next, FIXED_STEP)
  }
  return { bodies: next, moving: next.some(isMoving) }
}

function integrate(bodies: BubbleBody[], dt: number) {
  for (const body of bodies) {
    const ax = -STIFFNESS * (body.x - body.homeX) - DAMPING * body.vx
    const ay = -STIFFNESS * (body.y - body.homeY) - DAMPING * body.vy
    body.vx += ax * dt
    body.vy += ay * dt
    body.x += body.vx * dt
    body.y += body.vy * dt
  }
}

/**
 * Push overlapping bubbles apart. Position is corrected rather than velocity,
 * so a displacement propagates in one frame. Two bubbles at the same point are
 * separated along a fixed axis: a child is born at its parent's centre, where
 * the normal is undefined and would divide by zero into NaN.
 */
function separate(bodies: BubbleBody[], dt: number) {
  for (let i = 0; i < bodies.length; i += 1) {
    for (let j = i + 1; j < bodies.length; j += 1) {
      const a = bodies[i]
      const b = bodies[j]
      let dx = b.x - a.x
      let dy = b.y - a.y
      let distance = Math.hypot(dx, dy)
      if (distance === 0) {
        dx = 1
        dy = 0
        distance = 1
      }
      const overlap = a.r + b.r - distance
      if (overlap <= 0) continue

      const push = Math.min(overlap * SEPARATION * dt, overlap) / 2
      const nx = (dx / distance) * push
      const ny = (dy / distance) * push
      a.x -= nx
      a.y -= ny
      b.x += nx
      b.y += ny
    }
  }
}

function isMoving(body: BubbleBody): boolean {
  return (
    Math.hypot(body.vx, body.vy) > AT_REST_SPEED ||
    Math.hypot(body.x - body.homeX, body.y - body.homeY) > AT_REST_OFFSET
  )
}
