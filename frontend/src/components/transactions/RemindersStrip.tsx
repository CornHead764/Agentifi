/**
 * The register's reminders: the next and overdue occurrences, scoped as the
 * register is, as a horizontal rail of cards in date order. Over one account
 * the rail sits in the cash-flow card under the line it marks; over a group of
 * accounts it is a card of its own. Every write names a slot (series and due
 * date), since a twice-monthly bill has two a month.
 */

import { clsx } from 'clsx'
import { Check, SkipForward } from 'lucide-react'
import { useMemo } from 'react'

import { Money } from '@/components/Money'
import { BillLinkMark } from '@/components/BillLinkMark'
import { BillNote } from '@/components/BillNote'
import { MarkStatementPaidButton } from '@/components/MarkStatementPaidButton'
import { Badge, CollapsibleCard, IconButton } from '@/components/ui'
import {
  type Occurrence,
  isPayManually,
  occurrenceKey,
  reminderAccount,
  useAcceptOccurrence,
  useOccurrences,
  useSkipOccurrence,
} from '@/lib/clients/upcoming'
import { initial, relativeDay } from '@/lib/format'
import {
  REMINDER_DAYS_AHEAD,
  pastDueCount,
  reminderWindow,
  stripReminders,
} from '@/lib/transactions/reminders'
import { useAccountName } from '@/lib/transactions/queries'

function useReminders(accountIds: readonly string[] | null): Occurrence[] {
  // Recomputed once per mount rather than per render, so the window does not
  // shift under an open query and refetch on every keystroke in the register.
  const bounds = useMemo(() => reminderWindow(), [])
  const occurrences = useOccurrences(bounds.from, bounds.to)
  return useMemo(
    () => stripReminders(occurrences.data?.items ?? [], accountIds),
    [occurrences.data, accountIds],
  )
}

function summary(shown: readonly Occurrence[]): string {
  const late = pastDueCount(shown)
  return late > 0
    ? `${late} past due of ${shown.length}`
    : `${shown.length} scheduled in the next ${REMINDER_DAYS_AHEAD} days`
}

/** The rail as a card of its own, over a group of accounts. */
export function RemindersStrip({ accountIds }: { accountIds: readonly string[] | null }) {
  const shown = useReminders(accountIds)

  // Nothing scheduled is not a state worth a card. The strip appears when
  // there is something to be reminded of and is otherwise out of the way.
  if (shown.length === 0) return null

  return (
    <CollapsibleCard
      className="reminders-strip"
      flush
      storageKey="reminders.collapsed"
      what="reminders"
      title="Reminders"
      subtitle={summary(shown)}
    >
      <ReminderCards occurrences={shown} />
    </CollapsibleCard>
  )
}

/** The rail inside another card, under a line of its own. */
export function ReminderRail({ accountIds }: { accountIds: readonly string[] }) {
  const shown = useReminders(accountIds)
  if (shown.length === 0) return null

  return (
    <section className="reminder-strip" aria-label="Reminders">
      <p className="reminder-strip__head">
        <span className="reminder-strip__title">Reminders</span>
        <span className="muted">{summary(shown)}</span>
      </p>
      <ReminderCards occurrences={shown} />
    </section>
  )
}

function ReminderCards({ occurrences }: { occurrences: readonly Occurrence[] }) {
  const accountName = useAccountName()

  const accept = useAcceptOccurrence()
  const skip = useSkipOccurrence()
  const busy = accept.isPending || skip.isPending

  return (
    <ul className="reminder-strip__rail">
      {occurrences.map((one) => (
        <li
          key={occurrenceKey(one)}
          className={clsx('reminder-card', one.status === 'past_due' && 'reminder-card--late')}
        >
          <div className="row reminder-card__head">
            <span className="reminder__mark" aria-hidden="true">
              {initial(one.label)}
            </span>
            <span className="reminder-card__when">{relativeDay(one.due_on)}</span>
            {one.status === 'past_due' ? <Badge tone="expense">Past due</Badge> : null}
          </div>

          <p className="reminder-card__label" title={one.label}>
            {one.label}
            <BillLinkMark link={one.bill_link} />
          </p>
          <p className="reminder-card__amount">
            <Money value={one.amount} tone="flow" showPlus={one.amount > 0} />
          </p>
          <BillNote occurrence={one} />

          <div className="reminder-card__foot">
            <span className="reminder-card__account" title={reminderAccount(one, accountName)}>
              {reminderAccount(one, accountName)}
            </span>
            <span className="reminder-card__actions">
              {isPayManually(one) ? (
                <MarkStatementPaidButton occurrence={one} />
              ) : (
                <>
                  <IconButton
                    label={`Mark ${one.label} ${one.kind === 'income' ? 'received' : 'paid'}`}
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    onClick={() => accept.mutate({ occurrence: one })}
                  >
                    <Check size={13} />
                  </IconButton>
                  <IconButton
                    label={`Skip ${one.label}`}
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    onClick={() => skip.mutate(one)}
                  >
                    <SkipForward size={13} />
                  </IconButton>
                </>
              )}
            </span>
          </div>
        </li>
      ))}
    </ul>
  )
}
