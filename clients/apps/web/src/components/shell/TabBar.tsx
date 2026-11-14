import { Link, useLocation } from 'react-router-dom'

import { DESTINATIONS, destinationFor } from './destinations'
import { NavCount } from './NavCount'

export interface TabBarProps {
  /** A count drawn on a destination, by its path; zero draws none. */
  counts?: Readonly<Record<string, number>>
}

/**
 * The phone's navigation: every destination, each a tab. A destination's own
 * pages are tabs at the top of it, and Settings, the assistant and refresh are
 * in the header, so nothing hides behind a "More".
 */
export function TabBar({ counts = {} }: TabBarProps) {
  const { pathname } = useLocation()
  const active = destinationFor(pathname)

  return (
    <nav className="tabbar" aria-label="Main">
      {DESTINATIONS.map((destination) => {
        const Icon = destination.icon
        const count = counts[destination.path] ?? 0
        return (
          <Link
            key={destination.path}
            to={destination.path}
            className="tabbar__tab"
            aria-current={active === destination ? 'page' : undefined}
            aria-label={count > 0 ? `${destination.label}, ${count} to review` : undefined}
          >
            <span className="tabbar__icon">
              <Icon size={20} aria-hidden="true" />
              <NavCount count={count} />
            </span>
            <span className="tabbar__label">{destination.label}</span>
          </Link>
        )
      })}
    </nav>
  )
}
