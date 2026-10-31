import { RefreshCw } from 'lucide-react'
import { Fragment, type ReactElement } from 'react'
import { NavLink } from 'react-router-dom'

import { BrandMark } from '@/components/BrandMark'
import { Spinner, Tooltip } from '@/components/ui'

import { DESTINATIONS } from './destinations'

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
}: NavRailProps) {
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

      {DESTINATIONS.map((destination, index) => {
        const previous = DESTINATIONS[index - 1]
        const Icon = destination.icon
        return (
          <Fragment key={destination.path}>
            {previous && previous.group !== destination.group ? (
              <span className="rail__separator" />
            ) : null}
            {named(
              destination.label,
              <NavLink
                to={destination.path}
                // Without `end`, "/" matches every route and the dashboard
                // reads as active on every page.
                end={destination.path === '/'}
                className="rail__link"
                aria-label={destination.label}
              >
                <Icon size={18} aria-hidden="true" />
                <span className="rail__label">{destination.label}</span>
              </NavLink>,
            )}
          </Fragment>
        )
      })}

      <span className="rail__spacer" />

      <div className="rail__foot">
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
