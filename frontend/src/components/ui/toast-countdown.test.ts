import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { holdOnHover, startCountdown } from './toast-countdown'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('a toast countdown', () => {
  it('ends after its total', () => {
    const done = vi.fn()
    startCountdown(6000, done)
    vi.advanceTimersByTime(5999)
    expect(done).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(done).toHaveBeenCalledOnce()
  })

  it('keeps the time left across a hold', () => {
    const done = vi.fn()
    const countdown = startCountdown(6000, done)
    vi.advanceTimersByTime(2000)
    expect(countdown.hold('pointer')).toBe(true)
    vi.advanceTimersByTime(60_000)
    expect(done).not.toHaveBeenCalled()
    expect(countdown.elapsed()).toBe(2000)
    expect(countdown.release('pointer')).toBe(true)
    vi.advanceTimersByTime(3999)
    expect(done).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(done).toHaveBeenCalledOnce()
  })

  it('runs again only once every hold is let go', () => {
    const done = vi.fn()
    const countdown = startCountdown(6000, done)
    countdown.hold('pointer')
    countdown.hold('focus')
    expect(countdown.release('pointer')).toBe(false)
    expect(countdown.running()).toBe(false)
    expect(countdown.release('focus')).toBe(true)
    expect(countdown.running()).toBe(true)
  })

  it('reads the clock when its timer fires late, as in a throttled background tab', () => {
    const done = vi.fn()
    let clock = 0
    const countdown = startCountdown(6000, done, () => clock)
    // The clock passes the deadline while no timer has fired yet.
    clock = 9000
    countdown.catchUp()
    expect(done).toHaveBeenCalledOnce()
  })

  it('ends at its deadline from the clock, not from how many timers ran', () => {
    const done = vi.fn()
    let clock = 0
    startCountdown(6000, done, () => clock)
    clock = 1000
    // The first timer fires on time but the clock says 1s has gone: it waits
    // only the 5s that are left, never a fresh 6s.
    vi.advanceTimersByTime(6000)
    expect(done).not.toHaveBeenCalled()
    clock = 6000
    vi.advanceTimersByTime(5000)
    expect(done).toHaveBeenCalledOnce()
  })

  it('does nothing once cancelled', () => {
    const done = vi.fn()
    const countdown = startCountdown(6000, done)
    countdown.cancel()
    vi.advanceTimersByTime(10_000)
    countdown.catchUp()
    expect(done).not.toHaveBeenCalled()
  })
})

/** Just enough of a page for the listeners: events, `contains`, an owner document. */
function page() {
  const win = new EventTarget()
  const doc = Object.assign(new EventTarget(), { defaultView: win, visibilityState: 'visible' })
  const outside = Object.assign(new EventTarget(), { nodeType: 1 })
  const toast = Object.assign(new EventTarget(), {
    nodeType: 1,
    ownerDocument: doc,
    contains: (node: unknown) => node === toast || node === child,
  })
  const child = Object.assign(new EventTarget(), {
    nodeType: 1,
    matches: () => true,
  })
  return { win, doc, toast, child, outside }
}

function pointer(type: string, init: { pointerType?: string; relatedTarget?: unknown } = {}) {
  const event = new Event(type)
  Object.defineProperties(event, {
    pointerType: { value: init.pointerType ?? 'mouse' },
    relatedTarget: { value: init.relatedTarget ?? null },
  })
  return event
}

function focus(type: string, relatedTarget: unknown = null) {
  const event = new Event(type)
  Object.defineProperty(event, 'relatedTarget', { value: relatedTarget })
  return event
}

function hovered() {
  const done = vi.fn()
  const changed = vi.fn()
  const scene = page()
  const countdown = startCountdown(6000, done)
  const stop = holdOnHover(countdown, scene.toast as unknown as HTMLElement, changed)
  return { ...scene, countdown, done, changed, stop }
}

describe('a toast held by hover', () => {
  it('stops while a mouse rests on it and runs on when it leaves', () => {
    const { toast, countdown, done, changed } = hovered()
    vi.advanceTimersByTime(1000)
    toast.dispatchEvent(pointer('pointerenter'))
    expect(countdown.running()).toBe(false)
    expect(changed).toHaveBeenCalledOnce()
    vi.advanceTimersByTime(30_000)
    toast.dispatchEvent(pointer('pointerleave'))
    expect(countdown.running()).toBe(true)
    vi.advanceTimersByTime(5000)
    expect(done).toHaveBeenCalledOnce()
  })

  it('is not held by a touch', () => {
    const { toast, countdown } = hovered()
    toast.dispatchEvent(pointer('pointerenter', { pointerType: 'touch' }))
    expect(countdown.running()).toBe(true)
  })

  it('lets go when the pointer leaves the window from over it', () => {
    const { doc, toast, countdown } = hovered()
    toast.dispatchEvent(pointer('pointermove'))
    expect(countdown.running()).toBe(false)
    doc.dispatchEvent(pointer('pointerout', { relatedTarget: null }))
    expect(countdown.running()).toBe(true)
  })

  it('lets go when the pointer is seen anywhere else, though no pointerleave came', () => {
    const { doc, toast, outside, countdown } = hovered()
    toast.dispatchEvent(pointer('pointermove'))
    const over = pointer('pointerover')
    Object.defineProperty(over, 'target', { value: outside })
    doc.dispatchEvent(over)
    expect(countdown.running()).toBe(true)
  })

  it('stays held while the pointer moves within it', () => {
    const { doc, toast, child, countdown } = hovered()
    toast.dispatchEvent(pointer('pointermove'))
    const over = pointer('pointerover')
    Object.defineProperty(over, 'target', { value: child })
    doc.dispatchEvent(over)
    doc.dispatchEvent(pointer('pointerout', { relatedTarget: child }))
    expect(countdown.running()).toBe(false)
  })

  it('lets go when the window loses focus', () => {
    const { win, toast, countdown } = hovered()
    toast.dispatchEvent(pointer('pointermove'))
    win.dispatchEvent(new Event('blur'))
    expect(countdown.running()).toBe(true)
  })

  it('keeps counting in a hidden tab and ends on return when its time ran out', () => {
    const done = vi.fn()
    let clock = 0
    const scene = page()
    const countdown = startCountdown(6000, done, () => clock)
    holdOnHover(countdown, scene.toast as unknown as HTMLElement, () => {})
    scene.toast.dispatchEvent(pointer('pointermove'))
    scene.doc.visibilityState = 'hidden'
    scene.doc.dispatchEvent(new Event('visibilitychange'))
    expect(countdown.running()).toBe(true)
    // The background tab's timers are throttled: none fires, the clock moves on.
    clock = 20_000
    scene.doc.visibilityState = 'visible'
    scene.doc.dispatchEvent(new Event('visibilitychange'))
    expect(done).toHaveBeenCalledOnce()
  })

  it('is held by keyboard focus until focus leaves it', () => {
    const { toast, child, outside, countdown } = hovered()
    const into = focus('focusin')
    Object.defineProperty(into, 'target', { value: child })
    toast.dispatchEvent(into)
    expect(countdown.running()).toBe(false)
    toast.dispatchEvent(focus('focusout', child))
    expect(countdown.running()).toBe(false)
    toast.dispatchEvent(focus('focusout', outside))
    expect(countdown.running()).toBe(true)
  })

  it('leaves the page alone once stopped', () => {
    const { toast, countdown, stop } = hovered()
    stop()
    toast.dispatchEvent(pointer('pointerenter'))
    expect(countdown.running()).toBe(true)
  })

  it('starts unheld on a fresh toast, whatever a toast before it was left in', () => {
    const first = hovered()
    first.toast.dispatchEvent(pointer('pointerenter'))
    first.stop()
    first.countdown.cancel()
    const second = hovered()
    expect(second.countdown.running()).toBe(true)
    vi.advanceTimersByTime(6000)
    expect(second.done).toHaveBeenCalledOnce()
  })
})
