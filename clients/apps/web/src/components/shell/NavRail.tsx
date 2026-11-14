import { RefreshCw, Sparkles } from 'lucide-react'
import { Fragment, type ReactElement } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'

import { BrandMark } from '@/components/BrandMark'
import { Spinner, Tooltip } from '@/components/ui'

import { DESTINATIONS, destinationFor } from './destinations'
import { NavCount } from './NavCount'

export interface NavRailProps {
  onRefreshAll?: () => void
  refreshing?: boolean
  /** What a running refresh is doing, in a few words, beside its spinner. */
  refreshStatus?: string
  /** False when the household has no live connection to refresh. */
  canRefresh?: boolean
  /** Icons only, with the names in tooltips. Remembered by the shell. */
  collapsed?: boolean
  onToggleCollapsed?: () => void
  /** A count drawn on a destination, by its path; zero draws none. */
  counts?: Readonly<Record<string, number>>
}

/**
 * The navigation rail. Names by default; collapsed, the names move into
 * tooltips, which open for a pointer and not a finger, so every link keeps its
 * `aria-label` and the label element is hidden by the stylesheet rather than
 * dropped. The active link is marked by `NavLink`'s own `aria-current`.
 */
export function NavRail({
  onRefreshAll,
  refreshing = false,
  refreshStatus,
  canRefresh = true,
  collapsed = false,
  onToggleCollapsed,
  counts = {},
}: NavRailProps) {
  const { pathname } = useLocation()
  const active = destinationFor(pathname)
  // A tooltip on every link while the names are on screen would be a second
  // copy of what somebody is already reading.
  const named = (label: string, node: ReactElement) =>
    collapsed ? <Tooltip label={label}>{node}</Tooltip> : node
  const toggleLabel = collapsed ? 'Expand sidebar' : 'Collapse sidebar'

  return (
    <nav className="rail" aria-label="Main">
      {onToggleCollapsed ? (
        <Tooltip label={toggleLabel}>
          <button
            type="button"
            className="rail__brand"
            aria-label={toggleLabel}
            aria-expanded={!collapsed}
            onClick={onToggleCollapsed}
          >
            <BrandMark className="rail__brand-mark" />
          </button>
        </Tooltip>
      ) : (
        <div className="rail__brand" aria-hidden="true">
          <BrandMark className="rail__brand-mark" />
        </div>
      )}

      {DESTINATIONS.map((destination) => {
        const Icon = destination.icon
        const count = counts[destination.path] ?? 0
        const label = count > 0 ? `${destination.label}, ${count} to review` : destination.label
        return (
          <Fragment key={destination.path}>
            {named(
              destination.label,
              // A destination is current from any of its sections, which
              // NavLink's own matching cannot see.
              <Link
                to={destination.path}
                className="rail__link"
                aria-label={label}
                aria-current={active === destination ? 'page' : undefined}
              >
                <Icon size={18} aria-hidden="true" />
                <span className="rail__label">{destination.label}</span>
                <NavCount count={count} />
              </Link>,
            )}
          </Fragment>
        )
      })}

      <span className="rail__spacer" />

      <div className="rail__foot">
        {named(
          'Assistant',
          <NavLink to="/assistant" className="rail__link" aria-label="Assistant">
            <Sparkles size={18} aria-hidden="true" />
            <span className="rail__label">Assistant</span>
          </NavLink>,
        )}
        <Tooltip
          label={
            refreshing && refreshStatus
              ? refreshStatus
              : canRefresh
                ? 'Refresh all accounts'
                : 'No connected accounts'
          }
        >
          <button
            type="button"
            className="rail__refresh"
            aria-label="Refresh all accounts"
            disabled={refreshing || !canRefresh}
            onClick={onRefreshAll}
          >
            {refreshing ? <Spinner size={18} /> : <RefreshCw size={18} />}
            {refreshing && refreshStatus ? (
              <span className="rail__label" role="status">
                {refreshStatus}
              </span>
            ) : null}
          </button>
        </Tooltip>
      </div>
    </nav>
  )
}
