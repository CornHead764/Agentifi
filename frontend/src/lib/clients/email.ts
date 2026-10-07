/**
 * The watched mailbox — `/email`. Its mail becomes bills, and its one-time
 * codes answer waiting challenges. No secret ever comes back: the IMAP
 * password travels one way and is never held in a query cache. A poll files
 * bills, so every write here invalidates the `bills` tree too.
 */

import { skipToken, useMutation, useQuery } from '@tanstack/react-query'

import { api, type Money, type MoneyShape } from '@/lib/api'
import type { BillerId } from '@/lib/billers'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'

import type { Uuid } from './entities'
import { invalidateOccurrenceWrites } from '@/lib/clients/upcoming/keys'
import { BILLS_KEY } from './bills'
import { timeAgo } from '@/lib/format'

/** `graph` (Office 365) signs in by device code: it refuses a password. */
export type MailboxKind = 'graph' | 'imap'

/** `proposed` went to the assistant; `rule` was claimed by a household mail rule. */
export type MailOutcome = 'bill' | 'otp' | 'unrecognised' | 'proposed' | 'failed' | 'rule'

export interface EmailConnection {
  id: Uuid
  label: string
  kind: MailboxKind
  address: string
  /** `graph` rows only. Not a secret. */
  client_id: string
  tenant: string
  /** `imap` rows only. */
  host: string
  port: number | null
  username: string
  /** Defaulted per kind by the server; may be empty on some rows (see `mailboxFolder`). */
  folder: string
  enabled: boolean
  /** A secret is sealed on the server; never the secret itself. */
  connected: boolean
  last_polled_at: string | null
  last_poll_error: string
  created_at: string
}

/**
 * The server enforces which fields each kind needs: `graph` the client id and
 * tenant, `imap` the host and username. An omitted port defaults to 993.
 */
export interface EmailConnectionDraft {
  label: string
  kind: MailboxKind
  address: string
  client_id?: string
  tenant?: string
  host?: string
  port?: number | null
  username?: string
  folder?: string
}

export type EmailConnectionPatch = Partial<Omit<EmailConnectionDraft, 'kind'>> & {
  enabled?: boolean
}

export interface MailboxDeviceCode {
  session_id: string
  user_code: string
  verification_uri: string
  expires_at: string
}

export type MailboxSignInStateName = 'pending' | 'signed_in' | 'failed' | 'expired'

export interface MailboxSignInState {
  state: MailboxSignInStateName
  error: string
}

/** The outcome counts add up to `read`. */
export interface MailboxPollResult {
  read: number
  bills: number
  rules: number
  otps: number
  unrecognised: number
  proposed: number
  failed: number
  error: string
}

export interface MailboxMessage {
  id: Uuid
  connection_id: Uuid
  /** The mail's own Message-ID, which stops a re-read filing it twice. */
  message_id: string
  received_at: string
  sender: string
  subject: string
  biller: BillerId | ''
  outcome: MailOutcome
  /** Why a message failed, or what the parser noticed and would not guess. */
  note: string
  bill_id: Uuid | null
  document_id: Uuid | null
  rule_id: Uuid | null
  transaction_id: Uuid | null
  /** A rule's bill can land on any provider, so the row names it by this. */
  bill_connection_id: Uuid | null
}

function listEmailConnections(signal?: AbortSignal): Promise<EmailConnection[]> {
  return api.get<EmailConnection[]>('/email/connections', undefined, signal)
}

export function createEmailConnection(draft: EmailConnectionDraft): Promise<EmailConnection> {
  return api.post<EmailConnection>('/email/connections', draft)
}

export function updateEmailConnection(
  id: Uuid,
  patch: EmailConnectionPatch,
): Promise<EmailConnection> {
  return api.patch<EmailConnection>(`/email/connections/${id}`, patch)
}

export function deleteEmailConnection(id: Uuid): Promise<void> {
  return api.delete<void>(`/email/connections/${id}`)
}

/** Empty body: the refresh token lands on the server without passing through the client. */
export function startMailboxDeviceSignIn(id: Uuid): Promise<MailboxDeviceCode> {
  return api.post<MailboxDeviceCode>(`/email/connections/${id}/sign-in`, {})
}

/** The one request here that carries a secret. A refused password answers 422. */
export function signInMailboxWithPassword(id: Uuid, password: string): Promise<EmailConnection> {
  return api.post<EmailConnection>(`/email/connections/${id}/sign-in`, { password })
}

export function getMailboxSignIn(
  id: Uuid,
  session: string,
  signal?: AbortSignal,
): Promise<MailboxSignInState> {
  return api.get<MailboxSignInState>(
    `/email/connections/${id}/sign-in/${session}`,
    undefined,
    signal,
  )
}

/** Drops the sealed token or password. The mailbox row and its log stay. */
export function forgetMailboxSecret(id: Uuid): Promise<EmailConnection> {
  return api.delete<EmailConnection>(`/email/connections/${id}/secret`)
}

export function pollMailbox(id: Uuid): Promise<MailboxPollResult> {
  return api.post<MailboxPollResult>(`/email/connections/${id}/poll`)
}

/** Every mailbox's log, newest first. */
export function listRecentMail(limit = 50, signal?: AbortSignal): Promise<MailboxMessage[]> {
  return api.get<MailboxMessage[]>(`/email/messages?limit=${limit}`, undefined, signal)
}

/** A row can carry an empty folder. */
export function mailboxFolder(connection: Pick<EmailConnection, 'kind' | 'folder'>): string {
  if (connection.folder.trim() !== '') return connection.folder
  return connection.kind === 'imap' ? 'INBOX' : 'Inbox'
}

/** What needs doing comes before what happened. */
export function describePollStatus(
  connection: Pick<
    EmailConnection,
    'kind' | 'folder' | 'enabled' | 'connected' | 'last_polled_at' | 'last_poll_error'
  >,
  now: Date = new Date(),
): string {
  if (!connection.connected) return 'Not connected yet'
  if (connection.last_poll_error !== '')
    return `Could not read the mailbox: ${connection.last_poll_error}`
  const folder = mailboxFolder(connection)
  if (!connection.enabled) return `Reading is off, so nothing is taken from ${folder}`
  if (connection.last_polled_at === null) return `Watching ${folder}; nothing read yet`
  return `Watching ${folder}; read ${timeAgo(connection.last_polled_at, 'long', now)}`
}

export const EMAIL_KEY = ['email'] as const

export function useEmailConnections() {
  return useQuery({
    queryKey: [...EMAIL_KEY, 'connections'],
    queryFn: ({ signal }) => listEmailConnections(signal),
  })
}

export function useRecentMail(connections: readonly EmailConnection[] | undefined) {
  const ids = (connections ?? []).map((one) => one.id)
  return useQuery({
    queryKey: [...EMAIL_KEY, 'messages', 'all', ids],
    queryFn: connections === undefined ? skipToken : ({ signal }) => listRecentMail(50, signal),
  })
}

/** Refreshes the bills as well, because reading the mailbox files them. */
const EMAIL_WRITE: Invalidates = [EMAIL_KEY, BILLS_KEY]

export function useCreateEmailConnection() {
  return useInvalidatingMutation(
    (draft: EmailConnectionDraft) => createEmailConnection(draft),
    EMAIL_WRITE,
  )
}

export function useUpdateEmailConnection() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: EmailConnectionPatch }) => updateEmailConnection(id, patch),
    EMAIL_WRITE,
  )
}

export function useDeleteEmailConnection() {
  return useInvalidatingMutation((id: Uuid) => deleteEmailConnection(id), EMAIL_WRITE, {
    failure: 'That mailbox was not removed',
  })
}

export function useForgetMailboxSecret() {
  return useInvalidatingMutation((id: Uuid) => forgetMailboxSecret(id), EMAIL_WRITE, {
    failure: 'That sign-in was not forgotten',
  })
}

/** Invalidates the occurrences too: a bill read from mail moves a reminder's date and figure. */
export function usePollMailbox() {
  return useInvalidatingMutation(
    (id: Uuid) => pollMailbox(id),
    [EMAIL_KEY, BILLS_KEY, invalidateOccurrenceWrites],
    { failure: false },
  )
}

export function useStartMailboxDeviceSignIn() {
  return useMutation({
    meta: { failure: 'The sign-in did not work' },
    mutationFn: (id: Uuid) => startMailboxDeviceSignIn(id),
  })
}

/** The password is only an argument: never a mutation key, never cached. */
export function useSignInMailboxWithPassword() {
  return useInvalidatingMutation(
    ({ id, password }: { id: Uuid; password: string }) => signInMailboxWithPassword(id, password),
    EMAIL_WRITE,
    { failure: false },
  )
}

/**
 * Outside the `email` tree on purpose: every email write invalidates that tree,
 * and a just-landed session answers 404.
 */
function mailboxSignInKey(id: Uuid, session: string | null) {
  return ['email-sign-in', id, session] as const
}

export function useMailboxSignInStatus(id: Uuid, session: string | null) {
  return useQuery({
    queryKey: mailboxSignInKey(id, session),
    queryFn: session === null ? skipToken : ({ signal }) => getMailboxSignIn(id, session, signal),
    refetchInterval: (query) => {
      const state = query.state.data?.state
      return state === undefined || state === 'pending' ? 3_000 : false
    },
    staleTime: 0,
  })
}

/* --- Mail rules ------------------------------------------------------------
 *
 * An empty string means "not used"; only ids are ever null. Send unused text
 * fields as "" rather than omitting them: a patch that omits a key keeps it.
 */

/** A bill rule posts nothing: the payment arrives on the bank sync and pairs with the bill. */
export type MailRuleAction = 'transaction' | 'bill'

export type MailRuleDirection = 'expense' | 'income'

export interface MailRule {
  id: Uuid
  name: string
  enabled: boolean
  /** An address, `@domain` for anybody at one, or empty for any sender. */
  sender: string
  subject_contains: string
  body_contains: string
  /** The text just before the amount, e.g. `Receipt Total:`. */
  amount_label: string
  /** Or a regular expression with one capture group. Never both. */
  amount_pattern: string
  /** On a bill rule, the due date. */
  date_label: string
  date_pattern: string
  /** On a bill rule, the account number, which picks the billed account. */
  reference_label: string
  reference_pattern: string
  issued_label: string
  issued_pattern: string
  minimum_label: string
  minimum_pattern: string
  /** Fixed text for the payee… */
  payee: string
  /** …or the label whose line carries it — `Your receipt from`. */
  payee_label: string
  /**
   * A transaction rule keeps the lines below the one holding this label in the
   * notes, after the reference — the list of what was bought. Empty for none.
   */
  notes_label: string
  /** The line holding this ends them; empty ends them at the first blank line. */
  notes_end_label: string
  action: MailRuleAction
  /** A transaction rule's; null on a bill rule. */
  account_id: Uuid | null
  category_id: Uuid | null
  direction: MailRuleDirection
  /** Null on a transaction rule. */
  bill_connection_id: Uuid | null
  /** Null lets the account number the rule read choose. */
  bill_subaccount_id: Uuid | null
  /**
   * Also post the same figure as income: a payroll deduction never reaches the
   * bank as a debit, because the deposit is already net of it.
   */
  pad_income: boolean
  /** Where the padding income lands. Null is "the same account". */
  income_account_id: Uuid | null
  income_category_id: Uuid | null
  income_payee: string
  sort_order: number
  created_at: string
}

export type MailRuleDraft = Omit<MailRule, 'id' | 'sort_order' | 'created_at'>

export type MailRulePatch = Partial<MailRuleDraft>

/** Never stored. */
export interface MailSample {
  sender: string
  subject: string
  text: string
}

export interface MailRulePosting {
  account_id: Uuid
  amount: Money
  payee: string
  category_id: Uuid
}

/** `matched` and `error` are separate answers; neither is a failed request. */
export interface MailRuleTrial {
  matched: boolean
  amount: Money | null
  /** On a bill rule, the due date. */
  date: string | null
  reference: string
  payee: string
  /** What the posted rows' notes would say. */
  notes: string
  error: string
  would_post: MailRulePosting[]
  /** The rule files a bill; the fields below are that bill's. */
  would_file: boolean
  issued_on: string | null
  minimum_due: Money | null
  /** The provider's key, or only its last four. */
  account: string
}

const TRIAL_SHAPE: MoneyShape<MailRuleTrial> = {
  amount: 'money',
  minimum_due: 'money',
  would_post: { amount: 'money' },
}

export function listMailRules(signal?: AbortSignal): Promise<MailRule[]> {
  return api.get<MailRule[]>('/email/rules', undefined, signal)
}

export function createMailRule(draft: MailRuleDraft): Promise<MailRule> {
  return api.post<MailRule>('/email/rules', draft)
}

export function updateMailRule(id: Uuid, patch: MailRulePatch): Promise<MailRule> {
  return api.patch<MailRule>(`/email/rules/${id}`, patch)
}

export function deleteMailRule(id: Uuid): Promise<void> {
  return api.delete<void>(`/email/rules/${id}`)
}

/** Stores nothing. */
export function tryMailRule(rule: MailRuleDraft, sample: MailSample): Promise<MailRuleTrial> {
  return api.post<MailRuleTrial>('/email/rules/try', { rule, sample }, TRIAL_SHAPE)
}

/** The id is the log row's own, not the mail's `Message-ID`. */
export function rereadMailboxMessage(
  connectionId: Uuid,
  messageId: Uuid,
): Promise<MailboxMessage> {
  return api.post<MailboxMessage>(
    `/email/connections/${connectionId}/messages/${messageId}/reread`,
  )
}

export function useMailRules() {
  return useQuery({
    queryKey: [...EMAIL_KEY, 'rules'],
    queryFn: ({ signal }) => listMailRules(signal),
  })
}

/** Invalidates the transactions too, because a rule posts them. */
const MAIL_RULE_WRITE: Invalidates = [EMAIL_KEY, TRANSACTIONS_KEY]

export function useCreateMailRule() {
  return useInvalidatingMutation(
    (draft: MailRuleDraft) => createMailRule(draft),
    MAIL_RULE_WRITE,
  )
}

export function useUpdateMailRule() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: MailRulePatch }) => updateMailRule(id, patch),
    MAIL_RULE_WRITE,
  )
}

export function useDeleteMailRule() {
  return useInvalidatingMutation((id: Uuid) => deleteMailRule(id), MAIL_RULE_WRITE, {
    failure: 'That mail rule was not removed',
  })
}

export function useTryMailRule() {
  return useMutation({
    mutationFn: ({ rule, sample }: { rule: MailRuleDraft; sample: MailSample }) =>
      tryMailRule(rule, sample),
  })
}

/** A reread may produce a bill as easily as a transaction, so both trees go. */
export function useRereadMailboxMessage() {
  return useInvalidatingMutation(
    ({ connectionId, messageId }: { connectionId: Uuid; messageId: Uuid }) =>
      rereadMailboxMessage(connectionId, messageId),
    [EMAIL_KEY, BILLS_KEY, invalidateOccurrenceWrites, TRANSACTIONS_KEY],
    { failure: false },
  )
}

/**
 * A draft for the dialog, never saved. Ids are null where the model named
 * nothing the household has; `dropped` lists what did not fit a rule.
 */
export interface MailRuleSuggestion {
  rule: {
    action: MailRuleAction
    bill_connection_id: Uuid | null
    issued_label: string
    issued_pattern: string
    minimum_label: string
    minimum_pattern: string
    /** Not part of the rule: the account link the dialog offers to set on save. */
    statement_account_id: Uuid | null
    name: string
    sender: string
    subject_contains: string
    body_contains: string
    amount_label: string
    amount_pattern: string
    date_label: string
    date_pattern: string
    reference_label: string
    reference_pattern: string
    payee: string
    payee_label: string
    notes_label: string
    notes_end_label: string
    direction: MailRuleDirection
    account_id: Uuid | null
    category_id: Uuid | null
    pad_income: boolean
    income_account_id: Uuid | null
    income_category_id: Uuid | null
    income_payee: string
  }
  dropped: string[]
  sample: MailSample
}

/** Saves nothing. */
export function suggestMailRule(messageId: Uuid): Promise<MailRuleSuggestion> {
  return api.post<MailRuleSuggestion>(`/email/messages/${messageId}/suggest-rule`)
}

/** Its caller's failure toast offers the assistant's setup. */
export function useSuggestMailRule() {
  return useMutation({
    meta: { failure: false },
    mutationFn: (messageId: Uuid) => suggestMailRule(messageId),
  })
}
