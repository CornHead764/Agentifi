/**
 * How a login's second factor is answered. The choice is kept on the
 * connection; an authenticator key is sealed beside the password.
 */

import type { SecondFactor } from '@/lib/clients/bills'

import { authKeyProblem, normalizeAuthKey } from '../bills/authKey'

/** A select item cannot carry an empty value, so "none" stands for the wire's "". */
export type SecondFactorChoice = 'none' | 'email' | 'totp'

export const SECOND_FACTOR_CHOICES: readonly { value: SecondFactorChoice; label: string }[] = [
  { value: 'none', label: 'None / not sure' },
  { value: 'email', label: 'Code sent by e-mail' },
  { value: 'totp', label: 'Authenticator app (setup key)' },
]

/**
 * The login's kept choice, otherwise the authenticator at a provider known to
 * ask for one.
 */
export function initialSecondFactor(
  kept: SecondFactor | null | undefined,
  knownAuthenticator: boolean,
): SecondFactorChoice {
  if (kept === 'email' || kept === 'totp') return kept
  return knownAuthenticator ? 'totp' : 'none'
}

/** A select's value as a choice; anything it does not know is none. */
export function secondFactorChoice(value: string): SecondFactorChoice {
  return value === 'email' || value === 'totp' ? value : 'none'
}

/** The wire's word for a choice. */
export function secondFactorOf(choice: SecondFactorChoice): SecondFactor {
  return choice === 'none' ? '' : choice
}

/** Whether the setup key's field is drawn. */
export function asksForKey(choice: SecondFactorChoice): boolean {
  return choice === 'totp'
}

/** The choice always, and the key only for the authenticator. */
export function secondFactorRequest(
  choice: SecondFactorChoice,
  authKey: string,
): { second_factor: SecondFactor; totp_secret?: string } {
  const key = asksForKey(choice) ? normalizeAuthKey(authKey) : ''
  return { second_factor: secondFactorOf(choice), ...(key === '' ? {} : { totp_secret: key }) }
}

/** What is wrong with the key as it stands, in the household's words, or null. */
export function secondFactorProblem(choice: SecondFactorChoice, authKey: string): string | null {
  if (!asksForKey(choice)) return null
  return authKeyProblem(authKey)
}

/** E-mail chosen but the space has no mailbox; unknown (still loading) says nothing. */
export function mailboxMissing(choice: SecondFactorChoice, hasMailbox: boolean | undefined): boolean {
  return choice === 'email' && hasMailbox === false
}

/** Whether any of a space's mailboxes is one a code could be read from. */
export function hasReadableMailbox(
  connections: readonly { enabled: boolean; connected: boolean }[] | undefined,
): boolean | undefined {
  if (connections === undefined) return undefined
  return connections.some((one) => one.enabled && one.connected)
}

/**
 * What a card says about a kept password: the key beside it, or the mailbox
 * that answers its codes, or the password alone.
 */
export function keptPasswordFact(login: { has_totp: boolean; second_factor?: SecondFactor }): string {
  if (login.has_totp) return 'Password and authenticator key kept, encrypted, so updates sign in on their own.'
  if (login.second_factor === 'email')
    return 'Password kept, encrypted; codes are read from the billing mailbox, so updates sign in on their own.'
  return 'Password kept, encrypted, so updates sign in on their own.'
}
