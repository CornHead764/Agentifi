/**
 * A "Suggest categories" request, followed on the server: how far its runs
 * have got and, over rows that already had a category, how often the check
 * named the same one. The register's strip shows the latest.
 */

import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { asSentence, formatCount, plural } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'
import type { Uuid } from '@/lib/transactions/types'

/** Mirrors `domain.BulkSuggestionRows`: above it a request is asked about first and waits behind every other run. */
export const BULK_SUGGESTION_ROWS = 200

export interface SuggestionTally {
  agreed: number
  differs: number
  unsure: number
  suggested: number
  undetermined: number
  skipped: number
  failed: number
}

/** `reviewed` and `unreviewed` are the rows' review state when the request was queued. */
export interface SuggestionBatch {
  id: Uuid
  created_at: string
  cancelled: boolean
  dismissed: boolean
  rows: number
  done: number
  pending: number
  not_run: number
  finished: boolean
  reviewed: SuggestionTally
  unreviewed: SuggestionTally
}

export const SUGGESTION_BATCH_KEY = ['category-suggestion-batches', 'latest'] as const

const POLL_MS = 3000

export function useLatestSuggestionBatch() {
  return useQuery({
    queryKey: SUGGESTION_BATCH_KEY,
    queryFn: ({ signal }) =>
      api.get<{ batch: SuggestionBatch | null }>('/category-suggestion-batches/latest', undefined, signal),
    select: (data) => data.batch,
    refetchInterval: (query) => {
      const batch = query.state.data?.batch
      return batch && !batch.finished ? POLL_MS : false
    },
  })
}

export function useCancelSuggestionBatch() {
  return useInvalidatingMutation(
    (id: Uuid) => api.post<SuggestionBatch>(`/category-suggestion-batches/${id}/cancel`, {}),
    [SUGGESTION_BATCH_KEY],
    { failure: 'The suggestions were not cancelled' },
  )
}

export function useDismissSuggestionBatch() {
  return useInvalidatingMutation(
    (id: Uuid) => api.post<void>(`/category-suggestion-batches/${id}/dismiss`, {}),
    [SUGGESTION_BATCH_KEY],
  )
}

/** Whether asking for this many rows is asked about first. */
export function asksFirst(count: number): boolean {
  return count > BULK_SUGGESTION_ROWS
}

/** The question a bulk request is put as. */
export function bulkSuggestionPrompt(count: number): {
  title: string
  description: string
  confirmLabel: string
} {
  return {
    title: `Suggest categories for ${plural(count, 'transaction')}?`,
    description:
      'The assistant looks at each one in turn, a few at a time, so this can take hours and keeps the model busy throughout. New transactions from a sync are still checked first. Suggestions appear on their rows as they land, only where the assistant would file a row differently, and the bar above the register counts them off and can cancel the rest.',
    confirmLabel: `Suggest for ${formatCount(count)}`,
  }
}

/** What the strip says first: how far, or how it ended. */
export function batchHeadline(batch: SuggestionBatch): string {
  if (!batch.finished) {
    return `Suggesting categories — ${formatCount(batch.done)} of ${formatCount(batch.rows)}`
  }
  if (batch.cancelled) {
    return `Stopped after ${formatCount(batch.done - batch.not_run)} of ${plural(batch.rows, 'row')}`
  }
  return `Suggestions done for ${plural(batch.rows, 'row')}`
}

function hadCategory(tally: SuggestionTally): number {
  return tally.agreed + tally.differs + tally.unsure
}

/**
 * How the answers compare with the categories the rows already had, reviewed
 * and unreviewed apart, then everything else that happened. Null before
 * anything has finished.
 */
export function batchComparison(batch: SuggestionBatch): string | null {
  const { reviewed, unreviewed } = batch
  const sentences: string[] = []
  const had = hadCategory(reviewed) + hadCategory(unreviewed)
  if (had > 0) {
    let agreed = `Agreed with ${formatCount(reviewed.agreed + unreviewed.agreed)} of ${plural(
      had,
      'category',
      'categories',
    )} already set`
    if (hadCategory(reviewed) > 0 && hadCategory(unreviewed) > 0) {
      agreed += `: ${formatCount(reviewed.agreed)} of ${formatCount(hadCategory(reviewed))} reviewed, ${formatCount(
        unreviewed.agreed,
      )} of ${formatCount(hadCategory(unreviewed))} unreviewed`
    }
    sentences.push(`${agreed}.`)
  }

  const differs = reviewed.differs + unreviewed.differs
  const rest = [
    differs > 0
      ? `${formatCount(differs)} ${differs === 1 ? 'differs' : 'differ'}${
          reviewed.differs > 0 ? ` (${formatCount(reviewed.differs)} reviewed)` : ''
        }`
      : null,
    count(reviewed.suggested + unreviewed.suggested, 'suggested for uncategorized rows'),
    count(reviewed.unsure + unreviewed.unsure, 'unsure'),
    count(reviewed.skipped + unreviewed.skipped, 'skipped'),
    count(reviewed.failed + unreviewed.failed, 'failed'),
    count(batch.not_run, 'not run'),
  ].filter((part) => part !== null)
  if (rest.length > 0) sentences.push(asSentence(rest.join(', ')))
  return sentences.length > 0 ? sentences.join(' ') : null
}

function count(n: number, label: string): string | null {
  return n > 0 ? `${formatCount(n)} ${label}` : null
}

/** The batch's rows that needed a category and were not placed: the register's Undetermined filter. */
export function undeterminedIn(batch: SuggestionBatch): number {
  return batch.reviewed.undetermined + batch.unreviewed.undetermined
}
