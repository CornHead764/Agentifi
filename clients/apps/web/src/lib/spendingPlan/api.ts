import { api, type MoneyShape } from '../api'
import type {
  BucketKey,
  BucketOverrideBody,
  PlanBucket,
  PlanEntry,
  ProjectionType,
  SpendingCategory,
  SpendingPlanMonth,
  SpendingPlanMonthWire,
} from './types'

export const spendingPlanKeys = {
  all: ['spending-plan'] as const,
  month: (month: string) => ['spending-plan', month] as const,
  /** Under the categories root, so a category write refreshes it. */
  expenseCategories: ['categories', 'expense'] as const,
}

const ENTRY_SHAPE: MoneyShape<PlanEntry> = { amount: 'money' }

const BUCKET_SHAPE: MoneyShape<PlanBucket> = {
  calculated_amount: 'money',
  effective_amount: 'money',
  overwritten_amount: 'money',
  contributing: ENTRY_SHAPE,
  excluded: ENTRY_SHAPE,
}

/** Every money field the month carries; one left out reaches the screen still a string. */
const MONTH_SHAPE: MoneyShape<SpendingPlanMonthWire> = {
  buckets: BUCKET_SHAPE,
  bills: { amount: 'money' },
  bill_subtotals: { amount: 'money' },
  envelopes: {
    target_amount: 'money',
    overwritten_target_amount: 'money',
    target: 'money',
    rollover_amount: 'money',
    spent: 'money',
    budget: 'money',
    available: 'money',
  },
  // Exactly two levels: a group and its leaves.
  other_spend_by_category: { spent: 'money', children: { spent: 'money' } },
  projection: { buffer: 'money' },
  left_this_month: 'money',
  per_day: 'money',
  month_result: 'money',
  month_result_per_day: 'money',
  projected_month_result: 'money',
  other_spend_to_date: 'money',
  projected_other_spending: 'money',
  projected_left: 'money',
}

/**
 * Key the buckets by their own `key`. A missing bucket throws here, inside
 * the query function, so the page shows its error state; at render time it
 * would blank the whole app, since no error boundary sits above the router.
 */
function keyBuckets(wire: SpendingPlanMonthWire): SpendingPlanMonth {
  const byKey = new Map(wire.buckets.map((bucket) => [bucket.key, bucket]))
  const get = (key: BucketKey): PlanBucket => {
    const bucket = byKey.get(key)
    if (!bucket) throw new TypeError(`the spending plan month is missing ${key}`)
    return bucket
  }

  return {
    ...wire,
    buckets: {
      income: get('income'),
      bills: get('bills'),
      planned_spend: get('planned_spend'),
      other_spend: get('other_spend'),
      goals: get('goals'),
      rollover: get('rollover'),
    },
  }
}

/* ---- Calls --------------------------------------------------------------- */

/** Every call answers with the whole recalculated month. */
export const spendingPlanApi = {
  month: (month: string, signal?: AbortSignal) =>
    api.get<SpendingPlanMonthWire>(`/spending-plan/${month}`, MONTH_SHAPE, signal).then(keyBuckets),

  categories: (signal?: AbortSignal) =>
    api.get<SpendingCategory[]>('/categories?kind=expense', undefined, signal),

  overrideBucket: (month: string, bucket: BucketKey, body: BucketOverrideBody) =>
    api
      .patch<SpendingPlanMonthWire>(`/spending-plan/${month}/buckets/${bucket}`, body, MONTH_SHAPE)
      .then(keyBuckets),

  excludeEntry: (month: string, bucket: BucketKey, entryId: string) =>
    api
      .post<SpendingPlanMonthWire>(
        `/spending-plan/${month}/buckets/${bucket}/exclusions`,
        { entry_id: entryId },
        MONTH_SHAPE,
      )
      .then(keyBuckets),

  includeEntry: (month: string, bucket: BucketKey, entryId: string) =>
    api
      .delete<SpendingPlanMonthWire>(
        `/spending-plan/${month}/buckets/${bucket}/exclusions/${entryId}`,
        MONTH_SHAPE,
      )
      .then(keyBuckets),

  releaseRollover: (month: string, envelopeId: string) =>
    api
      .post<SpendingPlanMonthWire>(
        `/spending-plan/${month}/envelopes/${envelopeId}/release`,
        undefined,
        MONTH_SHAPE,
      )
      .then(keyBuckets),

  releaseAllRollover: (month: string) =>
    api
      .post<SpendingPlanMonthWire>(
        `/spending-plan/${month}/envelopes/release-all`,
        undefined,
        MONTH_SHAPE,
      )
      .then(keyBuckets),

  updateEnvelope: (
    month: string,
    envelopeId: string,
    body: {
      overwritten_target_amount?: string | null
      rollover_amount?: string
      auto_release_rollover?: boolean
      // The server rewrites the envelope's own filter in place, so anything
      // else pointed at that filter follows.
      name?: string
      category_ids?: string[]
      recurring?: boolean
    },
  ) =>
    api
      .patch<SpendingPlanMonthWire>(
        `/spending-plan/${month}/envelopes/${envelopeId}`,
        body,
        MONTH_SHAPE,
      )
      .then(keyBuckets),

  createEnvelope: (
    month: string,
    body: {
      name: string
      target_amount: string
      category_ids: string[]
      recurring: boolean
      rollover: boolean
    },
  ) =>
    api
      .post<SpendingPlanMonthWire>(`/spending-plan/${month}/envelopes`, body, MONTH_SHAPE)
      .then(keyBuckets),

  updateProjection: (
    month: string,
    body: { type: ProjectionType; window_months?: number; buffer?: string },
  ) =>
    api
      .patch<SpendingPlanMonthWire>(`/spending-plan/${month}/projection`, body, MONTH_SHAPE)
      .then(keyBuckets),
}
