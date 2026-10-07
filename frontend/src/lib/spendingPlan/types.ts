import type { Money } from '../money'

/** The six buckets, in the order the rail stacks them and the headline sums them. */
export const BUCKET_ORDER = [
  'income',
  'bills',
  'planned_spend',
  'other_spend',
  'goals',
  'rollover',
] as const

export type BucketKey = (typeof BUCKET_ORDER)[number]

/** The five stacked in the rail; rollover is carried in, not a row of its own. */
export const STACKED_BUCKETS: readonly BucketKey[] = [
  'income',
  'bills',
  'planned_spend',
  'other_spend',
  'goals',
]

export const BUCKET_LABELS: Record<BucketKey, string> = {
  income: 'Income',
  bills: 'Bills',
  planned_spend: 'Planned Spend',
  other_spend: 'Other Spend',
  goals: 'Goals',
  rollover: 'Rollover',
}

/**
 * Shared by the plan's rows, the reminders and a series' history.
 * `received` and `paid` are one state, worded for income and for everything else.
 */
export type EntryStatus = 'received' | 'paid' | 'past_due' | 'upcoming' | 'skipped'

export const STATUS_LABELS: Record<EntryStatus, string> = {
  received: 'Received',
  paid: 'Paid',
  past_due: 'Past due',
  upcoming: 'Upcoming',
  skipped: 'Skipped',
}

export type StatusTone = 'neutral' | 'income' | 'expense' | 'accent'

export const STATUS_TONES: Record<EntryStatus, StatusTone> = {
  received: 'income',
  paid: 'income',
  past_due: 'expense',
  upcoming: 'accent',
  skipped: 'neutral',
}

/** Transfers and card payments net to zero and must not reduce free-to-spend. */
export type EntryGroup = 'bill' | 'subscription' | 'transfer' | 'goal'

export interface PlanEntry {
  /** Stable per row: the transaction id, or `<series_id>:<due_on>` for an unfulfilled slot. */
  id: string
  txn_id: string | null
  series_id: string | null
  name: string
  /** ISO date. */
  due_on: string
  status: EntryStatus
  category_name: string | null
  amount: Money
  account_id: string | null
  /** One share of a split row; an envelope may take a single part of one. */
  is_split: boolean
  group: EntryGroup | null
  /** Excluded from the family's subtotal. */
  is_transfer: boolean
  /** Income recorded beside a payroll-deducted purchase; `foldPadding` lumps these. */
  is_padding: boolean
}

export interface PlanBucket {
  key: BucketKey
  calculated_amount: Money
  /** The override when there is one. */
  effective_amount: Money
  /** `null` is no override; zero is an override to zero. */
  overwritten_amount: Money | null
  contributing: PlanEntry[]
  /** Dropped for this month only, without mutating the transaction. */
  excluded: PlanEntry[]
  contributing_txn_ids: string[]
  excluded_entry_ids: string[]
}

/** One occurrence inside the folded Bills bucket, as the server lists them. */
export interface PlanBill {
  id: string
  group: EntryGroup
  series_id: string
  name: string
  due_on: string
  amount: Money
  is_fulfilled: boolean
  txn_ids: string[]
  is_excluded: boolean
}

export interface BillSubtotal {
  group: EntryGroup
  amount: Money
}

export type ProjectionType = 'run_rate' | 'prior_month' | 'average_n_months'

export const PROJECTION_LABELS: Record<ProjectionType, string> = {
  run_rate: 'Run rate',
  prior_month: 'Prior month',
  average_n_months: 'Average of prior months',
}

export interface Projection {
  type: ProjectionType
  buffer: Money
  window_months: number
  start_on: string | null
  end_on: string | null
}

export type EnvelopeState = 'normal' | 'with_rollover' | 'overspent'

export interface Envelope {
  id: string
  name: string
  filter_id: string
  categories: { id: string; name: string }[]
  target_amount: Money
  /** A target override for this month only; `null` is no override. */
  overwritten_target_amount: Money | null
  target: Money
  /** Stored and editable, never derived on read. */
  rollover_amount: Money
  /** Positive. */
  spent: Money
  budget: Money
  available: Money
  /** Decimal strings. `pct_used` has no upper bound. */
  pct_used: string
  bar_pct: string
  state: EnvelopeState
  auto_release_rollover: boolean
  recurring: boolean
  txn_ids: string[]
  /**
   * One row per part charged, so a split transaction appears once per part
   * and the list adds up to `spent` (whole rows from `txn_ids` do not).
   */
  entries: PlanEntry[]
}

export interface OtherSpendSlice {
  category_id: string | null
  category_name: string
  /** Positive. */
  spent: Money
  /** Indexes into the Other Spend bucket's own `contributing` list; never re-queried. */
  txn_ids: string[]
  /** Empty on a leaf and on a group of one. */
  children: OtherSpendSlice[]
  /** Client-side only: the group's direct-spend child, made by `withDirectChildren`. */
  direct?: boolean
  /** Client-side only: the fold made by `capChildren`. */
  rest?: boolean
}

/** One month with the buckets keyed by `keyBuckets`; the wire carries them as an array. */
export interface SpendingPlanMonth extends Omit<SpendingPlanMonthWire, 'buckets'> {
  buckets: Record<BucketKey, PlanBucket>
}

export interface SpendingPlanMonthWire {
  /** `YYYY-MM`. */
  month: string
  /** The server's today, so the per-day rate and the run rate share a divisor. */
  as_of: string
  /** Frozen: no recalculation, no edits, and the screen must say so. */
  is_closed_out: boolean
  closed_out_at: string | null
  buckets: PlanBucket[]
  bills: PlanBill[]
  bill_subtotals: BillSubtotal[]
  envelopes: Envelope[]
  /** A transaction, and the envelopes that matched it and lost the tie-break. */
  contested_txn_ids: Record<string, string[]>
  other_spend_by_category: OtherSpendSlice[]
  projection: Projection
  left_this_month: Money
  per_day: Money | null
  days_remaining: number
  /** Every bucket but the rollover; `left_this_month` is this plus the rollover. */
  month_result: Money
  /** `month_result` over the days left; null once the month is over. */
  month_result_per_day: Money | null
  /** The run rate's divisor: 0 before the month, its length after it. */
  days_elapsed: number
  other_spend_to_date: Money
  /** Never below what has already been spent. */
  projected_other_spending: Money
  projected_left: Money
  /** `projected_left` without the rollover. */
  projected_month_result: Money
}

export interface BucketOverrideBody {
  /** `null` clears the override. */
  overwritten_amount: string | null
}

/** Spend categories only. */
export interface SpendingCategory {
  id: string
  name: string
  /** A child whose parent is not an expense category reads as top level. */
  parent_id: string | null
}
