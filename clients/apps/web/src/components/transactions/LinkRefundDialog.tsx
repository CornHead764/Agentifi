/**
 * "This refunds…", from a credit: links it to the charge so the money goes
 * back under that charge's category rather than counting as income.
 *
 * The list is the server's ranking over the hundred days before the credit; a
 * search reaches past that window. Existing links are shown with a way to
 * release them.
 */

import { Undo2, X } from 'lucide-react'
import { useState } from 'react'

import { Money } from '@/components/Money'
import {
  Button,
  Dialog,
  DialogContent,
  EmptyState,
  SearchInput,
  SkeletonRows,
} from '@/components/ui'
import {
  useRefundCandidates,
  useRefundLinks,
  useUnlinkRefund,
  type RefundCharge,
} from '@/lib/clients/refunds'
import { formatDate } from '@/lib/format'
import { displayPayee } from '@/lib/transactions/edits'
import type { Transaction, Uuid } from '@/lib/transactions/types'

export function LinkRefundDialog({
  txn,
  onClose,
  onLink,
}: {
  /** Null closes the dialog, the way every other row dialog is driven. */
  txn: Transaction | null
  onClose: () => void
  onLink: (refundId: Uuid, chargeId: Uuid) => void
}) {
  const [search, setSearch] = useState('')
  const links = useRefundLinks(txn?.id ?? null)
  const candidates = useRefundCandidates(txn?.id ?? null, search, txn !== null)
  const unlink = useUnlinkRefund()

  const close = () => {
    setSearch('')
    onClose()
  }

  const linked = links.data?.refunds ?? []
  const linkedIds = new Set(linked.map((one) => one.id))
  // A charge already linked is not offered a second time: the same statement made
  // twice is not an error the server minds, and a row in both lists reads as one.
  const offered = (candidates.data?.candidates ?? []).filter((one) => !linkedIds.has(one.id))

  return (
    <Dialog
      open={txn !== null}
      onOpenChange={(open) => {
        if (open) return
        close()
      }}
    >
      <DialogContent
        fills
        className="link-refund"
        title="What does this refund?"
        description={
          txn === null ? undefined : (
            <>
              {displayPayee(txn)} · <Money value={txn.amount} tone="flow" /> ·{' '}
              {formatDate(txn.date)}
            </>
          )
        }
      >
        {txn === null ? null : (
          <>
            {linked.length === 0 ? null : (
              <section className="link-refund__linked">
                <h3 className="eyebrow">Refunds</h3>
                <ul className="pick-list link-refund__list">
                  {linked.map((one) => (
                    <li key={one.id}>
                      <ChargeSummary charge={one} />
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          unlink.mutate({ refundId: txn.id, chargeId: one.id })
                        }
                      >
                        <Undo2 size={13} aria-hidden="true" />
                        Release
                      </Button>
                    </li>
                  ))}
                </ul>
                <p className="link-refund__note muted">
                  This credit comes off {linked[0].category_name ?? 'the charge’s category'}{' '}
                  rather than counting as income.
                </p>
              </section>
            )}

            <SearchInput
              className="link-refund__search"
              placeholder="Search earlier charges"
              aria-label="Search earlier charges"
              value={search}
              onChange={setSearch}
            />

            {candidates.isPending ? (
              <div className="dialog__scroller">
                <SkeletonRows rows={5} />
              </div>
            ) : offered.length === 0 ? (
              <EmptyState
                compact
                className="dialog__scroller"
                title={
                  search.trim() === ''
                    ? 'No recent charge big enough. Search to look further back.'
                    : 'No charge big enough matches that.'
                }
              />
            ) : (
              <ul className="pick-list link-refund__list link-refund__list--offer dialog__scroller">
                {offered.map((one) => (
                  <li key={one.id}>
                    <button
                      type="button"
                      onClick={() => {
                        onLink(txn.id, one.id)
                        setSearch('')
                      }}
                    >
                      <ChargeSummary charge={one} />
                    </button>
                  </li>
                ))}
              </ul>
            )}

            <p className="link-refund__foot muted">
              <X size={13} aria-hidden="true" />
              Nothing here? File the credit under the charge&rsquo;s category instead.
            </p>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

/** One charge, named the way a person recognises it: shop, date, amount, category. */
function ChargeSummary({ charge }: { charge: RefundCharge }) {
  return (
    <span className="link-refund__charge">
      <span className="pick-list__name">
        {charge.payee === '' ? charge.statement_name : charge.payee}
        <small>
          {formatDate(charge.date)} · {charge.account_name}
        </small>
      </span>
      <span className="pick-list__figures">
        <Money value={charge.amount} tone="flow" />
        <small>{charge.category_name ?? 'Uncategorized'}</small>
      </span>
    </span>
  )
}
