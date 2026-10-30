import { Meter } from '@/components/ui'
import type { SyncRun } from '@/lib/clients/connections'

import { syncProgressLine, syncShare } from './syncText'

/** A running sync's line and bar. */
export function SyncProgress({ run }: { run: SyncRun }) {
  const line = syncProgressLine(run)
  return (
    <div className="sync-progress" role="status">
      <span className="hint">{line}…</span>
      <Meter size="xs" segments={[{ value: syncShare(run) }]} label="Sync progress" valueText={line} />
    </div>
  )
}
