/**
 * The target a watchlist was given, and how much is left. `left_to_target`
 * goes negative once breached, so it prints absolute with a word saying which
 * side of zero it is on.
 */

import { Money } from '@/components/Money'
import { Badge, Meter } from '@/components/ui'
import { targetBarPct, type WatchlistSummary } from '@/lib/watchlists'

export function WatchlistTarget({ watchlist }: { watchlist: WatchlistSummary }) {
  if (watchlist.target_amount === null || watchlist.left_to_target === null) return null

  return (
    <div className="wl-target">
      <p className="hint">
        Target <Money value={watchlist.target_amount} signs="absolute" tone="neutral" /> ·{' '}
        <Money value={watchlist.left_to_target} signs="absolute" tone="neutral" />{' '}
        {watchlist.is_over_target ? 'over' : 'left'}
      </p>
      <Meter
        size="sm"
        label="Spent against the target"
        segments={[
          {
            value: targetBarPct(watchlist),
            tone: watchlist.is_over_target
              ? 'expense'
              : watchlist.is_projected_over_target
                ? 'warning'
                : 'accent',
          },
        ]}
      />
    </div>
  )
}

/** Breached now, or on course to be by month end. Nothing without a target. */
export function WatchlistTargetBadge({ watchlist }: { watchlist: WatchlistSummary }) {
  if (watchlist.target_amount === null) return null
  if (watchlist.is_over_target) return <Badge tone="expense">Over target</Badge>
  if (watchlist.is_projected_over_target) return <Badge tone="warning">Projected over</Badge>
  return null
}
