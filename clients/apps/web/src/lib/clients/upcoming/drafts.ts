import {
  type Money,
  ZERO_MONEY,
  amountFieldError,
  amountToWire,
  optionalAmountWire,
  parseAmountInput,
  parseWholeNumber,
} from '@/lib/money'
import {
  initialDrafts,
  newSplitDraft,
  validateSplits,
  type SplitDraft,
} from '@/lib/transactions/splits'
import type { SplitWrite } from '@/lib/transactions/types'
import type { Recurrence } from '@/lib/recurrence'
import type { IsoDate } from '../entities'

import type { MatchCriteria, Series, SeriesKind, Suggestion } from './types'
import type { SeriesDraft, SeriesPatch } from './api'

/** `name` is the display name and `description` the matching text; never merged. */
export interface SeriesEdits {
  name: string
  description: string
  amount: string
  account_id: string
  category_id: string | null
  kind: SeriesKind
  recurrence: Recurrence
  start_on: IsoDate
  end_on: IsoDate | null
  match_criteria: MatchCriteria
  match_amount_min: string
  match_amount_max: string
  auto_adjust_due_on: boolean
  /** As typed. Blank sends nothing, leaving the stored value alone. */
  reminder_days: string
  /** One-off overrides for the next slot; each clears independently. */
  override_next_due_on: IsoDate | null
  override_next_amount: string
  tag_ids: string[]
  /** Drafts, because a half-typed amount is not an error yet. */
  splits: SplitDraft[]
}

/**
 * What a suggested series carries, read from charges (`Suggestion`) or from a
 * billed account's bills, where no account is known until a payment is found.
 */
export type SuggestedSeries = Pick<
  Suggestion,
  | 'display_name'
  | 'description'
  | 'amount'
  | 'category_id'
  | 'kind'
  | 'recurrence'
  | 'start_on'
  | 'match_criteria'
  | 'match_amount_min'
  | 'match_amount_max'
> & { account_id: string | null }

/** The name box holds the display name only, never `label` (see `editsFromSeries`). */
export function editsFromSuggestion(suggestion: SuggestedSeries): SeriesEdits {
  return {
    name: suggestion.display_name,
    description: suggestion.description,
    amount: amountToWire(suggestion.amount),
    account_id: suggestion.account_id ?? '',
    category_id: suggestion.category_id,
    kind: suggestion.kind,
    recurrence: suggestion.recurrence,
    start_on: suggestion.start_on,
    end_on: null,
    match_criteria: suggestion.match_criteria,
    match_amount_min: amountText(suggestion.match_amount_min),
    match_amount_max: amountText(suggestion.match_amount_max),
    auto_adjust_due_on: false,
    reminder_days: '',
    override_next_due_on: null,
    override_next_amount: '',
    tag_ids: [],
    splits: [],
  }
}

export function editsFromSeries(series: Series): SeriesEdits {
  return {
    // Not `label`: it falls back to `description`, which would then be saved
    // as the display name.
    name: series.display_name ?? '',
    description: series.description,
    amount: amountToWire(series.amount),
    account_id: series.account_id,
    category_id: series.category_id,
    kind: series.kind,
    recurrence: series.recurrence,
    start_on: series.start_on,
    end_on: series.end_on,
    match_criteria: series.match_criteria,
    match_amount_min: amountText(series.match_amount_min),
    match_amount_max: amountText(series.match_amount_max),
    auto_adjust_due_on: series.auto_adjust_due_on,
    reminder_days: String(series.reminder_days),
    override_next_due_on: series.override_next_due_on,
    override_next_amount: amountText(series.override_next_amount),
    tag_ids: series.tag_ids,
    splits: series.splits.map((split) => ({
      // A template split has no id; a fresh draft key keeps React from
      // remounting a line being typed in.
      ...newSplitDraft(amountToWire(split.amount)),
      category_id: split.category_id,
      memo: split.memo,
      tag_ids: split.tag_ids,
    })),
  }
}

function amountText(value: Money | null): string {
  return value === null ? '' : amountToWire(value)
}

function editedFields(edits: SeriesEdits) {
  return {
    account_id: edits.account_id,
    category_id: edits.category_id,
    kind: edits.kind,
    // A series with no matching input matches nothing.
    description: edits.description || edits.name,
    display_name: edits.name || null,
    amount: parseAmountInput(edits.amount)?.wire ?? '',
    recurrence: edits.recurrence,
    start_on: edits.start_on,
    end_on: edits.end_on,
    match_criteria: edits.match_criteria,
    match_amount_min: band(edits, edits.match_amount_min),
    match_amount_max: band(edits, edits.match_amount_max),
    auto_adjust_due_on: edits.auto_adjust_due_on,
    tag_ids: edits.tag_ids,
    splits: splitRows(edits),
  }
}

/** Not a whole number of days is null, not zero: zero is "remind me on the day". */
function reminderDays(edits: SeriesEdits): number | null {
  return parseWholeNumber(edits.reminder_days)
}

/** An unbalanced set is sent as no split; `splitsBalance` is the real gate. */
function splitRows(edits: SeriesEdits): SplitWrite[] {
  if (edits.splits.length === 0) return []
  return validateSplits(edits.splits, splitParent(edits)).rows ?? []
}

/** A half-typed amount reads as zero, so the grid never briefly claims to balance. */
export function splitParent(edits: SeriesEdits): Money {
  return parseAmountInput(edits.amount)?.cents ?? ZERO_MONEY
}

export function splitsBalance(edits: SeriesEdits): boolean {
  return edits.splits.length === 0 || validateSplits(edits.splits, splitParent(edits)).rows !== null
}

export function startSplitting(edits: SeriesEdits): SplitDraft[] {
  return initialDrafts([], splitParent(edits))
}

/** Only `range` reads the band; the rest clear it. */
function band(edits: SeriesEdits, value: string): string | null {
  return edits.match_criteria === 'range' ? (optionalAmountWire(value) ?? null) : null
}

export type SeriesAmountField =
  | 'amount'
  | 'match_amount_min'
  | 'match_amount_max'
  | 'override_next_amount'

/**
 * The typed amounts that are not amounts, as field errors. The drafts below
 * send only parsed amounts, so the editor saves nothing while one is named.
 */
export function seriesAmountErrors(edits: SeriesEdits): Partial<Record<SeriesAmountField, string>> {
  const errors: Partial<Record<SeriesAmountField, string>> = {}
  const check = (field: SeriesAmountField) => {
    const error = amountFieldError(edits[field])
    if (error !== null) errors[field] = error
  }
  check('amount')
  if (edits.match_criteria === 'range') {
    check('match_amount_min')
    check('match_amount_max')
  }
  check('override_next_amount')
  return errors
}

export function draftFromEdits(edits: SeriesEdits): SeriesDraft {
  return { ...editedFields(edits), reminder_days: reminderDays(edits) }
}

/**
 * The overrides are always sent, null included, so an emptied box clears the
 * column. A blank reminder lead time sends nothing.
 */
export function patchFromEdits(edits: SeriesEdits): SeriesPatch {
  const days = reminderDays(edits)
  return {
    ...editedFields(edits),
    override_next_due_on: edits.override_next_due_on,
    override_next_amount: optionalAmountWire(edits.override_next_amount) ?? null,
    ...(days === null ? {} : { reminder_days: days }),
  }
}
