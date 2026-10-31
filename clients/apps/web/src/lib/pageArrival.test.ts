import { describe, expect, it } from 'vitest'

import { PAGE_ARRIVING, motionIsOff, restartArrival, type ArrivalTarget } from './pageArrival'

function target(): ArrivalTarget & { log: string[] } {
  const log: string[] = []
  return {
    log,
    classList: {
      add: (name: string) => log.push(`add ${name}`),
      remove: (name: string) => log.push(`remove ${name}`),
    },
    get offsetWidth() {
      log.push('reflow')
      return 0
    },
  }
}

describe('restartArrival', () => {
  it('reads layout between taking the class off and putting it back', () => {
    // Without the read in the middle, route changes after the first play nothing.
    const element = target()
    restartArrival(element)
    expect(element.log).toEqual([`remove ${PAGE_ARRIVING}`, 'reflow', `add ${PAGE_ARRIVING}`])
  })

  it('does nothing without an element', () => {
    expect(() => restartArrival(null)).not.toThrow()
  })
})

describe('motionIsOff', () => {
  it('is true only when the duration really is nothing', () => {
    expect(motionIsOff('0ms')).toBe(true)
    expect(motionIsOff('0s')).toBe(true)
    expect(motionIsOff(' 0ms ')).toBe(true)
    expect(motionIsOff('160ms')).toBe(false)
    expect(motionIsOff('0.16s')).toBe(false)
  })

  it('animates rather than blanks the page when the value makes no sense', () => {
    // The property is missing before the provider has written it.
    expect(motionIsOff('')).toBe(false)
    expect(motionIsOff('inherit')).toBe(false)
  })
})
