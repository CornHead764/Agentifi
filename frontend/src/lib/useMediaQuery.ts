/**
 * A CSS media query, read from a component, for layout that is computed in
 * JavaScript (the register's virtualizer). `rem` in a media query is the
 * browser's 16px, not the root's 125%. Without `matchMedia` it reports false.
 */

import { useCallback, useSyncExternalStore } from 'react'

export function matchesMedia(query: string): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia(query).matches
}

export function subscribeToMedia(query: string, onChange: () => void): () => void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return () => {}
  }
  const list = window.matchMedia(query)
  list.addEventListener('change', onChange)
  return () => list.removeEventListener('change', onChange)
}

export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => subscribeToMedia(query, onChange),
    [query],
  )
  const snapshot = useCallback(() => matchesMedia(query), [query])
  return useSyncExternalStore(subscribe, snapshot, serverSnapshot)
}

/** Hoisted so the store is not re-subscribed by a new function identity. */
function serverSnapshot(): boolean {
  return false
}
