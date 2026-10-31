/**
 * Register endpoints, with the money shape each response needs. `MoneyShape<T>`
 * catches a misspelled field but not an omitted one, which arrives as a string
 * and concatenates: check these against the backend's response types.
 */

import { api, type MoneyShape } from '@/lib/api'

import type { Direction, GroupBy, TransactionAggregate } from './aggregate'
import type {
  Account,
  AccountWindowSummary,
  AccountWithBalances,
  BulkReviewResult,
  Category,
  CategoryCheckProgress,
  FilterRead,
  FilterWrite,
  Tag,
  Transaction,
  TransactionCreate,
  TransactionPage,
  TransactionUpdate,
  Uuid,
} from './types'

const TRANSACTION: MoneyShape<Transaction> = {
  amount: 'money',
  amount_primary: 'money',
  balance: 'money',
  splits: { amount: 'money' },
  matched_amount: 'money',
  suggestion: { splits: { amount: 'money' } },
}

const TRANSACTION_PAGE: MoneyShape<TransactionPage> = {
  items: TRANSACTION,
  total: 'money',
  full_total: 'money',
  padding_total: 'money',
}

const CATEGORY_CHECKS: MoneyShape<CategoryCheckProgress> = {
  rows: { suggestion: { splits: { amount: 'money' } } },
}

const ACCOUNT: MoneyShape<Account> = {
  provider_balance: 'money',
  withheld_balance: 'money',
  opening_balance: 'money',
  goal_balance: 'money',
  pending_holds: 'money',
  credit_limit: 'money',
  statement_balance: 'money',
  minimum_due: 'money',
  hide_below_balance: 'money',
}

// Shared with the settings page so there is one copy to keep complete.
export const ACCOUNT_WITH_BALANCES: MoneyShape<AccountWithBalances> = {
  ...ACCOUNT,
  balances: {
    balance: 'money',
    balance_with_pending: 'money',
    available_balance: 'money',
  },
  equity: {
    value: 'money',
    owed: 'money',
    equity: 'money',
  },
}

const ACCOUNT_SUMMARY: MoneyShape<AccountWindowSummary> = {
  opening_balance: 'money',
  ending_balance: 'money',
  total: 'money',
  balances: {
    balance: 'money',
    balance_with_pending: 'money',
    available_balance: 'money',
  },
}

export interface RegisterQuery {
  /** `null` is every account and `[]` is none of them. */
  accountIds: readonly Uuid[] | null
  from: string | null
  to: string | null
  /** Always sent rather than left to a default: the two can be a statement cycle apart. */
  dateField: 'posted' | 'effective'
  filterId: Uuid | null
  reviewed: boolean | null
  /**
   * `hide` folds padding income rows out of the list and the bulk review; the
   * page reports them in `padding_count`. Figures count them either way.
   */
  padding: 'show' | 'hide'
  order: 'asc' | 'desc'
  limit: number
  offset: number
}

export const DEFAULT_QUERY: RegisterQuery = {
  accountIds: null,
  from: null,
  to: null,
  dateField: 'posted',
  filterId: null,
  reviewed: null,
  padding: 'show',
  order: 'desc',
  limit: 200,
  offset: 0,
}

/**
 * The list and the bulk review action send this identical string, so "mark all
 * as reviewed" acts on exactly the rows on screen. An empty account selection
 * is sent as one empty `account_id`, since none at all means every account.
 */
export function registerParams(query: RegisterQuery): string {
  const params = new URLSearchParams()
  if (query.accountIds !== null) {
    if (query.accountIds.length === 0) params.append('account_id', '')
    for (const id of query.accountIds) params.append('account_id', id)
  }
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  params.set('date_field', query.dateField)
  if (query.filterId) params.set('filter_id', query.filterId)
  if (query.reviewed !== null) params.set('reviewed', String(query.reviewed))
  if (query.padding === 'hide') params.set('padding', 'hide')
  params.set('order', query.order)
  params.set('limit', String(query.limit))
  params.set('offset', String(query.offset))
  return params.toString()
}

export function listTransactions(query: RegisterQuery, signal?: AbortSignal) {
  return api.get<TransactionPage>(
    `/transactions?${registerParams(query)}`,
    TRANSACTION_PAGE,
    signal,
  )
}

const AGGREGATE: MoneyShape<TransactionAggregate> = {
  total: 'money',
  buckets: { total: 'money' },
  months: { buckets: { total: 'money' } },
}

/**
 * Built on `registerParams` so the chart and the activity table describe the
 * same rows (trap 5). `under` is the category a category chart is drilled
 * into, whose children are its lines.
 */
export function aggregateParams(
  query: RegisterQuery,
  direction: Direction,
  groupBy: GroupBy,
  under: Uuid | null = null,
): string {
  const params = new URLSearchParams(registerParams(query))
  params.set('direction', direction)
  params.set('group_by', groupBy)
  if (under !== null) params.set('under', under)
  return params.toString()
}

export function readAggregate(
  query: RegisterQuery,
  direction: Direction,
  groupBy: GroupBy,
  under: Uuid | null,
  signal?: AbortSignal,
) {
  return api.get<TransactionAggregate>(
    `/transactions/aggregate?${aggregateParams(query, direction, groupBy, under)}`,
    AGGREGATE,
    signal,
  )
}

export function readTransaction(id: Uuid, signal?: AbortSignal) {
  return api.get<Transaction>(`/transactions/${id}`, TRANSACTION, signal)
}

/** At most 200 ids per call. */
export function readCategoryChecks(ids: readonly Uuid[], signal?: AbortSignal) {
  const params = new URLSearchParams()
  for (const id of ids) params.append('id', id)
  return api.get<CategoryCheckProgress>(
    `/transactions/category-checks?${params.toString()}`,
    CATEGORY_CHECKS,
    signal,
  )
}

export function createTransaction(body: TransactionCreate) {
  return api.post<Transaction>('/transactions', body, TRANSACTION)
}

export function updateTransaction(id: Uuid, body: TransactionUpdate) {
  return api.patch<Transaction>(`/transactions/${id}`, body, TRANSACTION)
}

export function deleteTransaction(id: Uuid) {
  return api.delete<void>(`/transactions/${id}`)
}

export function setTags(id: Uuid, tagIds: Uuid[]) {
  return api.patch<Transaction>(`/transactions/${id}`, { tag_ids: tagIds }, TRANSACTION)
}

export function setReviewed(id: Uuid, reviewed: boolean) {
  return api.patch<Transaction>(`/transactions/${id}`, { is_reviewed: reviewed }, TRANSACTION)
}

/** Omitted `dueOn` picks the occurrence nearest the charge's date; the server refuses a slot already paid. */
export function linkSeries(id: Uuid, seriesId: Uuid, dueOn?: string) {
  return api.post<Transaction>(
    `/transactions/${id}/link-series`,
    { series_id: seriesId, due_on: dueOn ?? null },
    TRANSACTION,
  )
}

export function unlinkSeries(id: Uuid) {
  return api.post<Transaction>(`/transactions/${id}/unlink-series`, {}, TRANSACTION)
}

/** Applies to the whole query, not to the loaded page. */
export function markAllReviewed(query: RegisterQuery, reviewed: boolean) {
  return api.post<BulkReviewResult>(
    `/transactions/mark-reviewed?${registerParams(query)}`,
    { is_reviewed: reviewed },
  )
}

/** Adds to each row's own tags; `remove` takes the named tags off. One transaction on the server. */
export function bulkTag(ids: readonly Uuid[], change: { add?: readonly Uuid[]; remove?: readonly Uuid[] }) {
  return api.post<{ updated: number }>('/transactions/bulk-tags', {
    transaction_ids: ids,
    add_tag_ids: change.add ?? [],
    remove_tag_ids: change.remove ?? [],
  })
}

export function listAccounts(signal?: AbortSignal) {
  return api.get<AccountWithBalances[]>('/accounts', ACCOUNT_WITH_BALANCES, signal)
}

export function readAccountSummary(id: Uuid, query: RegisterQuery, signal?: AbortSignal) {
  const params = new URLSearchParams()
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  params.set('date_field', query.dateField)
  return api.get<AccountWindowSummary>(`/accounts/${id}/summary?${params}`, ACCOUNT_SUMMARY, signal)
}

export function listCategories(signal?: AbortSignal) {
  return api.get<Category[]>('/categories', undefined, signal)
}

export function listTags(signal?: AbortSignal) {
  return api.get<Tag[]>('/tags', undefined, signal)
}

/** Every payee in the ledger, most-used first. */
export function listPayees(signal?: AbortSignal) {
  return api.get<string[]>('/transactions/payees', undefined, signal)
}

export function createFilter(body: FilterWrite) {
  return api.post<FilterRead>('/filters', body)
}
