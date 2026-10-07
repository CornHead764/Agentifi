import { useEffect, useLayoutEffect, type RefObject } from 'react'

/** The tests render through `react-dom/server`, where there is no layout. */
export const useBrowserLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect

/**
 * Mark which side of a horizontal scroller has content still to come, as
 * `data-overflow-left` and `data-overflow-right`: CSS cannot ask how far a box
 * is scrolled. No dependency list on purpose: the answer changes with the
 * content, not only the ref.
 */
export function useOverflowEdges(ref: RefObject<HTMLElement | null>, also?: RefObject<HTMLElement | null>) {
  useBrowserLayoutEffect(() => {
    const element = ref.current
    if (!element) return

    const measure = () => {
      const remaining = element.scrollWidth - element.clientWidth - element.scrollLeft
      element.dataset.overflowLeft = String(element.scrollLeft > 1)
      element.dataset.overflowRight = String(remaining > 1)
    }

    measure()
    element.addEventListener('scroll', measure, { passive: true })
    if (typeof ResizeObserver === 'undefined') {
      return () => element.removeEventListener('scroll', measure)
    }

    const observer = new ResizeObserver(measure)
    observer.observe(element)
    if (also?.current) observer.observe(also.current)
    return () => {
      element.removeEventListener('scroll', measure)
      observer.disconnect()
    }
  })
}
