import { ExternalLink, EyeOff, Link2 } from 'lucide-react'
import { Link } from 'react-router-dom'

import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { Money } from '@/components/Money'
import { Badge, OverflowMenu, RowActions, Td } from '@/components/ui'
import {
  describeMatchBasis,
  describeSource,
  registerLinkFor,
  type MerchantOrder,
  type MerchantOrderItem,
} from '@/lib/clients/merchant'
import { formatDate } from '@/lib/format'
import {
  itemLabel,
  kindLabel,
  MERCHANTS,
  orderLinkText,
  orderUrl,
  type MerchantId,
} from '@/lib/merchants'
import { orderSubject } from '@/lib/assistant/subjects'
import { ZERO_MONEY } from '@/lib/money'

export function OrderRow({
  merchant,
  order,
  onPick,
  onIgnore,
}: {
  merchant: MerchantId
  order: MerchantOrder
  onPick: () => void
  onIgnore: (ignored: boolean) => void
}) {
  const { name, noun, kinds, hasGiftCardBalance } = MERCHANTS[merchant]
  const matched = order.matched_transaction_ids.length
  const url = orderUrl(merchant, order)
    // A shop with doors writes receipts as well as orders; an online-only one
    // has one kind and says nothing.
  const place = [kinds.length > 1 ? kindLabel(order.kind) : '', order.location]
    .filter((part) => part !== '')
    .join(' · ')
  return (
    <tr>
      <Td className="nowrap merchant-order__when" label="">
        <div>{formatDate(order.ordered_on)}</div>
        <div className="cell__sub cell__clip merchant-small" title={place || undefined}>
          {url ? (
            <a className="merchant-order-link" href={url} target="_blank" rel="noreferrer noopener">
              {orderLinkText(order)}
              <ExternalLink size={11} aria-hidden="true" />
              <span className="visually-hidden">{`Open this ${noun} on ${name}`}</span>
            </a>
          ) : (
            orderLinkText(order)
          )}
          {place ? ` · ${place}` : null}
        </div>
      </Td>
      <Td className="stack-inline" label="Account">
        <div className="cell__clip" title={order.account_label}>
          {order.account_label}
        </div>
        {/* Most orders are read off the shop's site by the daily update, so
            only one that came in a file says where it came from. */}
        {order.source === 'agentifi_json' ? null : (
          <div className="cell__sub cell__clip merchant-small">
            {describeSource(merchant, order.source)}
          </div>
        )}
      </Td>
      <Td className="stack-inline merchant-order__items" label="Items">
        <OrderItems items={order.items} />
      </Td>
      <Td numeric className="stack-inline nowrap" label="Total">
        <div>
          <Money value={order.total} tone="neutral" />
          {hasGiftCardBalance && order.gift_card_amount !== null &&
          order.gift_card_amount !== ZERO_MONEY ? (
            <div className="cell__sub merchant-small">
              {order.paid_by_gift_card ? (
                'all from a gift card'
              ) : (
                <>
                  <Money value={order.card_total} tone="neutral" /> to the card
                </>
              )}
            </div>
          ) : null}
        </div>
      </Td>
      <Td className="stack-inline" label="Bank row">
        {matched === 0 ? (
          order.ignored ? (
            <Badge tone="neutral">ignored</Badge>
          ) : order.cancelled ? (
            <Badge tone="neutral">cancelled</Badge>
          ) : order.paid_by_gift_card ? (
            <Badge tone="neutral">gift card</Badge>
          ) : (
            <Badge tone="neutral">none yet</Badge>
          )
        ) : (
          <>
            {order.matched_transactions.slice(0, 1).map((row) => (
              <Link
                key={row.id}
                className="cell__clip merchant-small"
                to={registerLinkFor(row)}
                title={`${describeMatchBasis(merchant, row.basis)} · ${row.statement_name}`}
              >
                {formatDate(row.date, 'short')} · <Money value={row.amount} tone="neutral" /> ·{' '}
                {row.account_name}
              </Link>
            ))}
            {matched > 1 || order.ignored ? (
              <span className="cell__sub merchant-small">
                {matched > 1 ? `and ${matched - 1} more` : null}
                {matched > 1 && order.ignored ? ' · ' : null}
                {order.ignored ? <Badge tone="neutral">ignored</Badge> : null}
              </span>
            ) : null}
          </>
        )}
      </Td>
      <Td numeric className="stack-corner" label="">
        <RowActions>
          <OverflowMenu
            label={`Actions for ${noun} ${order.order_number}`}
            actions={[
              <AskMenuItem
                subject={() =>
                  orderSubject({
                    id: order.id,
                    merchant: order.merchant,
                    title: `${MERCHANTS[order.merchant].name} ${noun} ${order.order_number}`,
                    total: order.total,
                    ordered_on: order.ordered_on,
                  })
                }
              />,
              {
                label: matched === 0 ? 'Match a bank row…' : 'Change the bank row…',
                icon: <Link2 size={14} />,
                onSelect: onPick,
              },
              {
                label: order.ignored ? `Stop ignoring this ${noun}` : 'Ignore: not on a tracked card',
                icon: <EyeOff size={14} />,
                onSelect: () => onIgnore(!order.ignored),
              },
            ]}
          />
        </RowActions>
      </Td>
    </tr>
  )
}

/**
 * The first item on the row's first line and the rest on its second, each cut
 * to one line; the tooltip lists every item.
 */
function OrderItems({ items }: { items: MerchantOrderItem[] }) {
  if (items.length === 0) return <span className="muted">No items in the file</span>
  const labels = items.map(
    (item) => `${item.quantity > 1 ? `${item.quantity} × ` : ''}${itemLabel(item)}`,
  )
  const all = labels.join('\n')
  const [first, ...rest] = labels
  return (
    <>
      <span className="cell__clip merchant-small" title={all}>
        {first}
      </span>
      {rest.length > 0 ? (
        <span className="cell__sub cell__clip merchant-small" title={all}>
          {rest.join(', ')}
        </span>
      ) : null}
    </>
  )
}
