/**
 * What the register's Reminders strip shows: late and upcoming slots, settled
 * ones dropped. The window must reach back, or nothing is ever past due.
 */

import { dayWindow } from '@/lib/dateRanges'
import type { IsoDate } from '@/lib/clients/entities'
import type { Occurrence } from '@/lib/clients/upcoming'

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
 */
export function stripReminders(
  occurrences: readonly Occurrence[],
  accountIds: readonly string[] | null,
): Occurrence[] {
  const scope = accountIds === null ? null : new Set(accountIds)
  return occurrences
    .filter((one) => one.status === 'past_due' || one.status === 'upcoming')
    .filter((one) => scope === null || (one.account_id !== null && scope.has(one.account_id)))
    .sort((a, b) => (a.due_on < b.due_on ? -1 : a.due_on > b.due_on ? 1 : 0))
}

export function pastDueCount(occurrences: readonly Occurrence[]): number {
  return occurrences.filter((one) => one.status === 'past_due').length
}
