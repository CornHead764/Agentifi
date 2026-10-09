import { ExternalLink, Package, Undo2 } from 'lucide-react'

import { ItemList } from '@/components/ItemList'
import { Money } from '@/components/Money'
import { Button, EmptyState } from '@/components/ui'
import type { MerchantOrder, MerchantRefund } from '@/lib/clients/merchant'
import { describeMatchBasis, useMerchantMatch } from '@/lib/clients/merchant'
import { formatDate } from '@/lib/format'
import { kindLabel, MERCHANTS, merchantsFor, orderLinkText, orderUrl } from '@/lib/merchants'
import { ZERO_MONEY } from '@/lib/money'
import type { Transaction } from '@/lib/transactions/types'

/**
 * What a shop sold this row, in the row's detail, with the way to change it;
 * an unexplained row offers the picker. Shown when the wording names a
 * merchant or a match exists (a hand match can be invisible to the wording).
 * Each line names its own merchant. The picker is the page's single mount.
 *
 * A credit shows only which order it came back from: its items and returns
 * are the purchase's, which RefundLinksPanel links to.
 */
export function MerchantPanel({
  transaction,
  onPick,
}: {
  transaction: Transaction
  onPick: () => void
}) {
  const match = useMerchantMatch(transaction.id)
  const orders = match.data?.orders ?? []
  const credit = transaction.amount > ZERO_MONEY

  if (merchantsFor(transaction).length === 0 && orders.length === 0) return null

  return (
    <div className="txn-merchant txn-form__full">
      <p className="filter-panel__section-title">Purchase</p>
      {match.isPending ? null : orders.length === 0 ? (
        <EmptyState compact title="Nothing on file explains this row." />
      ) : (
        <ul className="txn-merchant__list">
          {orders.map(({ order, basis, amount }) => {
            const { name, noun, kinds } = MERCHANTS[order.merchant]
            const url = orderUrl(order.merchant, order)
            const place =
              kinds.length > 1
                ? [kindLabel(order.kind), order.location].filter(Boolean).join(', ')
                : ''
            return (
              <li key={order.id}>
                <span className="row txn-merchant__head">
                  <Package size={12} aria-hidden="true" />
                  <span>
                    {name} · {order.account_label} ·{' '}
                    {place ? `${place} · ` : ''}
                    {formatDate(order.ordered_on, 'short')} ·{' '}
                    {url ? (
                      <a
                        className="merchant-order-link"
                        href={url}
                        target="_blank"
                        rel="noreferrer noopener"
                      >
                        {orderLinkText(order)}
                        <ExternalLink size={11} aria-hidden="true" />
                        <span className="visually-hidden">{`Open this ${noun} on ${name}`}</span>
                      </a>
                    ) : (
                      orderLinkText(order)
                    )}
                  </span>
                  <Money
                    value={orders.length > 1 ? amount : order.card_total}
                    signs="absolute"
                    tone="neutral"
                  />
                </span>
                {credit ? null : (
                  <>
                    <ItemList className="txn-merchant__items" items={order.items} />
                    <ReturnedLines order={order} />
                  </>
                )}
                <span className="hint hint--faint">
                  {describeMatchBasis(order.merchant, basis)}
                </span>
              </li>
            )
          })}
        </ul>
      )}
      <Button variant="ghost" size="sm" onClick={onPick}>
        <Package size={14} aria-hidden="true" />{' '}
        {orders.length === 0 ? 'Match a purchase…' : 'Change the purchase…'}
      </Button>
    </div>
  )
}

/** What the merchant's records say came back out of the order. */
function ReturnedLines({ order }: { order: MerchantOrder }) {
  if (order.refunds.length === 0) return null
  return (
    <ul className="txn-merchant__returns">
      {order.refunds.map((refund) => (
        <li key={refund.id}>
          <Undo2 size={12} aria-hidden="true" />
          <span>
            {returnedLabel(refund, order)}
            {refund.to_gift_card ? ' · to the gift card balance' : ''}
          </span>
          <Money value={refund.amount} signs="absolute" tone="neutral" />
        </li>
      ))}
    </ul>
  )
}

/** What came back, named the way the order names it. */
function returnedLabel(refund: MerchantRefund, order: MerchantOrder) {
  const item = order.items.find((one) => refund.sku !== '' && one.sku === refund.sku)
  const title = item?.catalog?.title || item?.title || refund.title
  return title ? `Returned ${title}` : 'Refunded'
}
