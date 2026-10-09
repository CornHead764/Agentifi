/** What a waiting suggestion says, shared by the register cell and the review dialog. */

import { categoryLabel } from '@/lib/categoryNames'
import { sumMoney } from '@/lib/money'

import type { Category, Split, Suggestion, Transaction, Uuid } from './types'

/** The tool that files the row itself under one category. */
const UPDATE_TOOL = 'update_transaction'
/** The tool that replaces the row's allocations. */
const SPLIT_TOOL = 'split_transaction'

/**
 * The category a suggestion proposes, or `undefined` when the category cell
 * cannot stand in for it. Uncategorized is never a suggestion.
 */
export function suggestedCategoryId(
  suggestion: Suggestion | null | undefined,
): Uuid | undefined {
  if (!suggestion) return undefined
  if (suggestion.tool !== UPDATE_TOOL) return undefined
  return suggestion.category_id ?? undefined
}

/**
 * The optimistic cache patch for applying a proposal: reviewed (the server
 * sets it on this path), filed as approved, nothing waiting. A proposed split
 * is drawn only when its parts sum to the row's amount, since the server
 * refuses any other.
 */
export function appliedSuggestion(
  txn: Transaction,
  categoryId?: Uuid | null,
): Partial<Transaction> {
  const patch: Partial<Transaction> = { is_reviewed: true, suggestion: null }
  // A category chosen instead, including none, wins over the proposal.
  if (categoryId !== undefined) {
    patch.category_id = categoryId
    return patch
  }
  const suggestion = txn.suggestion
  if (suggestion === null) return patch
  if (suggestion.tool === SPLIT_TOOL) {
    const splits = proposedSplits(txn, suggestion)
    if (splits !== null) patch.splits = splits
    return patch
  }
  const proposed = suggestedCategoryId(suggestion)
  if (proposed !== undefined) patch.category_id = proposed
  return patch
}

/** The proposal's parts as the row would hold them, or null if they are not this row's. */
function proposedSplits(txn: Transaction, suggestion: Suggestion): Split[] | null {
  const parts = suggestion.splits
  if (parts.length < 2) return null
  if (sumMoney(parts.map((part) => part.amount)) !== txn.amount) return null
  return parts.map((part, index) => ({
    // A placeholder until the refetch, unique across rows as a React key.
    id: `${suggestion.action_id}-${index}`,
    position: index,
    amount: part.amount,
    category_id: part.category_id,
    memo: part.memo === '' ? null : part.memo,
    tag_ids: [],
  }))
}

/** The change the suggestion would make, not the model's reason for it. */
export function describeSuggestion(
  suggestion: Suggestion,
  categories: readonly Category[],
): string {
  if (suggestion.tool === SPLIT_TOOL) {
    return `Split into ${suggestion.splits.length || '?'} categories`
  }
  const proposed = suggestedCategoryId(suggestion)
  if (proposed === undefined) return suggestion.summary || 'Change this transaction'
  return `File this under ${categoryLabel(categories, proposed)}`
}
