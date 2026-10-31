import { describe, expect, it } from 'vitest'

import { sliceOffset } from './sliceGeometry'

describe('the hovered slice’s offset', () => {
  it('moves a slice on the right of the ring to the right', () => {
    expect(sliceOffset(0, 6)).toEqual({ dx: 6, dy: 0 })
  })

  /**
   * Recharts measures anticlockwise from three o'clock and SVG's y grows
   * downwards, so moving up is a negative dy.
   */
  it('moves a slice at the top of the ring upwards', () => {
    expect(sliceOffset(90, 6)).toEqual({ dx: 0, dy: -6 })
  })

  it('moves a slice at the bottom of the ring downwards', () => {
    expect(sliceOffset(270, 6)).toEqual({ dx: 0, dy: 6 })
  })

  it('splits the distance along the bisector of a diagonal slice', () => {
    expect(sliceOffset(45, 10)).toEqual({ dx: 7.07, dy: -7.07 })
  })

  it('keeps the whole distance, however the slice is angled', () => {
    for (const angle of [0, 37, 90, 128, 180, 214, 270, 359]) {
      const { dx, dy } = sliceOffset(angle, 6)
      expect(Math.hypot(dx, dy)).toBeCloseTo(6, 1)
    }
  })

  it('reads an angle past a full turn the same as the angle itself', () => {
    expect(sliceOffset(400, 6)).toEqual(sliceOffset(40, 6))
  })
})
