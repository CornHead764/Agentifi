import { Ellipsis, RefreshCw, Settings } from 'lucide-react'
import { Fragment, useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'

import { Sheet, SheetContent, SheetTrigger, Spinner } from '@/components/ui'

import { DESTINATIONS, type Destination } from './destinations'

export interface TabBarProps {
  onRefreshAll?: () => void
  refreshing?: boolean
  /** What a running refresh is doing, in a few words. */
  refreshStatus?: string
  /** False when the household has no live connection to refresh. */
  canRefresh?: boolean
}

/** The destinations that get a tab of their own; everything else is under More. */
const PRIMARY = ['/', '/transactions', '/spending-plan', '/upcoming']

function matches(destination: Destination, pathname: string): boolean {
  return destination.path === '/'
    ? pathname === '/'
    : pathname === destination.path || pathname.startsWith(`${destination.path}/`)
}

/**
 * The phone's navigation. Four destinations fit under a thumb; the rest,
 * settings and refresh sit behind a fifth tab that opens a sheet listing every
 * destination with its full name. The tabs use `short` where a destination has
 * one. `More` reads as active whenever the open page is one it holds.
 */
export function TabBar({
  onRefreshAll,
  refreshing = false,
  refreshStatus,
  canRefresh = true,
}: TabBarProps) {
  const { pathname } = useLocation()
  const [open, setOpen] = useState(false)

  const primary = PRIMARY.map((path) => DESTINATIONS.find((one) => one.path === path)!)
  const underMore =
    pathname.startsWith('/settings') ||
    DESTINATIONS.some((one) => !PRIMARY.includes(one.path) && matches(one, pathname))

  return (
    <nav className="tabbar" aria-label="Main">
      {primary.map((destination) => {
        const Icon = destination.icon
        return (
          <NavLink
            key={destination.path}
            to={destination.path}
            end={destination.path === '/'}
            className="tabbar__tab"
          >
            <Icon size={20} />
            <span className="tabbar__label">{destination.short ?? destination.label}</span>
          </NavLink>
        )
      })}

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger asChild>
          <button
            type="button"
            className="tabbar__tab"
            aria-current={underMore ? 'page' : undefined}
            aria-label="More"
          >
            <Ellipsis size={20} />
            <span className="tabbar__label">More</span>
          </button>
        </SheetTrigger>
        <SheetContent title="Go to">
          <ul className="sheet__list">
            {DESTINATIONS.map((destination, index) => {
              const previous = DESTINATIONS[index - 1]
              const Icon = destination.icon
              return (
                <Fragment key={destination.path}>
                  {previous && previous.group !== destination.group ? (
                    <li role="presentation" className="sheet__separator" />
                  ) : null}
                  <li>
                    <NavLink
                      to={destination.path}
                      end={destination.path === '/'}
                      className="sheet__link"
                      onClick={() => setOpen(false)}
                    >
                      <Icon size={18} />
                      {destination.label}
                    </NavLink>
                  </li>
                </Fragment>
              )
            })}
            <li role="presentation" className="sheet__separator" />
            <li>
              <NavLink to="/settings" className="sheet__link" onClick={() => setOpen(false)}>
                <Settings size={18} />
                Settings
              </NavLink>
            </li>
            <li>
              <button
                type="button"
                className="sheet__link"
                disabled={refreshing || !canRefresh}
                title={canRefresh ? undefined : 'No connected accounts'}
                onClick={() => {
                  onRefreshAll?.()
                  setOpen(false)
                }}
              >
                {refreshing ? <Spinner size={18} /> : <RefreshCw size={18} />}
                {refreshing ? (refreshStatus ?? 'Refreshing…') : 'Refresh all accounts'}
              </button>
            </li>
          </ul>
        </SheetContent>
      </Sheet>
    </nav>
  )
}
