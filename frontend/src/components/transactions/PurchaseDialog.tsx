import { ExternalLink, Link2Off, Package } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { ItemList } from '@/components/ItemList'
import { Money } from '@/components/Money'
import {
  Badge,
  Button,
  Callout,
  ChipGroup,
  DialogActions,
  DialogContent,
  EmptyState,
  FormDialog,
  SearchInput,
  SkeletonRows,
  useHeld,
  useToast,
} from '@/components/ui'
import {
  describeMatchBasis,
  paysToTheCent,
  useAddMerchantMatch,
  useMerchantMatch,
  useMerchantOrderCandidates,
  useRemoveMerchantMatch,
  type MerchantOrder,
} from '@/lib/clients/merchant'
import { describeApiError } from '@/lib/errors'
import { formatDate } from '@/lib/format'
import {
  ALL_MERCHANTS,
  kindLabel,
  MERCHANTS,
  merchantsFor,
  orderUrl,
  type MerchantId,
} from '@/lib/merchants'
import { displayPayee } from '@/lib/transactions/edits'
import type { Transaction } from '@/lib/transactions/types'
import { useSettled } from '@/lib/useSettled'

/** The merchant the candidate list is narrowed to, or every one of them. */
type Filter = MerchantId | 'all'

/**
 * The purchase behind a bank row, from the row's side, across every shop.
 * Offered: records from the two months before the row, nearest amount first;
 * a search reaches any record. A row can pay for several records.
 */
export function PurchaseDialog({
  txn: openTxn,
  onClose,
}: {
  txn: Transaction | null
  onClose: () => void
}) {
  const txn = useHeld(openTxn)
  // A row whose wording names no shop gets no link.
  const settingsFor = txn ? (merchantsFor(txn)[0] ?? null) : null
  return (
    <FormDialog open={openTxn !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        fills
        className="merchant-pick"
        title="Purchase"
        description={
          txn ? (
            <>
              {displayPayee(txn)} · <Money value={txn.amount} tone="flow" /> · {formatDate(txn.date)}
            </>
          ) : undefined
        }
        footer={
          <DialogActions
            cancel="Close"
            onCancel={onClose}
            start={
              settingsFor ? (
                <Button variant="ghost" size="sm" asChild>
                  <Link to={MERCHANTS[settingsFor].settingsPath}>
                    <ExternalLink size={13} aria-hidden="true" /> Settings →{' '}
                    {MERCHANTS[settingsFor].name}
                  </Link>
                </Button>
              ) : null
            }
          />
        }
      >
        {txn ? <PurchaseBody txn={txn} onClose={onClose} /> : null}
      </DialogContent>
    </FormDialog>
  )
}

function PurchaseBody({ txn, onClose }: { txn: Transaction; onClose: () => void }) {
  const toast = useToast()
  const match = useMerchantMatch(txn.id)
  const [filter, setFilter] = useState<Filter>('all')
  const [search, setSearch] = useState('')
  const debounced = useSettled(search)
  const candidates = useMerchantOrderCandidates(
    txn.id,
    debounced,
    filter === 'all' ? null : filter,
  )
  const add = useAddMerchantMatch()
  const remove = useRemoveMerchantMatch()
  const busy = add.isPending || remove.isPending

  // A 404 is the ordinary answer for a row with nothing behind it.
  const matched = match.data?.orders ?? []
  // Picking always adds; Undo on the matched list is the only removal.
  const pick = (order: MerchantOrder) => {
    const { noun } = MERCHANTS[order.merchant]
    add.mutate(
      { transactionId: txn.id, orderId: order.id },
      {
        onSuccess: () => {
          toast.show({
            title:
              matched.length > 0
                ? `Also pays for ${noun} ${order.order_number}`
                : `Matched to ${noun} ${order.order_number}`,
          })
          onClose()
        },
      },
    )
  }

  return (
    <>
      <section className="merchant-pick__current">
        <h3>
          {matched.length === 0
            ? 'No purchase matched'
            : matched.length === 1
              ? 'Matched now'
              : 'Pays for these purchases'}
        </h3>
        {match.isPending ? (
          <SkeletonRows rows={2} />
        ) : matched.length === 0 ? (
          match.isError &&
          match.error instanceof Error &&
          !/404/.test(match.error.message) &&
          !('status' in match.error && match.error.status === 404) ? (
            <Callout tone="expense">{describeApiError(match.error, 'load')}</Callout>
          ) : (
            <EmptyState compact title="Nothing on file explains this row yet. Choose a purchase below." />
          )
        ) : (
          <ul className="pick-list pick-list--scroll merchant-pick__list">
            {matched.map(({ order, amount, basis }) => (
              <li key={order.id}>
                <span className="pick-list__row merchant-pick__row">
                  <span className="pick-list__name">
                    <OrderLine order={order} link />
                    <small>
                      {describeMatchBasis(order.merchant, basis)}
                      {matched.length > 1 ? (
                        <>
                          {' · share '}
                          <Money value={amount} signs="absolute" tone="neutral" />
                        </>
                      ) : null}
                    </small>
                  </span>
                  <span className="pick-list__figures merchant-pick__figures">
                    <Money value={order.card_total} signs="absolute" tone="neutral" />
                  </span>
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() =>
                    remove.mutate(
                      { transactionId: txn.id, orderId: order.id },
                      { onSuccess: () => toast.show({ title: 'Match undone' }) },
                    )
                  }
                >
                  <Link2Off size={13} aria-hidden="true" /> Undo
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <SearchInput
        placeholder="Search purchases by number or item"
        value={search}
        onChange={setSearch}
        aria-label="Search purchases"
      />

      <MerchantFilter value={filter} onChange={setFilter} />

      {candidates.isPending ? (
        <div className="dialog__scroller">
          <SkeletonRows rows={4} />
        </div>
      ) : !candidates.data || candidates.data.candidates.length === 0 ? (
        <EmptyState
          compact
          className="dialog__scroller"
          title={
            debounced
              ? 'Nothing on file, from any date, mentions that.'
              : `No ${filter === 'all' ? '' : `${MERCHANTS[filter].name} `}purchase in the two months before this row. Search by order number or item.`
          }
        />
      ) : (
        <ul className="pick-list pick-list--scroll merchant-pick__list dialog__scroller">
          {candidates.data.candidates.map((order) => {
            const elsewhere = order.matched_transactions.filter((row) => row.id !== txn.id)
            return (
              <li key={order.id}>
                <button
                  type="button"
                  className="pick-list__row merchant-pick__row"
                  disabled={busy}
                  onClick={() => pick(order)}
                >
                  <span className="pick-list__name">
                    <OrderLine order={order} />
                    <small>
                      <ItemList items={order.items} limit={5} inline />
                      {order.ignored ? <span className="merchant-pick__note">Ignored</span> : null}
                      {elsewhere.length > 0 ? (
                        <span className="merchant-pick__note">
                          {'Already paid by '}
                          {elsewhere.map((row, index) => (
                            <span key={row.id}>
                              {index > 0 ? ', ' : ''}
                              {`${formatDate(row.date, 'short')} `}
                              <Money value={row.amount} tone="neutral" />
                            </span>
                          ))}
                        </span>
                      ) : null}
                    </small>
                  </span>
                  <span className="pick-list__figures merchant-pick__figures">
                    <Money value={order.card_total} signs="absolute" tone="neutral" />
                    {paysToTheCent(txn.amount, order.card_total) ? (
                      <small>to the cent</small>
                    ) : null}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}

      <SettingsLink txn={txn} filter={filter} />
    </>
  )
}

/** Which shop to offer. It wraps rather than scrolling sideways. */
function MerchantFilter({
  value,
  onChange,
}: {
  value: Filter
  onChange: (next: Filter) => void
}) {
  const options: { id: Filter; label: string }[] = [{ id: 'all', label: 'All' }]
  for (const id of ALL_MERCHANTS) options.push({ id, label: MERCHANTS[id].name })
  return (
    <ChipGroup
      label="Which shop"
      layout="wrap"
      className="merchant-pick__filter"
      value={value}
      options={options.map((option) => ({ value: option.id, label: option.label }))}
      onChange={onChange}
    />
  )
}

/** The way through to the shop's own screen: the filtered shop, or the one the wording names. */
function SettingsLink({ txn, filter }: { txn: Transaction; filter: Filter }) {
  const merchant = filter === 'all' ? (merchantsFor(txn)[0] ?? null) : filter
  if (merchant === null) return null
  const { name, settingsPath } = MERCHANTS[merchant]
  return (
    <Button variant="ghost" size="sm" asChild className="merchant-pick__settings">
      <Link to={settingsPath}>
        <ExternalLink size={13} aria-hidden="true" /> Settings → {name}
      </Link>
    </Button>
  )
}

/**
 * One record, named by its shop. The kind is shown only where a shop has more
 * than one. `link` is off in the candidate list, because an anchor inside the
 * picking button is invalid.
 */
function OrderLine({ order, link = false }: { order: MerchantOrder; link?: boolean }) {
  const { name, noun, kinds } = MERCHANTS[order.merchant]
  const url = link ? orderUrl(order.merchant, order) : null
  const place =
    kinds.length > 1 ? [kindLabel(order.kind), order.location].filter(Boolean).join(', ') : ''
  return (
    <span className="merchant-order-line">
      <Package size={12} aria-hidden="true" />
      {name} · {order.account_label} ·{place ? ` ${place} ·` : ''}{' '}
      {formatDate(order.ordered_on, 'short')} ·{' '}
      {url ? (
        <a className="merchant-order-link" href={url} target="_blank" rel="noreferrer noopener">
          {order.order_number}
          <ExternalLink size={11} aria-hidden="true" />
          <span className="visually-hidden">{`Open this ${noun} on ${name}`}</span>
        </a>
      ) : (
        order.order_number
      )}
      {order.cancelled ? <Badge tone="neutral">cancelled</Badge> : null}
    </span>
  )
}
