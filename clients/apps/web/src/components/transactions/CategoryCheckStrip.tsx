/**
 * How far a request for category suggestions has got, above the register it
 * is changing, read from the server so a long one is still followed after a
 * reload. It stays after the last row lands: the comparison with the
 * categories the rows had, and the rows that could not be placed, are what is
 * worth acting on. Its link narrows the register to those rows through the
 * one Filter.
 */

import { Sparkles, X } from 'lucide-react'

import { Button, Callout, IconButton, Meter, Spinner } from '@/components/ui'
import {
  batchComparison,
  batchHeadline,
  undeterminedIn,
  type SuggestionBatch,
} from '@/lib/clients/suggestionBatches'

interface CategoryCheckStripProps {
  batch: SuggestionBatch
  /** Narrow the register to the rows the assistant could not place. */
  onShowUndetermined: () => void
  onCancel: () => void
  cancelling: boolean
  onDismiss: () => void
}

export function CategoryCheckStrip({
  batch,
  onShowUndetermined,
  onCancel,
  cancelling,
  onDismiss,
}: CategoryCheckStripProps) {
  const percent = batch.rows === 0 ? 100 : Math.round((batch.done / batch.rows) * 100)
  const undetermined = undeterminedIn(batch)
  const comparison = batchComparison(batch)
  return (
    <Callout
      className="check-strip"
      role="status"
      icon={batch.finished ? <Sparkles size={14} /> : <Spinner />}
      actions={
        <>
          {undetermined > 0 ? (
            <Button variant="ghost" size="sm" onClick={onShowUndetermined}>
              {undetermined} undetermined
            </Button>
          ) : null}
          {batch.finished || batch.cancelled ? null : (
            <Button variant="ghost" size="sm" disabled={cancelling} onClick={onCancel}>
              {cancelling ? 'Cancelling…' : 'Cancel'}
            </Button>
          )}
          <IconButton label="Dismiss" variant="ghost" size="sm" onClick={onDismiss}>
            <X size={14} />
          </IconButton>
        </>
      }
    >
      <span>{batchHeadline(batch)}</span>
      {/* Hidden from the reader once it is full: the sentence above already
          says so, and a completed bar reads as something still running. */}
      {batch.finished ? null : (
        <Meter
          size="xs"
          label="Rows done"
          value={batch.done}
          max={batch.rows}
          segments={[{ value: percent }]}
        />
      )}
      {comparison === null ? null : <span className="muted">{comparison}</span>}
    </Callout>
  )
}
