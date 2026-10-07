/** Why a toast's countdown is held: a mouse resting on it, or keyboard focus inside it. */
export type Hold = 'pointer' | 'focus'

export interface Countdown {
  /** Returns whether the countdown stopped running. */
  hold: (reason: Hold) => boolean
  /** Returns whether the countdown started running again. */
  release: (reason: Hold) => boolean
  /** Ends it at once when the deadline passed while the page's timers were throttled. */
  catchUp: () => void
  /** Milliseconds of the total already run. */
  elapsed: () => number
  running: () => boolean
  cancel: () => void
}

/**
 * A toast's time on screen, measured from the clock rather than counted by
 * timers or animation frames: a hidden tab throttles timers and stops frames,
 * so either would stall or drift. Nothing about the page's visibility holds it.
 */
export function startCountdown(
  total: number,
  onDone: () => void,
  now: () => number = Date.now,
): Countdown {
  const holds = new Set<Hold>()
  let spent = 0
  let since: number | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let over = false

  const elapsed = () => spent + (since === null ? 0 : now() - since)

  const finish = () => {
    if (over) return
    over = true
    since = null
    clearTimeout(timer)
    onDone()
  }

  // A timer can fire late, never trusted to be on time: it only prompts a
  // look at the clock.
  const tick = () => {
    const left = total - elapsed()
    if (left <= 0) finish()
    else timer = setTimeout(tick, left)
  }

  const run = () => {
    since = now()
    tick()
  }

  run()

  return {
    hold(reason) {
      if (over || holds.has(reason)) return false
      holds.add(reason)
      if (holds.size > 1) return false
      spent = elapsed()
      since = null
      clearTimeout(timer)
      return true
    },
    release(reason) {
      if (over || !holds.delete(reason) || holds.size > 0) return false
      run()
      return true
    },
    catchUp() {
      if (!over && since !== null && elapsed() >= total) finish()
    },
    elapsed: () => Math.min(total, elapsed()),
    running: () => !over && since !== null,
    cancel() {
      over = true
      since = null
      clearTimeout(timer)
    },
  }
}

/**
 * Holds the countdown while a mouse rests on the toast or keyboard focus is
 * inside it, and lets go whenever there is nothing to say the pointer is
 * still there: it left the window, the window lost focus, or the tab was hidden. A
 * pointer that went without a `pointerleave` (the window left from over the
 * toast, the toast moved out from under a still cursor) is caught by the
 * next `pointerover` anywhere else. `onChange` runs whenever the countdown
 * starts or stops, and when a hidden tab comes back.
 */
export function holdOnHover(
  countdown: Countdown,
  toast: HTMLElement,
  onChange: () => void,
): () => void {
  const doc = toast.ownerDocument
  const win = doc.defaultView
  const inside = (node: EventTarget | null) => isNode(node) && toast.contains(node)
  const hold = (reason: Hold) => {
    if (countdown.hold(reason)) onChange()
  }
  const release = (reason: Hold) => {
    if (countdown.release(reason)) onChange()
  }

  const onPointer = (event: PointerEvent) => {
    if (event.pointerType === 'mouse') hold('pointer')
  }
  const onPointerLeave = () => release('pointer')
  const onPointerOver = (event: PointerEvent) => {
    if (!inside(event.target)) release('pointer')
  }
  const onPointerOut = (event: PointerEvent) => {
    if (event.relatedTarget === null) release('pointer')
  }
  const onFocusIn = (event: FocusEvent) => {
    if (focusVisible(event.target)) hold('focus')
  }
  const onFocusOut = (event: FocusEvent) => {
    if (!inside(event.relatedTarget)) release('focus')
  }
  const onBlur = () => release('pointer')
  const onVisibility = () => {
    release('pointer')
    countdown.catchUp()
    onChange()
  }

  toast.addEventListener('pointerenter', onPointer)
  toast.addEventListener('pointermove', onPointer)
  toast.addEventListener('pointerleave', onPointerLeave)
  toast.addEventListener('focusin', onFocusIn)
  toast.addEventListener('focusout', onFocusOut)
  doc.addEventListener('pointerover', onPointerOver)
  doc.addEventListener('pointerout', onPointerOut)
  doc.addEventListener('visibilitychange', onVisibility)
  win?.addEventListener('blur', onBlur)
  return () => {
    toast.removeEventListener('pointerenter', onPointer)
    toast.removeEventListener('pointermove', onPointer)
    toast.removeEventListener('pointerleave', onPointerLeave)
    toast.removeEventListener('focusin', onFocusIn)
    toast.removeEventListener('focusout', onFocusOut)
    doc.removeEventListener('pointerover', onPointerOver)
    doc.removeEventListener('pointerout', onPointerOut)
    doc.removeEventListener('visibilitychange', onVisibility)
    win?.removeEventListener('blur', onBlur)
  }
}

/**
 * A click focuses the toast too, and that focus would hold it until the
 * person clicked somewhere else; only focus the keyboard put there holds it.
 */
function focusVisible(target: EventTarget | null): boolean {
  if (!isElement(target)) return false
  try {
    return target.matches(':focus-visible')
  } catch {
    return true
  }
}

function isNode(target: EventTarget | null): target is Node {
  return target !== null && 'nodeType' in target
}

function isElement(target: EventTarget | null): target is Element {
  return isNode(target) && 'matches' in target
}
