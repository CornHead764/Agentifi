import { ConfirmDialog, type Confirm } from '@/components/ui'
import type { WatchlistSummary } from '@/lib/watchlists'

/**
 * What deleting a watchlist does: the server soft-deletes the row and leaves
 * its filter alone, since filters are shared. Transactions are unaffected;
 * only the card goes.
 */
export function WatchlistDeleteDialog({ confirm }: { confirm: Confirm<WatchlistSummary> }) {
  return (
    <ConfirmDialog
      {...confirm.dialog}
      title={`Delete ${confirm.target?.name ?? 'this watchlist'}?`}
      description="Removes the card only. Its categories and transactions are unchanged."
      confirmLabel="Delete watchlist"
    />
  )
}
