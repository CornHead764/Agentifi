/**
 * Where a hovered donut slice moves: along its own bisector. Recharts gives
 * `midAngle` in degrees anticlockwise from three o'clock, and SVG's y grows
 * downwards, so the vertical component is negated.
 */

/** How far a hovered slice leaves the ring, in the chart's own user units. */
export const SLICE_POP = 6

export interface SliceOffset {
  dx: number
  dy: number
}

/**
 * Rounded to hundredths: the pair ends up in a `transform` string, and a new
 * string is a style write per frame. The `+ 0` turns `-0` from `cos(270°)`
 * into `0`, not `-0px`.
 */
function round(value: number): number {
  return Math.round(value * 100) / 100 + 0
}

export function sliceOffset(midAngle: number, distance: number): SliceOffset {
  if (distance === 0) return { dx: 0, dy: 0 }
  const radians = (midAngle * Math.PI) / 180
  return { dx: round(Math.cos(radians) * distance), dy: round(-Math.sin(radians) * distance) }
}
