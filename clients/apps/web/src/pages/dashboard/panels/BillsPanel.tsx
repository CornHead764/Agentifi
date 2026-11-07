import { clsx } from 'clsx'
import { Link2 } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { BillLinkMark } from '@/components/BillLinkMark'
import { BillNote } from '@/components/BillNote'
import { MarkedPaidBadge } from '@/components/MarkedPaidBadge'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { SuggestedPaymentLine } from '@/components/SuggestedPayment'
import { LinkPaymentDialog } from '@/components/transactions/LinkPaymentDialog'
import { Badge, Button, EmptyState, IconButton } from '@/components/ui'
import {
  isPayManually,
  markedSlot,
  occurrenceKey,
  useOccurrences,
  type Occurrence,
} from '@/lib/clients/upcoming'
import { dayWindow } from '@agentifi/core/dateRanges'
import { relativeDay } from '@agentifi/core/format'
import { paymentSlot } from '@/lib/transactions/paymentCandidates'

export function BillsPanel() {
  // Read once, in a lazy initializer rather than during render: today's date is
  // impure, and a window that moves between renders would refetch forever.
  const [bounds] = useState(() => dayWindow(0, 30))
  const occurrences = useOccurrences(bounds.from, bounds.to)
  const [linking, setLinking] = useState<Occurrence | null>(null)

  return (
    <>
      <QueryBoundary query={occurrences} rows={4}>
        {(data) =>
          data.items.length === 0 ? (
            <EmptyState
              title="Nothing scheduled"
              body="Bills and paychecks appear here once something is set up as recurring."
              action={
                <Button asChild size="sm">
                  <Link to="/upcoming/recurring?new=1">Add a recurring item</Link>
                </Button>
              }
            />
          ) : (
            <div className="tiles">
              {data.items.slice(0, 4).map((occurrence) => (
                <div
                  key={occurrenceKey(occurrence)}
                  className={clsx('tile', occurrence.marked_paid && 'tile--unlinked')}
                >
                  <span className="muted">{relativeDay(occurrence.due_on)}</span>
                  <span className="tile__label">
                    <span>{occurrence.label}</span>
                    <BillLinkMark link={occurrence.bill_link} />
                  </span>
                  {/* A statement is owed on a billed account, not paid from one. */}
                  {isPayManually(occurrence) ? (
                    <span className="muted">{occurrence.bill_link?.subaccount_label}</span>
                  ) : null}
                  <span className="row tile__foot">
                    <Money value={occurrence.amount} signs="absolute" tone="neutral" />
                    {occurrence.marked_paid ? (
                      <>
                        <MarkedPaidBadge income={occurrence.kind === 'income'} />
                        <IconButton
                          label={`Link a transaction to ${occurrence.label}`}
                          size="sm"
                          variant="ghost"
                          onClick={() => setLinking(occurrence)}
                        >
                          <Link2 size={13} />
                        </IconButton>
                      </>
                    ) : occurrence.status === 'past_due' ? (
                      <Badge tone="expense">Past due</Badge>
                    ) : null}
                  </span>
                  {/* How it is paid, when there is something to say, and the statement. */}
                  <BillNote occurrence={occurrence} />
                  {occurrence.marked_paid ? <MarkedSuggestion occurrence={occurrence} /> : null}
                </div>
              ))}
            </div>
          )
        }
      </QueryBoundary>
      <LinkPaymentDialog
        slot={linking === null ? null : paymentSlot(linking)}
        onClose={() => setLinking(null)}
      />
    </>
  )
}

function MarkedSuggestion({ occurrence }: { occurrence: Occurrence }) {
  const slot = markedSlot(occurrence)
  return slot === null ? null : <SuggestedPaymentLine slot={slot} />
}
