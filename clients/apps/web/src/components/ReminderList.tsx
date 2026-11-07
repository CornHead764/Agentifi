import { useMemo, useState } from 'react'

import { Money } from '@/components/Money'
import { BillLinkMark } from '@/components/BillLinkMark'
import { BillNote } from '@/components/BillNote'
import { MarkedPaidBadge } from '@/components/MarkedPaidBadge'
import { MarkStatementPaidButton } from '@/components/MarkStatementPaidButton'
import { ReminderActions } from '@/components/ReminderActions'
import { ReminderSeriesEditor, ReminderTitle } from '@/components/ReminderTitle'
import { SuggestedPaymentLine } from '@/components/SuggestedPayment'
import { Badge, EmptyState, List, ListRow } from '@/components/ui'
import {
  type Occurrence,
  SERIES_KIND_LABELS,
  isPayManually,
  markedSlot,
  occurrenceKey,
  reminderAccount,
} from '@/lib/clients/upcoming'
import { relativeDay } from '@agentifi/core/format'
import { STATUS_LABELS, STATUS_TONES } from '@/lib/spendingPlan'
import { useAccounts } from '@/lib/transactions/queries'
import { AskRowButton } from '@/components/assistant/AskMenuItem'
import { billSubject } from '@/lib/assistant/subjects'

/**
 * Occurrences as reminder rows, on Bills & Income and above the register.
 * Every write names a slot (series and due date), since a twice-monthly bill
 * has two a month; a pay-manually reminder has no slot, only its statement to
 * mark paid.
 */
export function ReminderList({ occurrences }: { occurrences: readonly Occurrence[] }) {
  const accounts = useAccounts()

  const accountName = useMemo(() => {
    const byId = new Map((accounts.data ?? []).map((one) => [one.id, one.name]))
    return (occurrence: Occurrence) =>
      reminderAccount(occurrence, (id) => byId.get(id) ?? SERIES_KIND_LABELS[occurrence.kind])
  }, [accounts.data])

  // A click anywhere on a recurring item's row opens its editor, as its name does.
  const [editingSeries, setEditingSeries] = useState<string | null>(null)

  if (occurrences.length === 0) {
    return <EmptyState compact title="Nothing is scheduled in this window." />
  }

  return (
    <>
      <List className="reminders">
        {occurrences.map((occurrence) => {
          const isIncome = occurrence.kind === 'income'
          // `received` is `paid` for income — a settled slot either way, and
          // one that must not still be offering "mark it paid". A mark with no
          // transaction is answered too, but still waits for its link.
          const marked = occurrence.marked_paid
          const slot = markedSlot(occurrence)
          const sub = (
            <>
              {relativeDay(occurrence.due_on)} · {accountName(occurrence)}
              <BillNote occurrence={occurrence} inline />
            </>
          )
          const settled =
            !marked &&
            (occurrence.status === 'paid' ||
              occurrence.status === 'received' ||
              occurrence.status === 'skipped')
          return (
            <ListRow
              key={occurrenceKey(occurrence)}
              className={settled ? 'row--excluded' : marked ? 'row--unlinked' : undefined}
              onOpen={
                occurrence.series_id === null
                  ? undefined
                  : () => setEditingSeries(occurrence.series_id)
              }
              title={
                <>
                  <ReminderTitle occurrence={occurrence} />
                  <BillLinkMark link={occurrence.bill_link} />
                </>
              }
              // Upcoming is the ordinary case and carries no chip; the rest
              // say what happened, in the plan's own words.
              badge={
                marked ? (
                  <MarkedPaidBadge income={isIncome} />
                ) : occurrence.status === 'upcoming' ? null : (
                  <Badge tone={STATUS_TONES[occurrence.status]}>
                    {STATUS_LABELS[occurrence.status]}
                  </Badge>
                )
              }
              // A row that could be the payment of a mark takes the second
              // line, which is otherwise cut short before it.
              sub={
                marked && slot !== null ? (
                  <SuggestedPaymentLine slot={slot} inline fallback={sub} />
                ) : (
                  sub
                )
              }
              figures={<Money value={occurrence.amount} showPlus={occurrence.amount > 0} />}
              actions={
                settled && !occurrence.accepted_by_hand ? null : isPayManually(occurrence) ? (
                  <>
                    <MarkStatementPaidButton occurrence={occurrence} />
                    <AskRowButton
                      subject={() =>
                        billSubject({
                          name: occurrence.label,
                          amount: occurrence.amount,
                          due_on: occurrence.due_on,
                        })
                      }
                    />
                  </>
                ) : (
                  <ReminderActions occurrence={occurrence}>
                    <AskRowButton
                      subject={() =>
                        billSubject({
                          // An occurrence has no id of its own; it is the slot
                          // `${series_id}:${due_on}`, so the series is named.
                          name: occurrence.label,
                          amount: occurrence.amount,
                          due_on: occurrence.due_on,
                          series_id: occurrence.series_id,
                        })
                      }
                    />
                  </ReminderActions>
                )
              }
            />
          )
        })}
      </List>

      {editingSeries === null ? null : (
        <ReminderSeriesEditor seriesId={editingSeries} onClose={() => setEditingSeries(null)} />
      )}
    </>
  )
}
