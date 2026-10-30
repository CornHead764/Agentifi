import type { Series } from '@/lib/clients/upcoming'
import { formatDate, toIsoDate } from '@/lib/format'

export type DueState = 'upcoming' | 'overdue' | 'done'

/**
 * Where a series stands against today: upcoming, overdue, or nothing left to
 * project. Kept apart because an import brings many long-overdue one-time
 * reminders that would otherwise lead a date-sorted list.
 */
export function dueState(series: Series, today = toIsoDate(new Date())): DueState {
  if (series.next_due_on === null) return 'done'
  return series.next_due_on < today ? 'overdue' : 'upcoming'
}

const STATE_ORDER: Record<DueState, number> = { upcoming: 0, overdue: 1, done: 2 }

/**
 * The order the Bills & Income series lists use: running series by soonest
 * due, then overdue ones most recent first, then those with nothing left to
 * project. Paused series follow in the same order, still listed.
 */
export function sortSeries(rows: readonly Series[], today = toIsoDate(new Date())): Series[] {
  return [...rows].sort((a, b) => {
    if (a.is_active !== b.is_active) return a.is_active ? -1 : 1
    const stateA = dueState(a, today)
    const stateB = dueState(b, today)
    if (stateA !== stateB) return STATE_ORDER[stateA] - STATE_ORDER[stateB]
    if (a.next_due_on !== b.next_due_on && a.next_due_on !== null && b.next_due_on !== null) {
      const ascending = a.next_due_on < b.next_due_on ? -1 : 1
      return stateA === 'overdue' ? -ascending : ascending
    }
    return a.label.localeCompare(b.label)
  })
}

/**
 * The Next due cell. A finished one-time or ended series has no next date,
 * which is not an error; a past date says so rather than calling itself next.
 */
export function nextDueText(series: Series, today = toIsoDate(new Date())): string {
  switch (dueState(series, today)) {
    case 'done':
      return 'No further occurrences'
    case 'overdue':
      return `Past due ${formatDate(series.next_due_on ?? '')}`
    case 'upcoming':
      return formatDate(series.next_due_on ?? '')
  }
}
