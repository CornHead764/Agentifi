import { api, ApiError, type MoneyShape } from '@/lib/api'
import { dayWindow } from '@agentifi/core/dateRanges'
import type { SplitWrite } from '@/lib/transactions/types'
import type { Recurrence } from '@agentifi/core/recurrence'
import { queryString, type IsoDate } from '../entities'

import {
  SERIES_SHAPE,
  SUGGESTED_PAYMENT_SHAPE,
  SUGGESTION_SHAPE,
  seriesFromWire,
  suggestionFromWire,
  type MatchCriteria,
  type Occurrence,
  type Refund,
  type Series,
  type SeriesKind,
  type SuggestedPayment,
  type Suggestion,
  type Wire,
} from './types'
import { amountToWire } from '@agentifi/core/money'

/**
 * Wide enough to offer a late monthly bill and the next one, narrow enough to
 * leave out a year of a weekly series. No date anchors on today.
 */
const SLOT_WINDOW_DAYS = 45

export function slotWindow(date: IsoDate | null): { from: IsoDate; to: IsoDate } {
  const anchor = date ? new Date(`${date}T00:00:00`) : new Date()
  return dayWindow(SLOT_WINDOW_DAYS, SLOT_WINDOW_DAYS, anchor)
}

/**
 * What marking a slot paid did. `transaction_id` is the transaction it wrote,
 * on an account no bank feeds; null when it only marked the slot, and then
 * `candidates` is the rows that could be its payment, nearest first.
 */
export interface MarkedPaid {
  transaction_id: string | null
  candidates: SuggestedPayment[]
}

const MARKED_PAID_SHAPE: MoneyShape<MarkedPaid> = { candidates: SUGGESTED_PAYMENT_SHAPE }

/**
 * Marks the slot paid (received, for income). For a screen that holds no
 * `Occurrence`.
 */
export function acceptSlot(seriesId: string, dueOn: IsoDate) {
  return api.post<MarkedPaid>(
    '/occurrences/accept',
    { series_id: seriesId, due_on: dueOn },
    MARKED_PAID_SHAPE,
  )
}

/** The rows that could be the payment for a slot marked paid; none once one is linked. */
export async function suggestedPayments(seriesId: string, dueOn: IsoDate, signal?: AbortSignal) {
  const list = await api.get<{ items: SuggestedPayment[] }>(
    `/occurrences/candidates${queryString({ series_id: seriesId, due_on: dueOn })}`,
    { items: SUGGESTED_PAYMENT_SHAPE },
    signal,
  )
  return list.items
}

export function unacceptSlot(seriesId: string, dueOn: IsoDate) {
  return api.post<unknown>('/occurrences/unaccept', { series_id: seriesId, due_on: dueOn })
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
  /** Written against `amount`; the server scales them to what a linked transaction cost. */
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

export function acceptOccurrence(one: Occurrence) {
  return acceptSlot(one.series_id ?? '', one.due_on)
}

export function unacceptOccurrence(one: Occurrence) {
  return unacceptSlot(one.series_id ?? '', one.due_on)
}

/** A refund is marked received like any series: the same mark, addressed by its slot. */
export function settleRefund(refund: Refund) {
  return acceptSlot(refund.series.id, refund.expected_on)
}

export function unsettleRefund(refund: Refund) {
  return unacceptSlot(refund.series.id, refund.expected_on)
}

export function skipOccurrence(one: Occurrence) {
  return api.post<unknown>('/occurrences/skip', {
    series_id: one.series_id,
    due_on: one.due_on,
  })
}
