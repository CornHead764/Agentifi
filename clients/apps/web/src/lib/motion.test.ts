import { describe, expect, it } from 'vitest'

import {
  DEFAULT_MOTION_MS,
  MAX_MOTION_MS,
  MOTION_SPEEDS,
  asMotionMs,
  motionDuration,
  motionSpeedLabel,
} from './motion'

describe('reading a motion preference', () => {
  it('takes a duration in range, as a number or as the string storage holds', () => {
    expect(asMotionMs(100)).toBe(100)
    expect(asMotionMs('240')).toBe(240)
  })

  // Zero turns animation off, so it must survive every falsy shortcut.
  it('keeps zero rather than reading it as no answer', () => {
    expect(asMotionMs(0)).toBe(0)
    expect(asMotionMs('0')).toBe(0)
  })

  it('falls back rather than putting nonsense into a stylesheet', () => {
    for (const value of [null, undefined, '', '  ', 'fast', NaN, Infinity, -1, MAX_MOTION_MS + 1]) {
      expect(asMotionMs(value)).toBe(DEFAULT_MOTION_MS)
    }
  })

  it('rounds, because no browser honours a fractional millisecond', () => {
    expect(asMotionMs(160.4)).toBe(160)
  })

  it('offers both the default and off among the speeds the card lists', () => {
    expect(MOTION_SPEEDS).toContain(DEFAULT_MOTION_MS)
    expect(MOTION_SPEEDS).toContain(0)
  })
})

describe('what the root element ends up saying', () => {
  it('animates for as long as the preference asks', () => {
    expect(motionDuration(240, false)).toBe(240)
  })

  it('is zero when the system asks for reduced motion, whatever was chosen', () => {
    expect(motionDuration(240, true)).toBe(0)
    expect(motionDuration(DEFAULT_MOTION_MS, true)).toBe(0)
  })

  it('is zero when zero is what was chosen', () => {
    expect(motionDuration(0, false)).toBe(0)
  })

  it('refuses an out-of-range preference the way a stored one is refused', () => {
    expect(motionDuration(9000, false)).toBe(DEFAULT_MOTION_MS)
  })
})

describe('naming a speed', () => {
  it('names the ones the card offers, and reads out one it does not', () => {
    expect(motionSpeedLabel(0)).toBe('Off')
    expect(motionSpeedLabel(160)).toBe('Standard · 160 ms')
    expect(motionSpeedLabel(375)).toBe('375 ms')
  })
})
