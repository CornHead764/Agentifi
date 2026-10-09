/**
 * Bill connections, what they bill for, and the bills themselves — `/bills`.
 * A connection is one login; a series links to a *subaccount* (one premise or
 * card under that login). A bill's identity is its subaccount, due date and,
 * for a provider that bills separately on one day, its invoice, so a second
 * pull with a new amount amends it rather than adding one.
 */

import {
  skipToken,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query'

import { api, ApiError, type MoneyShape } from '@/lib/api'
import { billerName, type BillerAccess, type BillerId } from '@/lib/billers'
import { explainConnectorFailure } from '@/lib/connectorFailure'
import { ordinal, plural, timeAgo } from '@/lib/format'
import type { Money } from '@/lib/money'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { recurrenceFromWire, type Recurrence } from '@/lib/recurrence'
import { ACCOUNTS_KEY, TRANSACTIONS_KEY } from '@/lib/transactions/cache'

import type { IsoDate, Uuid } from './entities'
import { usePullAfterSignIn } from './pullAfterSignIn'
import { anyPulling, PULL_POLL_MS, startPulling } from './pulling'
import { invalidateOccurrenceWrites } from '@/lib/clients/upcoming/keys'
import type { MatchCriteria, SeriesKind, Wire } from '@/lib/clients/upcoming/types'

export type CredentialSource = 'session' | 'stored' | 'typed'

/** Autopay as the household knows it. The provider's own stated date wins over it. */
export type AutopayRule = 'none' | 'days_before_due' | 'on_due_date' | 'day_of_month'

export type SignInPause = '' | 'password_refused' | 'code_needed' | 'page_check'

/** `sign_in_failed` is a sign-in somebody started that never landed, kept in the last pull's place. */
export type PullStatus =
  | ''
  | 'ok'
  | 'needs_sign_in'
  | 'challenge'
  | 'failed'
  | 'sign_in_failed'

/** What the provider says, not whether the ledger settled it. */
export type BillStatus = 'open' | 'paid' | 'superseded'

/** A provider's pull wins over an email's evidence. */
export type BillSource = 'provider' | 'email' | 'manual' | 'assistant'

export type ChallengeMethod = 'totp' | 'sms' | 'email' | 'push' | 'captcha'

export interface BillConnection {
  id: Uuid
  biller: BillerId
  label: string
  username: string
  /**
   * Which deployment, for a provider deployed per customer; empty otherwise. A
   * provider that needs one cannot sign in while it is empty.
   */
  site: string
  credential_source: CredentialSource
  /** An authenticator setup key is sealed beside the kept password. */
  has_totp: boolean
  second_factor: SecondFactor
  /** Whether a session is kept; the sealed state never leaves the server. */
  connected: boolean
  signed_in_at: string | null
  needs_sign_in: boolean
  /**
   * Why the scheduler stopped signing in on its own. It leaves the login alone
   * until somebody signs in, changes the password, or presses Update now.
   */
  sign_in_paused: SignInPause
  autopay_rule: AutopayRule
  autopay_days: number | null
  autopay_day: number | null
  autopay_account_id: Uuid | null
  pull_enabled: boolean
  /** `HH:MM` in the space's zone, or null for the ordinary sync window. */
  pull_at: string | null
  last_pulled_at: string | null
  last_pull_status: PullStatus
  last_pull_error: string
  /** The last pull stopped on a page, and `billFailureScreenshot` names where it is served. */
  has_failure_screenshot: boolean
  /** The last update, or sign-in that never landed, kept its trail, which `fetchBillTrail` reads. */
  has_trail: boolean
  /** The last sign-in that did not land can be run again with what was typed into it, which the server still holds. */
  can_retry_sign_in: boolean
  /** A pull is running; the `last_pull_*` fields describe the previous one until it finishes. */
  pulling: boolean
  created_at: string
}

export interface BillSubaccount {
  id: Uuid
  connection_id: Uuid
  biller: BillerId
  external_id: string
  label: string
  masked_number: string | null
  is_selected: boolean
  series_id: Uuid | null
  /** The linked card or loan; a bill filed here writes its statement balance, minimum due and due date. */
  account_id: Uuid | null
}

export interface Bill {
  id: Uuid
  subaccount_id: Uuid
  due_on: IsoDate
  /** A magnitude. */
  amount_due: Money
  minimum_due: Money | null
  currency: string
  issued_on: IsoDate | null
  period_start: IsoDate | null
  period_end: IsoDate | null
  /** The provider's stated payment date; empty for providers nobody signs in to. */
  autopay_on: IsoDate | null
  /** `autopay_on`, else the connection's autopay rule applied to the due date. Display this one. */
  pays_on: IsoDate | null
  status: BillStatus
  source: BillSource
  statement_url: string
  /** The stored statement, from a document link of kind `bill`. */
  document_id: Uuid | null
  fetched_at: string
  /** Set when a later pull corrected the figures. */
  amended_at: string | null
}

export interface SeriesBillLink {
  series_id: Uuid
  subaccount_id: Uuid
  /** Not served on the link; read off the subaccount by `getSeriesBillLink`. */
  connection_id: Uuid
}

/** What writing a link answers. */
export interface BillLink {
  series_id: Uuid
  subaccount_id: Uuid
  created_at: string
}

export interface BillChallenge {
  id: Uuid
  connection_id: Uuid
  method: ChallengeMethod
  prompt: string
  /** A base64 CAPTCHA. */
  image: string | null
  state: 'waiting' | 'answered' | 'expired' | 'failed'
  answered_by: 'mailbox' | 'person' | null
  raised_by: 'pull' | 'connect' | 'keepalive'
  created_at: string
  expires_at: string
  answered_at: string | null
}

const BILL_SHAPE: MoneyShape<Bill> = { amount_due: 'money', minimum_due: 'money' }

export interface BillConnectionDraft {
  biller: BillerId
  label: string
  username?: string
  site?: string
  credential_source?: CredentialSource
  autopay_rule?: AutopayRule
  autopay_days?: number | null
  autopay_day?: number | null
  autopay_account_id?: Uuid | null
  pull_enabled?: boolean
  pull_at?: string | null
}

export type BillConnectionPatch = Partial<BillConnectionDraft>

/**
 * `is_selected` off hides it: pulls and the reminder picker skip it, while its
 * bills stay on file.
 */
export interface BillSubaccountPatch {
  label?: string
  is_selected?: boolean
  /** Null unlinks it. */
  account_id?: Uuid | null
}

export function listBillConnections(signal?: AbortSignal): Promise<BillConnection[]> {
  return api.get<BillConnection[]>('/bills/connections', undefined, signal)
}

export function getBillConnection(id: Uuid, signal?: AbortSignal): Promise<BillConnection> {
  return api.get<BillConnection>(`/bills/connections/${id}`, undefined, signal)
}

/** The page a connection's last pull stopped on, when one was kept. */
export function billFailureScreenshot(
  connection: Pick<BillConnection, 'id' | 'has_failure_screenshot'>,
): { path: string } | null {
  if (!connection.has_failure_screenshot) return null
  return { path: `/bills/connections/${connection.id}/failure-screenshot` }
}

export function listBillSubaccounts(
  connectionId?: Uuid | null,
  signal?: AbortSignal,
): Promise<BillSubaccount[]> {
  const query = connectionId ? `?connection_id=${connectionId}` : ''
  return api.get<BillSubaccount[]>(`/bills/subaccounts${query}`, undefined, signal)
}

export function listBills(subaccountId: Uuid, signal?: AbortSignal): Promise<Bill[]> {
  return api.get<Bill[]>(`/bills/subaccounts/${subaccountId}/bills`, BILL_SHAPE, signal)
}

/** Read off the subaccount listing: no route takes a series id. */
export function getSeriesBillLink(
  seriesId: Uuid,
  signal?: AbortSignal,
): Promise<SeriesBillLink | null> {
  return listBillSubaccounts(undefined, signal).then((rows) => {
    const linked = rows.find((one) => one.series_id === seriesId)
    if (!linked) return null
    return { series_id: seriesId, subaccount_id: linked.id, connection_id: linked.connection_id }
  })
}

/** Keyed by the series: posting a different subaccount replaces the link. */
export function linkSeriesToBill(seriesId: Uuid, subaccountId: Uuid): Promise<BillLink> {
  return api.post<BillLink>('/bills/links', {
    series_id: seriesId,
    subaccount_id: subaccountId,
  })
}

/** A person's word that the statement was paid where the ledger cannot see. */
export function markStatementPaid(billId: Uuid): Promise<void> {
  return api.post<void>(`/bills/statements/${billId}/paid`, {})
}

export function unlinkSeriesFromBill(seriesId: Uuid): Promise<void> {
  return api.delete<void>(`/bills/links/${seriesId}`)
}

/**
 * The reminder a billed account's statements describe, for the series editor
 * to open on. `account_id` is null when no bank row was found paying the
 * bills; `paid_by_series_id` names a reminder those rows already belong to.
 */
export interface BillReminderSuggestion {
  subaccount_id: Uuid
  connection_id: Uuid
  account_id: Uuid | null
  category_id: Uuid | null
  kind: SeriesKind
  description: string
  display_name: string
  amount: Money
  amount_varies: boolean
  match_criteria: MatchCriteria
  match_amount_min: Money | null
  match_amount_max: Money | null
  recurrence: Recurrence
  start_on: IsoDate
  /** False when the due dates keep no exact rhythm and the schedule is a guess. */
  confident: boolean
  due_dates: number
  first_due: IsoDate
  last_due: IsoDate
  payment_ids: Uuid[]
  paid_by_series_id: Uuid | null
}

const BILL_REMINDER_SHAPE: MoneyShape<BillReminderSuggestion> = {
  amount: 'money',
  match_amount_min: 'money',
  match_amount_max: 'money',
}

/** Null (a 404) when the bills keep no schedule to read one from. */
export async function suggestBillReminder(
  subaccountId: Uuid,
  signal?: AbortSignal,
): Promise<BillReminderSuggestion | null> {
  try {
    const raw = await api.get<Wire<BillReminderSuggestion>>(
      `/bills/subaccounts/${subaccountId}/suggested-reminder`,
      BILL_REMINDER_SHAPE,
      signal,
    )
    return { ...raw, recurrence: recurrenceFromWire(raw.recurrence) }
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null
    throw error
  }
}

/** Not kept past the dialog: a bill pulled meanwhile changes the answer. */
export function useSuggestedReminder(subaccountId: Uuid) {
  return useQuery({
    queryKey: [...BILLS_KEY, 'suggested-reminder', subaccountId],
    queryFn: ({ signal }) => suggestBillReminder(subaccountId, signal),
    gcTime: 0,
    staleTime: 0,
    retry: false,
  })
}

export function describeAutopay(connection: {
  autopay_rule: AutopayRule
  autopay_days: number | null
  autopay_day: number | null
}): string {
  switch (connection.autopay_rule) {
    case 'days_before_due': {
      const days = connection.autopay_days ?? 0
      if (days <= 0) return 'Autopays on the due date'
      return `Autopays ${plural(days, 'day')} before due`
    }
    case 'on_due_date':
      return 'Autopays on the due date'
    case 'day_of_month': {
      const day = connection.autopay_day
      if (day === null) return 'Autopays monthly'
      return `Autopays on the ${ordinal(day)}`
    }
    default:
      return 'No autopay'
  }
}

export const BILLS_KEY = ['bills'] as const

export function useBillConnections() {
  return useQuery({
    queryKey: [...BILLS_KEY, 'connections'],
    queryFn: ({ signal }) => listBillConnections(signal),
    refetchInterval: (query) => (anyPulling(query.state.data) ? PULL_POLL_MS : false),
  })
}

export function useBillSubaccounts(connectionId: Uuid | null) {
  return useQuery({
    queryKey: [...BILLS_KEY, 'subaccounts', connectionId],
    queryFn:
      connectionId === null
        ? skipToken
        : ({ signal }) => listBillSubaccounts(connectionId, signal),
  })
}

export function useAllBillSubaccounts() {
  return useQuery({
    queryKey: [...BILLS_KEY, 'subaccounts', 'all'],
    queryFn: ({ signal }) => listBillSubaccounts(undefined, signal),
  })
}

export function useBills(subaccountId: Uuid | null) {
  return useQuery({
    queryKey: [...BILLS_KEY, 'bills', subaccountId],
    queryFn: subaccountId === null ? skipToken : ({ signal }) => listBills(subaccountId, signal),
  })
}

/**
 * Refreshes the whole `bills` tree: narrower keys would miss a write that
 * changes a connection's row or supersedes a bill already on screen.
 */
// A bill, or a billed account's link to a card, writes that card's statement
// figures, so the accounts refresh too.
const BILL_WRITE: Invalidates = [BILLS_KEY, ACCOUNTS_KEY]

export function useCreateBillConnection() {
  return useInvalidatingMutation(
    (draft: BillConnectionDraft) => api.post<BillConnection>('/bills/connections', draft),
    BILL_WRITE,
  )
}

export function useUpdateBillConnection() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: BillConnectionPatch }) =>
      api.patch<BillConnection>(`/bills/connections/${id}`, patch),
    BILL_WRITE,
  )
}

/**
 * Forgets before it invalidates: a bare invalidation refetches the bills of
 * the subaccounts the delete cascaded away, each of which now answers 404.
 */
export function useDeleteBillConnection() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/bills/connections/${id}`),
    [BILLS_KEY],
    {
      failure: 'That provider was not removed',
      onSuccess: (_removed, id) => forgetBillConnection(client, id),
    },
  )
}

/**
 * Drops one connection and everything cached beneath it. The listings are
 * pruned too, because a row still mounted asks again for a removed list.
 */
export function forgetBillConnection(client: QueryClient, id: Uuid): void {
  const orphaned = new Set<Uuid>()
  for (const [key, rows] of client.getQueriesData<BillSubaccount[]>({
    queryKey: [...BILLS_KEY, 'subaccounts'],
  })) {
    if (!rows) continue
    for (const row of rows) {
      if (row.connection_id === id) orphaned.add(row.id)
    }
    client.setQueryData(
      key,
      rows.filter((row) => row.connection_id !== id),
    )
  }

  const connections = client.getQueryData<BillConnection[]>([...BILLS_KEY, 'connections'])
  if (connections) {
    client.setQueryData(
      [...BILLS_KEY, 'connections'],
      connections.filter((one) => one.id !== id),
    )
  }

  for (const key of [
    ...[...orphaned].map((subaccountId) => [...BILLS_KEY, 'bills', subaccountId]),
    [...BILLS_KEY, 'subaccounts', id],
  ]) {
    void client.cancelQueries({ queryKey: key })
    client.removeQueries({ queryKey: key })
  }
}

export function useUpdateBillSubaccount() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: BillSubaccountPatch }) =>
      api.patch<BillSubaccount>(`/bills/subaccounts/${id}`, patch),
    BILL_WRITE,
  )
}

export function useSeriesBillLink(seriesId: Uuid | null) {
  return useQuery({
    queryKey: [...BILLS_KEY, 'link', seriesId],
    queryFn: seriesId === null ? skipToken : ({ signal }) => getSeriesBillLink(seriesId, signal),
  })
}

/** Invalidates the occurrences too: a bill's figures reach a reminder through them. */
const REMINDER_WRITE: Invalidates = [BILLS_KEY, invalidateOccurrenceWrites]

export function useLinkSeriesToBill() {
  return useInvalidatingMutation(
    ({ seriesId, subaccountId }: { seriesId: Uuid; subaccountId: Uuid }) =>
      linkSeriesToBill(seriesId, subaccountId),
    REMINDER_WRITE,
    { failure: 'The bill link was not saved' },
  )
}

/** Ends a pay-manually reminder. */
export function useMarkStatementPaid() {
  return useInvalidatingMutation((billId: Uuid) => markStatementPaid(billId), REMINDER_WRITE, {
    failure: 'That statement was not marked paid',
  })
}

export function useUnlinkSeriesFromBill() {
  return useInvalidatingMutation(
    (seriesId: Uuid) => unlinkSeriesFromBill(seriesId),
    REMINDER_WRITE,
    { failure: 'The bill link was not saved' },
  )
}

/* --- Bills against the payments that settled them -------------------------- */

/** What matching one billed account's history found. */
export interface BillAccountHistory {
  subaccount_id: Uuid
  label: string
  /** Null when no reminder is linked, which leaves nothing to match. */
  series_id: Uuid | null
  /** Bank rows this run linked to the reminder's slots. */
  matched: number
  /** Bills a bank row's slot settles, and how many of them have a statement. */
  settled: number
  with_statement: number
  /** Bills due by today that no bank row settles. */
  unsettled: number
  /** The bills' usual gap in days when it does not fit the reminder's schedule; 0 when it fits. */
  cadence_gap_days: number
}

export interface BillHistoryResult {
  /** False for a provider whose bills never carry a statement document. */
  statements_offered: boolean
  accounts: BillAccountHistory[]
}

/** A whole login, or one billed account under it. */
export type BillHistoryScope = { connectionId: Uuid } | { subaccountId: Uuid }

export function matchBillHistory(scope: BillHistoryScope): Promise<BillHistoryResult> {
  const path =
    'connectionId' in scope
      ? `/bill-payments/connections/${scope.connectionId}/match`
      : `/bill-payments/subaccounts/${scope.subaccountId}/match`
  return api.post<BillHistoryResult>(path, {})
}

/** Matching links bank rows to reminders and files statements on them. */
export function useMatchBillHistory() {
  return useInvalidatingMutation(matchBillHistory, [
    BILLS_KEY,
    TRANSACTIONS_KEY,
    ['attachments'],
    invalidateOccurrenceWrites,
  ])
}

/** "Linked 3 past payments to bills" and what the bills say beside it. */
export function describeBillHistory(
  result: BillHistoryResult,
  provider: string,
): { title: string; description: string } {
  const linked = result.accounts.filter((one) => one.series_id !== null)
  if (linked.length === 0) {
    return {
      title: 'Nothing to match',
      description: 'Link a reminder to a billed account first: its bills are matched against that reminder’s payments.',
    }
  }
  let matched = 0
  let settled = 0
  let withStatement = 0
  let unsettled = 0
  for (const one of linked) {
    matched += one.matched
    settled += one.settled
    withStatement += one.with_statement
    unsettled += one.unsettled
  }
  const lines: string[] = []
  if (result.statements_offered) {
    lines.push(
      `${plural(withStatement, 'statement')} on past payments, from ${plural(settled, 'bill')} with a matching payment`,
    )
  } else {
    lines.push(
      `${plural(settled, 'bill')} with a matching payment. ${provider} offers no statement documents, so each payment shows its bill without one`,
    )
  }
  if (unsettled > 0) lines.push(`${plural(unsettled, 'bill')} had no matching payment`)
  for (const one of linked) {
    if (one.cadence_gap_days > 0) {
      lines.push(
        `${one.label}’s bills come ${cadenceWords(one.cadence_gap_days)}, which its reminder’s schedule does not match; set the reminder to repeat that often so its payments land on the bills`,
      )
    }
  }
  return {
    title:
      matched === 0
        ? 'No new payments matched'
        : `Linked ${plural(matched, 'past payment')} to bills`,
    description: `${lines.join('. ')}.`,
  }
}

function cadenceWords(days: number): string {
  if (days >= 335 && days <= 395) return 'about once a year'
  if (days >= 28) return `about every ${Math.round(days / 30.44)} months`
  return `about every ${days} days`
}

/** A bill whose reminder slot a bank row settled, with or without a statement. */
export interface SettledBill {
  bill_id: Uuid
  connection_id: Uuid
  /** The provider with the connection's label beside it. */
  provider: string
  due_on: IsoDate
  /** A magnitude. */
  amount_due: Money
  status: BillStatus
  document_id: Uuid | null
}

const SETTLED_BILL_SHAPE: MoneyShape<SettledBill> = { amount_due: 'money' }

export function listBillsSettledBy(transactionId: Uuid, signal?: AbortSignal): Promise<SettledBill[]> {
  return api.get<SettledBill[]>(`/bill-payments/transactions/${transactionId}`, SETTLED_BILL_SHAPE, signal)
}

export const SETTLED_BILLS_KEY = (transactionId: Uuid) =>
  ['attachments', transactionId, 'settled-bills'] as const

export function useBillsSettledBy(transactionId: Uuid) {
  return useQuery({
    queryKey: SETTLED_BILLS_KEY(transactionId),
    queryFn: ({ signal }) => listBillsSettledBy(transactionId, signal),
  })
}

/* --- The engine: a sign-in, a pull, a challenge ---------------------------- */

/**
 * `typed` is the only kind the settings page uses; `live` is opened only by the
 * development routes. A provider with no kinds gets no Connect button.
 */
export type BillSignInKind = 'typed' | 'live'

export interface BillProviderInfo {
  id: BillerId
  name: string
  access: BillerAccess
  sign_in: { kinds: BillSignInKind[]; prompt: string }
  challenges: ChallengeMethod[]
  session_persists: boolean
  /** Zero for never. */
  keepalive_days: number
  reports_autopay: boolean
  has_documents: boolean
}

/** `configured`: this build carries an engine; `healthy`: it answered. */
export interface BillAgentStatus {
  configured: boolean
  healthy: boolean
  error: string
  providers: BillProviderInfo[]
}

/**
 * The merchants' state names verbatim, so one live view serves both, plus
 * `accounts`, where the household picks which billed things matter.
 */
export type BillSignInStateName =
  | 'signing_in'
  | 'email'
  | 'password'
  | 'otp'
  | 'captcha'
  | 'approval'
  | 'interactive'
  | 'accounts'
  | 'signed_in'
  | 'failed'

export interface BillSignInAccount {
  external_id: string
  label: string
  masked_number: string
}

export interface BillSignInState {
  session_id: string
  state: BillSignInStateName
  prompt: string
  /** Base64: a CAPTCHA, or the page itself on a failure; a JPEG of the live page when `interactive`. */
  image: string | null
  error: string
  /** The live view's size, which a click is sent in; zero when there is no live view. */
  width: number
  height: number
  /** Which second factor an `otp` state is, when the page says. */
  method: '' | ChallengeMethod
  /** Set only in the `accounts` state. */
  accounts: BillSignInAccount[] | null
  /** Set on a failed state, since the trail route dies with the reaped session. */
  trail: BillTrailEntry[] | null
}

/** Never what was typed: it must be safe to paste to somebody. */
export interface BillTrailEntry {
  at: string
  /** sign-in, factor, answer, failed; read for an update's line, whose state is page or note. */
  step: string
  state: string
  /** Without its query string. */
  url: string
  title: string
  form: {
    password: boolean
    username: boolean
    otp: boolean
    sign_out_link: boolean
  }
  /** Visible inputs by type. */
  inputs: Record<string, number>
  /** Capped. */
  error: string
  did: BillSignInAction
  /** Factor rounds only. */
  choices?: BillSignInChoice[] | null
  /** "" when none matched. */
  chose?: string
  /** Taken past the page's own checks on the control. */
  forced?: boolean
  /**
   * The page's structure on the round that ended the sign-in, and on each
   * page an update read. No value, no secret-named attribute, no long run of
   * digits.
   */
  snapshot?: string
  note?: string
}

/** The provider's own words; anything about the household in them is stripped before sending. */
export interface BillSignInChoice {
  /** "radio" for a control that selects, "button" or "link" for one that acts. */
  kind: string
  words: string
}

/** `words` are off the provider's own control; nothing typed into the page reaches here. */
export interface BillSignInAction {
  acted: boolean
  /** "button", "input", or "enter" when nothing matched. */
  pressed: string
  words: string
  /** The control was present but not pressable until it became so. */
  waited: boolean
  changed: boolean
  /** A cookie banner declined before typing; "" for none. */
  dismissed?: string
}

export type BillConnectionWithSubaccounts = BillConnection & {
  subaccounts: BillSubaccount[]
}

/** `status` is the pull's outcome, not the transport's: a code request is `challenge`, not a failure. */
export interface BillPullResult {
  status: 'ok' | 'challenge' | 'needs_sign_in' | 'failed'
  new: number
  amended: number
  unchanged: number
  /** Statements stored, not bills. */
  documents: number
  challenge: BillChallenge | null
  error: string
  notes: string[]
  /** The pull stopped on a page, and `billFailureScreenshot` names where it is served. */
  has_failure_screenshot: boolean
}

/** Finding no hold to release is an ordinary answer, not a failure. */
export interface BillBrowserRelease {
  released: boolean
  sessions: number
  /** A lock file on the profiles volume, left by a process that died. */
  lock: boolean
  singleton: boolean
  message: string
}

export function getBillAgent(signal?: AbortSignal): Promise<BillAgentStatus> {
  return api.get<BillAgentStatus>('/bills/agent', undefined, signal)
}

/** '' for none or not sure. Shared by bill connections and shop accounts. */
export type SecondFactor = '' | 'email' | 'totp'

/** The password reaches the agent's browser and stops there. */
export interface BillTypedSignIn {
  username: string
  password: string
  second_factor?: SecondFactor
  site?: string
  /** Sealed with the password so the agent can mint its own codes. */
  totp_secret?: string
}

export function startBillSignIn(
  connectionId: Uuid,
  credentials: BillTypedSignIn,
): Promise<BillSignInState> {
  return api.post<BillSignInState>(`/bills/connections/${connectionId}/sign-in`, {
    mode: 'typed',
    ...credentials,
  })
}

/** Runs the last sign-in that did not land again; a 409 means what was typed into it is no longer held. */
export function retryBillSignIn(connectionId: Uuid): Promise<BillSignInState> {
  return api.post<BillSignInState>(`/bills/connections/${connectionId}/sign-in/retry`)
}

export interface BillMailedCode extends BillSignInState {
  mailed_code_found: boolean
}

/** Held open by the server for a few minutes; a 409 means no mailbox is connected. */
export function waitForMailedBillCode(connectionId: Uuid, session: string): Promise<BillMailedCode> {
  return api.post<BillMailedCode>(`/bills/connections/${connectionId}/sign-in/${session}/mailed-code`)
}

/** Never toasts: a code that did not arrive, or no mailbox to read it from, leaves the person to type it. */
export function useMailedBillCode() {
  return useMutation({
    meta: { failure: false },
    mutationFn: ({ connectionId, session }: { connectionId: Uuid; session: string }) =>
      waitForMailedBillCode(connectionId, session),
  })
}

export function getBillSignInState(
  connectionId: Uuid,
  session: string,
  signal?: AbortSignal,
): Promise<BillSignInState> {
  return api.get<BillSignInState>(
    `/bills/connections/${connectionId}/sign-in/${session}`,
    undefined,
    signal,
  )
}

/** The trail a connection kept from its last update or sign-in that never landed. */
export function fetchBillTrail(connectionId: Uuid): Promise<BillTrailEntry[]> {
  return api.get<BillTrailEntry[]>(`/bills/connections/${connectionId}/trail`)
}

export function fetchBillSignInTrail(
  connectionId: Uuid,
  session: string,
): Promise<BillTrailEntry[]> {
  return api.get<BillTrailEntry[]>(
    `/bills/connections/${connectionId}/sign-in/${session}/trail`,
  )
}

export function answerBillSignIn(
  connectionId: Uuid,
  session: string,
  code: string,
): Promise<BillSignInState> {
  return api.post<BillSignInState>(
    `/bills/connections/${connectionId}/sign-in/${session}/answer`,
    { code },
  )
}

/** What a person did in the live view, in the picture's own pixels. */
export interface BillSignInInput {
  type: 'click'
  x: number
  y: number
}

export function sendBillSignInInput(
  connectionId: Uuid,
  session: string,
  events: BillSignInInput[],
): Promise<unknown> {
  return api.post(`/bills/connections/${connectionId}/sign-in/${session}/input`, { events })
}

export function completeBillSignIn(
  connectionId: Uuid,
  session: string,
): Promise<BillConnectionWithSubaccounts> {
  return api.post<BillConnectionWithSubaccounts>(
    `/bills/connections/${connectionId}/sign-in/${session}/complete`,
  )
}

/**
 * The agent holds one browser per connection and refuses a second, so a
 * dismissed dialog must release it. Fire-and-forget.
 */
export function cancelBillSignIn(connectionId: Uuid, session: string): Promise<unknown> {
  return api.delete(`/bills/connections/${connectionId}/sign-in/${session}`)
}

export function forgetBillSession(connectionId: Uuid): Promise<BillConnection> {
  return api.delete<BillConnection>(`/bills/connections/${connectionId}/session`)
}

/** Drops the kept password, and the session with it. */
export function forgetBillCredential(connectionId: Uuid): Promise<BillConnection> {
  return api.delete<BillConnection>(`/bills/connections/${connectionId}/credential`)
}

/**
 * Releases only the held sign-in and any stale profile lock; the session,
 * password and profile stay.
 */
export function releaseBillBrowser(connectionId: Uuid): Promise<BillBrowserRelease> {
  return api.delete<BillBrowserRelease>(`/bills/connections/${connectionId}/browser`)
}

export function pullBillConnection(connectionId: Uuid): Promise<BillPullResult> {
  return api.post<BillPullResult>(`/bills/connections/${connectionId}/pull`)
}

export function listBillChallenges(
  state: 'waiting' | '' = 'waiting',
  signal?: AbortSignal,
): Promise<BillChallenge[]> {
  return api.get<BillChallenge[]>(
    `/bills/challenges${state === '' ? '' : `?state=${state}`}`,
    undefined,
    signal,
  )
}

export function getBillChallenge(challengeId: Uuid, signal?: AbortSignal): Promise<BillChallenge> {
  return api.get<BillChallenge>(`/bills/challenges/${challengeId}`, undefined, signal)
}

/** The engine resumes the parked pull, so this answers the pull's result. */
export function answerBillChallenge(challengeId: Uuid, code: string): Promise<BillPullResult> {
  return api.post<BillPullResult>(`/bills/challenges/${challengeId}/answer`, { code })
}

/** What needs doing comes before what happened. */
export function describePullStatus(
  connection: Pick<
    BillConnection,
    | 'biller'
    | 'connected'
    | 'needs_sign_in'
    | 'credential_source'
    | 'sign_in_paused'
    | 'last_pull_status'
    | 'last_pull_error'
    | 'last_pulled_at'
    | 'pull_enabled'
    | 'pulling'
  >,
  now: Date = new Date(),
): string {
  if (connection.pulling) return 'Fetching bills now…'
  const because =
    connection.last_pull_error === ''
      ? ''
      : ` ${explainConnectorFailure(connection.last_pull_error, billerName(connection.biller)).message}`
  if (connection.sign_in_paused === 'code_needed')
    return `${billerName(connection.biller)} asked for a code. Automatic updates wait until you sign in.`
  if (connection.sign_in_paused === 'page_check')
    return `${billerName(connection.biller)} showed a check only a person can tick. Automatic updates wait until you sign in.`
  if (connection.needs_sign_in || connection.last_pull_status === 'needs_sign_in') {
    if (connection.credential_source === 'stored' && connection.sign_in_paused === '')
      return 'Session expired; the kept password signs in at the next update'
    return `Needs a sign-in.${because}`
  }
  if (connection.last_pull_status === 'challenge')
    return 'A code is needed to finish the last update'
  if (connection.last_pull_status === 'failed') return `Last update failed.${because}`
  if (connection.last_pull_status === 'sign_in_failed')
    return `The last sign-in did not finish.${because}`
  if (!connection.connected) return 'Not connected'
  if (connection.last_pulled_at === null) return 'Connected; not updated yet'
  const when = `Updated ${timeAgo(connection.last_pulled_at, 'long', now)}`
  return connection.pull_enabled ? when : `${when}; daily updates are off`
}

export function describePullResult(result: BillPullResult, provider = 'The provider'): string {
  if (result.status === 'challenge') return 'A code is needed to finish this update'
  if (result.status === 'needs_sign_in')
    return result.error === ''
      ? 'Sign in again before this one can update'
      : `Sign in again. ${explainConnectorFailure(result.error, provider).message}`
  if (result.status === 'failed')
    return result.error === ''
      ? 'The update failed'
      : `The update failed. ${explainConnectorFailure(result.error, provider).message}`

  const parts: string[] = []
  if (result.new > 0) parts.push(plural(result.new, 'new bill'))
  if (result.amended > 0) parts.push(`${result.amended} amended`)
  if (result.documents > 0) parts.push(plural(result.documents, 'statement'))
  if (parts.length > 0) return parts.join(', ')
  if (result.unchanged > 0) return `Nothing new; ${plural(result.unchanged, 'bill')} already on file`
  return 'Nothing new'
}

/** The provider's own wording wins; the fallbacks are for a module that reported only a method. */
export function challengeInstructions(method: ChallengeMethod, prompt: string): string {
  const said = prompt.trim()
  if (said !== '') return said
  switch (method) {
    case 'totp':
      return 'Enter the code your authenticator app is showing for this provider.'
    case 'sms':
      return 'Enter the code the provider just sent by text.'
    case 'email':
      return 'Enter the code the provider just emailed.'
    case 'push':
      return 'Approve the sign-in on your phone, then continue.'
    case 'captcha':
      return 'Type the characters in the picture.'
  }
}

const AGENT_KEY = [...BILLS_KEY, 'agent'] as const
const CHALLENGES_KEY = [...BILLS_KEY, 'challenges'] as const

export function useBillAgent() {
  return useQuery({
    queryKey: AGENT_KEY,
    queryFn: ({ signal }) => getBillAgent(signal),
    staleTime: 60_000,
  })
}

export function billProviderOf(
  agent: BillAgentStatus | undefined,
  biller: BillerId,
): BillProviderInfo | null {
  return agent?.providers.find((one) => one.id === biller) ?? null
}

/**
 * Outside the `bills` tree on purpose: every bills write invalidates that tree,
 * and a just-completed session answers 404.
 */
export function billSignInKey(connectionId: Uuid, session: string | null) {
  return ['bills-sign-in', connectionId, session] as const
}

/** Polls only while waiting on the provider, not on the person. */
export function useBillSignInStatus(connectionId: Uuid, session: string | null) {
  return useQuery({
    queryKey: billSignInKey(connectionId, session),
    queryFn:
      session === null
        ? skipToken
        : ({ signal }) => getBillSignInState(connectionId, session, signal),
    refetchInterval: (query) => {
      const state = query.state.data?.state
      if (state === undefined) return 1_500
      return state === 'signing_in' || state === 'approval' || state === 'interactive'
        ? 1_500
        : false
    },
    // staleTime, not gcTime, is zero: an answer is written into this entry
    // before the dialog's next render observes it.
    staleTime: 0,
  })
}

/** A click in the live view of a sign-in waiting on a check only a person can tick. */
export function useBillSignInInput() {
  return useMutation({
    meta: { failure: 'The click did not reach the sign-in page' },
    mutationFn: ({
      connectionId,
      session,
      events,
    }: {
      connectionId: Uuid
      session: string
      events: BillSignInInput[]
    }) => sendBillSignInInput(connectionId, session, events),
  })
}

export function useBillPullAfterSignIn(connectionId: Uuid, session: string | null) {
  return usePullAfterSignIn(
    ['bills-sign-in', connectionId, session, 'pull'],
    session === null ? null : (signal) => getBillConnection(connectionId, signal),
  )
}

export function useRetryBillSignIn() {
  return useMutation({
    meta: { failure: 'The sign-in could not be retried' },
    mutationFn: (connectionId: Uuid) => retryBillSignIn(connectionId),
  })
}

export function useStartBillSignIn() {
  return useMutation({
    meta: { failure: 'The sign-in did not work' },
    mutationFn: ({
      connectionId,
      credentials,
    }: {
      connectionId: Uuid
      credentials: BillTypedSignIn
    }) => startBillSignIn(connectionId, credentials),
  })
}

export function useAnswerBillSignIn() {
  return useMutation({
    meta: { failure: 'The sign-in did not work' },
    mutationFn: ({
      connectionId,
      session,
      code,
    }: {
      connectionId: Uuid
      session: string
      code: string
    }) => answerBillSignIn(connectionId, session, code),
  })
}

export function useCompleteBillSignIn() {
  return useInvalidatingMutation(
    ({ connectionId, session }: { connectionId: Uuid; session: string }) =>
      completeBillSignIn(connectionId, session),
    [BILLS_KEY],
    { failure: 'The sign-in did not work' },
  )
}

export function useForgetBillSession() {
  return useInvalidatingMutation(
    (connectionId: Uuid) => forgetBillSession(connectionId),
    BILL_WRITE,
    { failure: 'That sign-in was not forgotten' },
  )
}

export function useForgetBillCredential() {
  return useInvalidatingMutation(
    (connectionId: Uuid) => forgetBillCredential(connectionId),
    BILL_WRITE,
    { failure: 'That password was not forgotten' },
  )
}

export function useReleaseBillBrowser() {
  return useInvalidatingMutation(
    (connectionId: Uuid) => releaseBillBrowser(connectionId),
    BILL_WRITE,
    { failure: 'The sign-in did not work' },
  )
}

/** Invalidates the occurrences too: a pulled bill moves a reminder's date and figure. */
export function usePullBillConnection() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    (connectionId: Uuid) => pullBillConnection(connectionId),
    REMINDER_WRITE,
    {
      failure: false,
      onMutate: (connectionId) => startPulling(client, [...BILLS_KEY, 'connections'], connectionId),
    },
  )
}

/** Polled: the scheduler raises challenges, not this screen. */
export function useBillChallenges() {
  return useQuery({
    queryKey: [...CHALLENGES_KEY, 'waiting'],
    queryFn: ({ signal }) => listBillChallenges('waiting', signal),
    refetchInterval: 60_000,
  })
}

/** Polls for push approval, which nothing in the dialog observes. */
export function useBillChallenge(challengeId: Uuid | null, polling = false) {
  return useQuery({
    queryKey: [...CHALLENGES_KEY, challengeId],
    queryFn:
      challengeId === null
        ? skipToken
        : ({ signal }) => getBillChallenge(challengeId, signal),
    refetchInterval: polling ? 3_000 : false,
  })
}

export function useAnswerBillChallenge() {
  return useInvalidatingMutation(
    ({ challengeId, code }: { challengeId: Uuid; code: string }) =>
      answerBillChallenge(challengeId, code),
    REMINDER_WRITE,
    { failure: false },
  )
}

