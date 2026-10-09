import { describe, expect, it } from 'vitest'

import { canvasWidth, centredScroll, classifyPointer, TAP_SLOP } from './panGesture'

describe('classifyPointer', () => {
  it('calls a still or slightly wobbling pointer a tap', () => {
    expect(classifyPointer(0, 0)).toBe('tap')
    expect(classifyPointer(TAP_SLOP, 0)).toBe('tap')
    expect(classifyPointer(-3, 4)).toBe('tap')
  })

  it('calls a horizontal drag past the slop a pan, in either direction', () => {
    expect(classifyPointer(TAP_SLOP + 1, 0)).toBe('pan')
    expect(classifyPointer(-40, 10)).toBe('pan')
  })

  it('leaves a mostly vertical drag to the page', () => {
    expect(classifyPointer(8, 60)).toBe('tap')
  })
})

describe('centredScroll', () => {
  it('centres the focus when the canvas has room either side', () => {
    expect(centredScroll(300, 200, 600)).toBe(200)
  })

  it('stops at the canvas edges', () => {
    expect(centredScroll(20, 200, 600)).toBe(0)
    expect(centredScroll(590, 200, 600)).toBe(400)
  })

  it('does not scroll a canvas that fits', () => {
    expect(centredScroll(100, 300, 300)).toBe(0)
  })
})

describe('canvasWidth', () => {
  it('fills a viewport that is wide enough', () => {
    expect(canvasWidth(800, 500, 0.8)).toBe(800)
  })

  it('keeps a minimum scale in a narrow viewport, leaving the rest to pan', () => {
    expect(canvasWidth(340, 500, 0.8)).toBe(400)
  })
})
