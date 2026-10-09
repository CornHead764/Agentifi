/** What a shop account's row offers, decided away from the markup. */

import type { MerchantAccount } from '@/lib/clients/merchant'
import { plural } from '@/lib/format'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'

import { keptPasswordFact } from '../connector/secondFactor'
import { signInNeed, signInPaused, type SignInNeed } from '../connector/signInNeed'

type NeedFields = 'needs_sign_in' | 'last_sync_status' | 'has_password' | 'sign_in_paused'

/** What the account is waiting on to sign in; see `signInNeed`. */
export function accountSignInNeed(account: Pick<MerchantAccount, NeedFields>): SignInNeed {
  return signInNeed({
    needsSignIn: account.needs_sign_in || account.last_sync_status === 'needs_sign_in',
    paused: account.sign_in_paused,
    hasPassword: account.has_password,
  })
}

/** The one button a row shows; null when there is nothing it can do. */
export type AccountPrimary = 'connect' | 'update' | null

/** What waits in the row's menu, in the order it is listed. */
export type AccountMenuAction =
  | 'update'
  | 'history'
  | 'backfill'
  | 'connect'
  | 'forget-password'
  | 'forget'
  | 'edit'
  | 'remove'

/**
 * What an invoice backfill needs besides orders on file: a browser to print
 * in, and for some merchants a session. Reopening a merchant's own
 * invoice pages needs a working session; laying receipts out asks the
 * merchant nothing, so needs none.
 */
export interface BackfillOffer {
  invoices: 'page' | 'laid-out'
  /** The server has a browser engine, reachable or not. */
  engine: boolean
}

/**
 * The row's one visible action, and the rest for its menu. A sign-in whenever
 * one is missing, except a lapsed session with an unrefused kept password,
 * where the update signs in. Fetching history and backfilling invoices sit in
 * the menu.
 */
export function accountActions(
  account: Pick<MerchantAccount, 'connected' | NeedFields> &
    Partial<Pick<MerchantAccount, 'orders'>>,
  canSignIn: boolean,
  backfill?: BackfillOffer,
): { primary: AccountPrimary; menu: AccountMenuAction[] } {
  const signedIn = account.connected && !account.needs_sign_in
  const need = accountSignInNeed(account)
  const updates = signedIn || (account.connected && need === 'password-signs-in')
  const primary: AccountPrimary = updates ? 'update' : canSignIn ? 'connect' : null
  const menu: AccountMenuAction[] = []
  if (primary !== 'update' && account.connected && signInPaused(need)) menu.push('update')
  if (signedIn) menu.push('history')
  if (
    backfill?.engine &&
    (account.orders ?? 0) > 0 &&
    (backfill.invoices === 'laid-out' || signedIn)
  )
    menu.push('backfill')
  if (canSignIn && primary !== 'connect') menu.push('connect')
  if (account.has_password) menu.push('forget-password')
  if (account.connected) menu.push('forget')
  menu.push('edit', 'remove')
  return { primary, menu }
}

/** Whether the account's last update stopped, as opposed to finishing with a warning. */
export function accountStopped(
  account: Pick<MerchantAccount, 'needs_sign_in' | 'last_sync_status'>,
): boolean {
  return (
    account.needs_sign_in ||
    account.last_sync_status === 'needs_sign_in' ||
    account.last_sync_status === 'failed'
  )
}

/** What the row says about the password; the authenticator key is sealed with it. */
export function passwordFact(
  account: Pick<MerchantAccount, 'connected' | 'has_password' | 'has_totp' | 'second_factor' | NeedFields>,
  shop: string,
): string | null {
  if (!account.has_password) {
    return account.connected
      ? 'Only the signed-in session is kept, so a sign-in is needed whenever it lapses.'
      : null
  }
  const need = accountSignInNeed(account)
  if (need === 'password-refused')
    return `Password kept, encrypted, but ${shop} turned it away at the last update, so it is not tried again until you sign in or press Update now.`
  if (need === 'code-needed')
    return `Password kept, encrypted, but ${shop} asked for a code at the last update, so it is not tried again until you sign in or press Update now.`
  if (need === 'page-check')
    return `Password kept, encrypted, but ${shop} showed a check only a person can tick at the last update, so it is not tried again until you sign in or press Update now.`
  return keptPasswordFact(account)
}

/** The login's email, where the name does not already say it. */
export function signsInAs(account: Pick<MerchantAccount, 'name' | 'email'>): string | null {
  const email = account.email.trim()
  if (email === '' || email.toLowerCase() === account.name.trim().toLowerCase()) return null
  return email
}

/**
 * What the confirm before an invoice backfill says: what fetching them asks
 * of the merchant, how many stored orders lack an invoice, and the button.
 * `count` is undefined while it is on its way or when it could not be had.
 */
export function backfillWords(
  merchant: MerchantId,
  account: string,
  count: number | undefined,
  failed: boolean,
): { title: string; how: string; body: string; submit: string } {
  const { name, noun, nounPlural, invoices } = MERCHANTS[merchant]
  const how =
    invoices === 'page'
      ? `Opens each ${noun}'s invoice page at ${name}, newest first and a few seconds apart, with the session this account is signed in with. It runs in the background and updates wait until it finishes. If ${name} asks to sign in it stops there; run it again afterwards to go on.`
      : `Lays out a receipt for each ${noun} on file from the lines already read, and asks ${name} nothing.`
  const body =
    count === undefined
      ? failed
        ? `The ${nounPlural} without an invoice could not be counted.`
        : 'Counting…'
      : count === 0
        ? invoices === 'page'
          ? `Every ${noun} on file has its invoice, or has come back without one three times.`
          : `Every ${noun} on file that a receipt can be laid out for already has one.`
        : `${plural(count, noun, nounPlural)} on file ${count === 1 ? 'has' : 'have'} no invoice yet.`
  return {
    title: `Backfill ${account}'s invoices?`,
    how,
    body,
    submit: count ? `Backfill ${plural(count, 'invoice', 'invoices')}` : 'Backfill',
  }
}
