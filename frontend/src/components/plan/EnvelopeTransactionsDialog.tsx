import { ExternalLink } from 'lucide-react'
import { Link } from 'react-router-dom'

import { Money } from '@/components/Money'
import { Dialog, DialogContent, Table, Td, Th } from '@/components/ui'
import { formatDate, formatMonthKey } from '@/lib/format'
import { envelopeAvailable, envelopeBudget, type Envelope } from '@/lib/spendingPlan'
import { registerLinkFor } from '@/lib/transactions/links'
import { useAccountName } from '@/lib/transactions/queries'

/**
 * What an envelope was charged this month, one line per part: the envelope's
 * own `entries`, never a second query by its filter, which would show rows the
 * engine gave to a series, a goal, an exclusion or an earlier envelope. A split
 * appears once per part charged, with that part's share.
 */
export function EnvelopeTransactionsDialog({
  envelope,
  month,
  onOpenChange,
}: {
  envelope: Envelope | null
  /** `YYYY-MM`. */
  month: string
  onOpenChange: (open: boolean) => void
}) {
  const nameOf = useAccountName()
  const accountName = (id: string | null) =>
    id === null ? '' : nameOf(id)
  const entries = envelope?.entries ?? []

  return (
    <Dialog open={envelope !== null} onOpenChange={onOpenChange}>
      <DialogContent
        wide
        className="plan-detail"
        title={envelope?.name ?? 'Envelope'}
        description={
          envelope ? (
            <>
              {formatMonthKey(month)} · spent{' '}
              <Money value={envelope.spent} signs="absolute" tone="neutral" /> of{' '}
              <Money value={envelopeBudget(envelope)} signs="absolute" tone="neutral" /> ·{' '}
              <Money value={envelopeAvailable(envelope)} signs="absolute" tone="neutral" />{' '}
              {envelopeAvailable(envelope) < 0 ? 'overspent' : 'available'}
            </>
          ) : undefined
        }
      >
        {envelope === null ? null : entries.length === 0 ? (
          <p className="plan-entries__note hint hint--faint">
            Nothing in {envelope.categories.map((one) => one.name).join(', ')} this month.
          </p>
        ) : (
          <Table density="sm" stack>
            <thead>
              <tr>
                <Th>Date</Th>
                <Th>Payee</Th>
                <Th>Category</Th>
                <Th>Account</Th>
                <Th numeric>Amount</Th>
                <Th>
                  <span className="visually-hidden">Register</span>
                </Th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr key={entry.id}>
                  <Td label="" className="stack-lead">
                    {formatDate(entry.due_on)}
                  </Td>
                  <Td label="" className="stack-lead">
                    {entry.name}
                    {entry.is_split ? (
                      <span className="plan-entries__part" title="One split of a larger transaction">
                        split
                      </span>
                    ) : null}
                  </Td>
                  <Td className="stack-inline">{entry.category_name ?? 'Uncategorized'}</Td>
                  <Td className="stack-inline">{accountName(entry.account_id)}</Td>
                  <Td numeric className="stack-inline">
                    <Money value={entry.amount} signs="spend" tone="neutral" />
                  </Td>
                  <Td label="" className="stack-corner">
                    {entry.txn_id === null || entry.account_id === null ? null : (
                      <Link
                        to={registerLinkFor({
                          id: entry.txn_id,
                          account_id: entry.account_id,
                          date: entry.due_on,
                        })}
                        className="plan-entries__action"
                        aria-label={`Open ${entry.name} in the register`}
                      >
                        <ExternalLink size={13} aria-hidden="true" />
                      </Link>
                    )}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </DialogContent>
    </Dialog>
  )
}
