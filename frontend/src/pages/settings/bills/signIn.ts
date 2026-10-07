/**
 * What the sign-in dialog's one button does. It is a submit, so every Enter in
 * every field reaches it too; `nothing` is the answer while a call is in
 * flight, so held keys cannot send a burst of refused codes.
 */

import type {
  BillSignInAction,
  BillSignInChoice,
  BillSignInState,
  BillTrailEntry,
} from '@/lib/clients/bills'
import { formatTimestamp } from '@/lib/format'

/** What a press of the dialog's button should do. */
export type SignInSubmit = 'nothing' | 'start' | 'answer' | 'finish' | 'refetch'

/** Where the sign-in stands; `state` is null before the first step. */
export interface SignInStand {
  busy: boolean
  state: BillSignInState['state'] | null
  /** The authenticator key in the form is not a setup key. */
  keyProblem: boolean
}

/**
 * A state the dialog has nothing to draw for, shown as a failure rather than a
 * Continue button over nothing. The agent settles these into `failed` too; this
 * is the same rule on this side of the wire.
 *
 * `signing_in` must never count: the agent is driving the provider's pages and
 * the sign-in is going fine.
 */
export function signInStuck(state: BillSignInState['state'] | null): boolean {
  if (state === null) return false
  return !(
    state === 'signing_in' ||
    state === 'otp' ||
    state === 'captcha' ||
    state === 'approval' ||
    state === 'accounts' ||
    state === 'signed_in' ||
    state === 'failed'
  )
}

export function signInSubmit(stand: SignInStand): SignInSubmit {
  // A call is already with the provider: a second press is the same press.
  if (stand.busy) return 'nothing'
  // The agent is still driving the provider's pages. There is nothing to send
  // and nothing to keep; the poll is what moves this on.
  if (stand.state === 'signing_in') return 'nothing'
  if (stand.state === null || stand.state === 'failed' || signInStuck(stand.state)) {
    return stand.keyProblem ? 'nothing' : 'start'
  }
  if (stand.state === 'accounts' || stand.state === 'signed_in') return 'finish'
  // An approval is a tap on somebody's phone: there is nothing to send, only
  // the provider to ask again.
  if (stand.state === 'approval') return 'refetch'
  // Only a code step answers: an empty code posted at a page that was not
  // asking counts towards locking the account.
  return 'answer'
}

/** What the round did, the half of a trail line the page alone cannot show. */
function didPart(did: BillSignInAction | undefined): string {
  if (did === undefined || !did.acted) return ''
  const pressed =
    did.pressed === 'enter'
      ? enterPart(did)
      : did.words === ''
        ? `pressed the ${did.pressed}`
        : `pressed the ${did.pressed} “${did.words}”`
  const dismissed =
    did.dismissed === undefined || did.dismissed === ''
      ? ''
      : `dismissed the cookie banner (${did.dismissed}), then `
  const late = did.waited && did.pressed !== 'enter' ? ', which was not pressable at first' : ''
  return `did: ${dismissed}${pressed}${late}`
}

/** Why a round fell back to Enter: no button matched, or one did and never became pressable. */
function enterPart(did: BillSignInAction): string {
  if (!did.waited) return 'pressed Enter, nothing on the page matched a button'
  const button = did.words === '' ? 'the button' : `the “${did.words}” button`
  return `pressed Enter, ${button} never became pressable`
}

/**
 * What a factor page was offering and what was chosen: the words, the kinds
 * and whether there was a menu at all separate a vocabulary, shape or
 * visibility failure.
 */
function offeredPart(
  choices: BillSignInChoice[] | null | undefined,
  chose: string,
  forced: boolean,
): string {
  if (choices === null || choices === undefined || choices.length === 0) return ''
  const menu = choices.map((choice) => `“${choice.words}” (${choice.kind})`).join(', ')
  if (chose === '') return `offered: ${menu}`
  return `offered: ${menu} · chose: “${chose}”${forced ? ', forced' : ''}`
}

/** One round of a sign-in, or one line of a pull, as a single plain line somebody can paste. */
export function trailLine(entry: BillTrailEntry): string {
  const at = entry.at === '' ? '' : formatTimestamp(entry.at, 'time')
  const page = [entry.url, entry.title === '' ? '' : `“${entry.title}”`]
    .filter((part) => part !== '')
    .join(' ')
  const boxes = Object.entries(entry.inputs ?? {})
    .map(([kind, count]) => `${kind} ${count}`)
    .join(', ')
  const flags = (
    [
      ['password box', entry.form.password],
      ['username box', entry.form.username],
      ['code box', entry.form.otp],
      ['sign-out link', entry.form.sign_out_link],
    ] as const
  )
    .filter(([, on]) => on)
    .map(([label]) => label)
    .join(', ')
  return [
    [at, `${entry.step} → ${entry.state || 'nothing'}`].filter((part) => part !== '').join(' · '),
    page,
    flags === '' ? '' : `showing: ${flags}`,
    boxes === '' ? '' : `boxes: ${boxes}`,
    offeredPart(entry.choices, entry.chose ?? '', entry.forced === true),
    entry.error === '' ? '' : `said: ${entry.error}`,
    (entry.note ?? '') === '' ? '' : `note: ${entry.note}`,
    didPart(entry.did),
    entry.did?.acted === true ? (entry.did.changed ? 'the page changed' : 'the page did not change') : '',
  ]
    .filter((part) => part !== '')
    .join(' · ')
}

/**
 * The whole trail as plain text: a line per round or page, each page's
 * structure indented under the line that took it.
 */
export function trailText(trail: BillTrailEntry[]): string {
  return trail
    .map((entry) => {
      const snapshot = entry.snapshot ?? ''
      if (snapshot === '') return trailLine(entry)
      const indented = snapshot
        .split('\n')
        .map((line) => `    ${line}`)
        .join('\n')
      return `${trailLine(entry)}\n${indented}`
    })
    .join('\n')
}
