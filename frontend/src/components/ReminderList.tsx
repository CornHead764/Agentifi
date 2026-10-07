import { Check, Pencil, SkipForward } from 'lucide-react'
import { useMemo, useState } from 'react'

import { Money } from '@/components/Money'
import { BillLinkMark } from '@/components/BillLinkMark'
import { BillNote } from '@/components/BillNote'
import { MarkPaidDialog } from '@/components/MarkPaidDialog'
import { MarkStatementPaidButton } from '@/components/MarkStatementPaidButton'
import { Badge, EmptyState, IconButton, List, ListRow } from '@/components/ui'
import {
  type Occurrence,
  SERIES_KIND_LABELS,
  isPayManually,
  occurrenceKey,
  reminderAccount,
  useAcceptOccurrence,
  useSkipOccurrence,
} from '@/lib/clients/upcoming'
import { relativeDay } from '@/lib/format'
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

  const [paying, setPaying] = useState<Occurrence | null>(null)

  // Each write is a single icon with no room for a message beside it, so a
  // failure that is not a toast is a click that did nothing at all.
  const accept = useAcceptOccurrence()
  const skip = useSkipOccurrence()

  if (occurrences.length === 0) {
    return <EmptyState compact title="Nothing is scheduled in this window." />
  }

  return (
    <>
      <List className="reminders">
        {occurrences.map((occurrence) => {
          const isIncome = occurrence.kind === 'income'
          // `received` is `paid` for income — a settled slot either way, and
          // one that must not still be offering "mark it paid".
          const settled =
            occurrence.status === 'paid' ||
            occurrence.status === 'received' ||
            occurrence.status === 'skipped'
          const markVerb = isIncome ? 'received' : 'paid'
          return (
            <ListRow
              key={occurrenceKey(occurrence)}
              className={settled ? 'row--excluded' : undefined}
              title={
                <>
                  {occurrence.label}
                  <BillLinkMark link={occurrence.bill_link} />
                </>
              }
              // Upcoming is the ordinary case and carries no chip; the rest
              // say what happened, in the plan's own words.
              badge={
                occurrence.status === 'upcoming' ? null : (
                  <Badge tone={STATUS_TONES[occurrence.status]}>
                    {STATUS_LABELS[occurrence.status]}
                  </Badge>
                )
              }
              sub={
                <>
                  {relativeDay(occurrence.due_on)} · {accountName(occurrence)}
                  <BillNote occurrence={occurrence} inline />
                </>
              }
              figures={<Money value={occurrence.amount} showPlus={occurrence.amount > 0} />}
              actions={
                settled ? null : isPayManually(occurrence) ? (
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
                  <>
                    <IconButton
                      label={`Mark ${occurrence.label} ${markVerb}`}
                      size="sm"
                      variant="ghost"
                      disabled={accept.isPending}
                      onClick={() => accept.mutate({ occurrence })}
                    >
                      <Check size={13} />
                    </IconButton>
                    <IconButton
                      label={`Mark ${occurrence.label} ${markVerb} for a different amount`}
                      size="sm"
                      variant="ghost"
                      disabled={accept.isPending}
                      onClick={() => setPaying(occurrence)}
                    >
                      <Pencil size={13} />
                    </IconButton>
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
                    <IconButton
                      label={`Skip ${occurrence.label}`}
                      size="sm"
                      variant="ghost"
                      disabled={skip.isPending}
                      onClick={() => skip.mutate(occurrence)}
                    >
                      <SkipForward size={13} />
                    </IconButton>
                  </>
                )
              }
            />
          )
        })}
      </List>

      {paying === null ? null : (
        <MarkPaidDialog
          key={occurrenceKey(paying)}
          occurrence={paying}
          pending={accept.isPending}
          onCancel={() => setPaying(null)}
          onConfirm={(edits) =>
            accept.mutate({ occurrence: paying, edits }, { onSuccess: () => setPaying(null) })
          }
        />
      )}
    </>
  )
}
