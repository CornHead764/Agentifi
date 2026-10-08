/**
 * SimpleFIN connections. No response carries the Access URL, and the setup
 * token travels one way and is never cached. Keep the two failures apart:
 * `needs_setup_token` (the Access URL was refused; only a fresh token fixes it)
 * versus `bank_warnings` (one institution needs reauthorization at the Bridge).
 */

import { useIsMutating, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'

import { api, type Money, type MoneyShape } from '@/lib/api'
import { invalidate, useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { ACCOUNT_WITH_BALANCES } from '@/lib/transactions/api'
import { ACCOUNTS_KEY, TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { AccountKind, AccountWithBalances, RegisterTab, Uuid } from '@/lib/transactions/types'
import type { ValuePoint } from '@/lib/valueHistory'

import { balanceHistoryKeys } from './balancehistory'
import { netWorthKeys } from './networth'

export type ConnectionStatus =
  | 'active'
  | 'credentials_expired'
  | 'rate_limited'
  /** Claimed but not yet matched to local accounts; nothing imports until it is. */
  | 'pending_link'

export interface BankWarning {
  institution: string
  message: string
  /** When the Bridge last reported it. */
  at: string
}

export interface Connection {
  id: Uuid
  name: string
  status: ConnectionStatus
  status_detail: string | null
  needs_setup_token: boolean
  bank_warnings: BankWarning[]
  /** Accounts at the bank this connection must not create. */
  ignored: IgnoredAccount[]
  last_sync_at: string | null
  last_successful_sync_at: string | null
  /** Set while a throttled connection is parked. The credential is fine. */
  retry_not_before: string | null
  created_at: string
  /** The sync running, or the last one the server ran since it started. */
  sync: SyncRun | null
}

export type SyncState = 'running' | 'succeeded' | 'failed' | 'skipped'

export type SyncPhase = 'fetching' | 'importing' | 'settling'

/** One sync of a connection, as it runs and once it has ended. */
export interface SyncRun {
  state: SyncState
  /** Null once the run has ended. */
  phase: SyncPhase | null
  /** The account being read, from 1, of `accounts`; once ended, `accounts` is how many it reached. */
  account: number
  accounts: number
  account_name: string | null
  transactions_imported: number
  transactions_updated: number
  accounts_created: number
  /** Banks and accounts it could not read cleanly; the connection lists each. */
  warnings: number
  balances_held: number
  /** Why it failed or was skipped. */
  message: string | null
  started_at: string
  finished_at: string | null
}

export interface SyncSchedule {
  enabled: boolean
  /** "04:00", in `time_zone` (the server's), not the browser's. */
  at: string
  time_zone: string
  next_run_at: string | null
}

export interface ConnectionList {
  /** Off by default; distinct from "no connections yet". */
  simplefin_enabled: boolean
  connections: Connection[]
  schedule: SyncSchedule
}

export interface RemoteAccount {
  external_id: string
  name: string
  kind: string
  institution: string
  masked_number: string
  balance: Money
  currency: string
  linked_account_id: Uuid | null
  /** Best first. */
  suggested: Uuid[]
  /** The first suggestion, when an account number or a name singles it out. */
  likely: Uuid | null
  match: LinkMatch
}

/**
 * How sure the server is of its first suggestion: the same account number or
 * name, two that score alike, one that shares only a kind, a bank or a
 * balance, or nothing alike at all.
 */
export type LinkMatch = 'number' | 'name' | 'tie' | 'weak' | 'none'

export interface LinkTarget {
  id: Uuid
  name: string
  kind: string
  masked_number: string
  balance: Money
  /** Becomes the sync floor if paired, so the bank's backfill does not duplicate history. */
  sync_floor_on: string | null
  linked_to: string
}

/**
 * An account at the bank this connection must not create, e.g. one another
 * connection already reaches. The name is a snapshot from when it was ignored.
 */
export interface IgnoredAccount {
  id: Uuid
  external_id: string
  name: string
  institution: string
  masked_number: string
  ignored_at: string
}

/** The match screen's answer for one account at the bank. */
export type LinkChoice =
  | { external_id: string; action: 'link'; account_id: Uuid }
  | { external_id: string; action: 'create' | 'ignore' }

export interface LinkCandidates {
  remote: RemoteAccount[]
  local: LinkTarget[]
  ignored: IgnoredAccount[]
}

const CANDIDATES_SHAPE: MoneyShape<LinkCandidates> = {
  remote: { balance: 'money' },
  local: { balance: 'money' },
}

export const CONNECTIONS_KEY = ['connections'] as const

function linkCandidatesKey(id: Uuid) {
  return ['connections', id, 'candidates'] as const
}

export function useLinkCandidates(id: Uuid, enabled: boolean) {
  return useQuery({
    queryKey: linkCandidatesKey(id),
    queryFn: ({ signal }) =>
      api.get<LinkCandidates>(`/connections/${id}/candidates`, CANDIDATES_SHAPE, signal),
    enabled,
    // Each read costs a round trip to the Bridge.
    staleTime: 60_000,
    retry: false,
  })
}

/** Every mutation here can change the account list, so both caches are invalidated. */
const CONNECTION_WRITE: Invalidates = [CONNECTIONS_KEY, ACCOUNTS_KEY]

/** Creates nothing by itself; the next sync does. */
export function useRestoreRemoteAccount() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ignoredId }: { id: Uuid; ignoredId: Uuid }) =>
      api.delete<void>(`/connections/${id}/ignored/${ignoredId}`),
    onSettled: (_result, _error, { id }) => {
      void client.invalidateQueries({ queryKey: linkCandidatesKey(id) })
      void client.invalidateQueries({ queryKey: CONNECTIONS_KEY })
      void client.invalidateQueries({ queryKey: ACCOUNTS_KEY })
    },
  })
}

/** Puts a connection's answer where the listing reads it, so a run that ends at once is still seen. */
function storeConnection(client: QueryClient, connection: Connection) {
  client.setQueryData<ConnectionList>(CONNECTIONS_KEY, (current) =>
    current
      ? {
          ...current,
          connections: current.connections.map((one) => (one.id === connection.id ? connection : one)),
        }
      : current,
  )
}

/** Writes every choice at once and starts the first sync; the accounts exist when it answers. */
export function useFinishLinking() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    ({ id, choices }: { id: Uuid; choices: LinkChoice[] }) =>
      api.post<Connection>(`/connections/${id}/links/finish`, { choices }),
    CONNECTION_WRITE,
    { onSuccess: (connection) => storeConnection(client, connection) },
  )
}

export function isSyncing(connection: Connection): boolean {
  return connection.sync?.state === 'running'
}

const SYNC_POLL_MS = 1_500

export function useConnections() {
  return useQuery({
    queryKey: CONNECTIONS_KEY,
    queryFn: ({ signal }) => api.get<ConnectionList>('/connections', undefined, signal),
    refetchInterval: (query) =>
      query.state.data?.connections.some(isSyncing) ? SYNC_POLL_MS : false,
  })
}

/** When each connection's last run ended, as last read. */
export type SyncsSeen = ReadonlyMap<Uuid, string | null>

export function syncsSeen(list: readonly Connection[]): SyncsSeen {
  return new Map(list.map((one) => [one.id, one.sync?.finished_at ?? null]))
}

/** The connections whose run ended since `seen`; none on the first read, when `seen` is null. */
export function syncsEnded(seen: SyncsSeen | null, list: readonly Connection[]): Connection[] {
  if (seen === null) return []
  return list.filter(
    (one) => one.sync?.finished_at != null && one.sync.finished_at !== seen.get(one.id),
  )
}

/**
 * Calls `onEnd` for each sync that ends while the app is open, and refetches
 * everything a sync can change. A run already over when the listing first
 * loads is not reported. Mounted once, by the shell.
 */
export function useSyncEnded(onEnd: (connection: Connection) => void) {
  const connections = useConnections()
  const client = useQueryClient()
  const seen = useRef<SyncsSeen | null>(null)
  const list = connections.data?.connections
  useEffect(() => {
    if (list === undefined) return
    const ended = syncsEnded(seen.current, list)
    seen.current = syncsSeen(list)
    if (ended.length === 0) return
    void client.invalidateQueries()
    for (const one of ended) onEnd(one)
  }, [list, client, onEnd])
}

export function useClaimConnection() {
  return useInvalidatingMutation(
    ({ setupToken, name }: { setupToken: string; name?: string }) =>
      api.post<Connection>('/connections', {
        setup_token: setupToken,
        ...(name ? { name } : {}),
      }),
    CONNECTION_WRITE,
  )
}

/** Not a second claim, which would duplicate every account. */
export function useReplaceSetupToken() {
  return useInvalidatingMutation(
    ({ id, setupToken }: { id: Uuid; setupToken: string }) =>
      api.post<Connection>(`/connections/${id}/token`, { setup_token: setupToken }),
    CONNECTION_WRITE,
  )
}

/** Starts a sync, or joins the one running; `sync` on the answer says how it stands. */
export function useSyncConnection() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    (id: Uuid) => api.post<Connection>(`/connections/${id}/sync`),
    [CONNECTIONS_KEY],
    {
      failure: 'That sync did not start',
      onSuccess: (connection) => storeConnection(client, connection),
    },
  )
}

export function useRenameConnection() {
  return useInvalidatingMutation(
    ({ id, name }: { id: Uuid; name: string }) =>
      api.patch<Connection>(`/connections/${id}`, { name }),
    CONNECTION_WRITE,
    { failure: 'That name did not save' },
  )
}

/** Removing a connection keeps every account and every transaction. */
export function useDeleteConnection() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/connections/${id}`),
    CONNECTION_WRITE,
    { failure: 'That connection was not removed' },
  )
}

export function useUnlinkAccount() {
  return useInvalidatingMutation(
    ({ connectionId, accountId }: { connectionId: Uuid; accountId: Uuid }) =>
      api.delete<void>(`/connections/${connectionId}/accounts/${accountId}`),
    CONNECTION_WRITE,
    { failure: 'That account was not made manual' },
  )
}

// --- The account side of the page --------------------------------------------
//
// Here rather than in a second accounts client so every mutation invalidates the
// register's own ACCOUNTS_KEY.

export interface AccountSettingsPatch {
  name?: string
  /** Settles the drawer group. */
  type?: string
  /** What calculations switch on. Sent with `type`, never on its own. */
  kind?: AccountKind
  is_closed?: boolean
  excluded_from_reports?: boolean
  excluded_from_spending_plan?: boolean
  excluded_from_account_bar?: boolean
  include_in_net_worth?: boolean
  exclude_bank_pending?: boolean
  requires_receipts?: boolean

  /** ISO 4217. */
  currency?: string
  /** Null clears it. */
  masked_number?: string | null
  notes?: string | null
  /** A 2dp wire string, never a float. Null clears the limit. */
  credit_limit?: string | null
  /** Amounts owed, as 2dp wire strings; null clears. SimpleFIN carries none of these. */
  statement_balance?: string | null
  minimum_due?: string | null
  /** Null clears it. */
  due_date?: string | null
  /** Sent as typed: the server reads 24.99 and 0.2499 as the same rate (`domain.APRAsRate`). */
  interest_rate?: string | null
  /** A 2dp wire string; never null. */
  opening_balance?: string
  /** Null is "before the first row". */
  opening_balance_on?: string | null
  /** Null returns it to automatic. */
  history_starts_on?: string | null
  /** 2dp wire string; "0.00" always shows it, null follows the institution's setting. */
  hide_below_balance?: string | null
  /** Null opens the register on the rows. */
  default_register_tab?: RegisterTab | null
  /** 1–31. Null clears it. */
  statement_close_day?: number | null

  /** Null reverts to the provider's favicon. */
  custom_logo_url?: string | null
  property_address?: string | null
  vehicle_vin?: string | null
  /** Null is "unknown", not zero miles. */
  vehicle_mileage?: number | null
  vehicle_mileage_as_of?: string | null
  vehicle_miles_per_year?: number | null

  /** Null unpairs it. */
  secured_by_account_id?: Uuid | null
}

export function useUpdateAccountSettings() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: AccountSettingsPatch }) =>
      api.patch<AccountWithBalances>(`/accounts/${id}`, patch, ACCOUNT_WITH_BALANCES),
    CONNECTION_WRITE,
    { failure: 'That account change did not save' },
  )
}

/** Soft-delete: the server keeps its transactions. */
export function useDeleteAccount() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/accounts/${id}`),
    CONNECTION_WRITE,
    { failure: 'That account was not deleted' },
  )
}

/**
 * `kind` and `type` are both sent because neither implies the other: `savings`
 * and `checking` are one kind and two groups. The opening balance is written
 * as a transaction.
 */
export interface AccountCreate {
  name: string
  kind: AccountKind
  type: string
  currency?: string
  opening_balance?: string
  opening_balance_on?: string
  property_address?: string
  vehicle_vin?: string
}

export function useCreateAccount() {
  return useInvalidatingMutation(
    (body: AccountCreate) => api.post<AccountWithBalances>('/accounts', body, ACCOUNT_WITH_BALANCES),
    CONNECTION_WRITE,
    { failure: 'That account was not added' },
  )
}

// ----- valuation -------------------------------------------------------------

export interface ValuationResult {
  account_id: Uuid
  name: string
  /** Why nothing happened; null when something did. */
  skipped: string | null
  source: string | null
  estimate: Money | null
  adjustment: Money | null
  mileage_used: number | null
  /** What the source took the asset to be; null when it gave no estimate. */
  priced_as: PricedAs | null
}

export interface PricedAs {
  year: string | null
  make: string | null
  model: string | null
  trim: string | null
  mileage: number | null
  /** No odometer reading was stored, so the source used its own typical mileage. */
  typical_mileage: boolean
  /** The house the source matched. */
  address: string | null
  low: Money | null
  high: Money | null
}

export interface ValuationSources {
  asset_types: string[]
  /** The subset this deployment prices: all of them where Camoufox is configured. */
  configured: string[]
}

export const VALUATION_SOURCES_KEY = ['accounts', 'valuation-sources'] as const

export function useValuationSources() {
  return useQuery({
    queryKey: VALUATION_SOURCES_KEY,
    queryFn: ({ signal }) =>
      api.get<ValuationSources>('/accounts/valuation-sources', undefined, signal),
  })
}

export interface ValuationResults {
  results: ValuationResult[]
}

const VALUATION_RESULTS_SHAPE: MoneyShape<ValuationResults> = {
  results: {
    estimate: 'money',
    adjustment: 'money',
    priced_as: { low: 'money', high: 'money' },
  },
}

/** A revaluation writes a ledger row and moves the balance, so its history moves too. */
const REVALUATION_WRITE: Invalidates = [
  CONNECTIONS_KEY,
  ACCOUNTS_KEY,
  TRANSACTIONS_KEY,
  balanceHistoryKeys.all,
  netWorthKeys.all,
]

export function revalueAsset(accountId: Uuid): Promise<ValuationResults> {
  return api.post<ValuationResults>(`/accounts/${accountId}/revalue`, {}, VALUATION_RESULTS_SHAPE)
}

/** Every priceable asset, due or not: the button says "now". */
export function revalueAllAssets(): Promise<ValuationResults> {
  return api.post<ValuationResults>('/accounts/revalue?force=1', {}, VALUATION_RESULTS_SHAPE)
}

/**
 * Keyed, so that every place offering a re-price sees one started from any
 * other. The caller reports the outcome, failure included: these carry no
 * failure handler of their own.
 */
const REVALUE_KEY = ['revalue'] as const
const REVALUE_ONE_KEY = [...REVALUE_KEY, 'one'] as const
const REVALUE_ALL_KEY = [...REVALUE_KEY, 'all'] as const

export function useRevalueAsset() {
  const client = useQueryClient()
  return useMutation({
    meta: { failure: false },
    mutationKey: REVALUE_ONE_KEY,
    mutationFn: revalueAsset,
    onSettled: () => invalidate(client, REVALUATION_WRITE),
  })
}

export function useRevalueAllAssets() {
  const client = useQueryClient()
  return useMutation({
    meta: { failure: false },
    mutationKey: REVALUE_ALL_KEY,
    mutationFn: revalueAllAssets,
    onSettled: () => invalidate(client, REVALUATION_WRITE),
  })
}

/**
 * Whether a re-price is running: of this asset (by itself or as part of
 * "Re-price all"), or of every asset when no id is given.
 */
export function useRepricing(accountId?: Uuid): boolean {
  const running = useIsMutating({
    mutationKey: accountId === undefined ? REVALUE_ALL_KEY : REVALUE_KEY,
    predicate: (mutation) =>
      accountId === undefined ||
      mutation.options.mutationKey?.[1] === 'all' ||
      mutation.state.variables === accountId,
  })
  return running > 0
}

/** `written` counts ledger rows written, not points sent; a re-import writes nothing. */
export function useImportValueHistory() {
  return useInvalidatingMutation(
    ({ accountId, points }: { accountId: Uuid; points: ValuePoint[] }) =>
      api.post<{ written: number }>(`/accounts/${accountId}/value-history`, { points }),
    CONNECTION_WRITE,
    { failure: 'That value history was not imported' },
  )
}

export type Freshness = 'fresh' | 'stale' | 'never' | 'failing' | 'parked'

const STALE_AFTER_HOURS = 24

export function freshnessOf(connection: Connection, now: Date = new Date()): Freshness {
  if (connection.needs_setup_token) return 'failing'
  if (connection.status === 'rate_limited') return 'parked'
  if (!connection.last_successful_sync_at) return 'never'
  const age = now.getTime() - new Date(connection.last_successful_sync_at).getTime()
  return age > STALE_AFTER_HOURS * 3_600_000 ? 'stale' : 'fresh'
}
