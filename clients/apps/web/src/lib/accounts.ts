/**
 * Which accounts a person can write a new row into: not closed, not a gift-card
 * balance (fed entirely by the Amazon pull), and not linked to a connection
 * (the bank is the source of truth; a typed row would double-count).
 */

import { withoutSmallBalances } from '@/components/shell/accountTree'

import { formatDate, formatTimestamp } from '@agentifi/core/format'
import { absMoney, type Money } from '@agentifi/core/money'
import type { Account, AccountWithBalances, StatementSource } from './transactions/types'

const GIFT_CARD = 'gift_card'

/**
 * Whether a balance falls under a small-balance threshold: on its magnitude,
 * and strictly, so one exactly at the threshold does not (calculations.md,
 * "Small balances hidden from the account list").
 */
export function isUnderBalance(balance: Money, threshold: Money): boolean {
  return absMoney(balance) < threshold
}

/**
 * The accounts a picker that files something into an account lists, and how
 * many small balances it left out behind its "N small balances hidden" line.
 * The account already chosen is always listed, flagged or not: a picker that
 * cannot show its own value reads as having none.
 */
export function pickerAccounts<T extends { id: string; hidden_small_balance?: boolean }>(
  accounts: readonly T[],
  chosen: string | null,
  revealed: boolean,
): { shown: readonly T[]; hidden: number } {
  return withoutSmallBalances(
    accounts,
    (account) => account.hidden_small_balance === true && account.id !== chosen,
    revealed,
  )
}

/**
 * Whether an account is one the server flagged `hidden_small_balance`, for a
 * list that leaves such rows out. An id the loaded list lacks is shown.
 */
export function smallBalanceAccounts(
  accounts: readonly Pick<AccountWithBalances, 'id' | 'hidden_small_balance'>[] | undefined,
): (id: string) => boolean {
  const small = new Set(
    (accounts ?? []).filter((account) => account.hidden_small_balance).map((account) => account.id),
  )
  return (id) => small.has(id)
}

/**
 * The accounts a holding can be added to. Only an open investment account
 * holds a position; the server refuses the rest, so they are never offered.
 */
export function openInvestmentAccounts(accounts: readonly Account[]): Account[] {
  return accounts.filter((account) => account.kind === 'investment' && !account.is_closed)
}

export function acceptsManualTransactions(account: Account): boolean {
  if (account.is_closed) return false
  if (account.type === GIFT_CARD) return false
  return account.connection_id === null
}

/**
 * `keepId` keeps a row's current, ineligible account in the list, or the edit
 * form would silently show another account and saving would move the row.
 */
export function manualTransactionTargets(
  accounts: readonly Account[],
  keepId?: string | null,
): Account[] {
  return accounts.filter(
    (account) => acceptsManualTransactions(account) || account.id === keepId,
  )
}

/** The account being looked at when it can take a row, else the first that can, else ''. */
export function defaultTargetAccountId(
  accounts: readonly Account[],
  preferredId: string | null,
): string {
  const preferred = accounts.find((account) => account.id === preferredId)
  if (preferred && acceptsManualTransactions(preferred)) return preferred.id
  return accounts.find(acceptsManualTransactions)?.id ?? ''
}

/** The day is the statement's own where the bill states one, else the day it was filed. */
export function describeStatementSource(source: StatementSource): string {
  const day =
    source.issued_on === null
      ? formatTimestamp(source.fetched_at, 'date')
      : formatDate(source.issued_on)
  switch (source.source) {
    case 'email':
      return `From the ${source.provider} statement e-mail of ${day}`
    case 'provider':
      return `From the ${source.provider} statement fetched ${day}`
    case 'assistant':
      return `From the ${source.provider} statement the assistant read, ${day}`
    case 'manual':
      return `From the ${source.provider} bill entered on Bill providers, ${day}`
  }
}
