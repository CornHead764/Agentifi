/**
 * Restarting the page-in animation: a route change does not remount `<main>`,
 * and removing and re-adding the class in one task does nothing unless a
 * layout read in between forces a style recompute.
 */

export const PAGE_ARRIVING = 'app__page--arriving'

export interface ArrivalTarget {
  classList: { add: (name: string) => void; remove: (name: string) => void }
  /** Read, never used: reading it is what flushes the class change above. */
  readonly offsetWidth: number
}

/**
 * Whether `--motion` is zero. Chromium still runs a zero-length animation and
 * holds the first keyframe (opacity 0) for a frame, so it must not start at all.
 */
export function motionIsOff(value: string): boolean {
  const ms = Number.parseFloat(value)
  return Number.isFinite(ms) && ms === 0
}

export function restartArrival(target: ArrivalTarget | null, className = PAGE_ARRIVING) {
  if (!target) return
  target.classList.remove(className)
  void target.offsetWidth
  target.classList.add(className)
}
