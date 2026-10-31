/**
 * Sign-ins that outlive the page that started them. A provider sign-in can
 * take minutes, so it lives in the shell and a dialog is only a view of one:
 * minimized, it keeps its session, its poll and its form, and a toast stands in
 * for it. Everything here is a pure function of the list.
 */

import { billerName } from '@/lib/billers'
import {
  billFailureScreenshot,
  type BillConnection,
  type BillProviderInfo,
} from '@/lib/clients/bills'
import type { EmailConnection } from '@/lib/clients/email'
import { merchantFailureScreenshot, type MerchantAccount } from '@/lib/clients/merchant'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'

/** What is being signed in to, as the dialog that drives it needs it. */
export type SignInTarget =
  | {
      kind: 'bill'
      connection: BillConnection
      provider: BillProviderInfo | null
      /** Start at once from what the server holds of the last sign-in, rather than at the form. */
      retry?: boolean
    }
  | { kind: 'merchant'; merchant: MerchantId; account: MerchantAccount | null }
  | { kind: 'mailbox'; connection: EmailConnection }

/**
 * Where a sign-in stands, from the toast's side.
 *
 * `form` is nothing running yet: the fields, or a refusal shown over them.
 * `failed` is the same form after a sign-in that went wrong, which only
 * matters to a toast — a dialog on screen shows the failure itself. The three
 * after the sign-in are the first pull it started: running, done, and stopped.
 */
export type SignInPhase =
  | 'form'
  | 'working'
  | 'code'
  | 'approval'
  | 'failed'
  | 'fetching'
  | 'finished'
  | 'stopped'

export interface SignInTask {
  id: string
  target: SignInTarget
  /** The company, as the toast names it. */
  name: string
  minimized: boolean
  phase: SignInPhase
  /** The dialog's own sentence for where it stands, when it has a better one than the default. */
  note: string
}

export type SignInAction =
  | { type: 'open'; id: string; target: SignInTarget }
  | { type: 'minimize'; id: string }
  | { type: 'restore'; id: string }
  | { type: 'phase'; id: string; phase: SignInPhase; note: string }
  | { type: 'dismiss'; id: string }

/** The company a target signs in to. */
function targetName(target: SignInTarget): string {
  switch (target.kind) {
    case 'bill':
      return target.provider?.name ?? billerName(target.connection.biller)
    case 'merchant':
      return MERCHANTS[target.merchant].name
    case 'mailbox':
      return target.connection.label
  }
}

/**
 * Which login a target is, or null for one that has none yet — a shop account
 * the dialog is about to create. Two sign-ins to one login is one browser
 * profile twice, which the server refuses, so opening one that is already
 * under way brings the running one back instead.
 */
function targetKey(target: SignInTarget): string | null {
  switch (target.kind) {
    case 'bill':
      return `bill:${target.connection.id}`
    case 'merchant':
      return target.account === null ? null : `merchant:${target.merchant}:${target.account.id}`
    case 'mailbox':
      return `mailbox:${target.connection.id}`
  }
}

/**
 * Only one dialog is on screen at a time, so bringing one up puts away
 * whatever else was up. One that had not started yet is simply closed: there is
 * nothing running for a toast to stand in for.
 */
function putAwayOthers(tasks: SignInTask[], keep: string): SignInTask[] {
  return tasks.flatMap((task) => {
    if (task.id === keep || task.minimized) return [task]
    if (task.phase === 'form') return []
    return [{ ...task, minimized: true }]
  })
}

export function signInReducer(tasks: SignInTask[], action: SignInAction): SignInTask[] {
  switch (action.type) {
    case 'open': {
      const key = targetKey(action.target)
      const running = key === null ? undefined : tasks.find((task) => targetKey(task.target) === key)
      if (running) {
        return putAwayOthers(tasks, running.id).map((task) =>
          task.id === running.id ? { ...task, minimized: false } : task,
        )
      }
      const opened: SignInTask = {
        id: action.id,
        target: action.target,
        name: targetName(action.target),
        minimized: false,
        phase: 'form',
        note: '',
      }
      return [...putAwayOthers(tasks, action.id), opened]
    }
    case 'minimize':
      return tasks.map((task) => (task.id === action.id ? { ...task, minimized: true } : task))
    case 'restore':
      return putAwayOthers(tasks, action.id).map((task) =>
        task.id === action.id ? { ...task, minimized: false } : task,
      )
    case 'phase':
      return tasks.map((task) =>
        task.id === action.id ? { ...task, phase: action.phase, note: action.note } : task,
      )
    case 'dismiss':
      return tasks.filter((task) => task.id !== action.id)
  }
}

/** What a hosted dialog is handed besides its own target. */
export interface SignInViewProps {
  /** Put away: shown as a toast, and still running. */
  minimized: boolean
  onMinimize: () => void
  /** Where it stands, for the toast; `note` replaces the toast's default sentence. */
  onPhase: (phase: SignInPhase, note: string) => void
  /** Finished with: the task goes, toast and all. */
  onClose: () => void
}

/**
 * The phase a provider's sign-in step is in, for a dialog that has one.
 *
 * `pending` is a call to the server in flight, which is the server working
 * whatever the step said before it. A list of accounts waits on a press, so it
 * is asked for like an approval rather than shown as work nobody is doing. A
 * state the dialog cannot draw is the failure the dialog shows it as.
 */
export function phaseOfStep(state: string | null, pending: boolean): SignInPhase {
  if (pending) return 'working'
  switch (state) {
    case null:
      return 'form'
    case 'otp':
    case 'captcha':
    case 'interactive':
      return 'code'
    case 'approval':
    case 'accounts':
      return 'approval'
    case 'signing_in':
    case 'signed_in':
      return 'working'
    default:
      return 'failed'
  }
}

/** Something is running that a person can walk away from. */
export function canMinimize(phase: SignInPhase): boolean {
  return phase === 'working' || phase === 'code' || phase === 'approval' || phase === 'fetching'
}

/**
 * What leaving a dialog does.
 *
 * Closing (the ×, Escape, a click beside the dialog, a Close button) never
 * gives up a running sign-in: it becomes a toast, so a click a little wide of
 * the dialog cannot cost a texted code. With nothing running, closing
 * dismisses.
 *
 * Cancelling is the one explicit way to give a sign-in up. Once the session is
 * kept, the pull it started runs on the server regardless, so cancelling only
 * dismisses.
 */
export function leaving(
  phase: SignInPhase,
  how: 'close' | 'cancel',
): { cancel: boolean; then: 'minimize' | 'dismiss' } {
  if (how === 'close' && canMinimize(phase)) return { cancel: false, then: 'minimize' }
  if (phase === 'fetching' || phase === 'finished' || phase === 'stopped')
    return { cancel: false, then: 'dismiss' }
  return { cancel: true, then: 'dismiss' }
}

/** What the button that gives a sign-in up says: plainer while nothing has started. */
export function cancelLabel(phase: SignInPhase): string {
  return canMinimize(phase) ? 'Cancel sign-in' : 'Cancel'
}

export type PillTone = 'working' | 'attention' | 'success' | 'failure'

export interface Pill {
  tone: PillTone
  text: string
  /** The spinner, for a toast that is waiting on the server rather than on a person. */
  spinning: boolean
}

/** What the pull after a sign-in fetches: bills, or a shop's orders. */
function fetches(target: SignInTarget): string {
  switch (target.kind) {
    case 'bill':
      return 'bills'
    case 'merchant':
      return MERCHANTS[target.merchant].nounPlural
    case 'mailbox':
      return 'mail'
  }
}

/** What the toast says for a minimized sign-in. */
export function pillOf(task: SignInTask): Pill {
  const { name, note } = task
  switch (task.phase) {
    case 'working':
      return { tone: 'working', text: note || `${name}: signing in…`, spinning: true }
    case 'code':
      return { tone: 'attention', text: note || `${name} needs a code`, spinning: false }
    case 'approval':
      return { tone: 'attention', text: note || `${name}: approve on your phone`, spinning: false }
    case 'fetching':
      return {
        tone: 'working',
        text: note || `Signed in to ${name} — fetching ${fetches(task.target)}…`,
        spinning: true,
      }
    case 'finished':
      return { tone: 'success', text: note || `${name}: up to date`, spinning: false }
    case 'stopped':
      return { tone: 'failure', text: note || `${name}: the update stopped`, spinning: false }
    case 'failed':
      return { tone: 'failure', text: note || `${name}: sign-in failed`, spinning: false }
    case 'form':
      return { tone: 'attention', text: `${name}: sign-in not started`, spinning: false }
  }
}

/** The toasts, in the order the sign-ins were started. */
export function trayOf(tasks: readonly SignInTask[]): SignInTask[] {
  return tasks.filter((task) => task.minimized)
}

/**
 * How long a minimized toast stays once its sign-in is over, or null for one
 * that stays until somebody clicks it. Only good news leaves on its own: a
 * failure that faded before anybody looked is a failure nobody saw.
 */
export const PILL_CLEARS_AFTER_MS = 6_000

export function clearsAfter(task: SignInTask): number | null {
  return task.minimized && task.phase === 'finished' ? PILL_CLEARS_AFTER_MS : null
}

/**
 * How the pull a sign-in started ended, read off the row it wrote to: null
 * while it is still running.
 */
export interface PullOutcome {
  ok: boolean
  note: string
  /** The pull parked a code request, which the sign-in dialog answers itself. */
  asksForCode?: boolean
  /** The page the pull stopped on, when one was kept. */
  screenshot?: { path: string }
}

export function billPullOutcome(
  connection: Pick<BillConnection, 'id' | 'pulling' | 'last_pull_status' | 'has_failure_screenshot'>,
  name: string,
  describe: () => string,
): PullOutcome | null {
  if (connection.pulling) return null
  switch (connection.last_pull_status) {
    case 'ok':
      return { ok: true, note: `Fetched your bills from ${name}.` }
    case '':
      return { ok: true, note: `Signed in to ${name}.` }
    case 'challenge':
      return {
        ok: false,
        note: `${name} asked for a code to finish fetching bills.`,
        asksForCode: true,
      }
    default:
      return {
        ok: false,
        note: describe(),
        screenshot: billFailureScreenshot(connection) ?? undefined,
      }
  }
}

export function merchantPullOutcome(
  account: Pick<MerchantAccount, 'id' | 'pulling' | 'last_sync_status' | 'has_failure_screenshot'>,
  merchant: MerchantId,
  describe: () => string,
): PullOutcome | null {
  if (account.pulling) return null
  const { name, nounPlural } = MERCHANTS[merchant]
  switch (account.last_sync_status) {
    case 'ok':
      return { ok: true, note: `Fetched your ${nounPlural} from ${name}.` }
    case '':
      return { ok: true, note: `Signed in to ${name}.` }
    default:
      return {
        ok: false,
        note: describe(),
        screenshot: merchantFailureScreenshot(merchant, account) ?? undefined,
      }
  }
}
