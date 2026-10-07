import type { MoneyShape } from '@/lib/api'
import { formatDate } from '@/lib/format'
import type { Money } from '@/lib/money'
import { recurrenceFromWire, type Recurrence, type WireRecurrence } from '@/lib/recurrence'
import type { BillSource } from '../bills'
import type { IsoDate } from '../entities'

/**
 * `skipped` is handled without a charge (not unpaid); `received` is `paid` for
 * an income series. Labels and tones live in `lib/spendingPlan`.
 */
export type OccurrenceStatus = 'upcoming' | 'past_due' | 'paid' | 'received' | 'skipped'

/** The five kinds `domain.SeriesKind` routes, plus the refund tab's own. */
export type SeriesKind =
  'income' | 'bill' | 'subscription' | 'transfer' | 'credit_card_payment' | 'refund'

export const SERIES_KIND_LABELS: Record<SeriesKind, string> = {
  income: 'Income',
  bill: 'Bill',
  subscription: 'Subscription',
  transfer: 'Transfer',
  credit_card_payment: 'Credit Card Payment',
  refund: 'Refund',
}

/**
 * One expected instance of a series, or a pay-manually reminder: a provider's
 * newest unpaid statement that no autopay takes, with no series and no
 * account, its statement as `bill` and its provider as `bill_link`.
 */
export interface Occurrence {
  series_id: string | null
  account_id: string | null
  category_id: string | null
  kind: SeriesKind
  label: string
  due_on: IsoDate
  amount: Money
  status: OccurrenceStatus
  /**
   * The day the money moves when known to differ from `due_on` (autopay).
   * Only the projection steps the balance on it; "when is this due" reads `due_on`.
   */
  pays_on: IsoDate | null
  bill: OccurrenceBill | null
  /**
   * Present on every occurrence of a linked series, bill or not, and on a
   * pay-manually reminder; null when unlinked.
   */
  bill_link: OccurrenceBillLink | null
  transaction_id: string | null
}

export type BillLinkHealth = 'ok' | 'not_connected' | 'needs_sign_in' | 'challenge' | 'failed'

export interface OccurrenceBillLink {
  connection_id: string
  biller: string
  connection_label: string
  subaccount_label: string
  health: BillLinkHealth
  /** False when the connection has no autopay rule: the bill is paid by hand. */
  autopay: boolean
}

export function billLinkMark(
  link: OccurrenceBillLink | null,
  providerName: (biller: string) => string,
): { tone: 'linked' | 'broken'; text: string } | null {
  if (!link) return null
  const who = providerName(link.biller)
  switch (link.health) {
    case 'ok':
      return { tone: 'linked', text: `Kept current by ${who}` }
    case 'not_connected':
      return { tone: 'broken', text: `${who} is not connected: no bills will arrive until it is` }
    case 'needs_sign_in':
      return { tone: 'broken', text: `${who} needs a sign-in before the next bill can be pulled` }
    case 'challenge':
      return { tone: 'broken', text: `${who} is waiting on a code before the next bill can be pulled` }
    case 'failed':
      return { tone: 'broken', text: `The last pull from ${who} failed` }
  }
}

export interface OccurrenceBill {
  id: string
  amount_due: Money
  due_on: IsoDate
  /** The provider's word; a paid bill settles no reminder without a bank match. */
  status: 'open' | 'paid'
  source: BillSource
  fetched_at: string
  document_id: string | null
}

const OCCURRENCE_SHAPE: MoneyShape<Occurrence> = {
  amount: 'money',
  bill: { amount_due: 'money' },
}

/**
 * How a bill gets paid, when there is something to say: "autopays Oct 21"
 * when the money leaves before the due date, and "pay manually" for a
 * provider's bill no autopay takes.
 */
export function autopayNote(
  occurrence: Pick<Occurrence, 'due_on' | 'pays_on' | 'bill_link'>,
): string | null {
  const paysOn = occurrence.pays_on
  if (!paysOn) return occurrence.bill_link?.autopay === false ? 'pay manually' : null
  if (paysOn >= occurrence.due_on) return null
  return `autopays ${formatDate(paysOn, 'short')}`
}

/** A pay-manually reminder: a statement with no series behind it. */
export function isPayManually(occurrence: Pick<Occurrence, 'series_id'>): boolean {
  return occurrence.series_id === null
}

/**
 * The account a reminder's sub line names: the one it is paid from, or for a
 * pay-manually reminder, which is paid from no account anyone named, the
 * billed account it is owed on.
 */
export function reminderAccount(
  occurrence: Pick<Occurrence, 'account_id' | 'bill_link'>,
  accountName: (id: string) => string,
): string {
  if (occurrence.account_id !== null) return accountName(occurrence.account_id)
  return occurrence.bill_link?.subaccount_label ?? ''
}

/**
 * "biller reports paid", on a past-due slot whose statement the provider
 * marks paid. The slot stays past due: only a bank transaction settles it.
 */
export function billerPaidNote(occurrence: Pick<Occurrence, 'status' | 'bill'>): string | null {
  if (occurrence.status !== 'past_due' || occurrence.bill?.status !== 'paid') return null
  return 'biller reports paid'
}

/** "due Oct 26", read beside a payment date whenever the two dates differ. */
export function dueNote(occurrence: Pick<Occurrence, 'due_on' | 'pays_on'>): string | null {
  const paysOn = occurrence.pays_on
  if (!paysOn || paysOn === occurrence.due_on) return null
  return `due ${formatDate(occurrence.due_on, 'short')}`
}

/**
 * The date slot is half an occurrence's identity; two bills due the same day
 * are two occurrences of one slot, told apart by their statement.
 */
export function occurrenceKey(one: Pick<Occurrence, 'series_id' | 'due_on' | 'bill'>): string {
  const key = `${one.series_id ?? 'statement'}:${one.due_on}`
  return one.bill ? `${key}:${one.bill.id}` : key
}

/**
 * The cash-flow chart's markers for one account, keyed by `pays_on` (the day
 * the projection steps the balance), with a differing due date in the label.
 */
export function occurrenceMarkers(
  accountId: string,
  occurrences: readonly Occurrence[],
  toNumber: (amount: Money) => number | null,
): Record<string, { label: string; amount: number | null; emphasis?: boolean }[]> {
  const out: Record<string, { label: string; amount: number | null; emphasis?: boolean }[]> = {}
  for (const one of occurrences) {
    if (one.account_id !== accountId) continue
    if (one.status === 'skipped') continue
    const due = dueNote(one)
    const at = (out[one.pays_on ?? one.due_on] ??= [])
    at.push({
      label: due === null ? one.label : `${one.label} · ${due}`,
      amount: toNumber(one.amount),
      emphasis: one.kind === 'income' || undefined,
    })
  }
  return out
}

export interface OccurrenceSummary {
  income: Money
  expenses: Money
  net: Money
  count: number
  past_due: number
}

export interface OccurrenceList {
  window: { from: IsoDate | null; to: IsoDate | null; date_field: string }
  items: Occurrence[]
  summary: OccurrenceSummary
}

export const OCCURRENCE_LIST_SHAPE: MoneyShape<OccurrenceList> = {
  items: OCCURRENCE_SHAPE,
  summary: { income: 'money', expenses: 'money', net: 'money' },
}

export interface Series {
  id: string
  account_id: string
  category_id: string | null
  kind: SeriesKind
  /** Matching input, never a label. */
  description: string
  display_name: string | null
  /** `display_name or description`; every display path reads this. */
  label: string
  amount: Money
  currency: string
  recurrence: Recurrence
  start_on: IsoDate
  end_on: IsoDate | null
  next_due_on: IsoDate | null
  /** Overrides applied. */
  due_on: IsoDate
  override_next_due_on: IsoDate | null
  override_next_amount: Money | null
  auto_adjust_due_on: boolean
  reminder_days: number
  match_criteria: MatchCriteria
  match_amount_min: Money | null
  match_amount_max: Money | null
  /** Template applied to an accepted occurrence. */
  tag_ids: string[]
  splits: SeriesSplit[]
  is_active: boolean
  /** Expanded over a real calendar year, never a frequency lookup table. */
  annualized_amount: Money
  occurrences_per_year: number
}

/**
 * Before the recurrence is normalized: the wire carries the Go zero value for
 * a one-time rule, and every read of `frequency` tests for `null`.
 */
export type Wire<T extends { recurrence: Recurrence }> = Omit<T, 'recurrence'> & {
  recurrence: WireRecurrence
}

export function seriesFromWire(raw: Wire<Series>): Series {
  return { ...raw, recurrence: recurrenceFromWire(raw.recurrence) }
}

export function suggestionFromWire(raw: Wire<Suggestion>): Suggestion {
  return { ...raw, recurrence: recurrenceFromWire(raw.recurrence) }
}

export interface SeriesSplit {
  amount: Money
  category_id: string | null
  memo: string
  tag_ids: string[]
}

const SERIES_SPLIT_SHAPE: MoneyShape<SeriesSplit> = { amount: 'money' }

export const SERIES_SHAPE: MoneyShape<Series> = {
  amount: 'money',
  splits: SERIES_SPLIT_SHAPE,
  override_next_amount: 'money',
  match_amount_min: 'money',
  match_amount_max: 'money',
  annualized_amount: 'money',
}

export type MatchCriteria = 'auto' | 'any' | 'exact' | 'range'

/** Suggest, never auto-create. */
export interface Suggestion {
  signature: string
  account_id: string
  category_id: string | null
  kind: SeriesKind
  description: string
  display_name: string
  label: string
  amount: Money
  currency: string
  recurrence: Recurrence
  start_on: IsoDate
  occurrences: number
  first_seen: IsoDate
  last_seen: IsoDate
  confidence: number
  match_criteria: MatchCriteria
  match_amount_min: Money | null
  match_amount_max: Money | null
  transaction_ids: string[]
}

export const SUGGESTION_SHAPE: MoneyShape<Suggestion> = {
  amount: 'money',
  match_amount_min: 'money',
  match_amount_max: 'money',
}

export interface Refund {
  series: Series
  expected_on: IsoDate
  settled_on: IsoDate | null
  transaction_id: string | null
}

export interface RefundList {
  expected: Refund[]
  completed: Refund[]
}

type WireRefund = Omit<Refund, 'series'> & { series: Wire<Series> }

export interface WireRefundList {
  expected: WireRefund[]
  completed: WireRefund[]
}

export function refundFromWire(raw: WireRefund): Refund {
  return { ...raw, series: seriesFromWire(raw.series) }
}

export const REFUNDS_SHAPE: MoneyShape<RefundList> = {
  expected: { series: SERIES_SHAPE },
  completed: { series: SERIES_SHAPE },
}

export interface CashFlowPoint {
  on: IsoDate
  balance: Money
}

export interface CashFlowLine {
  account_id: string
  name: string
  starting_balance: Money
  points: CashFlowPoint[]
  lowest: CashFlowPoint | null
  first_below: CashFlowPoint | null
}

export interface CashFlow {
  window: { from: IsoDate | null; to: IsoDate | null; date_field: string }
  threshold: Money
  accounts: CashFlowLine[]
  combined: CashFlowPoint[]
  occurrences: Occurrence[]
}

export const CASH_FLOW_SHAPE: MoneyShape<CashFlow> = {
  threshold: 'money',
  accounts: {
    starting_balance: 'money',
    points: { balance: 'money' },
    lowest: { balance: 'money' },
    first_below: { balance: 'money' },
  },
  combined: { balance: 'money' },
  occurrences: OCCURRENCE_SHAPE,
}

/**
 * Filters over one list, except `suggested`, which reads its own endpoint
 * because a suggestion is not a series yet.
 */
export const SERIES_TABS = [
  'all_active',
  'bills',
  'subscriptions',
  'income',
  'transfers',
  'canceled',
  'suggested',
] as const

export type SeriesTab = (typeof SERIES_TABS)[number]

export const SERIES_TAB_LABELS: Record<SeriesTab, string> = {
  all_active: 'All active',
  bills: 'Bills',
  subscriptions: 'Subscriptions',
  income: 'Income',
  transfers: 'Transfers',
  canceled: 'Canceled',
  suggested: 'Suggested',
}

export const TAB_KINDS: Partial<Record<SeriesTab, SeriesKind>> = {
  bills: 'bill',
  subscriptions: 'subscription',
  income: 'income',
  transfers: 'transfer',
}

export interface SeriesHistoryRow {
  due_on: IsoDate
  expected: Money
  status: OccurrenceStatus
  /** A linked charge on a day the rule does not land on; still paid. */
  off_schedule: boolean
  transaction: SeriesHistoryTransaction | null
}

export interface SeriesHistoryTransaction {
  id: string
  account_id: string
  account_name: string
  date: IsoDate
  amount: Money
  payee: string
  statement_name: string
  category_id: string | null
}

/** Rows newest first. */
export interface SeriesHistory {
  series: Series
  /** `YYYY-MM`, inclusive at both ends. */
  from: string
  to: string
  rows: SeriesHistoryRow[]
  paid_count: number
  /** Null when no charge stands behind any row. */
  average_paid: Money | null
}

export const SERIES_HISTORY_SHAPE: MoneyShape<SeriesHistory> = {
  series: SERIES_SHAPE,
  rows: { expected: 'money', transaction: { amount: 'money' } },
  average_paid: 'money',
}
