import { describe, expect, it } from 'vitest'

import type { MailboxPollResult } from '@/lib/clients/email'

import {
  describeOutcome,
  describePollResult,
  mailboxBadge,
  mailboxKindName,
  mailboxPort,
  mailProviderName,
  mailRowOffers,
  missingStatementNote,
} from './mailbox'

/** One read that found nothing, until a test says otherwise. */
function pollResult(over: Partial<MailboxPollResult> = {}): MailboxPollResult {
  return {
    read: 0,
    bills: 0,
    rules: 0,
    otps: 0,
    unrecognised: 0,
    proposed: 0,
    failed: 0,
    error: '',
    ...over,
  }
}

describe('the word for where a mailbox stands', () => {
  it('calls a mailbox nobody has signed in to not connected, switch or no switch', () => {
    // "Off" would read as something to turn back on, and turning it on would
    // change nothing while there is no sealed token behind it.
    expect(mailboxBadge({ connected: false, enabled: true }).word).toBe('Not connected')
    expect(mailboxBadge({ connected: false, enabled: false }).word).toBe('Not connected')
  })

  it('keeps a paused mailbox apart from a working one', () => {
    expect(mailboxBadge({ connected: true, enabled: false })).toEqual({
      word: 'Off',
      tone: 'warning',
    })
    expect(mailboxBadge({ connected: true, enabled: true })).toEqual({
      word: 'Connected',
      tone: 'accent',
    })
  })

  it('names the two kinds as a household does', () => {
    expect(mailboxKindName('graph')).toBe('Office 365')
    expect(mailboxKindName('imap')).toBe('IMAP')
  })
})

describe('the port a form means', () => {
  it('reads an empty field as the server’s own default rather than as zero', () => {
    expect(mailboxPort('')).toBeNull()
    expect(mailboxPort('  ')).toBeNull()
  })

  it('takes a port', () => {
    expect(mailboxPort('993')).toBe(993)
    expect(mailboxPort(' 143 ')).toBe(143)
  })

  it('refuses what is not one, rather than sending a number nothing answers on', () => {
    expect(mailboxPort('993a')).toBeUndefined()
    expect(mailboxPort('0')).toBeUndefined()
    expect(mailboxPort('70000')).toBeUndefined()
    expect(mailboxPort('-1')).toBeUndefined()
  })
})

describe('what one read did', () => {
  it('counts only the things there were any of', () => {
    expect(describePollResult(pollResult({ read: 12, bills: 2, otps: 1, unrecognised: 9 }))).toBe(
      'Read 12 messages: 2 bills filed, 1 code answered, 9 were not bills',
    )
    expect(describePollResult(pollResult({ read: 1, bills: 1 }))).toBe('Read 1 message: 1 bill filed')
    expect(describePollResult(pollResult({ read: 2, rules: 2 }))).toBe('Read 2 messages: 2 posted by rules')
    expect(describePollResult(pollResult({ read: 1, unrecognised: 1 }))).toBe(
      'Read 1 message: 1 was not a bill',
    )
  })

  it('says in words that a read found nothing, rather than showing zeroes', () => {
    expect(describePollResult(pollResult())).toBe('No new mail')
  })

  it('separates mail that arrived from mail that could be filed', () => {
    // Every message read was somebody else's: the count is not zero and there
    // is still nothing to show for it.
    expect(describePollResult(pollResult({ read: 3 }))).toBe('Read 3 messages; nothing to file')
  })

  it('names what was kept for a person and what could not be filed', () => {
    expect(describePollResult(pollResult({ read: 2, proposed: 1, failed: 1 }))).toBe(
      'Read 2 messages: 1 kept for you to read, 1 could not be filed',
    )
  })

  it('carries the mailbox’s own words on a failure, and no counts', () => {
    expect(describePollResult(pollResult({ error: 'the consent was revoked' }))).toBe(
      'Could not read the mailbox: the consent was revoked',
    )
  })
})

describe('what became of one message', () => {
  it('says each outcome in words rather than as the stored token', () => {
    expect(describeOutcome({ outcome: 'bill', note: '' })).toBe('Filed a bill')
    expect(describeOutcome({ outcome: 'otp', note: '' })).toBe('Answered a code')
    expect(describeOutcome({ outcome: 'unrecognised', note: '' })).toBe('Not a bill')
    expect(describeOutcome({ outcome: 'proposed', note: '' })).toBe('Kept for you to read')
    // A mail no built-in parser knew, claimed by a rule the household wrote.
    expect(describeOutcome({ outcome: 'rule', note: '' })).toBe('Posted by rule')
  })

  it('carries the reader’s own reason for a failure, where it gave one', () => {
    expect(describeOutcome({ outcome: 'failed', note: 'no amount in the body' })).toBe(
      'Could not file: no amount in the body',
    )
    expect(describeOutcome({ outcome: 'failed', note: '  ' })).toBe('Could not file it')
  })
})

describe('what a recent-mail row offers', () => {
  it('offers the assistant for an ordinary message once it is set up', () => {
    expect(mailRowOffers({ outcome: 'rule' }, true)).toEqual({
      ask: 'offered',
      suggest: 'offered',
      reread: false,
    })
  })

  it('keeps the assistant items, switched off, until there is an assistant', () => {
    expect(mailRowOffers({ outcome: 'unrecognised' }, false)).toEqual({
      ask: 'disabled',
      suggest: 'disabled',
      reread: true,
    })
  })

  it('offers nothing about a message that carried a sign-in code', () => {
    // Its text is withheld from everybody, so a button to ask about it would
    // always fail.
    expect(mailRowOffers({ outcome: 'otp' }, true)).toEqual({
      ask: 'absent',
      suggest: 'absent',
      reread: false,
    })
  })

  it('reads again only a message nothing was made of', () => {
    expect(mailRowOffers({ outcome: 'failed' }, true).reread).toBe(true)
    expect(mailRowOffers({ outcome: 'bill' }, true).reread).toBe(false)
  })
})

describe('which provider a row names', () => {
  const connections = [
    { id: 'conn-water', biller: 'email-only' as const, label: 'Example Water' },
    { id: 'conn-power', biller: 't-mobile' as const, label: 'Mailbox' },
  ]

  it('is the connection a filed bill is on, named as its card is', () => {
    expect(
      mailProviderName({ biller: 'email-only', bill_connection_id: 'conn-water' }, connections),
    ).toBe('Example Water')
    expect(
      mailProviderName({ biller: 't-mobile', bill_connection_id: 'conn-power' }, connections),
    ).toBe('T-Mobile · Mailbox')
  })

  it('is the provider a parser claimed it for when no bill was filed', () => {
    expect(mailProviderName({ biller: 't-mobile', bill_connection_id: null }, connections)).toBe(
      'T-Mobile',
    )
    expect(mailProviderName({ biller: '', bill_connection_id: null }, connections)).toBeNull()
  })
})

describe('why a filed bill has no statement', () => {
  it('is the note, only on a bill that has no statement', () => {
    const failed = 'the mail could not be saved as a PDF: browser: Chrome would not launch'
    expect(missingStatementNote({ outcome: 'bill', note: failed, document_id: null })).toBe(failed)
    expect(missingStatementNote({ outcome: 'bill', note: failed, document_id: 'doc-1' })).toBeNull()
    expect(missingStatementNote({ outcome: 'rule', note: 'posted', document_id: null })).toBeNull()
    expect(missingStatementNote({ outcome: 'bill', note: ' ', document_id: null })).toBeNull()
  })
})
