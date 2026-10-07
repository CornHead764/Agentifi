/**
 * Which accounts a screen shows when nobody has said: the dashboard's Recent
 * Transactions widget (a list of ids) and the register (a node of the accounts
 * drawer). The widget keeps Liabilities and drops gift cards; the register
 * scopes to a drawer node, so every account under it is in scope and the
 * header's total agrees with the rows.
 */

import { useSyncExternalStore } from 'react'

import {
  ALL_ACCOUNTS_ID,
  BANKING_CLASS_ID,
  LIABILITIES_CLASS_ID,
  classIdFor,
  type AccountNode,
} from '@/components/shell/accountTree'
import { createListenerSet } from '@/lib/listenerSet'
import { readStored, writeStored } from '@/lib/storage'
import type { AccountWithBalances, Uuid } from '@/lib/transactions/types'

const EVERYDAY_CLASSES = new Set([BANKING_CLASS_ID, LIABILITIES_CLASS_ID])

/** Every open account somebody spends from: no investments, assets or gift cards. */
export function everydayAccounts(accounts: readonly AccountWithBalances[]): Uuid[] {
  return accounts
    .filter(
      (account) =>
        !account.is_closed &&
        EVERYDAY_CLASSES.has(classIdFor(account)) &&
        account.type !== 'gift_card',
    )
    .map((account) => account.id)
}

/**
 * The account ids the widget queries with. Only `null`/`undefined` mean
 * "nobody has chosen"; an empty list shows nothing. Closed or deleted ids are
 * dropped, since an unknown id narrows the query to nothing.
 */
export function recentAccountIds(
  chosen: readonly Uuid[] | null | undefined,
  accounts: readonly AccountWithBalances[],
): Uuid[] {
  if (chosen === null || chosen === undefined) return everydayAccounts(accounts)
  const live = new Set(accounts.filter((account) => !account.is_closed).map((one) => one.id))
  return chosen.filter((id) => live.has(id))
}

/**
 * Banking, or every account when the tree has no Banking class. A tree not yet
 * loaded has none either, so hold the query until the accounts land.
 */
export function defaultScopeNode(tree: readonly AccountNode[]): string {
  return tree.some((node) => node.id === BANKING_CLASS_ID) ? BANKING_CLASS_ID : ALL_ACCOUNTS_ID
}

/**
 * The node picked in the drawer, which outranks the default. Stored on the
 * device, not only in the URL, so it survives a trip away and back; an
 * explicit All Accounts is stored like any other pick.
 */
const SCOPE_KEY = 'register.scope'

let chosenNode: string | null | undefined
const listeners = createListenerSet()

function readChosen(): string | null {
  if (chosenNode === undefined) chosenNode = readStored(SCOPE_KEY)
  return chosenNode
}

export function rememberScopeNode(id: string): void {
  if (readChosen() === id) return
  chosenNode = id
  writeStored(SCOPE_KEY, id)
  listeners.notify()
}

export function useChosenScopeNode(): string | null {
  return useSyncExternalStore(listeners.subscribe, readChosen, readChosen)
}

