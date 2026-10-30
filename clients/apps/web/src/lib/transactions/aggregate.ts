/**
 * Presentation over `GET /transactions/aggregate` for the Spending and Income
 * tabs. The arithmetic is server-side, in `backend/internal/domain/aggregate.go`.
 */

import { ZERO_MONEY, addMoney, moneyToNumber, sumMoney, type Money } from '@/lib/money'

import { formatDate } from '@/lib/format'
import type { FilterDraft } from './filter'
import type { Transaction } from './types'

export type GroupBy = 'category' | 'payee' | 'tag' | 'none'
export type Direction = 'spending' | 'income'

export interface Bucket {
  key: string
  label: string
  total: Money
}

export interface AggregateMonth {
  month: string
  /** Only the buckets with something in them; the rest are zero. */
  buckets: Bucket[]
}

export interface TransactionAggregate {
  direction: Direction
  group_by: GroupBy
  /** Never derived from the buckets: a tagged allocation counts under every tag it carries. */
  total: Money
  /** Contributing allocations, not rows. */
  count: number
  buckets: Bucket[]
  months: AggregateMonth[]
}

/** Trap 4: `effective_date`, not `date`, is the reporting date. */
export function reportingDate(txn: Transaction): string {
  return txn.effective_date ?? txn.date
}

/** Keep the leading buckets and fold the rest into one, left negative rather than clamped when refunds outweigh spend. */
export function collapseTail(buckets: readonly Bucket[], keep: number): Bucket[] {
  if (buckets.length <= keep + 1) return [...buckets]
  const head = buckets.slice(0, keep)
  const tail = buckets.slice(keep)
  return [
    ...head,
    { key: 'everything-else', label: 'Everything else', total: sumMoney(tail.map((b) => b.total)) },
  ]
}

const EVERYTHING_ELSE = 'everything-else'

export interface MonthSeries {
  month: string
  label: string
  /** The one sanctioned exit from integer cents. */
  values: Record<string, number>
  /** Tooltips read these so a hovered amount is not re-rounded from a float. */
  amounts: Record<string, Money>
}

/**
 * One cluster per month, folded like the legend so every bar has a colour. A
 * ranked key with nothing that month is drawn at zero: recharts reads a missing
 * series as a gap.
 */
export function monthSeries(
  months: readonly AggregateMonth[],
  ranked: readonly Bucket[],
  direction: Direction,
): MonthSeries[] {
  const known = new Set(ranked.map((bucket) => bucket.key))

  return months.map((month) => {
    const folded = new Map<string, Money>()
    for (const bucket of month.buckets) {
      const key = known.has(bucket.key) ? bucket.key : EVERYTHING_ELSE
      folded.set(key, addMoney(folded.get(key) ?? ZERO_MONEY, bucket.total))
    }

    const values: Record<string, number> = {}
    const amounts: Record<string, Money> = {}
    for (const bucket of ranked) {
      const stored = folded.get(bucket.key) ?? ZERO_MONEY
      // Spending reads positive on a chart, as it does in the legend.
      const plotted = moneyToNumber(stored)
      values[bucket.key] = direction === 'spending' ? -plotted : plotted
      amounts[bucket.key] = stored
    }
    return {
      month: month.month,
      label: formatDate(`${month.month}-01`, 'monthLong'),
      values,
      amounts,
    }
  })
}

/**
 * The clicked wedge as a patch to the register's `FilterDraft`, or null for a
 * wedge no facet can express ("Everything else", no grouping, no payee). A
 * category and "uncategorized" clear each other, as do a tag and "no tags".
 */
export function bucketFacet(
  groupBy: GroupBy,
  bucket: Pick<Bucket, 'key' | 'label'>,
): Partial<FilterDraft> | null {
  if (bucket.key === EVERYTHING_ELSE) return null

  switch (groupBy) {
    case 'category':
      if (bucket.key === 'uncategorized') {
        return { uncategorized: true, categories: { ids: [], negated: false } }
      }
      return { categories: { ids: [bucket.key], negated: false }, uncategorized: false }
    case 'payee':
      if (bucket.key === '') return null
      // The label, not the casefolded key: the evaluator compares display names.
      return { payees: { values: [bucket.label], negated: false } }
    case 'tag':
      if (bucket.key === '') return { hasTags: false, tags: { ids: [], negated: false } }
      return { tags: { ids: [bucket.key], negated: false }, hasTags: null }
    default:
      return null
  }
}
