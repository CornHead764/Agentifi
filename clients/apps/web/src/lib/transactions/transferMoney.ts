/**
 * Moving money between two accounts: two ordinary transactions, then a pair.
 * The server's rules are checked here first, because a refusal at the pairing
 * step leaves two unpaired legs.
 */

import { parseAmountInput, ZERO_MONEY } from '@/lib/money'

import type { TransactionCreate } from './types'

export interface TransferAccount {
  id: string
  name: string
  currency: string
}

export interface TransferDraft {
  fromAccountId: string
  toAccountId: string
  amount: string
  date: string
  notes: string
}

export type TransferProblem =
  | 'no-accounts'
  | 'same-account'
  | 'mixed-currency'
  | 'unparseable'
  | 'not-positive'

export interface TransferPlan {
  paying: TransactionCreate
  receiving: TransactionCreate
}

export function blankTransferDraft(fromAccountId: string, today: string): TransferDraft {
  return { fromAccountId, toAccountId: '', amount: '', date: today, notes: '' }
}

/** Every rule here is one the server also enforces. */
export function transferProblem(
  draft: TransferDraft,
  accounts: readonly TransferAccount[],
): TransferProblem | null {
  const from = accounts.find((one) => one.id === draft.fromAccountId)
  const to = accounts.find((one) => one.id === draft.toAccountId)
  if (!from || !to) return 'no-accounts'
  if (from.id === to.id) return 'same-account'
  if (from.currency !== to.currency) return 'mixed-currency'

  const parsed = parseAmountInput(draft.amount)
  if (parsed === null) return 'unparseable'
  // A typed minus is ignored: the account fields give the direction.
  if (parsed.cents === ZERO_MONEY) return 'not-positive'
  return null
}

export function describeTransferProblem(problem: TransferProblem): string {
  switch (problem) {
    case 'no-accounts':
      return 'Pick the account the money leaves and the one it arrives in.'
    case 'same-account':
      return 'Both halves are in the same account, so no money would move between accounts.'
    case 'mixed-currency':
      return 'Those two accounts hold different currencies, and the same figure in two currencies is not the same money.'
    case 'unparseable':
      return 'That is not an amount.'
    case 'not-positive':
      return 'Enter how much to move.'
  }
}

/** The two legs, signed here from a positive amount and marked reviewed. */
export function planTransfer(
  draft: TransferDraft,
  accounts: readonly TransferAccount[],
): TransferPlan | null {
  if (transferProblem(draft, accounts) !== null) return null
  const parsed = parseAmountInput(draft.amount)
  if (parsed === null) return null

  const from = accounts.find((one) => one.id === draft.fromAccountId)
  const to = accounts.find((one) => one.id === draft.toAccountId)
  if (!from || !to) return null

  const size = parsed.wire.replace('-', '')
  const notes = draft.notes.trim() || null
  return {
    paying: {
      account_id: from.id,
      date: draft.date,
      amount: `-${size}`,
      payee: `Transfer to ${to.name}`,
      notes,
      is_reviewed: true,
    },
    receiving: {
      account_id: to.id,
      date: draft.date,
      amount: size,
      payee: `Transfer from ${from.name}`,
      notes,
      is_reviewed: true,
    },
  }
}
