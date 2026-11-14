import { useRef } from 'react'
import { Link, useLocation } from 'react-router-dom'

import { useBrowserLayoutEffect, useOverflowEdges } from '@/components/ui/overflow-edges'

import { destinationFor, sectionFor } from './destinations'

/**
 * The pages of the open destination, as pills across the top of the page.
 * They are pills rather than underlined tabs because a page may draw tabs of
 * its own beneath them.
 */
export function SectionTabs() {
  const { pathname } = useLocation()
  const destination = destinationFor(pathname)
  const current = sectionFor(pathname)
  const strip = useRef<HTMLElement>(null)
  useOverflowEdges(strip)

  // On a phone the strip is wider than the screen, and a link from elsewhere
  // can open a section off its end.
  useBrowserLayoutEffect(() => {
    strip.current
      ?.querySelector('[aria-current="page"]')
      ?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
  }, [pathname])

  if (!destination || destination.sections.length === 0) return null

  return (
    <nav className="section-tabs" ref={strip} aria-label={destination.label}>
      {destination.sections.map((section) => (
        <Link
          key={section.path}
          to={section.path}
          className="section-tabs__link"
          aria-current={section === current ? 'page' : undefined}
        >
          {section.label}
        </Link>
      ))}
    </nav>
  )
}
