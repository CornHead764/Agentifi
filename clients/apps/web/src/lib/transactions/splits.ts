/**
 * Split editing. The splits must sum to the amount: reports sum the splits
 * and the register shows the amount.
 */

import {
  type Money,
  ZERO_MONEY,
  amountToWire,
  parseAmountInput,
  subMoney,
  sumMoney,
} from '@/lib/money'

import type { Split, SplitWrite, Uuid } from './types'

export interface SplitDraft {
  /** Stable across re-orders so React does not remount a row being typed in. */
  key: string
  /** As typed. Parsed only on validation, so a half-typed "-" is not an error yet. */
  amount: string
  category_id: Uuid | null
  memo: string | null
  tag_ids: Uuid[]
}

export type SplitProblem =
  | { kind: 'unparseable'; key: string }
  | { kind: 'too-few' }
  | { kind: 'unbalanced'; remainder: Money; over: boolean }

export interface SplitValidation {
  /** The rows to send, or null when anything below is wrong. */
  rows: SplitWrite[] | null
  allocated: Money
  /** Parent minus allocated. Zero is the only state the server accepts. */
  remainder: Money
  problems: SplitProblem[]
}

let nextKey = 0

export function newSplitDraft(amount = ''): SplitDraft {
  nextKey += 1
  return { key: `draft-${nextKey}`, amount, category_id: null, memo: null, tag_ids: [] }
}

/** A row with no splits yet starts with the whole amount on the first part and nothing on the second. */
export function initialDrafts(splits: readonly Split[], parent: Money): SplitDraft[] {
  if (splits.length > 0) {
    return splits.map((split) => splitToDraft(split, amountToWire(split.amount)))
  }
  return [newSplitDraft(amountToWire(parent)), newSplitDraft('')]
}

function splitToDraft(split: Split, amountText: string): SplitDraft {
  return {
    key: split.id,
    amount: amountText,
    category_id: split.category_id,
    memo: split.memo,
    tag_ids: split.tag_ids,
  }
}

/** `remainder` is returned even when the set is invalid, for "left to allocate". */
export function validateSplits(drafts: readonly SplitDraft[], parent: Money): SplitValidation {
  const problems: SplitProblem[] = []
  const rows: SplitWrite[] = []
  const amounts: Money[] = []

  for (const draft of drafts) {
    const parsed = parseAmountInput(draft.amount)
    if (parsed === null) {
      problems.push({ kind: 'unparseable', key: draft.key })
      continue
    }
    amounts.push(parsed.cents)
    rows.push({
      amount: parsed.wire,
      category_id: draft.category_id,
      memo: draft.memo,
      tag_ids: draft.tag_ids,
    })
  }

  if (drafts.length < 2) problems.push({ kind: 'too-few' })

  const allocated = sumMoney(amounts)
  const remainder = subMoney(parent, allocated)
  if (problems.length === 0 && remainder !== ZERO_MONEY) {
    // Over-allocated is the remainder pointing away from the parent's sign.
    problems.push({ kind: 'unbalanced', remainder, over: parent >= 0 ? remainder < 0 : remainder > 0 })
  }

  return { rows: problems.length === 0 ? rows : null, allocated, remainder, problems }
}

/** Give the unallocated remainder to one row, which is what "balance" means here. */
export function absorbRemainder(
  drafts: readonly SplitDraft[],
  key: string,
  parent: Money,
): SplitDraft[] {
  const others = drafts.filter((draft) => draft.key !== key)
  const allocated = sumMoney(
    others.map((draft) => parseAmountInput(draft.amount)?.cents ?? ZERO_MONEY),
  )
  const target = subMoney(parent, allocated)
  return drafts.map((draft) =>
    draft.key === key ? { ...draft, amount: amountToWire(target) } : draft,
  )
}

export function describeProblem(problem: SplitProblem): string {
  switch (problem.kind) {
    case 'too-few':
      return 'A split needs at least two parts.'
    case 'unparseable':
      return 'One of the amounts is not a number.'
    case 'unbalanced':
      return problem.over
        ? 'The parts add up to more than the transaction.'
        : 'Not all of the transaction has been allocated.'
  }
}
