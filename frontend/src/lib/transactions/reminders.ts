/**
 * What the register's Reminders strip shows: late and upcoming slots, settled
 * ones dropped. The window must reach back, or nothing is ever past due.
 */

import { dayWindow } from '@/lib/dateRanges'
import type { IsoDate } from '@/lib/clients/entities'
import type { Occurrence } from '@/lib/clients/upcoming'
import { formatDate } from '@/lib/format'

/** Asymmetric on purpose: an old unpaid bill matters, and far-off ones would bury the late ones. */
export const REMINDER_DAYS_BACK = 60
export const REMINDER_DAYS_AHEAD = 30

export function reminderWindow(today: Date = new Date()): { from: IsoDate; to: IsoDate } {
  return dayWindow(REMINDER_DAYS_BACK, REMINDER_DAYS_AHEAD, today)
}

/** The strip's reach into the past alone, through today. */
export function pastDueWindow(today: Date = new Date()): { from: IsoDate; to: IsoDate } {
  return dayWindow(REMINDER_DAYS_BACK, 0, today)
}

/**
 * Oldest first. `accountIds` null is every account; an empty list shows
 * nothing. A pay-manually reminder belongs to no account, so only the whole
 * household's strip shows it.
 *
 * A reminder goes once its day has come, unless it is past due: income never
 * is (a paycheck not yet in stays upcoming), so it would otherwise sit there
 * for the whole window. A bill stays on the day it is due, since it would
 * only come back as past due the next morning.
 */
export function stripReminders(
  occurrences: readonly Occurrence[],
  accountIds: readonly string[] | null,
  today: IsoDate,
): Occurrence[] {
  const scope = accountIds === null ? null : new Set(accountIds)
  return occurrences
    .filter((one) => one.status === 'past_due' || (one.status === 'upcoming' && stillAhead(one, today)))
    .filter((one) => scope === null || (one.account_id !== null && scope.has(one.account_id)))
    .sort((a, b) => (a.due_on < b.due_on ? -1 : a.due_on > b.due_on ? 1 : 0))
}

function stillAhead(one: Occurrence, today: IsoDate): boolean {
  return one.due_on > today || (one.due_on === today && one.kind !== 'income')
}

export function pastDueCount(occurrences: readonly Occurrence[]): number {
  return occurrences.filter((one) => one.status === 'past_due').length
}

/** Where a reminder's recurring item opens: the Recurring tab, on its editor. */
export function seriesPath(seriesId: string): string {
  return `/upcoming/recurring?series=${encodeURIComponent(seriesId)}`
}

/** The question the skip asks first, naming the occurrence and what a skip does. */
export function skipPrompt(occurrence: Pick<Occurrence, 'label' | 'due_on'>): {
  title: string
  description: string
} {
  return {
    title: `Skip ${occurrence.label} on ${formatDate(occurrence.due_on)}?`,
    description:
      'Only this occurrence is skipped, as if it is not happening. No transaction is recorded, the spending plan stops expecting it that month, and the next one carries on.',
  }
}
