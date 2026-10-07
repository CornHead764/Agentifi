import { api, ApiError } from '@/lib/api'
import { dayWindow } from '@/lib/dateRanges'
import type { SplitWrite } from '@/lib/transactions/types'
import type { Recurrence } from '@/lib/recurrence'
import type { IsoDate } from '../entities'

import {
  SERIES_SHAPE,
  SUGGESTION_SHAPE,
  seriesFromWire,
  suggestionFromWire,
  type MatchCriteria,
  type Occurrence,
  type Refund,
  type Series,
  type SeriesKind,
  type Suggestion,
  type Wire,
} from './types'
import { amountToWire } from '@/lib/money'

/**
 * Wide enough to offer a late monthly bill and the next one, narrow enough to
 * leave out a year of a weekly series. No date anchors on today.
 */
const SLOT_WINDOW_DAYS = 45

export function slotWindow(date: IsoDate | null): { from: IsoDate; to: IsoDate } {
  const anchor = date ? new Date(`${date}T00:00:00`) : new Date()
  return dayWindow(SLOT_WINDOW_DAYS, SLOT_WINDOW_DAYS, anchor)
}

/** For a screen that holds no `Occurrence`. */
export function acceptSlot(seriesId: string, dueOn: IsoDate, amount?: string) {
  return api.post<unknown>('/occurrences/accept', {
    series_id: seriesId,
    due_on: dueOn,
    amount,
  })
}

export function skipSlot(seriesId: string, dueOn: IsoDate) {
  return api.post<unknown>('/occurrences/skip', {
    series_id: seriesId,
    due_on: dueOn,
  })
}

/** Keyed on the group's signature, because suggestions are re-derived on every read. */
export function dismissSuggestion(signature: string) {
  return api.post<void>(`/series/suggested/${signature}/dismiss`)
}

export function restoreSuggestion(signature: string) {
  return api.delete<void>(`/series/suggested/${signature}/dismiss`)
}

/**
 * Null (a 404) when the server has nothing to add, e.g. the row is already in
 * a series; the caller still opens its editor seeded from the transaction.
 */
export async function suggestSeriesFor(
  transactionId: string,
  signal?: AbortSignal,
): Promise<Suggestion | null> {
  try {
    const raw = await api.get<Wire<Suggestion>>(
      `/series/suggested/for/${transactionId}`,
      SUGGESTION_SHAPE,
      signal,
    )
    return suggestionFromWire(raw)
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null
    throw error
  }
}

/** `description` is matching input, never a label. */
export interface SeriesDraft {
  account_id: string
  category_id: string | null
  kind: SeriesKind
  description: string
  display_name: string | null
  /** A decimal string, never a float. */
  amount: string
  recurrence: Recurrence
  start_on: IsoDate
  end_on: IsoDate | null
  match_criteria: MatchCriteria
  match_amount_min: string | null
  match_amount_max: string | null
  auto_adjust_due_on: boolean
  /** Null takes the server's default. */
  reminder_days: number | null
  tag_ids: string[]
  /** Written against `amount`; the server scales them to what a bill cost. */
  splits: SplitWrite[]
}

export async function createSeries(draft: SeriesDraft) {
  return seriesFromWire(await api.post<Wire<Series>>('/series', draft, SERIES_SHAPE))
}

export function promoteSuggestion(suggestion: Suggestion) {
  return createSeries({
    account_id: suggestion.account_id,
    category_id: suggestion.category_id,
    kind: suggestion.kind,
    description: suggestion.description,
    display_name: suggestion.display_name || null,
    amount: amountToWire(suggestion.amount),
    recurrence: suggestion.recurrence,
    start_on: suggestion.start_on,
    end_on: null,
    match_criteria: suggestion.match_criteria,
    match_amount_min: null,
    match_amount_max: null,
    auto_adjust_due_on: false,
    reminder_days: null,
    tag_ids: [],
    splits: [],
  })
}

/** Omitted fields are untouched. Renaming goes in `display_name` so matching is unaffected. */
export interface SeriesPatch {
  account_id?: string
  category_id?: string | null
  kind?: SeriesKind
  description?: string
  display_name?: string | null
  amount?: string
  recurrence?: Recurrence
  start_on?: IsoDate
  end_on?: IsoDate | null
  match_criteria?: MatchCriteria
  match_amount_min?: string | null
  match_amount_max?: string | null
  auto_adjust_due_on?: boolean
  /** Null clears the one-off override. */
  override_next_due_on?: IsoDate | null
  override_next_amount?: string | null
  reminder_days?: number
  /** Absent leaves the template alone; empty clears it. */
  tag_ids?: string[]
  splits?: SplitWrite[]
  is_active?: boolean
}

export async function updateSeries(id: string, patch: SeriesPatch) {
  return seriesFromWire(await api.patch<Wire<Series>>(`/series/${id}`, patch, SERIES_SHAPE))
}

/** Soft delete: matched charges keep their `series_id`; only the projection stops. */
export function deleteSeries(id: string) {
  return api.delete<null>(`/series/${id}`)
}

/** An omitted field falls back to the occurrence's own; the slot identifies the charge. */
export interface AcceptEdits {
  /** A decimal string, never a float. */
  amount?: string
  date?: IsoDate
  payee?: string
  notes?: string
}

export function acceptOccurrence(one: Occurrence, edits: AcceptEdits = {}) {
  return api.post<unknown>('/occurrences/accept', {
    series_id: one.series_id,
    due_on: one.due_on,
    ...edits,
  })
}

/** A refund settles like any series: the same accept, addressed by its slot. */
export function settleRefund(refund: Refund, amount?: string) {
  return api.post<unknown>('/occurrences/accept', {
    series_id: refund.series.id,
    due_on: refund.expected_on,
    amount,
  })
}

export function skipOccurrence(one: Occurrence) {
  return api.post<unknown>('/occurrences/skip', {
    series_id: one.series_id,
    due_on: one.due_on,
  })
}
