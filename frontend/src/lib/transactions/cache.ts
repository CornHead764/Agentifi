/**
 * Optimistic register edits. A failed write restores the exact snapshot rather
 * than refetching, which would be a second chance to fail.
 */

import type { InfiniteData, QueryClient, QueryKey } from '@tanstack/react-query'

import { ZERO_MONEY, addMoney, subMoney } from '@/lib/money'

import { registerParams, type RegisterQuery } from './api'
import type { Transaction, TransactionPage, Uuid } from './types'

export const REGISTER_ROOT: QueryKey = ['transactions', 'register']

export function registerKey(query: RegisterQuery): QueryKey {
  return [...REGISTER_ROOT, registerParams({ ...query, offset: 0 })]
}

/** A sibling of `REGISTER_ROOT`, not a child: the patches below treat everything under it as paged rows. */
export const AGGREGATE_ROOT: QueryKey = ['transactions', 'aggregate']

/** A sibling of `REGISTER_ROOT` for the same reason. */
export const ADJUSTMENTS_ROOT: QueryKey = ['transactions', 'balance-adjustments']

export function aggregateKey(
  query: RegisterQuery,
  direction: string,
  groupBy: string,
  under: string | null,
): QueryKey {
  return [...AGGREGATE_ROOT, direction, groupBy, under, registerParams({ ...query, offset: 0 })]
}

export const ACCOUNTS_KEY: QueryKey = ['accounts']
export const CATEGORIES_KEY: QueryKey = ['categories']
export const TAGS_KEY: QueryKey = ['tags']
export const PAYEES_KEY: QueryKey = ['payees']
/** The dashboard's tiles counting the rows that wait on somebody. */
export const REVIEW_TILES_ROOT: QueryKey = ['dashboard', 'review']

/** What any transaction write leaves stale; invalidating the register alone leaves its totals wrong. */
export const TRANSACTIONS_CHANGED_KEYS: readonly QueryKey[] = [
  REGISTER_ROOT,
  AGGREGATE_ROOT,
  ADJUSTMENTS_ROOT,
  ACCOUNTS_KEY,
  REVIEW_TILES_ROOT,
]

/** Not a real prefix: `useInvalidatingMutation` expands it to `TRANSACTIONS_CHANGED_KEYS`. */
export const TRANSACTIONS_KEY: QueryKey = ['transactions']

export function invalidateTransactions(client: QueryClient): void {
  for (const key of TRANSACTIONS_CHANGED_KEYS) void client.invalidateQueries({ queryKey: key })
}

export function transactionKey(id: string | null): QueryKey {
  return ['transaction', id]
}

/** Under `ACCOUNTS_KEY`, so an account refresh refreshes its summaries too. */
export function accountSummaryKey(
  accountId: string | null,
  from: string | null,
  to: string | null,
  dateField: string,
): QueryKey {
  return [...ACCOUNTS_KEY, accountId, 'summary', from, to, dateField]
}

export function adHocFilterKey(signature: string): QueryKey {
  return ['filters', 'ad-hoc', signature]
}

type RegisterData = InfiniteData<TransactionPage>

export type RegisterSnapshot = readonly [QueryKey, RegisterData | undefined][]

export function snapshotRegister(client: QueryClient): RegisterSnapshot {
  return client.getQueriesData<RegisterData>({ queryKey: REGISTER_ROOT })
}

export function restoreRegister(client: QueryClient, snapshot: RegisterSnapshot): void {
  for (const [key, data] of snapshot) client.setQueryData(key, data)
}

/** Rewrite one row everywhere it is cached, moving the page totals with its amount. */
export function patchRegisterCache(
  client: QueryClient,
  id: Uuid,
  patch: Partial<Transaction>,
): void {
  client.setQueriesData<RegisterData>({ queryKey: REGISTER_ROOT }, (data) =>
    data === undefined ? data : patchRegisterData(data, id, patch),
  )
}

export function patchRegisterData(
  data: RegisterData,
  id: Uuid,
  patch: Partial<Transaction>,
): RegisterData {
  return { ...data, pages: data.pages.map((page) => patchPage(page, id, patch)) }
}

export function patchPage(
  page: TransactionPage,
  id: Uuid,
  patch: Partial<Transaction>,
): TransactionPage {
  const index = page.items.findIndex((item) => item.id === id)
  if (index === -1) return page

  const previous = page.items[index]
  const next = { ...previous, ...patch }
  const items = [...page.items]
  items[index] = next

  const delta =
    patch.amount === undefined ? ZERO_MONEY : subMoney(next.amount, previous.amount)
  return {
    ...page,
    items,
    // A partial row counts its matching splits, which an amount edit does not move.
    total: previous.matched_amount == null ? addMoney(page.total, delta) : page.total,
    full_total: addMoney(page.full_total, delta),
  }
}

export function removeFromRegisterCache(client: QueryClient, id: Uuid): void {
  client.setQueriesData<RegisterData>({ queryKey: REGISTER_ROOT }, (data) =>
    data === undefined
      ? data
      : { ...data, pages: data.pages.map((page) => removeFromPage(page, id)) },
  )
}

export function removeFromPage(page: TransactionPage, id: Uuid): TransactionPage {
  const row = page.items.find((item) => item.id === id)
  if (row === undefined) return page
  return {
    ...page,
    items: page.items.filter((item) => item.id !== id),
    count: page.count - 1,
    total: subMoney(page.total, row.matched_amount ?? row.amount),
    full_total: subMoney(page.full_total, row.amount),
    partial_count: page.partial_count - (row.matched_amount == null ? 0 : 1),
  }
}

/** Deduplicated across page seams. */
export function flattenPages(data: RegisterData | undefined): Transaction[] {
  if (data === undefined) return []
  const seen = new Set<Uuid>()
  const rows: Transaction[] = []
  for (const page of data.pages) {
    for (const item of page.items) {
      if (seen.has(item.id)) continue
      seen.add(item.id)
      rows.push(item)
    }
  }
  return rows
}
