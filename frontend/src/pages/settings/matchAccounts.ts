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
 * The line under an account's choice: that it is paired or will be made new,
 * or, while nobody has chosen, why the screen chose what it did. `choice` is
 * what the household picked on the screen, if anything.
 */
export function matchNote(
  remote: RemoteAccount,
  local: readonly LinkTarget[],
  choice?: string,
): string {
  if (choice === NEW_ACCOUNT) return 'A new account is made when you finish'
  if (choice === IGNORE) return 'Not imported'
  if (choice !== undefined || remote.linked_account_id !== null) return 'Paired'
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
