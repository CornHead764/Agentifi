import { Undo2 } from 'lucide-react'
import { Link } from 'react-router-dom'

import { Money } from '@/components/Money'
import { useRefundLinks, type RefundCharge, type RefundLinks } from '@/lib/clients/refunds'
import { formatDate } from '@/lib/format'
import { registerLinkFor } from '@/lib/transactions/links'
import type { Uuid } from '@/lib/transactions/types'

/**
 * The other end of a refund link, in the row's detail: a credit names the
 * purchase it gives back, and a purchase says whether it was refunded in full
 * or in part and by which credits. Each opens that row. Nothing shows for a
 * row with no links.
 */
export function RefundLinksPanel({ transactionId }: { transactionId: Uuid }) {
  const links = useRefundLinks(transactionId).data
  if (!links || (links.refunds.length === 0 && links.refunded_by.length === 0)) return null
  return (
    <div className="txn-refunds txn-form__full">
      {links.refunds.length > 0 ? (
        <>
          <p className="filter-panel__section-title">Refund of</p>
          <RefundRows rows={links.refunds} />
        </>
      ) : null}
      {links.refunded_by.length > 0 ? (
        <>
          <p className="filter-panel__section-title">{refundedTitle(links.refund_state)}</p>
          <RefundRows rows={links.refunded_by} />
        </>
      ) : null}
    </div>
  )
}

/** The server's word on how much came back; see domain.RefundState. */
function refundedTitle(state: RefundLinks['refund_state']): string {
  return state === 'full' ? 'Fully refunded' : 'Partially refunded'
}

function RefundRows({ rows }: { rows: RefundCharge[] }) {
  return (
    <ul className="txn-refunds__list">
      {rows.map((row) => (
        <li key={row.id}>
          <Undo2 size={14} aria-hidden="true" />
          <span className="txn-refunds__what">
            <Link to={registerLinkFor(row, { open: true })}>
              {row.payee || row.statement_name}
            </Link>
            <span className="hint">{`${formatDate(row.date, 'short')} · ${row.account_name}`}</span>
          </span>
          <Money value={row.amount} signs="absolute" tone="neutral" />
        </li>
      ))}
    </ul>
  )
}
