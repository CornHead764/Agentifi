/**
 * Investments — `/holdings`, `/securities` and `/performance`.
 *
 * `Money | null` means unknown, never zero, and renders as a dash: treating an
 * unknown cost basis as zero reports the position as pure profit. The headline
 * TWR/IRR cannot be derived from the per-account lines, so `/performance` is
 * asked for the selection and once per account.
 */

import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, type MoneyShape } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { ZERO_MONEY, addMoney, optionalAmountWire, type Money } from '@/lib/money'

import { windowFor, type RangePreset } from '@/lib/dateRanges'

import { idsKey, queryString, type EchoedWindow, type Granularity, type IsoDate, type WireRate } from './entities'

const investmentKeys = {
  holdings: ['holdings'] as const,
  portfolio: (accountIds: readonly string[] | null) => ['holdings', idsKey(accountIds)] as const,
  news: ['investment-news'] as const,
  securities: ['securities'] as const,
  performance: (from: string | null, to: string, granularity: string, accounts: string) =>
    ['performance', from, to, granularity, accounts] as const,
  security: ['security'] as const,
  securityDetail: (securityId: string | null, from: string | null, to: string) =>
    ['security', securityId, from, to] as const,
  activity: (from: string | null, to: string, accountIds: readonly string[] | null) =>
    ['investment-activity', from, to, idsKey(accountIds)] as const,
}

/** Every figure that can be unknown is nullable. */
export interface Holding {
  id: string
  account_id: string
  security_id: string
  symbol: string
  name: string
  shares: WireRate
  /** Null for a security with no quote — see `is_unquoted`. */
  price: WireRate
  /** The security's own currency, not the space's; empty when never recorded. */
  currency: string
  market_value: Money
  /** The market value is the provider's figure with no price behind it (e.g. an employer plan's fund). */
  is_unquoted: boolean
  cost_basis: Money | null
  total_gain: Money | null
  total_gain_pct: WireRate
  /** False when the basis, the gain and the gain percentage are all null. */
  is_cost_basis_complete: boolean
  day_change: Money | null
  day_change_pct: WireRate
  /** Fraction of the portfolio's market value; null for a portfolio worth nothing. */
  share: WireRate
}

/**
 * `cost_basis` and `total_gain` cover only holdings whose basis is known, so
 * they are deliberately not `market_value − cost_basis`.
 */
export interface PortfolioTotals {
  market_value: Money
  cost_basis: Money | null
  total_gain: Money | null
  day_change: Money | null
  day_change_pct: WireRate
  is_cost_basis_incomplete: boolean
  is_day_change_incomplete: boolean
  /** Balance of investment accounts with no positions on file; included in `total_value`. */
  account_balance_not_held: Money
  total_value: Money
}

export interface AllocationSlice {
  security_id: string
  symbol: string
  share: WireRate
  value: Money
}

/** Group on `key`, not `label`: two accounts can share a name. */
export interface AllocationGroup {
  key: string
  label: string
  share: WireRate
  value: Money
}

export interface Portfolio {
  items: Holding[]
  totals: PortfolioTotals
  /** Empty for a portfolio worth nothing. */
  allocation: AllocationSlice[]
  allocation_by_class: AllocationGroup[]
  allocation_by_account: AllocationGroup[]
}

const PORTFOLIO_SHAPE: MoneyShape<Portfolio> = {
  items: {
    market_value: 'money',
    cost_basis: 'money',
    total_gain: 'money',
    day_change: 'money',
  },
  totals: {
    market_value: 'money',
    cost_basis: 'money',
    total_gain: 'money',
    day_change: 'money',
    account_balance_not_held: 'money',
    total_value: 'money',
  },
  allocation: { value: 'money' },
  allocation_by_class: { value: 'money' },
  allocation_by_account: { value: 'money' },
}

export interface Security {
  id: string
  symbol: string
  name: string
  kind: string
  exchange: string | null
  currency: string
  /** Null before the first quote; `prior_close` null before the first session. */
  last_price: WireRate
  prior_close: WireRate
  last_price_at: string | null
}

export interface PerformancePoint {
  on: IsoDate
  value: Money
  /** Rebased to the window start, null where the start was zero. */
  return_pct: WireRate
}

/** TWR and IRR arrive together (`calculations.md` §10), so the toggle never refetches. */
export interface Performance {
  window: EchoedWindow
  granularity: string
  account_ids: string[]
  points: PerformancePoint[]
  twr: WireRate
  irr: WireRate
  twr_pct: WireRate
  irr_pct: WireRate
  start_value: Money
  end_value: Money
  net_flows: Money
}

const PERFORMANCE_SHAPE: MoneyShape<Performance> = {
  points: { value: 'money' },
  start_value: 'money',
  end_value: 'money',
  net_flows: 'money',
}

export type PerformanceMetric = 'twr' | 'irr'

/**
 * `market_value` is required only for a symbol with no price on file: the
 * valuer refuses a holding with neither rather than show it as worthless.
 */
export interface HoldingDraft {
  account_id: string
  symbol: string
  name: string
  shares: string
  cost_basis: string | null
  market_value: string | null
}

/** The new-holding form as typed. `needsValue` is a symbol with no price on file. */
export interface HoldingFields {
  accountId: string
  symbol: string
  name: string
  shares: string
  costBasis: string
  marketValue: string
  needsValue: boolean
}

/**
 * The body, or `null` while a required field is blank or an amount is not an
 * amount. The share count goes as typed: it is not money.
 */
export function holdingDraftFrom(fields: HoldingFields): HoldingDraft | null {
  const symbol = fields.symbol.trim().toUpperCase()
  const shares = fields.shares.trim()
  const costBasis = optionalAmountWire(fields.costBasis)
  const marketValue = fields.needsValue ? optionalAmountWire(fields.marketValue) : null
  if (fields.accountId === '' || symbol === '' || shares === '') return null
  if (costBasis === undefined || marketValue === undefined) return null
  if (fields.needsValue && marketValue === null) return null
  return {
    account_id: fields.accountId,
    symbol,
    name: fields.name.trim(),
    shares,
    cost_basis: costBasis,
    market_value: marketValue,
  }
}

export function useAddHolding() {
  return useInvalidatingMutation(
    (draft: HoldingDraft) => api.post<unknown>('/holdings', draft),
    [investmentKeys.holdings],
    { failure: 'That holding was not added' },
  )
}

export function useRemoveHolding() {
  return useInvalidatingMutation(
    (id: string) => api.delete<void>(`/holdings/${id}`),
    [investmentKeys.holdings],
    { failure: 'That holding was not removed' },
  )
}

export function usePortfolio(accountIds: readonly string[] | null) {
  return useQuery({
    queryKey: investmentKeys.portfolio(accountIds),
    queryFn: ({ signal }) =>
      api.get<Portfolio>(
        `/holdings${queryString({}, { account_id: accountIds })}`,
        PORTFOLIO_SHAPE,
        signal,
      ),
  })
}

export interface NewsItem {
  id: string
  title: string
  publisher: string
  url: string
  thumbnail_url: string
  /** The held tickers this story was found through. */
  symbols: string[]
  published_at: string | null
}

export interface NewsFeed {
  items: NewsItem[]
  /** False when the deployment has no news source; the section is hidden. */
  available: boolean
  /** Set when a source exists but would not answer. */
  unavailable: string
  symbols: string[]
}

/** Refetches rarely: a cache miss on the server spends the news source's quota. */
export function useInvestmentNews() {
  return useQuery({
    queryKey: investmentKeys.news,
    queryFn: ({ signal }) => api.get<NewsFeed>('/news', undefined, signal),
    staleTime: 15 * 60 * 1000,
    refetchOnWindowFocus: false,
  })
}

export interface RefreshPricesResult {
  updated: number
  unpriced: string[]
}

export function useRefreshPrices() {
  const client = useQueryClient()
  return useMutation({
    meta: { failure: false },
    mutationFn: () => api.post<RefreshPricesResult>('/securities/refresh'),
    onSuccess: () => client.invalidateQueries(),
  })
}

export function useSecurities() {
  return useQuery({
    queryKey: investmentKeys.securities,
    queryFn: ({ signal }) => api.get<Security[]>('/securities', undefined, signal),
  })
}

function performancePath(
  from: IsoDate | null,
  to: IsoDate,
  granularity: Granularity,
  accountIds: readonly string[] | null,
): string {
  return `/performance${queryString({ from, to, granularity }, { account_id: accountIds })}`
}

export function usePerformance(
  range: RangePreset,
  accountIds: readonly string[] | null,
  granularity: Granularity = 'auto',
) {
  const bounds = windowFor(range)
  return useQuery({
    queryKey: investmentKeys.performance(bounds.from, bounds.to, granularity, idsKey(accountIds)),
    queryFn: ({ signal }) =>
      api.get<Performance>(
        performancePath(bounds.from, bounds.to, granularity, accountIds),
        PERFORMANCE_SHAPE,
        signal,
      ),
  })
}

/** The window is resolved once so every account's line shares it. */
export function useAccountPerformance(
  range: RangePreset,
  accountIds: readonly string[],
  granularity: Granularity = 'auto',
) {
  const bounds = windowFor(range)
  return useQueries({
    queries: accountIds.map((id) => ({
      queryKey: investmentKeys.performance(bounds.from, bounds.to, granularity, id),
      queryFn: ({ signal }) =>
        api.get<Performance>(
          performancePath(bounds.from, bounds.to, granularity, [id]),
          PERFORMANCE_SHAPE,
          signal,
        ),
    })),
  })
}

/** Null when any holding has no prior close: a partly unknown day move is unknown. */
export function accountDayChange(
  holdings: readonly Holding[],
  accountId: string,
): Money | null {
  let total = ZERO_MONEY
  let seen = false
  for (const holding of holdings) {
    if (holding.account_id !== accountId) continue
    if (holding.day_change === null) return null
    total = addMoney(total, holding.day_change)
    seen = true
  }
  return seen ? total : null
}

/** One read so the header, positions and chart share one window. */
export interface SecurityDetail {
  security: Security
  window: EchoedWindow
  positions: Holding[]
  shares: WireRate
  market_value: Money
  cost_basis: Money | null
  total_gain: Money | null
  total_gain_pct: WireRate
  day_change: Money | null
  day_change_pct: WireRate
  is_cost_basis_incomplete: boolean
  /** Oldest first. Nothing fills the gaps. */
  prices: { on: IsoDate; close: WireRate }[]
  price_change: WireRate
  price_change_pct: WireRate
}

const SECURITY_DETAIL_SHAPE: MoneyShape<SecurityDetail> = {
  positions: {
    market_value: 'money',
    cost_basis: 'money',
    total_gain: 'money',
    day_change: 'money',
  },
  market_value: 'money',
  cost_basis: 'money',
  total_gain: 'money',
  day_change: 'money',
}

export function useSecurityDetail(securityId: string | null, range: RangePreset) {
  const bounds = windowFor(range)
  return useQuery({
    queryKey: investmentKeys.securityDetail(securityId, bounds.from, bounds.to),
    enabled: securityId !== null,
    queryFn: ({ signal }) =>
      api.get<SecurityDetail>(
        `/securities/${securityId}${queryString({ from: bounds.from, to: bounds.to })}`,
        SECURITY_DETAIL_SHAPE,
        signal,
      ),
  })
}

export interface BackfilledHistory {
  symbol: string
  stored: number
  from: IsoDate
  to: IsoDate
}

/** A mutation rather than a fetch on open: it spends the price source's rate limit. */
export function useBackfillHistory() {
  return useInvalidatingMutation(
    ({ securityId, range }: { securityId: string; range: RangePreset }) => {
      const bounds = windowFor(range)
      return api.post<BackfilledHistory>(
        `/securities/${securityId}/history${queryString({ from: bounds.from, to: bounds.to })}`,
      )
    },
    [investmentKeys.security],
    { failure: false },
  )
}

/**
 * Investment activity rows are ordinary transactions; the server derives the
 * kind from the bank's wording, not from the category.
 */
export type ActivityKind =
  | 'buy'
  | 'sell'
  | 'dividend'
  | 'reinvestment'
  | 'interest'
  | 'fee'
  | 'contribution'
  | 'withdrawal'
  | 'unknown'

export interface ActivityRow {
  transaction_id: string
  account_id: string
  on: IsoDate
  payee: string
  statement_name: string
  category_id: string | null
  amount: Money
  kind: ActivityKind
  is_pending: boolean
}

export interface ActivitySummary {
  kind: ActivityKind
  count: number
  total: Money
}

export interface InvestmentActivity {
  window: EchoedWindow
  account_ids: string[]
  items: ActivityRow[]
  summary: ActivitySummary[]
  /** Dividends, interest and reinvested distributions. Fees are positive. */
  income: Money
  fees: Money
}

const ACTIVITY_SHAPE: MoneyShape<InvestmentActivity> = {
  items: { amount: 'money' },
  summary: { total: 'money' },
  income: 'money',
  fees: 'money',
}

export function useInvestmentActivity(range: RangePreset, accountIds: readonly string[] | null) {
  const bounds = windowFor(range)
  return useQuery({
    queryKey: investmentKeys.activity(bounds.from, bounds.to, accountIds),
    queryFn: ({ signal }) =>
      api.get<InvestmentActivity>(
        `/investment-activity${queryString(
          { from: bounds.from, to: bounds.to },
          { account_id: accountIds },
        )}`,
        ACTIVITY_SHAPE,
        signal,
      ),
  })
}

/** The day-change denominator. */
export function accountMarketValue(holdings: readonly Holding[], accountId: string): Money {
  let total = ZERO_MONEY
  for (const holding of holdings) {
    if (holding.account_id === accountId) total = addMoney(total, holding.market_value)
  }
  return total
}
