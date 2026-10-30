/**
 * Reports: presets over one engine endpoint (`calculations.md` §11). Nothing
 * here re-groups or re-totals a figure the engine produced. Running a report is
 * a GET so a viewer may; a saved report is a `Filter` of scope `report`, not a
 * table of its own. Monthly Summary stays its own endpoint because it drops
 * bills and subscriptions from its top lists server side.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type MoneyShape } from '@/lib/api'
import type { Money } from '@/lib/money'
import type { FilterItemWrite, FilterRead, Uuid } from '@/lib/transactions/types'

import { queryString, type EchoedWindow, type IsoDate, type WireRate } from './entities'

/* ---- The shell ---------------------------------------------------------- */

export const reportKeys = {
  all: ['reports'] as const,
  presets: ['reports', 'presets'] as const,
  run: (path: string) => ['reports', 'run', path] as const,
  monthlySummary: (month: string) => ['reports', 'monthly-summary', month] as const,
  savings: (from: string | null, to: string | null) => ['reports', 'savings', from, to] as const,
  spending: (path: string) => ['reports', 'spending', path] as const,
  saved: ['reports', 'saved'] as const,
}

export type ReportMode = 'transaction' | 'summary'
export type RowDimension = 'category' | 'account' | 'tag' | 'payee' | 'txf'
export type ColumnDimension = 'time' | 'account' | 'tag' | 'payee'
export type TimeGrain = 'day' | 'week' | 'month' | 'quarter' | 'year'
/** Storage never flips a sign; this selects which side prints. */
export type ReportSign = 'both' | 'expenses' | 'income'

export interface ReportConfig {
  /** A label over the fields, never a branch. */
  preset: string
  mode: ReportMode
  rows: RowDimension
  columns: ColumnDimension
  time_grain: TimeGrain
  sign: ReportSign
}

export interface ReportPreset {
  preset: string
  label: string
  config: ReportConfig
  /** Null when `/reports/run` answers; otherwise the path that does. */
  served_by: string | null
}

export function useReportPresets() {
  return useQuery({
    queryKey: reportKeys.presets,
    queryFn: ({ signal }) => api.get<ReportPreset[]>('/reports/presets', undefined, signal),
    staleTime: Infinity,
  })
}

/* ---- Results ------------------------------------------------------------ */

export interface ReportTransactionRow {
  transaction_id: Uuid
  split_id: Uuid | null
  on: IsoDate
  payee: string
  account_id: Uuid
  category_id: Uuid | null
  amount: Money
  notes: string | null
}

export interface ReportNode {
  key: string
  label: string
  depth: number
  total: Money
  count: number
  children: ReportNode[]
  transactions: ReportTransactionRow[]
}

export interface ReportTransactionResult {
  groups: ReportNode[]
  total: Money
  count: number
}

export interface ReportColumn {
  key: string
  label: string
}

export interface ReportPivotRow {
  key: string
  label: string
  /** Income, Expenses, when the row dimension has a family. */
  section: string
  /** Runs parallel to `columns`. */
  cells: Money[]
  total: Money
}

export interface ReportSection {
  key: string
  label: string
  cells: Money[]
  total: Money
}

export interface ReportSummaryResult {
  row_dimension: string
  column_dimension: string
  columns: ReportColumn[]
  rows: ReportPivotRow[]
  /** Empty when every row shares one family. */
  sections: ReportSection[]
  /** For an Income & Expense report this is also the net line. */
  column_totals: Money[]
  total: Money
}

export interface ReportTotals {
  income: Money
  expenses: Money
  net: Money
  /** Null when nothing came in. */
  savings_rate: WireRate
  count: number
}

export interface ReportResult {
  window: EchoedWindow
  config: ReportConfig
  filter_id: Uuid | null
  totals: ReportTotals
  /** Exactly one is set, by `config.mode`. */
  transaction: ReportTransactionResult | null
  summary: ReportSummaryResult | null
}

// The getter defers the self-reference; the walk terminates at a leaf.
const NODE_SHAPE: MoneyShape<ReportNode> = {
  total: 'money',
  transactions: { amount: 'money' },
  get children(): MoneyShape<ReportNode> {
    return NODE_SHAPE
  },
}

const RESULT_SHAPE: MoneyShape<ReportResult> = {
  totals: { income: 'money', expenses: 'money', net: 'money' },
  transaction: { groups: NODE_SHAPE, total: 'money' },
  summary: {
    rows: { cells: 'money', total: 'money' },
    sections: { cells: 'money', total: 'money' },
    column_totals: 'money',
    total: 'money',
  },
}

/* ---- Running ------------------------------------------------------------ */

/**
 * The two renderings over the same data differ only in `mode`, so they cannot
 * disagree about a subtotal.
 */
export interface ReportQuery {
  from: IsoDate | null
  to: IsoDate
  config: ReportConfig
  filterId: Uuid | null
}

export function reportParams(query: ReportQuery): Record<string, string | undefined> {
  return {
    from: query.from ?? undefined,
    to: query.to,
    date_field: 'effective',
    // `columns` rides along in transaction mode too, where it is ignored, so
    // the two renderings differ in `mode` alone.
    mode: query.config.mode,
    rows: query.config.rows,
    columns: query.config.columns,
    time_grain: query.config.time_grain,
    sign: query.config.sign,
    filter_id: query.filterId ?? undefined,
  }
}

export function reportRunPath(query: ReportQuery): string {
  return `/reports/run${queryString(reportParams(query))}`
}

export function useReportRun(query: ReportQuery, enabled = true) {
  const path = reportRunPath(query)
  return useQuery({
    queryKey: reportKeys.run(path),
    enabled,
    queryFn: ({ signal }) => api.get<ReportResult>(path, RESULT_SHAPE, signal),
  })
}

/* ---- Monthly Summary ---------------------------------------------------- */

export interface MonthlySummaryEntry {
  key: string
  label: string
  total: Money
  /** Transactions, not splits. */
  count: number
  /** Null when the same row had nothing last month. */
  change_pct: WireRate
}

export interface MonthlySummary {
  month: string
  prior_month: string
  income: Money
  expenses: Money
  net: Money
  income_change_pct: WireRate
  expenses_change_pct: WireRate
  net_change_pct: WireRate
  /** Sums to `expenses` with `discretionary`. */
  bills: Money
  discretionary: Money
  /** Bills and subscriptions are already excluded server side; do not re-rank. */
  top_categories: MonthlySummaryEntry[]
  top_payees: MonthlySummaryEntry[]
}

const MONTHLY_SHAPE: MoneyShape<MonthlySummary> = {
  income: 'money',
  expenses: 'money',
  net: 'money',
  bills: 'money',
  discretionary: 'money',
  top_categories: { total: 'money' },
  top_payees: { total: 'money' },
}

/** `YYYY-MM`: per calendar month, not per window. */
export function monthlySummaryPath(month: string): string {
  return `/reports/monthly-summary${queryString({ month })}`
}

export function useMonthlySummary(month: string) {
  return useQuery({
    queryKey: reportKeys.monthlySummary(month),
    queryFn: ({ signal }) =>
      api.get<MonthlySummary>(monthlySummaryPath(month), MONTHLY_SHAPE, signal),
  })
}

/* ---- Savings ------------------------------------------------------------ */

/** Balances, not transactions: savings-account balance plus an end-of-month pivot. */
export interface SavingsReportData {
  granularity: string
  points: { on: string; balance: Money }[]
  end: Money
  change: Money
  change_pct: string | null
  /** `YYYY-MM` columns; `totals` is the Total row, parallel to them. */
  months: string[]
  accounts: { account_id: string; name: string; cells: Money[] }[]
  totals: Money[]
}

const SAVINGS_SHAPE: MoneyShape<SavingsReportData> = {
  points: { balance: 'money' },
  end: 'money',
  change: 'money',
  accounts: { cells: 'money' },
  totals: 'money',
}

export function useSavingsReport(from: string | null, to: string) {
  const path = `/reports/savings${queryString({ from: from ?? undefined, to })}`
  return useQuery({
    queryKey: reportKeys.savings(from, to),
    queryFn: ({ signal }) => api.get<SavingsReportData>(path, SAVINGS_SHAPE, signal),
  })
}

/* ---- Spending ----------------------------------------------------------- */

/**
 * One calendar period beside a comparison (`calculations.md` §11, "Spending
 * report"). Every figure is the register's own aggregate over the register's
 * scope, so nothing here sums, averages or compares: the client only orders
 * and draws.
 */

export type SpendingGrain = 'month' | 'quarter' | 'year'
export type SpendingCompare =
  | 'same_last_year'
  | 'prior'
  | 'ytd_average'
  | 'average_2'
  | 'average_3'
  | 'average_4'
  | 'average_6'
  | 'average_12'
  | 'none'
export type SpendingGroup = 'category' | 'payee' | 'tag'
export type DifferenceState = 'change' | 'new_spend' | 'no_spend' | 'none'
export type SavingsRating = 'none' | 'low' | 'good' | 'great'

export interface SpendingPeriod {
  /** `2026-10`, `2026-Q4` or `2026`. */
  key: string
  from: IsoDate
  /** Today for the period in progress, the period's end otherwise. */
  through: IsoDate
  end: IsoDate
  partial: boolean
}

export interface SpendingChartPeriod extends SpendingPeriod {
  income: Money
  /** Stored sign: spending is negative. */
  spent: Money
  /** Negative is overspent. */
  remaining: Money
}

export interface SpendingDifference {
  /** Positive is more spent than the comparison. */
  amount: Money
  /** Percent units, null against a comparison of zero. */
  pct: WireRate
  state: DifferenceState
}

export interface SpendingComparison {
  compare: SpendingCompare
  periods: SpendingPeriod[]
  average: boolean
  spent: Money
  difference: SpendingDifference
}

export interface SpendingSummary {
  income: Money
  spent: Money
  /** Negative is overspent. */
  remaining: Money
  /** Fractions, null with nothing coming in. */
  savings_rate: WireRate
  spending_rate: WireRate
  /** For the period in progress, the projection's rating. */
  rating: SavingsRating
  /** The period in progress carried to its end by what is still scheduled. */
  projection: SpendingProjection | null
}

export interface SpendingProjection {
  /** The period's last day. */
  end: IsoDate
  expected_income: Money
  /** Stored sign: spending is negative. */
  expected_spent: Money
  /** How many reminders the expected figures hold. */
  count: number
  income: Money
  spent: Money
  remaining: Money
  savings_rate: WireRate
  spending_rate: WireRate
}

export interface SpendingRow {
  key: string
  label: string
  /** Stored sign: a line netting to a credit is positive. */
  amount: Money
  comparison: Money
  difference: SpendingDifference
  /** A fraction of the period's spending; null for a credit. */
  share: WireRate
}

export interface SpendingTableRow {
  key: string
  label: string
  /** Parallel to `table.periods`. */
  cells: Money[]
  total: Money
  difference: SpendingDifference
}

export interface SpendingFlowNode {
  key: string
  label: string
  /** A magnitude. */
  amount: Money
  /** A fraction of income, null with none. */
  share: WireRate
}

export interface SpendingFlow {
  income: SpendingFlowNode[]
  credits: SpendingFlowNode[]
  spending: SpendingFlowNode[]
  income_total: Money
  /** A magnitude: the spend lines less the credits. */
  spent: Money
  spent_share: WireRate
}

export interface SpendingReportData {
  grain: SpendingGrain
  today: IsoDate
  period: SpendingPeriod
  /** The selected period as a register window, for the list beneath (trap 5). */
  window: EchoedWindow
  periods: SpendingChartPeriod[]
  compare: SpendingCompare
  compare_options: SpendingCompare[]
  comparison: SpendingComparison | null
  summary: SpendingSummary
  group_by: SpendingGroup
  rows: SpendingRow[]
  uncategorized_count: number
  table: { periods: SpendingPeriod[]; prior: SpendingPeriod; rows: SpendingTableRow[] }
  flow: SpendingFlow
}

const DIFFERENCE_SHAPE: MoneyShape<SpendingDifference> = { amount: 'money' }
const FLOW_NODE_SHAPE: MoneyShape<SpendingFlowNode> = { amount: 'money' }

const SPENDING_SHAPE: MoneyShape<SpendingReportData> = {
  periods: { income: 'money', spent: 'money', remaining: 'money' },
  comparison: { spent: 'money', difference: DIFFERENCE_SHAPE },
  summary: {
    income: 'money',
    spent: 'money',
    remaining: 'money',
    projection: {
      expected_income: 'money',
      expected_spent: 'money',
      income: 'money',
      spent: 'money',
      remaining: 'money',
    },
  },
  rows: { amount: 'money', comparison: 'money', difference: DIFFERENCE_SHAPE },
  table: { rows: { cells: 'money', total: 'money', difference: DIFFERENCE_SHAPE } },
  flow: {
    income: FLOW_NODE_SHAPE,
    credits: FLOW_NODE_SHAPE,
    spending: FLOW_NODE_SHAPE,
    income_total: 'money',
    spent: 'money',
  },
}

export interface SpendingQuery {
  grain: SpendingGrain
  /** Any day of the selected period; null is the period today is in. */
  period: IsoDate | null
  compare: SpendingCompare
  groupBy: SpendingGroup
  /** The category the breakdown is drilled into. */
  under: Uuid | null
  filterId: Uuid | null
}

export function spendingReportPath(query: SpendingQuery): string {
  return `/reports/spending${queryString({
    grain: query.grain,
    period: query.period ?? undefined,
    compare: query.compare,
    group_by: query.groupBy,
    under: query.under ?? undefined,
    filter_id: query.filterId ?? undefined,
  })}`
}

export function useSpendingReport(query: SpendingQuery, enabled = true) {
  const path = spendingReportPath(query)
  return useQuery({
    queryKey: reportKeys.spending(path),
    enabled,
    queryFn: ({ signal }) => api.get<SpendingReportData>(path, SPENDING_SHAPE, signal),
    // The breakdown stays on screen while a new period or comparison loads.
    placeholderData: (previous) => previous,
  })
}

/* ---- Saved reports ------------------------------------------------------ */

export interface SavedReport {
  id: Uuid
  name: string
  config: ReportConfig
  filter: FilterRead
}

export function useSavedReports() {
  return useQuery({
    queryKey: reportKeys.saved,
    queryFn: ({ signal }) => api.get<SavedReport[]>('/reports', undefined, signal),
  })
}

export interface SavedReportWrite {
  name: string
  config: ReportConfig
  items: FilterItemWrite[]
  /** Only settable at create. */
  queryText?: string
}

export function createSavedReport(body: SavedReportWrite) {
  return api.post<SavedReport>('/reports', {
    name: body.name,
    config: body.config,
    items: body.items,
    query_text: body.queryText ?? null,
  })
}

export function updateSavedReport(id: Uuid, body: SavedReportWrite) {
  return api.patch<SavedReport>(`/reports/${id}`, {
    name: body.name,
    config: body.config,
    items: body.items,
  })
}

/** Name alone: a config sent from a list read would overwrite unsaved knobs. */
export function renameSavedReport(id: Uuid, name: string) {
  return api.patch<SavedReport>(`/reports/${id}`, { name })
}

export function deleteSavedReport(id: Uuid) {
  return api.delete<void>(`/reports/${id}`)
}
