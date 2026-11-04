import { Badge } from '@/components/ui'
import { markedPaidLabel } from '@/lib/clients/upcoming'

/**
 * The status of an occurrence a person marked paid (received, for income)
 * that no transaction is linked to. It counts at its expected amount until the
 * bank's row is linked, so it is told apart from a paid one in words as well
 * as by its stripes.
 */
export function MarkedPaidBadge({ income }: { income: boolean }) {
  const label = markedPaidLabel(income)
  return (
    <Badge className="badge--unlinked" title={label}>
      <span aria-hidden="true">{income ? 'Received, not linked' : 'Paid, not linked'}</span>
      <span className="visually-hidden">{label}</span>
    </Badge>
  )
}
