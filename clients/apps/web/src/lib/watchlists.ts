/**
 * Spending watchlists (not securities); shapes mirror
 * `backend/internal/domain/watchlists.go`, figures are calculations.md §7.
 * The trailing average uses full months only, taken from the same series the
 * bars render, and a month with no matching rows counts as zero spend.
 */

import { api, type MoneyShape } from './api'
import type { WireRate } from './clients/entities'
import { formatPercent, parseRate, formatMonthKey } from './format'
import {
  ZERO_MONEY,
  amountToWire,
  divideMoney,
  optionalAmountWire,
  sumMoney,
  type Money,
} from './money'
import { fromFilterItems } from './reports/savedFilter'
import { useInvalidatingMutation } from './queryClient'
import { TRANSACTIONS_KEY } from './transactions/cache'
import {
  EMPTY_DRAFT,
  toFilterItems,
  type FilterDraft,
  type FilterUniverse,
} from './transactions/filter'
import type { FilterItemWrite, FilterRead } from './transactions/types'

export const WATCHLISTS_KEY = ['watchlists']

/** Under the list's key, so a write that refreshes the list refreshes the detail too. */
export function watchlistDetailKey(id: string, month: string) {
  return [...WATCHLISTS_KEY, id, month]
}

export interface MonthSpend {
  /** `YYYY-MM`. */
  month: string
  /** Positive, net of refunds. */
  spent: Money
  /** The current month; no average may use it. */
  is_partial: boolean
}

/** Free text; `month` unless the user said otherwise. */
export type WatchlistPeriod = string

/**
 * The periods the server accepts, less `custom`, which the create body
 * carries no dates for.
 */
export const WATCHLIST_PERIODS: readonly { value: string; label: string }[] = [
  { value: 'month', label: 'Monthly' },
  { value: 'quarter', label: 'Quarterly' },
  { value: 'year', label: 'Yearly' },
]

/** Stored rows may say `monthly` where the sheet says `month`; unknown values print as stored. */
export function periodLabel(period: WatchlistPeriod): string {
  const known = WATCHLIST_PERIODS.find(
    (one) => one.value === period || `${one.value}ly` === period || one.label.toLowerCase() === period,
  )
  return known?.label ?? period
}

export interface WatchlistSummary {
  id: string
  name: string
  emoji: string | null
  filter_id: string
  period: WatchlistPeriod
  this_month_spent: Money
  /** Run rate: spend so far this month, continued to month end. */
  month_projection: Money
  /** January 1 to the end of this month. */
  year_to_date: Money
  /** Oldest first, ending with the current partial month. */
  monthly_trend: MonthSpend[]
  /** `null` when the user set none. */
  target_amount: Money | null
  /** Negative once the target is breached; `null` with no target to breach. */
  left_to_target: Money | null
  /** 0–100. `null` with no target, and with a target of zero. */
  pct_of_target: WireRate
  is_over_target: boolean
  is_projected_over_target: boolean
  as_of: string
}

export type BreakdownDimension = 'category' | 'payee' | 'tag'

export interface BreakdownRow {
  /** A category or tag id, or the payee's name; empty for the uncategorized or untagged slice. */
  key: string
  label: string
  spent: Money
  /**
   * A **fraction** (0.5 is half), or `null` with no spend. Tag shares can pass
   * 100%: a two-tag transaction counts in full under each.
   */
  share: WireRate
}

export interface WatchlistDetail extends WatchlistSummary {
  /** The month the breakdown and the transaction table cover. */
  month: string
  /** Not the card's `this_month_spent`. */
  spent: Money
  by_category: BreakdownRow[]
  by_payee: BreakdownRow[]
  by_tag: BreakdownRow[]
}

const SUMMARY_SHAPE: MoneyShape<WatchlistSummary> = {
  this_month_spent: 'money',
  month_projection: 'money',
  year_to_date: 'money',
  target_amount: 'money',
  left_to_target: 'money',
  monthly_trend: { spent: 'money' },
}

export const DETAIL_SHAPE: MoneyShape<WatchlistDetail> = {
  ...SUMMARY_SHAPE,
  spent: 'money',
  by_category: { spent: 'money' },
  by_payee: { spent: 'money' },
  by_tag: { spent: 'money' },
}

export function breakdownFor(
  detail: WatchlistDetail,
  dimension: BreakdownDimension,
): BreakdownRow[] {
  if (dimension === 'payee') return detail.by_payee
  if (dimension === 'tag') return detail.by_tag
  return detail.by_category
}

/** The wire carries a fraction, so multiply before rounding. */
export function sharePercent(row: BreakdownRow): string {
  return formatPercent(parseRate(row.share), { digits: 0 })
}

/** Capped at 100; the caption beside it is not. */
export function targetBarPct(watchlist: WatchlistSummary): number {
  const pct = parseRate(watchlist.pct_of_target)
  if (pct === null) return 0
  return Math.max(0, Math.min(100, pct))
}

export function fullMonths(trend: readonly MonthSpend[]): MonthSpend[] {
  return trend.filter((month) => !month.is_partial)
}

/**
 * The trailing monthly average over full months only. With fewer bars than
 * `months`, the divisor is what there is.
 */
export function averageOfFullMonths(trend: readonly MonthSpend[], months = 12): Money {
  const window = fullMonths(trend).slice(-months)
  if (window.length === 0) return ZERO_MONEY
  return divideMoney(sumMoney(window.map((month) => month.spent)), window.length)
}

export function averageWindowLabel(trend: readonly MonthSpend[], months = 12): string {
  const window = fullMonths(trend).slice(-months)
  const first = window.at(0)
  const last = window.at(-1)
  if (!first || !last) return 'No full months yet'
  if (first.month === last.month) return `${formatMonthKey(first.month, 'month')} only`
  return `${window.length} full months, ${formatMonthKey(first.month, 'month')}–${formatMonthKey(last.month, 'month')}`
}

/**
 * Exactly one selection: filter items or a saved report's `filter_id`. The
 * server refuses neither being set, since an empty filter matches everything.
 */
export interface WatchlistBody {
  name: string
  emoji: string | null
  target_amount: string | null
  period: string
  items?: FilterItemWrite[]
  filter_id?: string
}

/** Both selections are kept while the sheet is open; only the one `source` names is sent. */
export interface WatchlistForm {
  name: string
  emoji: string
  target: string
  period: string
  source: 'filter' | 'report'
  filter: FilterDraft
  reportFilterId: string
}

export function blankWatchlistForm(): WatchlistForm {
  return {
    name: '',
    emoji: '',
    target: '',
    period: 'month',
    source: 'filter',
    filter: EMPTY_DRAFT,
    reportFilterId: '',
  }
}

/** A filter scoped to the watchlist comes back as facets; any other as a saved report. */
export function watchlistFormFrom(
  watchlist: WatchlistSummary,
  stored: FilterRead,
  universe: FilterUniverse,
): WatchlistForm {
  const own = stored.scope === 'watchlist'
  return {
    name: watchlist.name,
    emoji: watchlist.emoji ?? '',
    target: watchlist.target_amount === null ? '' : amountToWire(watchlist.target_amount),
    period: watchlist.period,
    source: own ? 'filter' : 'report',
    filter: own ? fromFilterItems(stored.items, universe) : EMPTY_DRAFT,
    reportFilterId: own ? '' : stored.id,
  }
}

/**
 * The body, or `null` without a name and a selection, or with a target that
 * is not an amount. Blank emoji and target go as `null` so a deleted value
 * actually clears.
 */
export function buildWatchlistBody(
  form: WatchlistForm,
  universe: FilterUniverse,
): WatchlistBody | null {
  const name = form.name.trim()
  if (name === '') return null
  const target = optionalAmountWire(form.target)
  if (target === undefined) return null
  const emoji = form.emoji.trim()
  const common = {
    name,
    emoji: emoji === '' ? null : emoji,
    target_amount: target,
    period: form.period,
  }
  if (form.source === 'report') {
    return form.reportFilterId === '' ? null : { ...common, filter_id: form.reportFilterId }
  }
  const items = toFilterItems(form.filter, universe)
  return items.length === 0 ? null : { ...common, items }
}

export const watchlistsApi = {
  list: (signal?: AbortSignal) => api.get<WatchlistSummary[]>('/watchlists', SUMMARY_SHAPE, signal),

  detail: (id: string, month: string, signal?: AbortSignal) =>
    api.get<WatchlistDetail>(`/watchlists/${id}?month=${month}`, DETAIL_SHAPE, signal),

  create: (body: WatchlistBody) => api.post<WatchlistSummary>('/watchlists', body, SUMMARY_SHAPE),

  update: ({ id, body }: { id: string; body: WatchlistBody }) =>
    api.patch<WatchlistSummary>(`/watchlists/${id}`, body, SUMMARY_SHAPE),

  filter: (filterId: string, signal?: AbortSignal) =>
    api.get<FilterRead>(`/filters/${filterId}`, undefined, signal),

  remove: (id: string) => api.delete<void>(`/watchlists/${id}`),
}

export function useCreateWatchlist() {
  return useInvalidatingMutation(watchlistsApi.create, [WATCHLISTS_KEY])
}

/** An edit can rewrite the filter's items under the same id, so transactions refresh too. */
export function useUpdateWatchlist() {
  return useInvalidatingMutation(watchlistsApi.update, [WATCHLISTS_KEY, TRANSACTIONS_KEY])
}

export function useDeleteWatchlist() {
  return useInvalidatingMutation(watchlistsApi.remove, [WATCHLISTS_KEY], {
    failure: 'That watchlist was not deleted',
  })
}
