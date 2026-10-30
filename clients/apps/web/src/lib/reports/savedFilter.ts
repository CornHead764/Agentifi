/**
 * A stored `Filter` back into the panel's draft: the inverse of
 * `lib/transactions/filter.ts`. `fromFilterItems(toFilterItems(d))` must equal
 * `d`, or the unsaved-edit dot lights on an untouched report. Has-any/no-tag is
 * stored as `not any of [every tag]`, which also stores uncategorized the
 * same way over every category; an expanded parent category stays expanded.
 */

import {
  EMPTY_DRAFT,
  amountOperator,
  type AmountFacet,
  type AmountOperator,
  type FilterDraft,
  type FilterUniverse,
} from '@/lib/transactions/filter'
import type { FilterItemWrite, Uuid } from '@/lib/transactions/types'

export function toAmountOperator(value: string): AmountOperator {
  switch (value) {
    case 'between':
    case 'greater_than':
    case 'less_than':
      return value
    default:
      return 'equals'
  }
}

/**
 * A stored amount item as the facet that wrote it. `direction` and `operator`
 * are set only when the encoder would not imply them, so the result equals
 * the panel's own draft.
 */
function readAmount(item: FilterItemWrite): AmountFacet {
  const facet: AmountFacet = { min: item.amount_min, max: item.amount_max }
  if (item.state !== null) facet.direction = item.state ? 'income' : 'expense'

  const operator = toAmountOperator(item.operator)
  if (operator !== amountOperator(facet)) facet.operator = operator
  return facet
}

export function fromFilterItems(
  items: readonly FilterItemWrite[],
  universe: FilterUniverse,
): FilterDraft {
  const draft: FilterDraft = { ...EMPTY_DRAFT, texts: [] }
  const everyCategory = universe.categories.map((category) => category.id)
  const everyTag = universe.tags.map((tag) => tag.id)

  for (const item of items) {
    switch (item.field) {
      case 'category':
        if (item.negated && covers(item.value_ids, everyCategory)) draft.uncategorized = true
        else draft.categories = { ids: [...item.value_ids], negated: item.negated }
        break
      case 'payee':
        draft.payees = { values: [...item.value_texts], negated: item.negated }
        break
      case 'tag':
        if (covers(item.value_ids, everyTag)) draft.hasTags = !item.negated
        else draft.tags = { ids: [...item.value_ids], negated: item.negated }
        break
      case 'account':
        draft.accounts = { ids: [...item.value_ids], negated: item.negated }
        break
      case 'flag':
        draft.flags = { values: [...item.value_texts], negated: item.negated }
        break
      case 'text':
        if (item.text !== null) draft.texts = [...draft.texts, item.text]
        break
      case 'amount':
        draft.amount = readAmount(item)
        break
      case 'date':
        draft.date = { from: item.date_from, to: item.date_to, preset: item.date_preset }
        break
      case 'is_bill_or_subscription':
        draft.isBillOrSubscription = item.state
        break
      case 'is_excluded_from_reports':
        draft.excludedFromReports = item.state
        break
      case 'is_excluded_from_spending_plan':
        draft.excludedFromSpendingPlan = item.state
        break
      case 'is_reviewed':
        draft.isReviewed = item.state
        break
      case 'is_category_undetermined':
        draft.categoryUndetermined = item.state === true
        break
      case 'has_category_suggestion':
        draft.hasCategorySuggestion = item.state
        break
      case 'has_attachment':
        draft.hasAttachment = item.state
        break
      case 'is_missing_receipt':
        draft.missingReceipt = item.state
        break
      case 'is_uncategorized':
        draft.uncategorized = item.state === true
        break
      case 'is_pending':
        draft.isPending = item.state
        break
    }
  }

  return draft
}

/**
 * A saved report's filter, split into facets and the search box. The box is
 * sent as a `text` item and also stored in `query_text`, but the report PATCH
 * has no `query_text`, so the items are the authority. The chosen text leaves
 * the draft, or reopening would apply it twice.
 */
export function splitSavedSearch(
  items: readonly FilterItemWrite[],
  queryText: string | null,
  universe: FilterUniverse,
): { filter: FilterDraft; search: string } {
  const draft = fromFilterItems(items, universe)
  const stored = queryText ?? ''
  const search = draft.texts.includes(stored) ? stored : (draft.texts.at(-1) ?? '')
  if (search === '') return { filter: draft, search }

  const index = draft.texts.lastIndexOf(search)
  const texts = [...draft.texts.slice(0, index), ...draft.texts.slice(index + 1)]
  return { filter: { ...draft, texts }, search }
}

/** An empty universe cannot be covered. */
function covers(ids: readonly Uuid[], universe: readonly Uuid[]): boolean {
  if (universe.length === 0 || ids.length !== universe.length) return false
  const present = new Set(ids)
  return universe.every((id) => present.has(id))
}
