import { describe, expect, it } from 'vitest'

import { NEVER_OPENED, nextOpenings, type Openings } from './opening-key'

describe('dialog openings', () => {
  it('counts each opening, and keeps the count through a close', () => {
    let openings: Openings = NEVER_OPENED
    const counts: number[] = []
    for (const open of [true, true, false, false, true]) {
      openings = nextOpenings(openings, open)
      counts.push(openings.count)
    }
    expect(counts).toEqual([1, 1, 1, 1, 2])
  })

  it('stays at zero for a dialog that has never opened', () => {
    expect(nextOpenings(NEVER_OPENED, false)).toBe(NEVER_OPENED)
  })
})
