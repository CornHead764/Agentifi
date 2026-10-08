/**
 * What the match screen chooses for a bank account before anybody does, and
 * the line that says why.
 */

import type { LinkChoice, LinkTarget, RemoteAccount } from '@/lib/clients/connections'

export const NEW_ACCOUNT = 'new'
export const IGNORE = 'ignore'

/** What Finish sends for one bank account: a local account's id, NEW_ACCOUNT or IGNORE. */
export function linkChoice(externalId: string, value: string): LinkChoice {
  if (value === IGNORE) return { external_id: externalId, action: 'ignore' }
  if (value === NEW_ACCOUNT) return { external_id: externalId, action: 'create' }
  return { external_id: externalId, action: 'link', account_id: value }
}

/** One local account cannot receive two feeds, so one already paired is not offered. */
export function usable(target: LinkTarget, remote: RemoteAccount): boolean {
  return target.linked_to === '' || target.id === remote.linked_account_id
}

/**
 * What a bank account pairs with until somebody chooses: the pairing already
 * made, else the likely match while no other bank account holds it, else a
 * new account.
 */
export function pairing(remote: RemoteAccount, local: readonly LinkTarget[]): string {
  if (remote.linked_account_id !== null) return remote.linked_account_id
  const likely = local.find((target) => target.id === remote.likely)
  return likely !== undefined && usable(likely, remote) ? likely.id : NEW_ACCOUNT
}

/**
 * Every bank account's choice, by external id: the household's where it made
 * one, else pairing(). A default that names an account the household chose
 * for another bank account falls back to a new one.
 */
export function resolveChoices(
  remote: readonly RemoteAccount[],
  local: readonly LinkTarget[],
  chosen: Readonly<Record<string, string>>,
): Record<string, string> {
  const values: Record<string, string> = {}
  const taken = new Set<string>()
  for (const one of remote) {
    const value = chosen[one.external_id]
    if (value === undefined) continue
    values[one.external_id] = value
    taken.add(value)
  }
  for (const one of remote) {
    if (values[one.external_id] !== undefined) continue
    const value = pairing(one, local)
    values[one.external_id] = taken.has(value) && value !== NEW_ACCOUNT ? NEW_ACCOUNT : value
    taken.add(value)
  }
  return values
}

/**
 * Whether a bank account's select offers one of the household's accounts:
 * never one with another feed, nor one another row on the screen has chosen.
 */
export function offered(
  target: LinkTarget,
  remote: RemoteAccount,
  values: Readonly<Record<string, string>>,
): boolean {
  if (!usable(target, remote)) return false
  return !Object.entries(values).some(
    ([externalId, value]) => externalId !== remote.external_id && value === target.id,
  )
}

/**
 * The line under an account's choice: that it is paired or will be made new,
 * or, while nobody has chosen, why the screen chose what it did. `choice` is
 * what the household picked on the screen, if anything, and `value` what
 * resolveChoices settled on.
 */
export function matchNote(
  remote: RemoteAccount,
  local: readonly LinkTarget[],
  choice?: string,
  value?: string,
): string {
  if (choice === NEW_ACCOUNT) return 'A new account is made when you finish'
  if (choice === IGNORE) return 'Not imported'
  if (choice !== undefined || remote.linked_account_id !== null) return 'Paired'
  if (value === NEW_ACCOUNT && pairing(remote, local) !== NEW_ACCOUNT) {
    return 'Its likely match is chosen for another account'
  }
  if (remote.likely !== null && pairing(remote, local) === NEW_ACCOUNT) {
    return 'Its likely match is paired with another account'
  }
  switch (remote.match) {
    case 'number':
      return 'Likely match: same account number'
    case 'name':
      return 'Likely match: same name'
    case 'tie':
      return 'No clear match: more than one account fits as well'
    case 'weak':
      return 'No clear match: only the type, bank or balance is alike'
    case 'none':
      return 'Nothing here resembles it, so a new account is made'
  }
}
