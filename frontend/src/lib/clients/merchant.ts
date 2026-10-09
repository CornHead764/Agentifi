/**
 * The merchant connector: Amazon's orders, Costco's purchases.
 * `/merchants/{merchant}/…` is the shop's side and names the merchant in its
 * query key; `/merchants/transactions/…` is the bank row's side and names no
 * merchant, since one row may be backed by orders from either shop.
 */

import {
  skipToken,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'

import { api, ApiError, type MoneyShape } from '@/lib/api'
import type { SecondFactor } from '@/lib/clients/bills'
import { explainConnectorFailure } from '@/lib/connectorFailure'
import { describeApiError } from '@/lib/errors'
import { parseIsoDay, plural, timeAgo } from '@/lib/format'
import { MERCHANTS, type MerchantId, type OrderKind } from '@/lib/merchants'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { absMoney, type Money } from '@/lib/money'
import { registerLinkFor } from '@/lib/transactions/links'
import type { Uuid } from '@/lib/transactions/types'

import { queryString } from './entities'
import { usePullAfterSignIn } from './pullAfterSignIn'
import { anyPulling, PULL_POLL_MS, startPulling } from './pulling'

export interface MerchantAccount {
  id: Uuid
  merchant: MerchantId
  label: string
  /** Display name, decided by the server so every screen agrees. */
  name: string
  orders: number
  email: string
  connected: boolean
  signed_in_at: string | null
  /** Whether a password (and authenticator key) is kept; neither is sent back. */
  has_password: boolean
  has_totp: boolean
  second_factor: SecondFactor
  /**
   * Why updates stopped signing in with the kept password. A person's sign-in,
   * a new password or a pull that gets in lifts it.
   */
  sign_in_paused: '' | 'password_refused' | 'code_needed' | 'page_check'
  sync_enabled: boolean
  sync_days: number
  last_synced_at: string | null
  last_sync_status: '' | 'ok' | 'needs_sign_in' | 'failed'
  last_sync_error: string
  /** The last pull stopped on a page, and `merchantFailureScreenshot` names where it is served. */
  has_failure_screenshot: boolean
  needs_sign_in: boolean
  /** A pull is running; the `last_sync_*` fields describe the previous one until it finishes. */
  pulling: boolean
  /** What the running pull is doing now; null when none is running or it has not begun reporting. */
  progress: MerchantPullProgress | null
  /** Null for a merchant whose gift card is a tender line rather than a balance. */
  gift_card_account_id: Uuid | null
  gift_card_balance: Money | null
  gift_card_balance_at: string | null
  /** A running invoice backfill's progress, else how the last one ended; null when none has run. */
  backfill: MerchantBackfill | null
  created_at: string
}

/** The line a running pull last reported, when the pull began and when the line last changed. */
export interface MerchantPullProgress {
  line: string
  started_at: string
  updated_at: string
}

/**
 * While `running`, `total`, `done` and `filed` count the orders it set out
 * to file; once finished, `filed`, `left` and `stopped` say how it ended.
 */
export interface MerchantBackfill {
  running: boolean
  total: number
  done: number
  filed: number
  left: number
  /** Why it ended before the last order; empty when it reached the end. */
  stopped: string
  finished_at: string | null
}

export interface MerchantAgentStatus {
  configured: boolean
  reachable: boolean
  detail: string
  /** Why this merchant cannot be signed in to although the engine is there; empty when it can. */
  unavailable: string
}

export type SignInState =
  'signed_in' | 'otp' | 'captcha' | 'approval' | 'failed' | 'email' | 'password'

export interface MerchantSignIn {
  session_id: string
  state: SignInState
  prompt: string
  /** Base64 PNG: the CAPTCHA, or the page on a failure. */
  image: string
  error: string
}

export interface MerchantOrderItem {
  /** Amazon's ASIN, Costco's item number. */
  sku: string
  /** May be a register abbreviation on a Costco receipt. */
  title: string
  quantity: number
  unit_price: Money | null
  total_owed: Money | null
  shipped_on: string | null
  condition: string
  url: string
  /** The product behind an abbreviated title; null until the catalog pass finds it. */
  catalog: MerchantCatalogItem | null
}

/** A `to_gift_card` refund has no bank row behind it, so nothing waits to match it. */
export interface MerchantRefund {
  id: Uuid
  /** Both empty for a refund of the whole order. */
  sku: string
  title: string
  quantity: number
  refunded_on: string
  /** Positive. */
  amount: Money
  instrument: string
  to_gift_card: boolean
  status: string
  transaction_id: Uuid | null
}

export interface MerchantCatalogItem {
  title: string
  brand: string
  size: string
  category: string
  image_url: string
  url: string
}

export interface MerchantOrder {
  id: Uuid
  merchant: MerchantId
  merchant_account_id: Uuid
  account_label: string
  order_number: string
  ordered_on: string
  total: Money
  currency: string
  status: string
  details_url: string
  source: 'amazon_csv' | 'extension_json' | 'agentifi_json'
  kind: OrderKind
  /** Empty for an online order. */
  location: string
  items: MerchantOrderItem[]
  refunds: MerchantRefund[]
  matched_transaction_ids: Uuid[]
  matched_transactions: MerchantMatchedTransaction[]
  /** The total less what a gift card paid. */
  card_total: Money
  /** Null until a pull has read the invoice. */
  gift_card_amount: Money | null
  tax: Money | null
  paid_by_gift_card: boolean
  cancelled: boolean
  /** No bank row will explain it (an untracked card); offered to no row. */
  ignored: boolean
}

export interface MerchantMatchedTransaction {
  id: Uuid
  account_id: Uuid
  date: string
  amount: Money
  payee: string
  statement_name: string
  account_name: string
  basis: MerchantMatch['basis']
  confidence: number
}

export interface MerchantOrderList {
  orders: MerchantOrder[]
  total: number
}

export interface MerchantImportResult {
  format: MerchantOrder['source']
  dry_run: boolean
  account_hint: string
  orders: number
  new_orders: number
  items: number
  charges: number
  new_charges: number
  refunds: number
  new_refunds: number
  /** Bank rows that found their order after the import. */
  matched: number
  gift_card_rows: number
  warnings: string[]
}

export interface MerchantSummary {
  accounts: number
  orders: number
  items: number
  charges: number
  /** Ledger rows whose wording names this merchant. */
  merchant_transactions: number
  matched_transactions: number
  newest_order: string | null
  oldest_order: string | null
}

export interface MerchantMatch {
  transaction_id: Uuid
  amount: Money
  basis: 'charge' | 'order_total' | 'shipment' | 'item' | 'refund' | 'refund_total' | 'manual'
  confidence: number
  /** The first order; `orders` is all of them when one payment settled several. */
  order: MerchantOrder
  orders: { amount: Money; basis: MerchantMatch['basis']; order: MerchantOrder }[]
  /** Null for a purchase, whose returns are listed on its order. */
  refund: MerchantRefund | null
}

export interface MerchantMatchCandidate {
  id: Uuid
  account_id: Uuid
  account_name: string
  date: string
  amount: Money
  payee: string
  statement_name: string
  is_pending: boolean
  matched_order_id: Uuid | null
  matched_order_number: string
}

export { registerLinkFor }

export const ACCOUNT_SHAPE: MoneyShape<MerchantAccount> = { gift_card_balance: 'money' }

const REFUND_SHAPE: MoneyShape<MerchantRefund> = { amount: 'money' }

const ORDER_SHAPE: MoneyShape<MerchantOrder> = {
  total: 'money',
  card_total: 'money',
  gift_card_amount: 'money',
  tax: 'money',
  items: { unit_price: 'money', total_owed: 'money' },
  refunds: REFUND_SHAPE,
  matched_transactions: { amount: 'money' },
}

export const ORDER_LIST_SHAPE: MoneyShape<MerchantOrderList> = { orders: ORDER_SHAPE }

export const MATCH_SHAPE: MoneyShape<MerchantMatch> = {
  amount: 'money',
  order: ORDER_SHAPE,
  orders: { amount: 'money', order: ORDER_SHAPE },
  refund: REFUND_SHAPE,
}

const CANDIDATES_SHAPE: MoneyShape<{ candidates: MerchantMatchCandidate[] }> = {
  candidates: { amount: 'money' },
}

const ORDER_CANDIDATES_SHAPE: MoneyShape<{ candidates: MerchantOrder[] }> = {
  candidates: ORDER_SHAPE,
}

/** A bank row pays for a record to the cent whatever the sign it is stored with. */
export function paysToTheCent(rowAmount: Money, cardTotal: Money): boolean {
  return absMoney(rowAmount) === absMoney(cardTotal)
}

/** Every merchant query's root, for a write that touches matching across all of them. */
export const merchantRootKey = ['merchant'] as const

export function merchantKey(merchant: MerchantId) {
  return [...merchantRootKey, merchant] as const
}

/** Names no merchant: one row may be backed by orders from either shop. */
export function merchantTransactionKey(transactionId: Uuid | null) {
  return [...merchantRootKey, 'transactions', transactionId] as const
}

/** Both sides of a match change; the row-level calls do not know the merchant. */
const MATCH_WRITE: Invalidates = [merchantRootKey]

/** The page an account's last pull stopped on, when one was kept. */
export function merchantFailureScreenshot(
  merchant: MerchantId,
  account: Pick<MerchantAccount, 'id' | 'has_failure_screenshot'>,
): { path: string } | null {
  if (!account.has_failure_screenshot) return null
  return { path: `/merchants/${merchant}/accounts/${account.id}/failure-screenshot` }
}

/**
 * Why "Update now" was refused. A 409 or 502 is the connector's own words,
 * read as a card reads its last error; anything else is the server's.
 */
export function describeMerchantPullFailure(merchant: MerchantId, error: unknown): string {
  if (error instanceof ApiError && (error.status === 409 || error.status === 502)) {
    return explainConnectorFailure(error.detail ?? '', MERCHANTS[merchant].name).message
  }
  return describeApiError(error)
}

/**
 * The page a refused pull stopped on: "Update now" answers a failure as an
 * error, whose body says whether the page was kept.
 */
export function merchantPullFailureScreenshot(
  merchant: MerchantId,
  accountId: Uuid,
  error: unknown,
): { path: string } | null {
  const body = error instanceof ApiError ? error.body : null
  const kept =
    typeof body === 'object' && body !== null && 'has_failure_screenshot' in body
      ? body.has_failure_screenshot === true
      : false
  return merchantFailureScreenshot(merchant, { id: accountId, has_failure_screenshot: kept })
}

export function useMerchantAccounts(merchant: MerchantId) {
  return useQuery({
    queryKey: [...merchantKey(merchant), 'accounts'],
    queryFn: ({ signal }) =>
      api.get<MerchantAccount[]>(`/merchants/${merchant}/accounts`, ACCOUNT_SHAPE, signal),
    refetchInterval: (query) =>
      anyPulling(query.state.data) || query.state.data?.some((one) => one.backfill?.running)
        ? PULL_POLL_MS
        : false,
  })
}

/** How many stored orders a backfill of this account would file an invoice for. */
export function useMerchantBackfillWanting(merchant: MerchantId, id: Uuid | null) {
  return useQuery({
    queryKey: [...merchantKey(merchant), 'accounts', id, 'backfill'] as const,
    queryFn:
      id === null
        ? skipToken
        : ({ signal }) =>
            api.get<{ wanting: number }>(
              `/merchants/${merchant}/accounts/${id}/backfill`,
              undefined,
              signal,
            ),
    staleTime: 0,
  })
}

/** Starts the backfill; it answers at once, and the account's `backfill` reports it. */
export function useStartMerchantBackfill(
  merchant: MerchantId
) {
  return useInvalidatingMutation(
    (id: Uuid) =>
      api.post<MerchantAccount>(
        `/merchants/${merchant}/accounts/${id}/backfill`,
        {},
        ACCOUNT_SHAPE,
      ),
    [merchantKey(merchant)],
  )
}

/** The row's line about a running backfill or the last one; null when none has run. */
export function describeBackfill(
  merchant: MerchantId,
  backfill: MerchantBackfill | null,
): string | null {
  if (backfill === null) return null
  const { noun, nounPlural } = MERCHANTS[merchant]
  if (backfill.running)
    return backfill.total === 0
      ? 'Backfilling invoices…'
      : `Backfilling invoices: ${backfill.done} of ${backfill.total}…`
  const filed = `${plural(backfill.filed, 'invoice')} filed`
  const left =
    backfill.left === 0
      ? `no ${noun} left without one`
      : `${plural(backfill.left, noun, nounPlural)} still without one`
  const when = backfill.finished_at ? `, ${timeAgo(backfill.finished_at, 'long')}` : ''
  const stopped =
    backfill.stopped === '' ? '' : ` It stopped early: ${backfill.stopped.replace(/[.\s]+$/, '')}.`
  return `Last invoice backfill${when}: ${filed}, ${left}.${stopped}`
}

export function useMerchantSummary(merchant: MerchantId) {
  return useQuery({
    queryKey: [...merchantKey(merchant), 'summary'],
    queryFn: ({ signal }) =>
      api.get<MerchantSummary>(`/merchants/${merchant}/summary`, undefined, signal),
  })
}

export function useMerchantOrders(
  merchant: MerchantId,
  options: {
    accountId?: Uuid
    unmatched?: boolean
    limit?: number
  },
) {
  const size = options.limit ?? 50
  const page = (offset: number) =>
    queryString({
      account_id: options.accountId || undefined,
      unmatched: options.unmatched || undefined,
      limit: size,
      offset: offset > 0 ? offset : undefined,
    })
  return useInfiniteQuery({
    queryKey: [...merchantKey(merchant), 'orders', page(0)],
    queryFn: ({ pageParam, signal }) =>
      api.get<MerchantOrderList>(
        `/merchants/${merchant}/orders${page(pageParam)}`,
        ORDER_LIST_SHAPE,
        signal,
      ),
    initialPageParam: 0,
    getNextPageParam: (last, all) => {
      const loaded = all.reduce((sum, one) => sum + one.orders.length, 0)
      return loaded < last.total ? loaded : undefined
    },
  })
}

export function merchantOrdersOf(
  pages: { pages: MerchantOrderList[] } | undefined,
): MerchantOrder[] {
  return (pages?.pages ?? []).flatMap((one) => one.orders)
}

export function useMerchantMatchCandidates(
  merchant: MerchantId,
  orderId: Uuid | null,
  search: string,
) {
  const query = queryString({ q: search.trim() || undefined })
  return useQuery({
    queryKey: [...merchantKey(merchant), 'candidates', orderId, query],
    queryFn: ({ signal }) =>
      api.get<{ candidates: MerchantMatchCandidate[] }>(
        `/merchants/${merchant}/orders/${orderId}/candidates${query}`,
        CANDIDATES_SHAPE,
        signal,
      ),
    enabled: orderId !== null,
    placeholderData: (previous) => previous,
  })
}

/**
 * Without `merchant`, the server offers the shop the row's wording names, or
 * every shop when it names none.
 */
export function useMerchantOrderCandidates(
  transactionId: Uuid | null,
  search: string,
  merchant?: MerchantId | null,
) {
  const query = queryString({ q: search.trim() || undefined, merchant: merchant || undefined })
  return useQuery({
    queryKey: [...merchantTransactionKey(transactionId), 'candidates', query],
    queryFn: ({ signal }) =>
      api.get<{ candidates: MerchantOrder[] }>(
        `/merchants/transactions/${transactionId}/candidates${query}`,
        ORDER_CANDIDATES_SHAPE,
        signal,
      ),
    enabled: transactionId !== null,
    placeholderData: (previous) => previous,
  })
}

/** 404 when the row has no purchase behind it. */
export function useMerchantMatch(transactionId: Uuid | null) {
  return useQuery({
    queryKey: merchantTransactionKey(transactionId),
    queryFn: ({ signal }) =>
      api.get<MerchantMatch>(`/merchants/transactions/${transactionId}`, MATCH_SHAPE, signal),
    enabled: transactionId !== null,
    retry: false,
  })
}

export function useCreateMerchantAccount(
  merchant: MerchantId
) {
  return useInvalidatingMutation(
    (label: string = '') =>
      api.post<MerchantAccount>(`/merchants/${merchant}/accounts`, { label }, ACCOUNT_SHAPE),
    [merchantKey(merchant)],
  )
}

export function useUpdateMerchantAccount(
  merchant: MerchantId
) {
  return useInvalidatingMutation(
    ({
      id,
      ...changes
    }: {
      id: Uuid
      label?: string
      sync_enabled?: boolean
    }) => api.patch<MerchantAccount>(`/merchants/${merchant}/accounts/${id}`, changes, ACCOUNT_SHAPE),
    [merchantKey(merchant)],
  )
}

export function useMerchantAgent(merchant: MerchantId) {
  return useQuery({
    queryKey: [...merchantKey(merchant), 'agent'],
    queryFn: ({ signal }) =>
      api.get<MerchantAgentStatus>(`/merchants/${merchant}/agent`, undefined, signal),
    staleTime: 60_000,
  })
}

export interface MerchantSignInRequest {
  email: string
  password: string
  totp_secret?: string
  second_factor?: SecondFactor
}

export function useStartMerchantSignIn(merchant: MerchantId) {
  return useMutation({
    meta: { failure: 'The sign-in did not work' },
    mutationFn: ({ id, ...login }: { id: Uuid } & MerchantSignInRequest) =>
      api.post<MerchantSignIn>(`/merchants/${merchant}/accounts/${id}/sign-in`, login),
  })
}

export function useAnswerMerchantSignIn(
  merchant: MerchantId
) {
  return useMutation({
    meta: { failure: 'The sign-in did not work' },
    mutationFn: ({ id, session, code }: { id: Uuid; session: string; code: string }) =>
      api.post<MerchantSignIn>(`/merchants/${merchant}/accounts/${id}/sign-in/${session}/answer`, {
        code,
      }),
  })
}

export interface MerchantMailedCode extends MerchantSignIn {
  mailed_code_found: boolean
}

/** Held open by the server for a few minutes; a 409 means no mailbox is connected. */
export function waitForMailedMerchantCode(
  merchant: MerchantId,
  id: Uuid,
  session: string,
): Promise<MerchantMailedCode> {
  return api.post<MerchantMailedCode>(
    `/merchants/${merchant}/accounts/${id}/sign-in/${session}/mailed-code`,
  )
}

/** Never toasts: a code that did not arrive, or no mailbox to read it from, leaves the person to type it. */
export function useMailedMerchantCode(merchant: MerchantId) {
  return useMutation({
    meta: { failure: false },
    mutationFn: ({ id, session }: { id: Uuid; session: string }) =>
      waitForMailedMerchantCode(merchant, id, session),
  })
}

export function useMerchantSignInStatus(
  merchant: MerchantId
) {
  return useMutation({
    meta: { failure: 'The sign-in did not work' },
    mutationFn: ({ id, session }: { id: Uuid; session: string }) =>
      api.get<MerchantSignIn>(`/merchants/${merchant}/accounts/${id}/sign-in/${session}`),
  })
}

export function useCompleteMerchantSignIn(
  merchant: MerchantId
) {
  return useInvalidatingMutation(
    ({ id, session, email }: { id: Uuid; session: string; email: string }) =>
      api.post<MerchantAccount>(
        `/merchants/${merchant}/accounts/${id}/sign-in/${session}/complete`,
        { email },
        ACCOUNT_SHAPE,
      ),
    [merchantKey(merchant)],
    { failure: 'The sign-in did not work' },
  )
}

export function useMerchantPullAfterSignIn(
  merchant: MerchantId,
  id: Uuid | null,
  session: string | null,
) {
  return usePullAfterSignIn(
    ['merchant-sign-in', merchant, id, session, 'pull'],
    id === null || session === null
      ? null
      : async (signal) => {
          const accounts = await api.get<MerchantAccount[]>(
            `/merchants/${merchant}/accounts`,
            ACCOUNT_SHAPE,
            signal,
          )
          const account = accounts.find((one) => one.id === id)
          if (account === undefined) {
            throw new Error(`That ${MERCHANTS[merchant].name} account is gone`)
          }
          return account
        },
  )
}

export function useForgetMerchantSession(
  merchant: MerchantId
) {
  return useInvalidatingMutation(
    (id: Uuid) =>
      api.delete<MerchantAccount>(`/merchants/${merchant}/accounts/${id}/session`, ACCOUNT_SHAPE),
    [merchantKey(merchant)],
    { failure: 'That sign-in was not forgotten' },
  )
}

/** Forgets the kept password and the authenticator key beside it; the session stays. */
export function useForgetMerchantPassword(
  merchant: MerchantId
) {
  return useInvalidatingMutation(
    (id: Uuid) =>
      api.delete<MerchantAccount>(`/merchants/${merchant}/accounts/${id}/credential`, ACCOUNT_SHAPE),
    [merchantKey(merchant)],
    { failure: 'That password was not forgotten' },
  )
}

/** `days` overrides the account's window; the server caps it at ten years. */
export function usePullMerchant(merchant: MerchantId) {
  const client = useQueryClient()
  return useInvalidatingMutation(
    ({ id, days }: { id: Uuid; days?: number }) =>
      api.post<MerchantImportResult>(
        `/merchants/${merchant}/accounts/${id}/pull`,
        days ? { days } : {},
      ),
    [merchantKey(merchant)],
    {
      failure: false,
      onMutate: ({ id }) => startPulling(client, [...merchantKey(merchant), 'accounts'], id),
    },
  )
}

/** At least one; null for an invalid date. */
export function daysBackTo(iso: string, today: Date = new Date()): number | null {
  const then = parseIsoDay(iso)
  if (then === null) return null
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  return Math.max(1, Math.round((start.getTime() - then.getTime()) / 86_400_000))
}

export function describeSync(merchant: MerchantId, account: MerchantAccount): string {
  const { name, nounPlural, files } = MERCHANTS[merchant]
  const why = explainConnectorFailure(account.last_sync_error, name).message
  if (account.pulling) return `Fetching ${nounPlural} now…`
  if (!account.connected)
    return files.length > 0
      ? `Not signed in: ${nounPlural} arrive from files only`
      : 'Not signed in yet'
  if (account.needs_sign_in && account.has_password && account.sign_in_paused === '')
    return 'Session expired; the kept password signs in at the next update'
  if (account.needs_sign_in) return `Needs a sign-in. ${why}`
  if (account.last_sync_status === 'failed') return `Last update failed. ${why}`
  if (!account.sync_enabled) return 'Signed in; daily updates are off'
  if (!account.last_synced_at) return 'Signed in; the first update runs in the next quiet hour'
  return `Updating daily, the last ${account.sync_days} days`
}

export function useDeleteMerchantAccount(
  merchant: MerchantId
) {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/merchants/${merchant}/accounts/${id}`),
    [merchantKey(merchant)],
    { failure: 'That account was not removed' },
  )
}

export function importMerchantFile(
  merchant: MerchantId,
  file: File,
  accountId: Uuid,
  dryRun: boolean,
) {
  const fields: Record<string, string> = { merchant_account_id: accountId }
  if (dryRun) fields.dry_run = 'true'
  return api.upload<MerchantImportResult>(`/merchants/${merchant}/imports`, file, fields)
}

export function useImportMerchantFile(merchant: MerchantId) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ file, accountId, dryRun }: { file: File; accountId: Uuid; dryRun: boolean }) =>
      importMerchantFile(merchant, file, accountId, dryRun),
    onSuccess: (result) => {
      if (!result.dry_run) void client.invalidateQueries({ queryKey: merchantKey(merchant) })
    },
  })
}

export function useMatchMerchant(merchant: MerchantId) {
  return useInvalidatingMutation(
    () => api.post<{ matched: number }>(`/merchants/${merchant}/match`, {}),
    [merchantKey(merchant)],
  )
}

/** For rows categorized before their orders were on file. */
export function useSuggestMerchantCategories(
  merchant: MerchantId
) {
  return useMutation({
    meta: { failure: 'Categories were not suggested' },
    mutationFn: () =>
      api.post<{ rows: number; queued: number }>(`/merchants/${merchant}/suggest-categories`, {}),
  })
}

/** Adds beside any existing match: one payment may settle several orders. */
export function useAddMerchantMatch() {
  return useInvalidatingMutation(
    ({ transactionId, orderId }: { transactionId: Uuid; orderId: Uuid }) =>
      api.post<MerchantMatch>(
        `/merchants/transactions/${transactionId}/orders`,
        { order_id: orderId },
        MATCH_SHAPE,
      ),
    MATCH_WRITE,
  )
}

export function useRemoveMerchantMatch() {
  return useInvalidatingMutation(
    ({ transactionId, orderId }: { transactionId: Uuid; orderId: Uuid }) =>
      api.delete<void>(`/merchants/transactions/${transactionId}/orders/${orderId}`),
    MATCH_WRITE,
  )
}

export function useIgnoreMerchantOrder(merchant: MerchantId) {
  return useInvalidatingMutation(
    ({ orderId, ignored }: { orderId: Uuid; ignored: boolean }) =>
      api.patch<MerchantOrder>(`/merchants/${merchant}/orders/${orderId}`, { ignored }, ORDER_SHAPE),
    [merchantKey(merchant)],
  )
}

export function describeMatchBasis(merchant: MerchantId, basis: MerchantMatch['basis']): string {
  const { name, noun } = MERCHANTS[merchant]
  switch (basis) {
    case 'charge':
      return `${name}'s own charge record agrees to the cent`
    case 'order_total':
      return `the ${noun} total agrees to the cent`
    case 'shipment':
      return `one shipment of the ${noun} agrees; it was charged in parts`
    case 'item':
      return `one item of the ${noun} agrees; it was charged per item`
    case 'refund':
      return `${name}'s record of the return agrees; this is money coming back`
    case 'refund_total':
      return `the ${noun}'s invoice says this much was refunded; this is money coming back`
    case 'manual':
      return 'matched by hand'
  }
}

/** `agentifi_json` is what the daily update reads off the shop's pages, so it is named by the shop. */
export function describeSource(merchant: MerchantId, source: MerchantOrder['source']): string {
  switch (source) {
    case 'amazon_csv':
      return "Amazon's order-history file"
    case 'extension_json':
      return "The browser extension's file"
    case 'agentifi_json':
      return `Read from ${MERCHANTS[merchant].name}'s site`
  }
}
