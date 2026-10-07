/**
 * The one `Filter`, client side (ground rule 3). The panel and the DSL parser
 * both produce a `FilterDraft`; `toFilterItems` is the only place a draft
 * becomes the wire shape.
 *
 * Two encodings are deliberate: "has any tag / no tag" is `tag [not] in
 * [every tag]`, and a parent category is expanded to its children here because
 * the server's register path does not expand the tree.
 */

import { absMoney, amountToWire, parseAmountInput } from '@/lib/money'

import type { Category, FilterField, FilterItemWrite, FilterOperator, Tag, Uuid } from './types'

export interface IdFacet {
  ids: Uuid[]
  /** On the facet as a whole: three excluded categories excludes all three. */
  negated: boolean
}

export interface TextFacet {
  values: string[]
  negated: boolean
  /** Read by the payee facet only: the panel means `in`, `payee:wal` means `contains`. Absent is `in`. */
  operator?: FilterOperator
}

export type AmountOperator = 'equals' | 'between' | 'greater_than' | 'less_than'

export interface AmountFacet {
  min: string | null
  max: string | null
  /** The bounds compare magnitudes; the evaluator tests the sign separately. */
  direction?: 'expense' | 'income' | null
  /** Absent derives it from the bounds; the builders set *equals* so it does not reopen as *between 20 and 20*. */
  operator?: AmountOperator
}

export interface DateFacet {
  from: string | null
  to: string | null
  preset: string | null
}

export interface FilterDraft {
  categories: IdFacet
  /** Payee *names*: the evaluator compares them to the display name, casefolded. */
  payees: TextFacet
  tags: IdFacet
  accounts: IdFacet
  flags: TextFacet
  /** Null is "don't care" on every tri-state below. */
  isBillOrSubscription: boolean | null
  excludedFromReports: boolean | null
  excludedFromSpendingPlan: boolean | null
  isReviewed: boolean | null
  isPending: boolean | null
  texts: string[]
  amount: AmountFacet | null
  date: DateFacet | null
  uncategorized: boolean
  /** Rows with no category that the category check could not place. */
  categoryUndetermined: boolean
  hasCategorySuggestion: boolean | null
  hasTags: boolean | null
  /** A file attached by hand or a receipt filed on the row: what the paperclip counts. */
  hasAttachment: boolean | null
  /** A spending row on an account that requires receipts, with none behind it. */
  missingReceipt: boolean | null
}

const NO_IDS: IdFacet = { ids: [], negated: false }
const NO_TEXTS: TextFacet = { values: [], negated: false }

export const EMPTY_DRAFT: FilterDraft = {
  categories: NO_IDS,
  payees: NO_TEXTS,
  tags: NO_IDS,
  accounts: NO_IDS,
  flags: NO_TEXTS,
  isBillOrSubscription: null,
  excludedFromReports: null,
  excludedFromSpendingPlan: null,
  isReviewed: null,
  isPending: null,
  texts: [],
  amount: null,
  date: null,
  uncategorized: false,
  categoryUndetermined: false,
  hasCategorySuggestion: null,
  hasTags: null,
  hasAttachment: null,
  missingReceipt: null,
}

export interface FilterUniverse {
  categories: readonly Category[]
  tags: readonly Tag[]
}

export const EMPTY_UNIVERSE: FilterUniverse = { categories: [], tags: [] }

export function activeFacetCount(draft: FilterDraft): number {
  let count = 0
  if (draft.categories.ids.length > 0 || draft.uncategorized || draft.categoryUndetermined) {
    count += 1
  }
  if (draft.payees.values.length > 0) count += 1
  if (draft.tags.ids.length > 0 || draft.hasTags !== null) count += 1
  if (draft.accounts.ids.length > 0) count += 1
  if (draft.flags.values.length > 0) count += 1
  if (
    draft.isBillOrSubscription !== null ||
    draft.excludedFromReports !== null ||
    draft.excludedFromSpendingPlan !== null ||
    draft.isReviewed !== null ||
    draft.hasCategorySuggestion !== null ||
    draft.hasAttachment !== null ||
    draft.missingReceipt !== null
  ) {
    count += 1
  }
  return count
}

/** The draft with every facet the Filter popover sets cleared; its free text and window stay. */
export function withoutFacets(draft: FilterDraft): FilterDraft {
  return { ...EMPTY_DRAFT, texts: draft.texts, date: draft.date }
}

/** Lists concatenate; a tri-state takes the search box's answer when it has one. */
export function mergeDrafts(panel: FilterDraft, search: FilterDraft): FilterDraft {
  return {
    categories: mergeIds(panel.categories, search.categories),
    payees: mergeTexts(panel.payees, search.payees),
    tags: mergeIds(panel.tags, search.tags),
    accounts: mergeIds(panel.accounts, search.accounts),
    flags: mergeTexts(panel.flags, search.flags),
    isBillOrSubscription: search.isBillOrSubscription ?? panel.isBillOrSubscription,
    excludedFromReports: search.excludedFromReports ?? panel.excludedFromReports,
    excludedFromSpendingPlan: search.excludedFromSpendingPlan ?? panel.excludedFromSpendingPlan,
    isReviewed: search.isReviewed ?? panel.isReviewed,
    isPending: search.isPending ?? panel.isPending,
    texts: [...panel.texts, ...search.texts],
    amount: search.amount ?? panel.amount,
    date: search.date ?? panel.date,
    uncategorized: panel.uncategorized || search.uncategorized,
    categoryUndetermined: panel.categoryUndetermined || search.categoryUndetermined,
    hasCategorySuggestion: search.hasCategorySuggestion ?? panel.hasCategorySuggestion,
    hasTags: search.hasTags ?? panel.hasTags,
    hasAttachment: search.hasAttachment ?? panel.hasAttachment,
    missingReceipt: search.missingReceipt ?? panel.missingReceipt,
  }
}

function mergeIds(a: IdFacet, b: IdFacet): IdFacet {
  if (a.ids.length === 0) return b
  if (b.ids.length === 0) return a
  return { ids: [...new Set([...a.ids, ...b.ids])], negated: a.negated || b.negated }
}

function mergeTexts(a: TextFacet, b: TextFacet): TextFacet {
  if (a.values.length === 0) return b
  if (b.values.length === 0) return a
  // One item has one comparison, so the wider one wins.
  return {
    values: [...new Set([...a.values, ...b.values])],
    negated: a.negated || b.negated,
    operator: a.operator === 'contains' || b.operator === 'contains' ? 'contains' : a.operator,
  }
}

interface ItemSeed {
  field: FilterField
  operator?: FilterOperator
  negated?: boolean
  value_ids?: Uuid[]
  value_texts?: string[]
  text?: string | null
  amount_min?: string | null
  amount_max?: string | null
  date_from?: string | null
  date_to?: string | null
  date_preset?: string | null
  state?: boolean | null
}

/** Every item lands in group 0: a second group would widen the result rather than narrow it. */
export function toFilterItems(draft: FilterDraft, universe: FilterUniverse): FilterItemWrite[] {
  const seeds: ItemSeed[] = []

  if (draft.categories.ids.length > 0) {
    seeds.push({
      field: 'category',
      value_ids: expandCategories(draft.categories.ids, universe.categories),
      negated: draft.categories.negated,
    })
  }
  // Its own field, not "not any of every category", which goes stale as categories are added.
  if (draft.uncategorized) {
    seeds.push({ field: 'is_uncategorized', operator: 'is_true', state: true })
  }
  if (draft.payees.values.length > 0) {
    seeds.push({
      field: 'payee',
      operator: draft.payees.operator,
      value_texts: draft.payees.values,
      negated: draft.payees.negated,
    })
  }
  if (draft.tags.ids.length > 0) {
    seeds.push({ field: 'tag', value_ids: draft.tags.ids, negated: draft.tags.negated })
  }
  if (draft.hasTags !== null) {
    const everyTag = universe.tags.map((tag) => tag.id)
    if (everyTag.length > 0) {
      seeds.push({ field: 'tag', value_ids: everyTag, negated: !draft.hasTags })
    }
  }
  if (draft.accounts.ids.length > 0) {
    seeds.push({
      field: 'account',
      value_ids: draft.accounts.ids,
      negated: draft.accounts.negated,
    })
  }
  if (draft.flags.values.length > 0) {
    seeds.push({ field: 'flag', value_texts: draft.flags.values, negated: draft.flags.negated })
  }
  for (const text of draft.texts) {
    seeds.push({ field: 'text', operator: 'contains', text })
  }
  if (draft.amount !== null) {
    seeds.push({
      field: 'amount',
      operator: amountOperator(draft.amount),
      amount_min: amountBound(draft.amount.min),
      amount_max: amountBound(draft.amount.max),
      // Null is either sign; false means expenses.
      state: draft.amount.direction == null ? null : draft.amount.direction === 'income',
    })
  }
  if (draft.date !== null) {
    seeds.push({
      field: 'date',
      operator: 'between',
      date_from: draft.date.from,
      date_to: draft.date.to,
      date_preset: draft.date.preset,
    })
  }
  if (draft.isBillOrSubscription !== null) {
    seeds.push({ field: 'is_bill_or_subscription', operator: 'is_true', state: draft.isBillOrSubscription })
  }
  if (draft.excludedFromReports !== null) {
    seeds.push({ field: 'is_excluded_from_reports', operator: 'is_true', state: draft.excludedFromReports })
  }
  if (draft.excludedFromSpendingPlan !== null) {
    seeds.push({
      field: 'is_excluded_from_spending_plan',
      operator: 'is_true',
      state: draft.excludedFromSpendingPlan,
    })
  }
  if (draft.isReviewed !== null) {
    seeds.push({ field: 'is_reviewed', operator: 'is_true', state: draft.isReviewed })
  }
  if (draft.isPending !== null) {
    seeds.push({ field: 'is_pending', operator: 'is_true', state: draft.isPending })
  }
  if (draft.categoryUndetermined) {
    seeds.push({ field: 'is_category_undetermined', operator: 'is_true', state: true })
  }
  if (draft.hasCategorySuggestion !== null) {
    seeds.push({
      field: 'has_category_suggestion',
      operator: 'is_true',
      state: draft.hasCategorySuggestion,
    })
  }
  if (draft.hasAttachment !== null) {
    seeds.push({ field: 'has_attachment', operator: 'is_true', state: draft.hasAttachment })
  }
  if (draft.missingReceipt !== null) {
    seeds.push({ field: 'is_missing_receipt', operator: 'is_true', state: draft.missingReceipt })
  }

  return seeds.map((seed, position) => ({
    field: seed.field,
    operator: seed.operator ?? 'in',
    group_index: 0,
    position,
    negated: seed.negated ?? false,
    value_ids: seed.value_ids ?? [],
    value_texts: seed.value_texts ?? [],
    text: seed.text ?? null,
    amount_min: seed.amount_min ?? null,
    amount_max: seed.amount_max ?? null,
    date_from: seed.date_from ?? null,
    date_to: seed.date_to ?? null,
    date_preset: seed.date_preset ?? null,
    state: seed.state ?? null,
  }))
}

/** The facet's comparison; without one, the bounds set imply it. */
export function amountOperator(amount: AmountFacet): AmountOperator {
  if (amount.operator !== undefined) return amount.operator
  if (amount.min !== null && amount.max !== null) return 'between'
  return amount.min !== null ? 'greater_than' : 'less_than'
}

/**
 * A typed bound as the wire's decimal magnitude, or null when it is blank or
 * not an amount. The bounds compare sizes, so a typed sign is dropped.
 */
export function amountBound(typed: string | null): string | null {
  const parsed = typed === null ? null : parseAmountInput(typed)
  return parsed === null ? null : amountToWire(absMoney(parsed.cents))
}

/** A chosen category stands for its whole subtree, however many levels it runs. */
export function expandCategories(chosen: readonly Uuid[], categories: readonly Category[]): Uuid[] {
  const children = new Map<Uuid, Uuid[]>()
  for (const category of categories) {
    if (category.parent_id === null) continue
    children.set(category.parent_id, [...(children.get(category.parent_id) ?? []), category.id])
  }
  const selected = new Set(chosen)
  const pending = [...chosen]
  for (let id = pending.pop(); id !== undefined; id = pending.pop()) {
    for (const child of children.get(id) ?? []) {
      if (selected.has(child)) continue
      selected.add(child)
      pending.push(child)
    }
  }
  return [...selected]
}
