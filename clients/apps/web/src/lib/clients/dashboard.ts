/**
 * The dashboard's reads. There is deliberately no `/dashboard` resource: each
 * widget queries the same endpoint its own page does, so a card and its page
 * cannot drift and a switched-off widget costs nothing.
 */

import { useState } from 'react'
import { useQueries, useQuery } from '@tanstack/react-query'

import { api, type MoneyShape } from '@/lib/api'
import { dayWindow } from '@/lib/dateRanges'
import { toIsoDate, monthKey } from '@/lib/format'
import { spendingPlanApi, spendingPlanKeys } from '@/lib/spendingPlan'
import { listTransactions, readAggregate, type RegisterQuery } from '@/lib/transactions/api'
import { REVIEW_TILES_ROOT } from '@/lib/transactions/cache'
import type { Uuid } from '@/lib/transactions/types'
import { watchlistsApi, WATCHLISTS_KEY } from '@/lib/watchlists'

import { queryString, type IsoDate } from './entities'
import type { ReportResult } from './reports'

export const dashboardKeys = {
  all: ['dashboard'] as const,
  monthly: (sign: string, from: string, to: string) =>
    ['dashboard', 'monthly', sign, from, to] as const,
  topCategories: (from: string, to: string) => ['dashboard', 'top-categories', from, to] as const,
  recentSpend: (from: string, to: string, accountIds: readonly string[] | null) =>
    ['dashboard', 'recent-spend', from, to, accountIds] as const,
  recent: (limit: number, today: string, accountIds: readonly string[] | null) =>
    ['dashboard', 'recent', limit, today, accountIds] as const,
  review: (filterId: string | null, from: string | null, to: string) =>
    [...REVIEW_TILES_ROOT, filterId ?? 'all', from ?? 'all-time', to] as const,
}

const REPORT_SHAPE: MoneyShape<ReportResult> = {
  totals: { income: 'money', expenses: 'money', net: 'money' },
  summary: { rows: { cells: 'money', total: 'money' }, column_totals: 'money', total: 'money' },
}

/** From the first of the month `months - 1` back, to today. */
function trailingMonths(months: number, today: Date = new Date()): {
  from: IsoDate
  to: IsoDate
} {
  const to = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  const from = new Date(to.getFullYear(), to.getMonth() - (months - 1), 1)
  return { from: toIsoDate(from), to: toIsoDate(to) }
}

function reportPath(params: Record<string, string | undefined>): string {
  return `/reports/run${queryString({ ...params, date_field: 'effective' })}`
}

/** Read off `column_totals`: the months are the columns. */
export function useMonthlyTotals(direction: 'income' | 'spending', months = 6) {
  const bounds = trailingMonths(months)
  const sign = direction === 'income' ? 'income' : 'expenses'
  return useQuery({
    queryKey: dashboardKeys.monthly(sign, bounds.from, bounds.to),
    queryFn: ({ signal }) =>
      api.get<ReportResult>(
        reportPath({
          from: bounds.from,
          to: bounds.to,
          mode: 'summary',
          rows: 'category',
          columns: 'time',
          time_grain: 'month',
          sign,
        }),
        REPORT_SHAPE,
        signal,
      ),
  })
}

export function useTopCategories() {
  const month = monthKey()
  const bounds = { from: `${month}-01`, to: toIsoDate(new Date()) }
  return useQuery({
    queryKey: dashboardKeys.topCategories(bounds.from, bounds.to),
    queryFn: ({ signal }) =>
      api.get<ReportResult>(
        reportPath({
          from: bounds.from,
          to: bounds.to,
          mode: 'summary',
          rows: 'category',
          columns: 'time',
          time_grain: 'month',
          sign: 'expenses',
        }),
        REPORT_SHAPE,
        signal,
      ),
  })
}

/**
 * The window is frozen at first run so it cannot roll over at midnight under
 * its caption. `/transactions/aggregate` rather than the report engine, which
 * has no account filter, so the figure covers the same accounts as the rows
 * beneath it (trap 5).
 */
export function useRecentSpend(accountIds: readonly Uuid[] | null, days = 7, enabled = true) {
  const [bounds] = useState(() => dayWindow(days, 0))
  const query = useQuery({
    queryKey: dashboardKeys.recentSpend(bounds.from, bounds.to, accountIds),
    enabled,
    queryFn: ({ signal }) =>
      readAggregate(
        { ...RECENT_QUERY, accountIds, from: bounds.from, to: bounds.to, dateField: 'effective' },
        'spending',
        'none',
        null,
        signal,
      ),
  })
  return { ...query, bounds }
}

const RECENT_QUERY: RegisterQuery = {
  accountIds: null,
  from: null,
  to: null,
  dateField: 'posted',
  filterId: null,
  reviewed: null,
  // As the register shows them: a tile of padding rows would crowd out the
  // purchases they pad.
  padding: 'hide',
  order: 'desc',
  limit: 5,
  offset: 0,
}

/**
 * `to` is capped at today: newest-first over an open end would lead with
 * future-dated (projected) rows. The start stays open. Pending rows dated
 * today are still included.
 */
export function recentTransactionsQuery(
  limit: number,
  today: string,
  accountIds: readonly Uuid[] | null = null,
): RegisterQuery {
  return { ...RECENT_QUERY, to: today, limit, accountIds }
}

export function useRecentTransactions(
  accountIds: readonly Uuid[] | null,
  limit = 5,
  enabled = true,
) {
  // Frozen at first run so the cap cannot roll over at midnight under a cached list.
  const [today] = useState(() => toIsoDate(new Date()))
  return useQuery({
    queryKey: dashboardKeys.recent(limit, today, accountIds),
    enabled,
    queryFn: ({ signal }) =>
      listTransactions(recentTransactionsQuery(limit, today, accountIds), signal),
  })
}

/** `limit: 1` on purpose: `count` and `total` describe the whole match set. */
export function useReviewTile(filterId: string | null, days = 30, enabled = true) {
  const bounds = dayWindow(days, 0)
  return useQuery({
    queryKey: dashboardKeys.review(filterId, bounds.from, bounds.to),
    enabled,
    queryFn: ({ signal }) =>
      listTransactions({
        ...RECENT_QUERY,
        from: bounds.from,
        to: bounds.to,
        reviewed: false,
        filterId,
        limit: 1,
      }, signal),
  })
}

/**
 * The rows a filter keeps over all time, marked or not: the window the
 * register's "All time" asks for, so the count and the list it links to agree
 * (trap 5).
 */
export function useAllTimeTile(filterId: string | null, enabled = true) {
  const [today] = useState(() => toIsoDate(new Date()))
  return useQuery({
    queryKey: dashboardKeys.review(filterId, null, today),
    enabled,
    queryFn: ({ signal }) =>
      listTransactions({ ...RECENT_QUERY, to: today, filterId, limit: 1 }, signal),
  })
}

export function useSpendingPlanMonth() {
  const month = monthKey()
  return useQuery({
    queryKey: spendingPlanKeys.month(month),
    queryFn: ({ signal }) => spendingPlanApi.month(month, signal),
  })
}

/**
 * The plan's own per-month endpoint, not a report: the plan counts by
 * `counts_toward_spending_plan`, a different predicate from reports'
 * (calculations.md §2). Shares the Spending Plan page's cache key.
 */
export function useSpendingPlanMonths(months: readonly string[]) {
  return useQueries({
    queries: months.map((month) => ({
      queryKey: spendingPlanKeys.month(month),
      queryFn: ({ signal }: { signal: AbortSignal }) => spendingPlanApi.month(month, signal),
    })),
  })
}


export function useWatchlists() {
  return useQuery({
    queryKey: WATCHLISTS_KEY,
    queryFn: ({ signal }) => watchlistsApi.list(signal),
  })
}
