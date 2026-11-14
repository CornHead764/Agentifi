import { parseIsoDate, plural, relativeDay } from '@agentifi/core/format'

import type { ReviewReason, ReviewRow, ReviewSummary } from '@/lib/clients/review'

/** "Fri, Oct 31", or "Today" and "Yesterday" for the two days that have names. */
export function dayLabel(iso: string, today: Date = new Date()): string {
  const relative = relativeDay(iso, today)
  if (relative === 'Today' || relative === 'Yesterday') return relative
  const date = parseIsoDate(iso)
  if (Number.isNaN(date.getTime())) return iso
  return new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric' }).format(date)
}

/** What a row that needs a look is waiting on, in a few words. */
export const REASON_WORDS: Record<ReviewReason, string> = {
  not_recognised: 'Not recognised yet',
  possible_duplicate: 'Possible duplicate: decide above',
  no_category: 'Needs a category',
  guess: 'A guess',
  unusual_amount: 'Unusual amount',
}

/** "2 to act on now, 5 with money at stake and 9 rows that need a look." */
export function badgeSentence(summary: ReviewSummary): string {
  const parts = [
    summary.act_now > 0 ? `${summary.act_now} to act on now` : null,
    summary.money_at_stake > 0 ? `${summary.money_at_stake} with money at stake` : null,
    summary.tidy_up > 0 ? plural(summary.tidy_up, 'proposal') : null,
    summary.needs_a_look > 0 ? `${plural(summary.needs_a_look, 'row')} that need a look` : null,
  ].filter((part): part is string => part !== null)
  if (parts.length === 0) return ''
  if (parts.length === 1) return `${parts[0]}.`
  return `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}.`
}

/** The category chip's text: the filed category, else the suggestion, else a prompt. */
export function chipText(row: ReviewRow): { text: string; guess: boolean; missing: boolean } {
  if (row.splits.length > 0) return { text: `Split ${row.splits.length} ways`, guess: false, missing: false }
  if (row.suggestion) {
    if (row.suggestion.splits.length > 0) {
      return { text: `Split ${row.suggestion.splits.length} ways?`, guess: row.reason === 'guess', missing: false }
    }
    return { text: row.suggestion.category_name, guess: row.reason === 'guess', missing: false }
  }
  if (row.category_name) return { text: row.category_name, guess: false, missing: row.reason === 'no_category' }
  return { text: 'Choose a category', guess: false, missing: true }
}
