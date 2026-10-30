/** What the mailbox row says, decided away from the markup so each rule has a test. */

import type { BadgeTone } from '@/components/ui'
import { billerName, connectionTitle } from '@/lib/billers'
import type { BillConnection } from '@/lib/clients/bills'
import type { EmailConnection, MailboxMessage, MailboxPollResult } from '@/lib/clients/email'
import { plural } from '@/lib/format'

/** The one word for where this mailbox stands, and the colour it carries. */
export interface MailboxBadge {
  word: string
  tone: BadgeTone
}

/**
 * The badge a mailbox row wears. A row with no sealed secret reads "Not
 * connected" whatever the enabled switch says; "Off" is only a paused one.
 */
export function mailboxBadge(
  connection: Pick<EmailConnection, 'connected' | 'enabled'>,
): MailboxBadge {
  if (!connection.connected) return { word: 'Not connected', tone: 'neutral' }
  if (!connection.enabled) return { word: 'Off', tone: 'warning' }
  return { word: 'Connected', tone: 'accent' }
}

/** The two kinds as a household names them, rather than as the column stores them. */
export function mailboxKindName(kind: EmailConnection['kind']): string {
  return kind === 'graph' ? 'Office 365' : 'IMAP'
}

/**
 * The port a form's text means: null for empty (the server fills in 993), and
 * undefined for text that is not a port.
 */
export function mailboxPort(text: string): number | null | undefined {
  const said = text.trim()
  if (said === '') return null
  if (!/^\d{1,5}$/.test(said)) return undefined
  const port = Number(said)
  return port >= 1 && port <= 65535 ? port : undefined
}

/** One mailbox read, as a toast: counts only where nonzero, filed bills first. */
export function describePollResult(result: MailboxPollResult): string {
  if (result.error !== '') return `Could not read the mailbox: ${result.error}`

  const parts: string[] = []
  if (result.bills > 0) parts.push(`${plural(result.bills, 'bill')} filed`)
  if (result.rules > 0) parts.push(`${result.rules} posted by ${result.rules === 1 ? 'a rule' : 'rules'}`)
  if (result.otps > 0) parts.push(`${plural(result.otps, 'code')} answered`)
  if (result.proposed > 0) parts.push(`${result.proposed} kept for you to read`)
  if (result.failed > 0) parts.push(`${result.failed} could not be filed`)
  if (result.unrecognised > 0)
    parts.push(result.unrecognised === 1 ? '1 was not a bill' : `${result.unrecognised} were not bills`)

  if (result.read === 0) return 'No new mail'
  const read = `Read ${plural(result.read, 'message')}`
  return parts.length === 0 ? `${read}; nothing to file` : `${read}: ${parts.join(', ')}`
}

/** What became of one message, with the reader's own failure reason where it gave one. */
export function describeOutcome(
  message: Pick<MailboxMessage, 'outcome' | 'note'>,
): string {
  const note = message.note.trim()
  switch (message.outcome) {
    case 'bill':
      return 'Filed a bill'
    case 'otp':
      return 'Answered a code'
    case 'unrecognised':
      return 'Not a bill'
    case 'proposed':
      return 'Kept for you to read'
    case 'rule':
      return 'Posted by rule'
    case 'failed':
      return note === '' ? 'Could not file it' : `Could not file: ${note}`
  }
}

/** Whether a menu item is there to press, there but switched off, or not there. */
export type MailOffer = 'offered' | 'disabled' | 'absent'

export interface MailRowOffers {
  ask: MailOffer
  suggest: MailOffer
  reread: boolean
}

/**
 * What one recent-mail row's menu offers. A message that carried a sign-in
 * code offers neither assistant item, since its text is withheld from the
 * assistant too. Without an assistant the items stay, switched off. Only a
 * mail nothing was made of is offered a reread; the resource refuses others.
 */
export function mailRowOffers(
  message: Pick<MailboxMessage, 'outcome'>,
  assistantReady: boolean,
): MailRowOffers {
  const assistant: MailOffer =
    message.outcome === 'otp' ? 'absent' : assistantReady ? 'offered' : 'disabled'
  return {
    ask: assistant,
    suggest: assistant,
    reread: message.outcome === 'unrecognised' || message.outcome === 'failed',
  }
}

/**
 * Which provider a message is about: the connection a filed bill landed on
 * first, since the parser's provider id cannot say which login it was.
 */
export function mailProviderName(
  message: Pick<MailboxMessage, 'biller' | 'bill_connection_id'>,
  connections: readonly Pick<BillConnection, 'id' | 'biller' | 'label'>[],
): string | null {
  const on =
    message.bill_connection_id === null
      ? undefined
      : connections.find((one) => one.id === message.bill_connection_id)
  if (on !== undefined) return connectionTitle(on)
  return message.biller === '' ? null : billerName(message.biller)
}

/**
 * Why a filed bill has no statement: a mail that could not be printed is still
 * filed, and this note is the only place that says why.
 */
export function missingStatementNote(
  message: Pick<MailboxMessage, 'outcome' | 'note' | 'document_id'>,
): string | null {
  if (message.outcome !== 'bill' || message.document_id !== null) return null
  const note = message.note.trim()
  return note === '' ? null : note
}
