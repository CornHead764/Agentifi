/**
 * Data access for the register. Nothing here retries: a retried POST that
 * succeeded the first time puts a duplicate row in the ledger.
 */

import {
  mutationOptions,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
  type QueryKey,
} from '@tanstack/react-query'


import { useCallback, useEffect, useMemo, useReducer } from 'react'

import type { Direction, GroupBy } from './aggregate'
import { useInvalidatingMutation } from '@/lib/queryClient'
import {
  applyAssistantAction,
  discardAssistantAction,
} from '@/lib/clients/assistant'
import { PENDING_KEY } from '@/lib/clients/automations'

import {
  createFilter,
  createTransaction,
  deleteTransaction,
  listAccounts,
  readAggregate,
  listCategories,
  listPayees,
  listTags,
  bulkTag,
  linkSeries,
  listTransactions,
  DEFAULT_QUERY,
  markAllReviewed,
  readAccountSummary,
  readCategoryChecks,
  readTransaction,
  setReviewed,
  setTags,
  unlinkSeries,
  updateTransaction,
  type RegisterQuery,
} from './api'
import {
  ACCOUNTS_KEY,
  ADJUSTMENTS_ROOT,
  CATEGORIES_KEY,
  PAYEES_KEY,
  TRANSACTIONS_CHANGED_KEYS,
  TRANSACTIONS_KEY,
  invalidateTransactions,
  accountSummaryKey,
  adHocFilterKey,
  transactionKey,
  TAGS_KEY,
  aggregateKey,
  patchRegisterCache,
  registerKey,
  removeFromRegisterCache,
  snapshotRegister,
  restoreRegister,
} from './cache'
import { NO_CHECKS, checkReducer, checkingIds } from './categoryCheck'
import { accountNamer } from '@/lib/accounts'
import { buildTransactionUpdate } from './edits'
import { appliedSuggestion } from './suggestions'
import type {
  FilterItemWrite,
  FilterWrite,
  Suggestion,
  Transaction,
  TransactionCreate,
  Uuid,
} from './types'

export function useRegister(query: RegisterQuery, enabled = true) {
  return useInfiniteQuery({
    queryKey: registerKey(query),
    enabled,
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      listTransactions({ ...query, offset: pageParam }, signal),
    getNextPageParam: (last) => {
      const next = last.offset + last.items.length
      return next < last.count ? next : undefined
    },
  })
}

const MAX_PAGE = 500

/**
 * Every balance adjustment on one account, independent of the register's search
 * and paging. The endpoint has no source filter, so this reads the whole history.
 */
export function useBalanceAdjustments(accountId: Uuid | null) {
  return useQuery({
    queryKey: [...ADJUSTMENTS_ROOT, accountId],
    enabled: accountId !== null,
    queryFn: async ({ signal }) => {
      const query = { ...DEFAULT_QUERY, accountIds: [accountId ?? ''], limit: MAX_PAGE }
      const rows: Transaction[] = []
      for (let offset = 0; ; offset += MAX_PAGE) {
        const page = await listTransactions({ ...query, offset }, signal)
        rows.push(...page.items)
        if (page.items.length === 0 || offset + page.items.length >= page.count) break
      }
      return rows.filter((row) => row.source === 'balance_adjustment')
    },
  })
}

const CHECK_POLL_MS = 2000

/** The endpoint refuses more than 200 ids; chunked, not truncated, since the rest are still waiting. */
const CHECK_BATCH = 100

function chunked(ids: readonly Uuid[], size: number): Uuid[][] {
  const out: Uuid[][] = []
  for (let at = 0; at < ids.length; at += size) out.push(ids.slice(at, at + size))
  return out
}

export interface CategoryChecks {
  pending: ReadonlySet<Uuid>
  start: (ids: readonly Uuid[]) => void
}

/**
 * Watch the category checks these rows are waiting on, patching each result
 * into its own row; nothing here invalidates the register. A row the server
 * says is busy joins the pending set whoever started it, and leaves it only on
 * its own result or when the server no longer returns it (deleted mid-batch).
 */
export function useCategoryChecks(rows: readonly Transaction[]): CategoryChecks {
  const client = useQueryClient()
  const [pending, dispatch] = useReducer(checkReducer, NO_CHECKS)

  const busy = useMemo(() => checkingIds(rows), [rows])
  useEffect(() => {
    if (busy.length > 0) dispatch({ kind: 'checking', ids: busy })
  }, [busy])

  useEffect(() => {
    if (pending.size === 0) return
    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const poll = async () => {
      const ids = [...pending]
      try {
        const pages = await Promise.all(
          chunked(ids, CHECK_BATCH).map((batch) => readCategoryChecks(batch)),
        )
        if (stopped) return
        const settled: Uuid[] = []
        const seen = new Set<Uuid>()
        for (const row of pages.flatMap((page) => page.rows)) {
          seen.add(row.transaction_id)
          if (row.checking) continue
          settled.push(row.transaction_id)
          patchRegisterCache(client, row.transaction_id, {
            category_id: row.category_id,
            category_checked_at: row.category_checked_at,
            category_check_note: row.category_check_note,
            category_check_run_id: row.category_check_run_id,
            suggestion: row.suggestion,
            checking_category: false,
          })
        }
        for (const id of ids) if (!seen.has(id)) settled.push(id)
        if (settled.length > 0) dispatch({ kind: 'settled', ids: settled })
      } catch {
        // A failed poll says nothing about the runs; the next tick asks again.
      }
      if (!stopped) timer = setTimeout(() => void poll(), CHECK_POLL_MS)
    }

    timer = setTimeout(() => void poll(), CHECK_POLL_MS)
    return () => {
      stopped = true
      if (timer !== undefined) clearTimeout(timer)
    }
  }, [client, pending])

  const start = useCallback((ids: readonly Uuid[]) => dispatch({ kind: 'checking', ids }), [])
  return { pending, start }
}

/**
 * The Spending and Income tabs' figures. Takes the register's own query so both
 * are narrowed by the same window and filter (trap 5).
 */
export function useTransactionAggregate(
  query: RegisterQuery,
  direction: Direction,
  groupBy: GroupBy,
  under: Uuid | null,
  enabled = true,
) {
  return useQuery({
    queryKey: aggregateKey(query, direction, groupBy, under),
    enabled,
    queryFn: ({ signal }) => readAggregate(query, direction, groupBy, under, signal),
  })
}

export function useAccounts() {
  return useQuery({ queryKey: ACCOUNTS_KEY, queryFn: ({ signal }) => listAccounts(signal) })
}

/** `accountNamer` over the space's accounts. */
export function useAccountName(): (id: string) => string {
  const accounts = useAccounts()
  return useMemo(() => accountNamer(accounts.data), [accounts.data])
}

export function useCategories() {
  return useQuery({ queryKey: CATEGORIES_KEY, queryFn: ({ signal }) => listCategories(signal) })
}

export function usePayees(enabled = true) {
  return useQuery({ queryKey: PAYEES_KEY, enabled, queryFn: ({ signal }) => listPayees(signal) })
}

export function useTags() {
  return useQuery({ queryKey: TAGS_KEY, queryFn: ({ signal }) => listTags(signal) })
}

/** One row by its id: a second copy, not the register pages' cache entry. */
export function useTransaction(id: Uuid | null) {
  return useQuery({
    queryKey: transactionKey(id),
    enabled: id !== null,
    queryFn: ({ signal }) => readTransaction(id ?? '', signal),
  })
}

export function useAccountSummary(accountId: Uuid | null, query: RegisterQuery) {
  return useQuery({
    queryKey: accountSummaryKey(accountId, query.from, query.to, query.dateField),
    enabled: accountId !== null,
    queryFn: ({ signal }) => readAccountSummary(accountId ?? '', query, signal),
  })
}

export interface AdHocFilter {
  filterId: Uuid | null
  pending: boolean
  /** The register stays unloaded on error rather than falling back to no filter,
   *  which would show every row and the wrong total. */
  error: unknown
}

const AD_HOC_FILTER_REFRESH = 6 * 60 * 60 * 1000

/**
 * The panel's and search box's items as a stored filter, since the list endpoint
 * takes only a `filter_id`. One row per item signature, never rewritten in place,
 * so a rewrite cannot repoint an id a page still holds. The server prunes these a
 * day after they are written, so a page left open takes a fresh one well before.
 */
export function useAdHocFilter(items: readonly FilterItemWrite[], queryText: string): AdHocFilter {
  const signature = JSON.stringify(items)

  const query = useQuery({
    queryKey: adHocFilterKey(signature),
    enabled: items.length > 0,
    staleTime: AD_HOC_FILTER_REFRESH,
    refetchInterval: AD_HOC_FILTER_REFRESH,
    refetchIntervalInBackground: true,
    queryFn: async () => {
      const body: FilterWrite = {
        name: null,
        scope: 'ad_hoc',
        query_text: queryText || null,
        items: [...items],
      }
      const created = await createFilter(body)
      return created.id
    },
  })

  if (items.length === 0) return { filterId: null, pending: false, error: null }
  return {
    filterId: query.data ?? null,
    pending: query.data === undefined,
    error: query.error,
  }
}

/** Not imported from the dashboard client, which would pull goals, watchlists and the plan in. */
const DASHBOARD_ROOT: QueryKey = ['dashboard']

export interface EditVariables {
  id: Uuid
  optimistic: Partial<Transaction>
  /** Passed through `buildTransactionUpdate`, so it cannot carry `statement_name`. */
  patch: Record<string, unknown>
}

export function useEditTransaction() {
  return useMutation(editTransactionOptions(useQueryClient()))
}

export function editTransactionOptions(client: QueryClient) {
  return mutationOptions({
    mutationFn: ({ id, patch }: EditVariables) =>
      updateTransaction(id, buildTransactionUpdate(patch)),
    // The server discards a waiting suggestion once the row is given a category
    // by hand, so a category edit settles like a decided suggestion.
    onMutate: ({ id, patch, optimistic }) => {
      const snapshot = snapshotRegister(client)
      patchRegisterCache(
        client,
        id,
        'category_id' in patch ? { ...optimistic, suggestion: null } : optimistic,
      )
      return { snapshot }
    },
    onError: (_error, _variables, context) => {
      if (context) restoreRegister(client, context.snapshot)
    },
    onSuccess: (row) => patchRegisterCache(client, row.id, row),
    onSettled: (_row, _error, { id, patch }) => {
      if ('category_id' in patch) invalidateDecision(client, id)
      else invalidateTransactions(client)
    },
  })
}

export function useSetReviewed() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, reviewed }: { id: Uuid; reviewed: boolean }) => setReviewed(id, reviewed),
    onMutate: ({ id, reviewed }) => {
      const snapshot = snapshotRegister(client)
      patchRegisterCache(client, id, { is_reviewed: reviewed })
      return { snapshot }
    },
    onError: (_error, _variables, context) => {
      if (context) restoreRegister(client, context.snapshot)
    },
    onSettled: () => invalidateTransactions(client),
  })
}

/** Not optimistic: the server picks the slot and can refuse one another charge already pays. */
export function useLinkSeries() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    ({ id, seriesId, dueOn }: { id: Uuid; seriesId: Uuid; dueOn?: string }) =>
      linkSeries(id, seriesId, dueOn),
    [TRANSACTIONS_KEY],
    {
      onSuccess: (row) => patchRegisterCache(client, row.id, row),
    },
  )
}

export function useUnlinkSeries() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    (id: Uuid) => unlinkSeries(id),
    [TRANSACTIONS_KEY],
    {
      onSuccess: (row) => patchRegisterCache(client, row.id, row),
    },
  )
}

export function useSetTags() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, tagIds }: { id: Uuid; tagIds: Uuid[] }) => setTags(id, tagIds),
    onMutate: ({ id, tagIds }) => {
      const snapshot = snapshotRegister(client)
      patchRegisterCache(client, id, { tag_ids: tagIds })
      return { snapshot }
    },
    onError: (_error, _variables, context) => {
      if (context) restoreRegister(client, context.snapshot)
    },
    onSettled: () => invalidateTransactions(client),
  })
}

export function useCreateTransaction() {
  return useInvalidatingMutation(
    (body: TransactionCreate) => createTransaction(body),
    [TRANSACTIONS_KEY],
  )
}

export function useDeleteTransaction() {
  const client = useQueryClient()
  return useMutation({
    meta: { failure: 'That transaction was not deleted' },
    mutationFn: (id: Uuid) => deleteTransaction(id),
    onMutate: (id) => {
      const snapshot = snapshotRegister(client)
      removeFromRegisterCache(client, id)
      return { snapshot }
    },
    onError: (_error, _id, context) => {
      if (context) restoreRegister(client, context.snapshot)
    },
    onSettled: () => invalidateTransactions(client),
  })
}

/**
 * Every key a decided suggestion can have changed, named rather than a blanket
 * invalidate, which holds the swiped row until a whole register page returns.
 */
export function decidedSuggestionKeys(id: Uuid): QueryKey[] {
  return [
    ...TRANSACTIONS_CHANGED_KEYS,
    ['transaction', id],
    PENDING_KEY,
    DASHBOARD_ROOT,
  ]
}

function invalidateDecision(client: QueryClient, id: Uuid): void {
  for (const key of decidedSuggestionKeys(id)) void client.invalidateQueries({ queryKey: key })
}

export interface DecideVariables {
  txn: Transaction
  /** Beside the row rather than read off it, so the type proves there is one. */
  suggestion: Suggestion
}

export interface ApplyVariables extends DecideVariables {
  categoryId?: Uuid | null
  splitOverrides?: { index: number; category_id: string }[]
}

/** Applies through the assistant's own endpoint; the row is drawn optimistically by `appliedSuggestion`. */
export function useApplySuggestion() {
  const client = useQueryClient()
  return useMutation({
    // Omitted `categoryId` keeps the model's choice; `null` chooses no category
    // and travels as `""`, a different instruction.
    mutationFn: ({ suggestion, categoryId, splitOverrides }: ApplyVariables) =>
      applyAssistantAction(
        suggestion.action_id,
        categoryId === undefined && !splitOverrides
          ? undefined
          : {
              ...(categoryId !== undefined ? { category_id: categoryId ?? '' } : {}),
              ...(splitOverrides ? { split_categories: splitOverrides } : {}),
            },
      ),
    onMutate: ({ txn, categoryId }) => {
      const snapshot = snapshotRegister(client)
      patchRegisterCache(client, txn.id, appliedSuggestion(txn, categoryId))
      return { snapshot }
    },
    onError: (_error, _variables, context) => {
      if (context) restoreRegister(client, context.snapshot)
    },
    onSettled: (_row, _error, { txn }) => invalidateDecision(client, txn.id),
  })
}

export function useDiscardSuggestion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ suggestion }: DecideVariables) => discardAssistantAction(suggestion.action_id),
    // Only the proposal goes: the row stays unreviewed and filed where it was.
    onMutate: ({ txn }) => {
      const snapshot = snapshotRegister(client)
      patchRegisterCache(client, txn.id, { suggestion: null })
      return { snapshot }
    },
    onError: (_error, _variables, context) => {
      if (context) restoreRegister(client, context.snapshot)
    },
    onSettled: (_row, _error, { txn }) => invalidateDecision(client, txn.id),
  })
}

/** The review queue's primary button: the whole query, not the loaded page. */
export function useBulkTag() {
  return useInvalidatingMutation(
    ({ ids, add, remove }: { ids: readonly Uuid[]; add?: readonly Uuid[]; remove?: readonly Uuid[] }) =>
      bulkTag(ids, { add, remove }),
    [TRANSACTIONS_KEY],
    { failure: 'Those rows were not tagged' },
  )
}

export function useMarkAllReviewed() {
  return useInvalidatingMutation(
    ({ query, reviewed }: { query: RegisterQuery; reviewed: boolean }) =>
      markAllReviewed(query, reviewed),
    [TRANSACTIONS_KEY],
    { failure: 'Those rows were not marked reviewed' },
  )
}

export { describeApiError, type FailureContext } from '@/lib/errors'
