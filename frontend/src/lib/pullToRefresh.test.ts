import { describe, expect, it } from 'vitest'

import { PULL_MAX, PULL_THRESHOLD, pullDirection, pullRefreshes, pullTravel } from './pullToRefresh'

describe('pulling the page down from the top', () => {
  it('moves the indicator half as far as the finger, and no further than its stop', () => {
    expect(pullTravel(40)).toBe(20)
    expect(pullTravel(-30)).toBe(0)
    expect(pullTravel(1000)).toBe(PULL_MAX)
  })

  it('refreshes only once the indicator reaches the threshold', () => {
    expect(pullRefreshes(pullTravel(PULL_THRESHOLD * 2 - 2))).toBe(false)
    expect(pullRefreshes(pullTravel(PULL_THRESHOLD * 2))).toBe(true)
  })

  it('leaves a sideways drag to the row swipes and an upward one to the scroll', () => {
    expect(pullDirection(2, 3)).toBeUndefined()
    expect(pullDirection(0, 12)).toBe('pull')
    expect(pullDirection(20, 12)).toBe('other')
    expect(pullDirection(0, -12)).toBe('other')
  })
})
