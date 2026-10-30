/**
 * "Link a payment", from a recurring slot: a past-due reminder or a bill in
 * the plan. The reverse of `LinkSeriesDialog`, which starts from the row; both
 * file the row under the slot through the same link.
 */

import { Receipt } from 'lucide-react'
import { useMemo, useState } from 'react'

import { Money } from '@/components/Money'
import {
  DialogContent,
  EmptyState,
  FormDialog,
  SearchInput,
  SkeletonRows,
  useHeld,
  useToast,
} from '@/components/ui'
import { slotWindow } from '@/lib/clients/upcoming'
import { formatDate } from '@/lib/format'
import { displayPayee } from '@/lib/transactions/edits'
import { paymentCandidates, type PaymentSlot } from '@/lib/transactions/paymentCandidates'
import { useAccountName, useLinkSeries, useTransactionsBetween } from '@/lib/transactions/queries'
import type { Transaction } from '@/lib/transactions/types'

export function LinkPaymentDialog({
  slot: openSlot,
  onClose,
}: {
  slot: PaymentSlot | null
  onClose: () => void
}) {
  const slot = useHeld(openSlot)
  return (
    <FormDialog open={openSlot !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      {slot === null ? null : <LinkPaymentForm slot={slot} onClose={onClose} />}
    </FormDialog>
  )
}

function matches(row: Transaction, search: string): boolean {
  const needle = search.trim().toLowerCase()
  if (needle === '') return true
  return [displayPayee(row), row.statement_name].some((text) =>
    text.toLowerCase().includes(needle),
  )
}

function LinkPaymentForm({ slot, onClose }: { slot: PaymentSlot; onClose: () => void }) {
  const [search, setSearch] = useState('')
  const toast = useToast()
  const accountName = useAccountName()
  const link = useLinkSeries()

  const bounds = useMemo(() => slotWindow(slot.due_on), [slot.due_on])
  const rows = useTransactionsBetween(bounds.from, bounds.to)
  const candidates = useMemo(
    () => paymentCandidates(slot, rows.data?.items ?? []).filter((row) => matches(row, search)),
    [slot, rows.data, search],
  )

  const choose = (row: Transaction) => {
    link.mutate(
      { id: row.id, seriesId: slot.series_id, dueOn: slot.due_on },
      {
        onSuccess: () =>
          toast.show({
            title: 'Linked to the recurring item',
            description: `${displayPayee(row)} pays ${slot.label}, due ${formatDate(slot.due_on)}.`,
          }),
      },
    )
    onClose()
  }

  return (
    <DialogContent
      fills
      className="link-series"
      title="Link a payment"
      description={
        <>
          {slot.label} · <Money value={slot.amount} tone="flow" /> · due {formatDate(slot.due_on)}
        </>
      }
    >
      <div className="link-series__filters">
        <SearchInput
          className="link-series__search"
          placeholder="Search payees"
          aria-label="Search payees"
          value={search}
          onChange={setSearch}
        />
      </div>

      {rows.isPending ? (
        <div className="dialog__scroller">
          <SkeletonRows rows={5} />
        </div>
      ) : candidates.length === 0 ? (
        <EmptyState
          compact
          className="dialog__scroller"
          title="No unlinked charge around this date."
        />
      ) : (
        <ul className="pick-list dialog__scroller">
          {candidates.map((row) => (
            <li key={row.id}>
              <button
                type="button"
                className="pick-list__row"
                disabled={link.isPending}
                onClick={() => choose(row)}
              >
                <Receipt size={13} aria-hidden="true" />
                <span className="pick-list__name">
                  {displayPayee(row)}
                  <small>
                    {formatDate(row.date)} · {accountName(row.account_id)}
                  </small>
                </span>
                <span className="pick-list__figures">
                  <Money value={row.amount} tone="flow" />
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </DialogContent>
  )
}
