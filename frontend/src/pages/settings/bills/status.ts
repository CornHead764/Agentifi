/** What a connection card offers, decided away from the markup so each rule has a test. */

import type { BillAgentStatus, BillChallenge, BillConnection, BillProviderInfo } from '@/lib/clients/bills'

import { billEngine, engineSentence } from '../connector/engine'
import {
  forgetPasswordLabel,
  signInLabel,
  signInNeed,
  signInPaused,
  type SignInNeed,
} from '../connector/signInNeed'

/**
 * The challenge this connection is waiting on: the live one, since a provider
 * can raise a second request before the first is answered.
 */
export function waitingChallengeFor(
  connectionId: string,
  challenges: readonly BillChallenge[],
): BillChallenge | null {
  let best: BillChallenge | null = null
  for (const challenge of challenges) {
    if (challenge.connection_id !== connectionId) continue
    if (challenge.state !== 'waiting') continue
    if (best === null || challenge.created_at > best.created_at) best = challenge
  }
  return best
}

/** What the connection is waiting on to sign in; see `signInNeed`. */
export function connectionSignInNeed(
  connection: Pick<
    BillConnection,
    'needs_sign_in' | 'last_pull_status' | 'credential_source' | 'sign_in_paused'
  >,
): SignInNeed {
  return signInNeed({
    needsSignIn: connection.needs_sign_in || connection.last_pull_status === 'needs_sign_in',
    paused: connection.sign_in_paused,
    hasPassword: connection.credential_source === 'stored',
  })
}

/** Which dialog the card's own state calls for, and the row behind it. */
export interface CardAction {
  action: 'challenge' | 'connect' | 'none'
  challenge: BillChallenge | null
}

/**
 * The dialog a card opens. A code request outranks a sign-in: a connection
 * stopped for a code is signed in, and a new sign-in would throw that session
 * away. A `challenge` status with no waiting row falls back to the sign-in.
 */
export function cardAction(
  connection: Pick<
    BillConnection,
    | 'id'
    | 'connected'
    | 'needs_sign_in'
    | 'last_pull_status'
    | 'credential_source'
    | 'sign_in_paused'
  >,
  challenges: readonly BillChallenge[],
): CardAction {
  const waiting = waitingChallengeFor(connection.id, challenges)
  if (waiting !== null) return { action: 'challenge', challenge: waiting }
  const need = connectionSignInNeed(connection)
  if (need === 'password-signs-in') return { action: 'none', challenge: null }
  if (signInPaused(need)) return { action: 'connect', challenge: null }
  if (!connection.connected || connection.needs_sign_in) return { action: 'connect', challenge: null }
  if (connection.last_pull_status === 'challenge') return { action: 'connect', challenge: null }
  return { action: 'none', challenge: null }
}

/** The one button a card shows; null for a provider nobody can sign in to. */
export type PrimaryAction = 'challenge' | 'retry' | 'connect' | 'update' | null

/** What waits in a card's menu, in the order it is listed. */
export type MenuAction =
  | 'update'
  | 'connect'
  | 'forget-password'
  | 'forget-session'
  | 'match-history'
  | 'edit'
  | 'website'
  | 'remove'

/**
 * The card's one visible action, and the rest for its menu: a waiting code,
 * then a missing sign-in, otherwise an update (which also signs a lapsed
 * session in with the kept password). A missing sign-in whose last attempt
 * did not land, and whose typing the server still holds, is retried rather
 * than typed again. Signing in again stays in the menu, because a changed
 * password is something the card cannot see coming.
 */
export function connectionActions(
  connection: Pick<
    BillConnection,
    | 'id'
    | 'connected'
    | 'needs_sign_in'
    | 'last_pull_status'
    | 'credential_source'
    | 'sign_in_paused'
    | 'can_retry_sign_in'
  >,
  challenges: readonly BillChallenge[],
  signsIn: boolean,
  hasWebsite: boolean,
): { primary: PrimaryAction; menu: MenuAction[] } {
  const tail: MenuAction[] = hasWebsite
    ? ['match-history', 'edit', 'website', 'remove']
    : ['match-history', 'edit', 'remove']
  if (!signsIn) return { primary: null, menu: tail }
  const intent = cardAction(connection, challenges).action
  const retries =
    connection.can_retry_sign_in && connection.last_pull_status === 'sign_in_failed'
  const primary: PrimaryAction =
    intent === 'challenge'
      ? 'challenge'
      : intent === 'connect'
        ? retries
          ? 'retry'
          : 'connect'
        : 'update'
  const menu: MenuAction[] = []
  // A paused login is still one more try away when somebody asks for it.
  const updates =
    (connection.connected && !connection.needs_sign_in) ||
    signInPaused(connectionSignInNeed(connection))
  if (primary !== 'update' && updates) menu.push('update')
  if (primary !== 'connect') menu.push('connect')
  if (connection.credential_source === 'stored') menu.push('forget-password')
  else if (connection.connected) menu.push('forget-session')
  return { primary, menu: [...menu, ...tail] }
}

/** What a menu entry says. */
export function menuActionLabel(
  action: MenuAction,
  connection: Pick<
    BillConnection,
    | 'connected'
    | 'credential_source'
    | 'has_totp'
    | 'needs_sign_in'
    | 'last_pull_status'
    | 'sign_in_paused'
  >,
  providerName: string,
): string {
  switch (action) {
    case 'update':
      return 'Update now'
    case 'connect':
      return signInLabel(connection.connected, connectionSignInNeed(connection))
    case 'forget-password':
      return forgetPasswordLabel(connection.has_totp)
    case 'forget-session':
      return 'Forget session'
    case 'match-history':
      return 'Match history'
    case 'edit':
      return 'Edit'
    case 'website':
      return `${providerName} website`
    case 'remove':
      return 'Remove'
  }
}

/**
 * Why this connection cannot be signed in to, or null when it can. `undefined`
 * is the engine still being asked, which is not a refusal.
 */
export function signInBlocked(
  agent: BillAgentStatus | undefined,
  provider: BillProviderInfo | null,
): string | null {
  const engine = billEngine(agent)
  if (engine.kind === 'checking') return 'Checking whether this server has a browser engine…'
  const refused = engineSentence(engine)
  if (refused !== null) return refused
  if (provider === null) return 'The engine on this server does not carry this provider.'
  if (provider.sign_in.kinds.length === 0) {
    return `${provider.name} has no sign-in: its bills arrive by email, and its card is for the ones you enter.`
  }
  return null
}

/**
 * Why a scheduled pull will not run although the daily pull is on: a provider
 * that texts a code or wants a tap cannot be pulled while everyone is asleep,
 * so the scheduler skips it unless the household names an hour it is awake.
 */
export function scheduleNote(
  connection: Pick<BillConnection, 'pull_enabled' | 'pull_at'>,
  provider: BillProviderInfo | null,
): string | null {
  if (provider === null || !connection.pull_enabled || connection.pull_at !== null) return null
  const asks = provider.challenges.includes('sms')
    ? 'asks for a text code'
    : provider.challenges.includes('push')
      ? 'asks for a tap on your phone'
      : ''
  if (asks === '') return null
  return `Updated only when you press Update now: ${provider.name} ${asks}, and nobody is awake to answer one at night.`
}
