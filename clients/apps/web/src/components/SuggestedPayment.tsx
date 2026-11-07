/**
 * "Is this it?": the rows that could be the payment for an occurrence marked
 * paid, asked as it is marked and suggested on the mark wherever it shows.
 * Linking one is the same hand link as "Link a payment"; the wording is not
 * compared, so each row is shown by its statement name.
 */

import { useState, type ReactNode } from 'react'

import { Money } from '@/components/Money'
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  useHeld,
  useToast,
} from '@/components/ui'
import {
  type MarkedSlot,
  type SuggestedPayment,
  useSuggestedPayments,
} from '@/lib/clients/upcoming'
import { formatDate } from '@agentifi/core/format'
import { useLinkSeries } from '@/lib/transactions/queries'

function useLinkSuggested(slot: MarkedSlot) {
  const link = useLinkSeries()
  const toast = useToast()
  const choose = (row: SuggestedPayment, then?: () => void) =>
    link.mutate(
      { id: row.id, seriesId: slot.series_id, dueOn: slot.due_on },
      {
        onSuccess: () =>
          toast.show({
            title: 'Linked to the recurring item',
            description: `${row.payee || row.statement_name} pays ${slot.label}, due ${formatDate(slot.due_on)}.`,
          }),
        onSettled: then,
      },
    )
  return { choose, pending: link.isPending }
}

/**
 * Asked once a mark is on file, so "Mark paid anyway" only closes it. Open
 * while `candidates` is not empty.
 */
export function IsThisItDialog({
  slot,
  candidates,
  onClose,
}: {
  slot: MarkedSlot
  candidates: readonly SuggestedPayment[]
  onClose: () => void
}) {
  const shown = useHeld(candidates.length > 0 ? candidates : null)
  return (
    <Dialog open={candidates.length > 0} onOpenChange={(open) => (open ? undefined : onClose())}>
      {shown === null ? null : <IsThisItBody slot={slot} candidates={shown} onClose={onClose} />}
    </Dialog>
  )
}

function IsThisItBody({
  slot,
  candidates,
  onClose,
}: {
  slot: MarkedSlot
  candidates: readonly SuggestedPayment[]
  onClose: () => void
}) {
  const { choose, pending } = useLinkSuggested(slot)
  const verb = slot.income ? 'received' : 'paid'

  return (
    <DialogContent
      title="Is this it?"
      description={`${slot.label}, due ${formatDate(slot.due_on)}, is marked ${verb}. ${
        candidates.length === 1 ? 'This transaction' : 'One of these transactions'
      } may already be the ${slot.income ? 'deposit' : 'payment'}.`}
      footer={
        <DialogActions cancel={false}>
          <Button onClick={onClose} disabled={pending}>
            Mark {verb} anyway
          </Button>
        </DialogActions>
      }
    >
      <ul className="pick-list">
        {candidates.map((row) => (
          <li key={row.id} className="pick-list__row">
            <span className="pick-list__name">
              {row.statement_name}
              <small>{formatDate(row.date)}</small>
            </span>
            <span className="pick-list__figures">
              <Money value={row.amount} tone="flow" />
            </span>
            <Button size="sm" variant="primary" disabled={pending} onClick={() => choose(row, onClose)}>
              Link this
            </Button>
          </li>
        ))}
      </ul>
    </DialogContent>
  )
}

/**
 * The nearest row that could be the payment for a slot marked paid, as one
 * quiet line: computed on read, and gone once a row is linked. `inline` is a
 * row's second line, `fallback` standing in for it when there is no
 * suggestion; the statement name gives way first so the link stays in reach.
 */
export function SuggestedPaymentLine({
  slot,
  inline = false,
  fallback = null,
}: {
  slot: MarkedSlot
  inline?: boolean
  fallback?: ReactNode
}) {
  const suggested = useSuggestedPayments(slot.series_id, slot.due_on)
  const { choose, pending } = useLinkSuggested(slot)
  const [asking, setAsking] = useState<readonly SuggestedPayment[]>([])
  const rows = suggested.data ?? []
  const best = rows[0]
  if (best === undefined) return fallback

  const label = `Link ${best.statement_name} on ${formatDate(best.date)} to ${slot.label}`
  const more =
    rows.length > 1 ? (
      <button type="button" className="statement-link" onClick={() => setAsking(rows)}>
        {rows.length - 1} more
      </button>
    ) : null
  const dialog = <IsThisItDialog slot={slot} candidates={asking} onClose={() => setAsking([])} />

  if (inline) {
    return (
      <span className="suggested-payment suggested-payment--inline">
        <span>Is this it?</span>
        <button
          type="button"
          className="statement-link suggested-payment__name"
          disabled={pending}
          aria-label={label}
          title={`Link this: ${best.statement_name}`}
          onClick={() => choose(best)}
        >
          {formatDate(best.date, 'short')} · {best.statement_name}
        </button>
        {more}
        {dialog}
      </span>
    )
  }
  return (
    <p className="suggested-payment">
      <span>Is this it?</span>
      <span>
        {best.statement_name}, {formatDate(best.date, 'short')},
      </span>
      <Money value={best.amount} tone="flow" />
      <button
        type="button"
        className="statement-link"
        disabled={pending}
        aria-label={label}
        onClick={() => choose(best)}
      >
        Link this
      </button>
      {more}
      {dialog}
    </p>
  )
}
