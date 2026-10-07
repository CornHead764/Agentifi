import { useState } from 'react'
import { Link } from 'react-router-dom'

import { BillLinkMark } from '@/components/BillLinkMark'
import { BillNote } from '@/components/BillNote'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Badge, Button, EmptyState } from '@/components/ui'
import { isPayManually, occurrenceKey, useOccurrences } from '@/lib/clients/upcoming'
import { dayWindow } from '@/lib/dateRanges'
import { relativeDay } from '@/lib/format'

export function BillsPanel() {
  // Read once, in a lazy initializer rather than during render: today's date is
  // impure, and a window that moves between renders would refetch forever.
  const [bounds] = useState(() => dayWindow(0, 30))
  const occurrences = useOccurrences(bounds.from, bounds.to)

  return (
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
              <div key={occurrenceKey(occurrence)} className="tile">
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
                  {occurrence.status === 'past_due' ? (
                    <Badge tone="expense">Past due</Badge>
                  ) : null}
                </span>
                {/* How it is paid, when there is something to say, and the statement. */}
                <BillNote occurrence={occurrence} />
              </div>
            ))}
          </div>
        )
      }
    </QueryBoundary>
  )
}
