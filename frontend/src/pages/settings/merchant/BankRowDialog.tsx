import { EyeOff, Link2Off } from 'lucide-react'

import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { Button, Dialog, DialogActions, DialogContent, useToast } from '@/components/ui'
import {
  describeMatchBasis,
  useIgnoreMerchantOrder,
  useAddMerchantMatch,
  useRemoveMerchantMatch,
  type MerchantMatchCandidate,
  type MerchantOrder,
} from '@/lib/clients/merchant'
import { capitalize, formatDate } from '@/lib/format'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'

import { BankRowCandidates } from './BankRowCandidates'
import { displayPayee } from '@/lib/transactions/edits'

/**
 * A person's word on which bank row a record is. The candidates are the
 * merchant's rows in the following weeks, nearest amount first; a search
 * widens to any row on any date.
 */
export function BankRowDialog({
  merchant,
  order,
  onClose,
}: {
  merchant: MerchantId
  order: MerchantOrder | null
  onClose: () => void
}) {
  const { noun } = MERCHANTS[merchant]
  const { show } = useToast()
  const moneyText = useMoneyText()
  const addMatch = useAddMerchantMatch()
  const unmatch = useRemoveMerchantMatch()
  const ignore = useIgnoreMerchantOrder(merchant)
  const busy = addMatch.isPending || unmatch.isPending || ignore.isPending

  // Picking always adds; rows paying for several records divide the payment
  // by their card totals.
  const pick = (row: MerchantMatchCandidate) => {
    if (order === null) return
    addMatch.mutate(
      { transactionId: row.id, orderId: order.id },
      {
        onSuccess: () => {
          show({
            title: row.matched_order_number
              ? `${capitalize(noun)} ${order.order_number} added to that payment`
              : `Matched to ${noun} ${order.order_number}`,
            description: `${formatDate(row.date, 'short')} · ${moneyText(row.amount)} · ${row.account_name}`,
          })
          onClose()
        },
      },
    )
  }

  return (
    <Dialog open={order !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        className="merchant-pick"
        title={order ? `Bank row for ${noun} ${order.order_number}` : 'Bank row'}
        description={
          order ? (
            <>
              {formatDate(order.ordered_on)} · {order.account_label} ·{' '}
              <Money value={order.card_total} tone="neutral" /> to the card
              {order.items.length > 0
                ? ` · ${order.items.map((item) => item.title).join(', ')}`
                : ''}
            </>
          ) : undefined
        }
        footer={
          order ? (
            <DialogActions
              cancel="Close"
              onCancel={onClose}
              start={
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() =>
                    ignore.mutate(
                      { orderId: order.id, ignored: !order.ignored },
                      { onSuccess: onClose },
                    )
                  }
                >
                  <EyeOff size={14} aria-hidden="true" />{' '}
                  {order.ignored ? `Stop ignoring this ${noun}` : 'Ignore: not on a tracked card'}
                </Button>
              }
            />
          ) : undefined
        }
      >
        {order === null ? null : (
          <>
            {order.matched_transactions.length > 0 ? (
              <section className="merchant-pick__current">
                <h3>Matched now</h3>
                <ul className="pick-list pick-list--scroll merchant-pick__list">
                  {order.matched_transactions.map((row) => (
                    <li key={row.id}>
                      <span className="pick-list__row merchant-pick__row">
                        <span className="pick-list__name">
                          {formatDate(row.date, 'short')} · {row.account_name}
                          <small>
                            {displayPayee(row)} · {describeMatchBasis(merchant, row.basis)}
                          </small>
                        </span>
                        <span className="pick-list__figures merchant-pick__figures">
                          <Money value={row.amount} tone="neutral" />
                        </span>
                      </span>
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={busy}
                        onClick={() =>
                          unmatch.mutate(
                            { transactionId: row.id, orderId: order.id },
                            {
                              onSuccess: () =>
                                show({
                                  title: 'Match undone',
                                  description:
                                    'The row keeps its category and splits; Match now may offer it an order again.',
                                }),
                            },
                          )
                        }
                      >
                        <Link2Off size={14} aria-hidden="true" /> Undo
                      </Button>
                    </li>
                  ))}
                </ul>
              </section>
            ) : null}

            <BankRowCandidates
              key={order.id}
              merchant={merchant}
              order={order}
              busy={busy}
              onPick={pick}
            />
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
