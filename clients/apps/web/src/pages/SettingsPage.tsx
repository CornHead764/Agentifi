import { Suspense, useEffect, useRef, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'

import { ErrorBoundary } from '@/components/ErrorBoundary'
import { SETTINGS_SECTIONS } from '@/components/shell/destinations'
import { SkeletonRows } from '@/components/ui'
import { useBrowserLayoutEffect } from '@/components/ui/overflow-edges'
import { useAuth } from '@/contexts/auth'
import { motionIsOff, restartArrival } from '@/lib/pageArrival'

/** Which edges of the strip still have sections behind them, for the fades. */
type Overflow = 'none' | 'start' | 'end' | 'both'

function overflowOf(strip: HTMLElement): Overflow {
  // A sub-pixel scroll position is not a section hidden behind the fade.
  const start = strip.scrollLeft > 1
  const end = strip.scrollLeft + strip.clientWidth < strip.scrollWidth - 1
  if (start && end) return 'both'
  if (start) return 'start'
  if (end) return 'end'
  return 'none'
}

export function SettingsPage() {
  const { pathname, hash } = useLocation()
  const { user } = useAuth()
  // Until `/auth/me` answers, `user` is null and Server admin stays out:
  // showing it and taking it away is worse than showing it late.
  const sections = SETTINGS_SECTIONS.filter(
    (section) => !('superuser' in section) || (user?.is_superuser ?? false),
  )
  const strip = useRef<HTMLElement>(null)
  const panels = useRef<HTMLDivElement>(null)
  const enteredSettings = useRef(false)
  const [overflow, setOverflow] = useState<Overflow>('none')

  useEffect(() => {
    const element = strip.current
    if (!element) return
    const update = () => setOverflow(overflowOf(element))
    update()
    element.addEventListener('scroll', update, { passive: true })
    const observer =
      typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update)
    observer?.observe(element)
    return () => {
      element.removeEventListener('scroll', update)
      observer?.disconnect()
    }
  }, [])

  useEffect(() => {
    // Below 64rem the nav is a horizontal strip and the active section can be
    // off its edge, so it is scrolled into the centre.
    const active = strip.current?.querySelector('[aria-current="page"]')
    active?.scrollIntoView({ block: 'nearest', inline: 'center' })
  }, [pathname])

  // A link from outside settings names its card with a fragment, which the
  // router does not scroll to by itself.
  useEffect(() => {
    if (hash === '') return
    document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView({ block: 'start' })
  }, [pathname, hash])

  // The content fades between sections, but not the nav, and not on the first
  // section, which the shell already faded in.
  useBrowserLayoutEffect(() => {
    if (!enteredSettings.current) {
      enteredSettings.current = true
      return
    }
    const motion = getComputedStyle(document.documentElement).getPropertyValue('--motion')
    if (motionIsOff(motion)) return
    restartArrival(panels.current, 'settings__panels--arriving')
  }, [pathname])

  return (
    <div className="page settings">
      <nav className="settings__nav" data-overflow={overflow} ref={strip} aria-label="Settings">
        {sections.map((section) => (
          <NavLink key={section.path} to={section.path} className="settings__link">
            {section.label}
          </NavLink>
        ))}
      </nav>
      <div className="settings__panels" ref={panels}>
        <ErrorBoundary scope="page" resetKey={pathname}>
          <Suspense fallback={<SkeletonRows rows={6} />}>
            <Outlet />
          </Suspense>
        </ErrorBoundary>
      </div>
    </div>
  )
}
