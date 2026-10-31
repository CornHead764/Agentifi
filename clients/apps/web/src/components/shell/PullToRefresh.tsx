/**
 * Pull to refresh on a touch screen: a drag down from the top of the page
 * refetches every query on screen. The listeners are passive, so the page's
 * own scroll and bounce are never held up; the indicator rides beside them.
 */

import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { Spinner } from '@/components/ui'
import { pullDirection, pullRefreshes, pullTravel, PULL_THRESHOLD } from '@/lib/pullToRefresh'
import { useMediaQuery } from '@/lib/useMediaQuery'

/** Surfaces that scroll or gesture on their own, and the bars a finger only taps. */
const OWN_GESTURES = '.drawer, .rail, .header, .tabbar, [role="dialog"], [data-radix-popper-content-wrapper]'

function startsPull(target: EventTarget | null): boolean {
  if (window.scrollY > 0) return false
  // A dialog or sheet locks the page under it.
  if (document.body.hasAttribute('data-scroll-locked')) return false
  if (!(target instanceof Element) || target.closest(OWN_GESTURES)) return false
  for (let at: Element | null = target; at; at = at.parentElement) {
    if (at.scrollTop > 0) return false
  }
  return true
}

export function PullToRefresh() {
  const touch = useMediaQuery('(pointer: coarse)')
  const client = useQueryClient()
  const [travel, setTravel] = useState(0)
  const [refreshing, setRefreshing] = useState(false)
  const busy = useRef(false)

  useEffect(() => {
    if (!touch) return
    let start: { x: number; y: number; pulling: boolean | undefined } | null = null
    let reached = 0

    const onStart = (event: TouchEvent) => {
      start =
        !busy.current && event.touches.length === 1 && startsPull(event.target)
          ? { x: event.touches[0].clientX, y: event.touches[0].clientY, pulling: undefined }
          : null
      reached = 0
    }
    const onMove = (event: TouchEvent) => {
      if (start === null) return
      const dx = event.touches[0].clientX - start.x
      const dy = event.touches[0].clientY - start.y
      if (start.pulling === undefined) {
        const direction = pullDirection(dx, dy)
        if (direction === undefined) return
        start.pulling = direction === 'pull'
      }
      if (!start.pulling || window.scrollY > 0) {
        start = null
        reached = 0
        setTravel(0)
        return
      }
      reached = pullTravel(dy)
      setTravel(reached)
    }
    const onEnd = () => {
      if (start === null) return
      start = null
      if (!pullRefreshes(reached)) {
        setTravel(0)
        return
      }
      busy.current = true
      setTravel(PULL_THRESHOLD)
      setRefreshing(true)
      void client.invalidateQueries({ type: 'active' }).finally(() => {
        busy.current = false
        setRefreshing(false)
        setTravel(0)
      })
    }

    window.addEventListener('touchstart', onStart, { passive: true })
    window.addEventListener('touchmove', onMove, { passive: true })
    window.addEventListener('touchend', onEnd, { passive: true })
    window.addEventListener('touchcancel', onEnd, { passive: true })
    return () => {
      window.removeEventListener('touchstart', onStart)
      window.removeEventListener('touchmove', onMove)
      window.removeEventListener('touchend', onEnd)
      window.removeEventListener('touchcancel', onEnd)
    }
  }, [touch, client])

  if (travel === 0 && !refreshing) return null
  return (
    <div
      className="pull-refresh"
      data-refreshing={refreshing ? 'true' : undefined}
      style={{
        transform: `translate(-50%, calc(${travel}px - 100%)) scale(${Math.min(1, travel / PULL_THRESHOLD)})`,
      }}
    >
      <Spinner size={18} label={refreshing ? 'Refreshing' : undefined} />
    </div>
  )
}
