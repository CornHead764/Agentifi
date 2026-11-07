/** Presentation arithmetic over a month the server computed; nothing here re-derives a figure the server sends. */

import {
  absMoney,
  addMoney,
  type Money,
  moneyFromCents,
  moneyToNumber,
  subMoney,
  sumMoney,
  ZERO_MONEY,
} from '@agentifi/core/money'
import { formatMonthKey } from '@agentifi/core/format'
import type {
  EntryGroup,
  EnvelopeState,
  Envelope,
  OtherSpendSlice,
  PlanBucket,
  PlanEntry,
  Projection,
  SpendingCategory,
  SpendingPlanMonth,
} from './types'

/**
 * A bucket's rows by family, in a fixed order. The server materializes the
 * same subtotals as `bill_subtotals`; a test holds the two together.
 */
export function groupEntries(entries: PlanEntry[]): [EntryGroup | null, PlanEntry[]][] {
  const order: (EntryGroup | null)[] = [null, 'bill', 'subscription', 'goal', 'transfer']
  return order
    .map((group): [EntryGroup | null, PlanEntry[]] => [
      group,
      entries.filter((entry) => (entry.group ?? null) === group),
    ])
    .filter(([, group]) => group.length > 0)
}

/** One line of a bucket's list: a row, or the padding rows folded into one. */
export type EntryLine =
  | { kind: 'entry'; entry: PlanEntry }
  | {
      kind: 'padding'
      key: string
      name: string
      category_name: string | null
      entries: PlanEntry[]
      amount: Money
    }

/**
 * A bucket's rows with the padding income rows (`is_padding`) folded into one
 * line per name and category, where the first of them stood. A name with a
 * single row stays a row. The bucket's figure is unchanged: the line's amount
 * is the sum of the rows it holds.
 */
export function foldPadding(entries: readonly PlanEntry[]): EntryLine[] {
  const keyOf = (entry: PlanEntry) => `${entry.category_name ?? ''}\u0000${entry.name}`
  const groups = new Map<string, PlanEntry[]>()
  for (const entry of entries) {
    if (!entry.is_padding) continue
    const key = keyOf(entry)
    groups.set(key, [...(groups.get(key) ?? []), entry])
  }
  const lines: EntryLine[] = []
  const placed = new Set<string>()
  for (const entry of entries) {
    const group = entry.is_padding ? groups.get(keyOf(entry)) : undefined
    if (group === undefined || group.length < 2) {
      lines.push({ kind: 'entry', entry })
      continue
    }
    const key = keyOf(entry)
    if (placed.has(key)) continue
    placed.add(key)
    lines.push({
      kind: 'padding',
      key: `padding:${key}`,
      name: entry.name,
      category_name: entry.category_name,
      entries: group,
      amount: sumMoney(group.map((one) => one.amount)),
    })
  }
  return lines
}

/** The uncategorized share of Other Spending, or null below `atLeast`. */
export function uncategorizedShare(
  slices: readonly OtherSpendSlice[],
  atLeast = 0.2,
): { amount: Money; share: number; count: number } | null {
  let uncategorized: OtherSpendSlice | undefined
  let total = 0
  for (const slice of slices) {
    total += slice.spent
    if (slice.category_id === null) uncategorized = slice
  }
  if (uncategorized === undefined || total <= 0) return null
  const share = uncategorized.spent / total
  if (share < atLeast) return null
  return { amount: uncategorized.spent, share, count: uncategorized.txn_ids.length }
}

/**
 * The share below which a top-level bubble cannot carry its own label:
 * `packCircles` gives r = (size/2.6)·√share in a 420-unit box, and text
 * hides under about 20 units of radius.
 */
const TINY_SHARE = (20 / (420 / 2.6)) ** 2

/**
 * Fold the bubbles too small to carry text into one "Other" bubble (a
 * departure from Simplifi). The tiny slices become its children, so their
 * rows stay reachable; they join an existing "Other" slice when there is one,
 * and a single tiny slice with nowhere to go stays itself.
 */
export function groupTinySlices(slices: readonly OtherSpendSlice[]): OtherSpendSlice[] {
  const total = slices.reduce((sum, one) => sum + Math.max(one.spent, 0), 0)
  if (total <= 0) return [...slices]

  const kept: OtherSpendSlice[] = []
  const tiny: OtherSpendSlice[] = []
  for (const slice of slices) {
    ;(Math.max(slice.spent, 0) / total < TINY_SHARE ? tiny : kept).push(slice)
  }

  const homeIndex = kept.findIndex((one) => one.category_name === 'Other')
  if (tiny.length === 0 || (tiny.length === 1 && homeIndex === -1)) return [...slices]

  const spent = tiny.reduce((sum, one) => addMoney(sum, one.spent), moneyFromCents(0))
  const txnIds = tiny.flatMap((one) => one.txn_ids)

  if (homeIndex !== -1) {
    const home = kept[homeIndex]
    kept[homeIndex] = {
      ...home,
      spent: addMoney(home.spent, spent),
      txn_ids: [...home.txn_ids, ...txnIds],
      children: [...home.children, ...tiny],
    }
    return kept
  }

  return [
    ...kept,
    {
      category_id: null,
      category_name: 'Other',
      spent,
      txn_ids: txnIds,
      children: tiny,
    },
  ]
}

/**
 * Give every group with children a child for its own direct spend, as
 * Simplifi draws it. Its rows are the group's minus the real children's —
 * the bucket's contributing ids, never a re-query.
 */
export function withDirectChildren(slices: readonly OtherSpendSlice[]): OtherSpendSlice[] {
  return slices.map((slice) => {
    if (slice.children.length === 0) return slice
    let remainder = slice.spent
    for (const child of slice.children) remainder = subMoney(remainder, child.spent)
    if (remainder <= 0) return slice
    const claimed = new Set(slice.children.flatMap((child) => child.txn_ids))
    const direct: OtherSpendSlice = {
      category_id: slice.category_id,
      category_name: slice.category_name,
      spent: remainder,
      txn_ids: slice.txn_ids.filter((id) => !claimed.has(id)),
      children: [],
      direct: true,
    }
    return { ...slice, children: [direct, ...slice.children] }
  })
}

const MAX_CHILDREN = 5

/**
 * Fold a group's children past the four largest into one "Other" child, which
 * keeps the folded slices and their rows so every transaction stays reachable.
 */
export function capChildren(slices: readonly OtherSpendSlice[]): OtherSpendSlice[] {
  return slices.map((slice) => {
    if (slice.children.length <= MAX_CHILDREN) return slice
    const ranked = [...slice.children].sort((a, b) => b.spent - a.spent)
    const keep = new Set(ranked.slice(0, MAX_CHILDREN - 1))
    // Kept children stay in their original order: their index picks their
    // shade, and a fold should not recolour the family it trimmed.
    const kept = slice.children.filter((child) => keep.has(child))
    const folded = slice.children.filter((child) => !keep.has(child))
    const rest: OtherSpendSlice = {
      category_id: null,
      category_name: 'Other',
      spent: folded.reduce((sum, one) => addMoney(sum, one.spent), moneyFromCents(0)),
      txn_ids: folded.flatMap((one) => one.txn_ids),
      children: folded,
      rest: true,
    }
    return { ...slice, children: [...kept, rest] }
  })
}

/**
 * A slice's identity. Uncategorized has no id, so its name stands in,
 * prefixed so a collision cannot select the wrong bubble.
 */
export function sliceKey(slice: OtherSpendSlice): string {
  const base = slice.category_id ?? `name:${slice.category_name}`
  // The direct-spend child shares its group's category, and a fold named
  // "Other" can sit inside the top-level Other; the prefixes are what keep
  // selecting one from reading as selecting the other.
  if (slice.direct) return `direct:${base}`
  if (slice.rest) return `rest:${base}`
  return base
}

/**
 * The rows behind the selected bubble, else the open group, else the whole
 * bucket. Looked up by id in the bucket's own `contributing` list and never
 * re-queried, so the list always agrees with the total printed over the
 * bubble; an id with no row is dropped.
 */
export function otherSpendRows(
  bucket: PlanBucket,
  open: OtherSpendSlice | null,
  selected: OtherSpendSlice | null,
): PlanEntry[] {
  const ids = selected?.txn_ids ?? open?.txn_ids ?? null
  if (ids === null) return bucket.contributing

  const byTxn = new Map<string, PlanEntry>()
  for (const entry of bucket.contributing) {
    if (entry.txn_id !== null) byTxn.set(entry.txn_id, entry)
  }
  return ids.map((id) => byTxn.get(id)).filter((entry): entry is PlanEntry => entry !== undefined)
}

/**
 * The excluded rows behind the same part of the chart, matched by category
 * name (the bubbles are built from contributing rows only). A row excluded
 * under a subcategory with no spend this month is only listed at the top level.
 */
export function otherSpendExcludedRows(
  bucket: PlanBucket,
  open: OtherSpendSlice | null,
  selected: OtherSpendSlice | null,
): PlanEntry[] {
  const target = selected ?? open
  if (target === null) return bucket.excluded
  const names = new Set([target.category_name])
  if (selected === null) {
    for (const child of target.children) names.add(child.category_name)
  }
  return bucket.excluded.filter((entry) => names.has(entry.category_name ?? 'Uncategorized'))
}

/** The bubble *Add to Planned Spend* would convert; never Uncategorized, which has no category to point at. */
export function convertibleSlice(
  open: OtherSpendSlice | null,
  selected: OtherSpendSlice | null,
): OtherSpendSlice | null {
  const target = selected ?? open
  return target === null || target.category_id === null ? null : target
}

/**
 * The categories the new envelope should claim. Envelope filters are a strict
 * membership test with no subtree expansion, so a group spells out every
 * subcategory from the full category list, not just this month's children.
 */
export function envelopeSeedCategories(
  slice: OtherSpendSlice,
  categories: readonly SpendingCategory[],
): string[] {
  if (slice.category_id === null) return []
  // The direct-spend child is the group's own category and nothing below it:
  // converting it must not also claim the subcategories sitting beside it.
  if (slice.direct) return [slice.category_id]
  const ids = [slice.category_id]
  for (const category of categories) {
    if (category.parent_id === slice.category_id) ids.push(category.id)
  }
  return ids
}

/* ---- Buckets ------------------------------------------------------------- */

export function effectiveAmount(bucket: PlanBucket): Money {
  return bucket.overwritten_amount ?? bucket.calculated_amount
}

export function isOverridden(bucket: PlanBucket): boolean {
  return bucket.overwritten_amount !== null
}

export type MonthPhase = 'past' | 'current' | 'future'

/** Shared so the dashboard panel and the plan's rail name the headline figure the same way. */
export const MONTH_HEADLINES: Record<MonthPhase, string> = {
  past: 'Ended the month at',
  current: 'So far this month',
  future: 'Expected this month',
}

export function monthPhase(month: SpendingPlanMonth): MonthPhase {
  const asOfMonth = month.as_of.slice(0, 7)
  if (asOfMonth > month.month) return 'past'
  if (asOfMonth < month.month) return 'future'
  return 'current'
}

/** What closing out a month does, asked before it happens. */
export function closeOutPrompt(month: string): { title: string; description: string } {
  return {
    title: `Close out ${formatMonthKey(month)}?`,
    description:
      'Its figures freeze as they stand now. A transaction changed later still changes reports, but not this month, and the months after it carry what it shows. You can reopen it.',
  }
}

/** What reopening a closed month does, asked before it happens. */
export function reopenPrompt(month: string): { title: string; description: string } {
  return {
    title: `Reopen ${formatMonthKey(month)}?`,
    description:
      'Its figures will be recalculated from current transactions, so they can differ from what it closed with. A later month that is closed out keeps its own figures.',
  }
}

export function projectionMethod(projection: Projection, month: SpendingPlanMonth): string {
  switch (projection.type) {
    case 'run_rate':
      // Before the month starts the engine averages prior months instead.
      if (monthPhase(month) === 'future') {
        return `${projection.window_months}-month average (month not started)`
      }
      return `Run rate, ${month.days_elapsed} days so far`
    case 'prior_month':
      return 'Last month'
    case 'average_n_months':
      return `${projection.window_months}-month average`
  }
}

/* ---- Envelopes ----------------------------------------------------------- */

/** The override when the user set one. */
export function envelopeTarget(envelope: Envelope): Money {
  return envelope.overwritten_target_amount ?? envelope.target_amount
}

/** Target plus rollover, less what was released: what the bar and the percentage divide by. */
export function envelopeBudget(envelope: Envelope): Money {
  return subMoney(
    addMoney(envelopeTarget(envelope), envelope.rollover_amount),
    envelope.released_amount,
  )
}

export function envelopeAvailable(envelope: Envelope): Money {
  return subMoney(envelopeBudget(envelope), envelope.spent)
}

/**
 * Percent of budget consumed, unclamped (Simplifi shows 250%). Spend against
 * a zero budget reads as fully used.
 */
export function envelopePctUsed(envelope: Envelope): number {
  const budget = envelopeBudget(envelope)
  if (budget <= 0) return envelope.spent > 0 ? 100 : 0
  return (moneyToNumber(envelope.spent) / moneyToNumber(budget)) * 100
}

/** What the bar fills to. The label overflows; the bar does not. */
export function envelopeBarPct(envelope: Envelope): number {
  return Math.min(envelopePctUsed(envelope), 100)
}

/** Overspending wins over carrying rollover. */
export function envelopeState(envelope: Envelope): EnvelopeState {
  if (envelopeAvailable(envelope) < 0) return 'overspent'
  if (envelope.rollover_amount > 0) return 'with_rollover'
  return 'normal'
}

/** Both shares are of the budget (target plus rollover), so the segments cannot drift apart. */
export function envelopeBarSegments(envelope: Envelope): {
  spentPct: number
  targetPct: number
} {
  const budget = envelopeBudget(envelope)
  if (budget <= 0) return { spentPct: envelope.spent > 0 ? 100 : 0, targetPct: 100 }
  return {
    spentPct: envelopeBarPct(envelope),
    targetPct: Math.min(
      (moneyToNumber(envelopeTarget(envelope)) / moneyToNumber(budget)) * 100,
      100,
    ),
  }
}

/**
 * *Release unspent funds*: the whole of Available goes back to this month's
 * free-to-spend, negative included, and the envelope reads zero.
 */
export function releaseAvailable(envelope: Envelope): {
  envelope: Envelope
  released: Money
} {
  const released = envelopeAvailable(envelope)
  return {
    envelope: { ...envelope, released_amount: addMoney(envelope.released_amount, released) },
    released,
  }
}

export type ReleaseChoice = 'all' | 'this_month' | 'carried_in' | 'custom'

/**
 * The quick picks the release dialog offers and the one it starts at
 * (calculations.md §5). The two parts are offered only when both are there;
 * otherwise either one is the whole Available. An envelope that rolls over
 * starts at this month's part, so what carried in keeps rolling.
 */
export function releaseChoices(envelope: Envelope): {
  picks: { choice: Exclude<ReleaseChoice, 'custom'>; amount: Money }[]
  initial: Exclude<ReleaseChoice, 'custom'>
} {
  const all = { choice: 'all' as const, amount: envelopeAvailable(envelope) }
  if (envelope.unspent_carried_in <= 0 || all.amount <= 0) return { picks: [all], initial: 'all' }
  return {
    picks: [
      all,
      { choice: 'carried_in', amount: envelope.unspent_carried_in },
      { choice: 'this_month', amount: envelope.unspent_this_month },
    ],
    initial: envelope.rolls_over ? 'this_month' : 'all',
  }
}

/**
 * The amount a release choice moves, signed as the server takes it; null
 * until an amount of the person's own is typed. A typed amount is how much to
 * move, so covering an overspend sends its negative.
 */
export function releaseOf(envelope: Envelope, choice: ReleaseChoice, typed: Money | null): Money | null {
  if (choice !== 'custom') {
    return releaseChoices(envelope).picks.find((pick) => pick.choice === choice)?.amount ?? null
  }
  if (typed === null) return null
  const magnitude = absMoney(typed)
  return envelopeAvailable(envelope) < 0 ? subMoney(ZERO_MONEY, magnitude) : magnitude
}

/** A release lies between zero and Available, either side of zero (calculations.md §5). */
export function releaseInBounds(envelope: Envelope, amount: Money): boolean {
  const available = envelopeAvailable(envelope)
  return available < 0 ? amount >= available && amount <= 0 : amount >= 0 && amount <= available
}

/**
 * *Release all unspent funds*: every envelope that does not roll over, as one
 * release each. `releasing` lists those with something to move; `kept` are the
 * envelopes that roll over, which next month already keeps it for.
 */
export function releaseAllAvailable(envelopes: Envelope[]): {
  releasing: { envelope: Envelope; released: Money }[]
  kept: Envelope[]
  released: Money
} {
  const releasing = envelopes
    .filter((envelope) => !envelope.rolls_over)
    .map((envelope) => ({ envelope, released: releaseAvailable(envelope).released }))
    .filter((one) => one.released !== 0)
  return {
    releasing,
    kept: envelopes.filter((envelope) => envelope.rolls_over),
    released: sumMoney(releasing.map((one) => one.released)),
  }
}

/** Why *Release all unspent funds* has nothing to do in the month, or null when it has. */
export function releaseAllUnavailable(month: SpendingPlanMonth): string | null {
  if (month.is_closed_out) return 'This month is closed out.'
  if (month.envelopes.length === 0) return 'There are no envelopes this month.'
  const { releasing, kept } = releaseAllAvailable(month.envelopes)
  if (releasing.length > 0) return null
  if (kept.length === month.envelopes.length) {
    return "Every envelope rolls over, so there's nothing to release."
  }
  return kept.length === 0
    ? 'No envelope has anything left to release.'
    : "The envelopes that don't roll over have nothing left to release."
}

/**
 * The amounts a savings rate is made of. `saved` is `month_result` (the
 * buckets without rollover) so the rate and the amounts cannot drift;
 * `committed` counts bills and envelope targets whether or not yet spent.
 */
export interface SavingsFigures {
  month: string
  income: Money
  committed: Money
  saved: Money
  rate: number | null
  /** The same three once the other spending still expected has landed. */
  projectedCommitted: Money
  projectedSaved: Money
  projectedRate: number | null
}

export function savingsFigures(month: SpendingPlanMonth): SavingsFigures {
  const income = effectiveAmount(month.buckets.income)
  const saved = month.month_result
  const projectedSaved = month.projected_month_result
  return {
    month: month.month,
    income,
    committed: subMoney(income, saved),
    saved,
    rate: rateOf(saved, income),
    projectedCommitted: subMoney(income, projectedSaved),
    projectedSaved,
    projectedRate: rateOf(projectedSaved, income),
  }
}

/**
 * Everything saved over everything earned — not the mean of monthly rates,
 * which would give every month an equal vote and has no answer for a month
 * with no income. Pass completed months only.
 */
export function trailingSavingsRate(months: readonly SpendingPlanMonth[]): {
  months: number
  income: Money
  saved: Money
  rate: number | null
} {
  const income = sumMoney(months.map((one) => effectiveAmount(one.buckets.income)))
  const saved = sumMoney(months.map((one) => one.month_result))
  return { months: months.length, income, saved, rate: rateOf(saved, income) }
}

/** Null, not zero, where there was no income. */
function rateOf(saved: Money, income: Money): number | null {
  return income <= 0 ? null : saved / income
}
