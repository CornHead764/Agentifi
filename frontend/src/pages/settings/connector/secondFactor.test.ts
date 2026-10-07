import { describe, expect, it } from 'vitest'

import {
  asksForKey,
  hasReadableMailbox,
  initialSecondFactor,
  keptPasswordFact,
  mailboxMissing,
  secondFactorChoice,
  secondFactorOf,
  secondFactorProblem,
  secondFactorRequest,
} from './secondFactor'

// An invented setup key: base32, and the only one in these tests.
const KEY = 'JBSWY3DPEHPK3PXP'

describe('the second-factor select', () => {
  it('starts on the kept choice, or the authenticator where the provider is known to ask', () => {
    expect(initialSecondFactor('email', true)).toBe('email')
    expect(initialSecondFactor('totp', false)).toBe('totp')
    expect(initialSecondFactor('sms', true)).toBe('sms')
    expect(initialSecondFactor('', true)).toBe('totp')
    expect(initialSecondFactor('', false)).toBe('none')
    expect(initialSecondFactor(undefined, false)).toBe('none')
  })

  it('reads a select value it does not know as none', () => {
    expect(secondFactorChoice('email')).toBe('email')
    expect(secondFactorChoice('totp')).toBe('totp')
    expect(secondFactorChoice('sms')).toBe('sms')
    expect(secondFactorChoice('carrier-pigeon')).toBe('none')
  })

  it('draws the key field only for the authenticator', () => {
    expect(asksForKey('totp')).toBe(true)
    expect(asksForKey('email')).toBe(false)
    expect(asksForKey('sms')).toBe(false)
    expect(asksForKey('none')).toBe(false)
  })

  it('says a mailbox is missing only for e-mail, and only once it is known', () => {
    expect(mailboxMissing('email', false)).toBe(true)
    expect(mailboxMissing('email', true)).toBe(false)
    expect(mailboxMissing('email', undefined)).toBe(false)
    expect(mailboxMissing('totp', false)).toBe(false)
    expect(hasReadableMailbox(undefined)).toBeUndefined()
    expect(hasReadableMailbox([{ enabled: true, connected: false }])).toBe(false)
    expect(hasReadableMailbox([{ enabled: false, connected: true }, { enabled: true, connected: true }])).toBe(
      true,
    )
  })
})

describe('what a sign-in sends', () => {
  it('always sends the choice, in the wire’s words', () => {
    expect(secondFactorOf('none')).toBe('')
    expect(secondFactorRequest('none', KEY)).toEqual({ second_factor: '' })
    expect(secondFactorRequest('email', KEY)).toEqual({ second_factor: 'email' })
    expect(secondFactorRequest('sms', KEY)).toEqual({ second_factor: 'sms' })
  })

  it('sends the key only for the authenticator, in one spelling', () => {
    expect(secondFactorRequest('totp', 'jbsw y3dp ehpk 3pxp')).toEqual({
      second_factor: 'totp',
      totp_secret: KEY,
    })
    expect(secondFactorRequest('totp', '')).toEqual({ second_factor: 'totp' })
  })

  it('finds what is wrong with the key before it is sent', () => {
    expect(secondFactorProblem('totp', '123456')).toMatch(/code from the app/)
    expect(secondFactorProblem('totp', KEY)).toBeNull()
    expect(secondFactorProblem('totp', '')).toBeNull()
    expect(secondFactorProblem('email', '123456')).toBeNull()
  })
})

describe('what a card says about a kept password', () => {
  it('names the key, the mailbox, or the password alone', () => {
    expect(keptPasswordFact({ has_totp: true, second_factor: 'totp' })).toBe(
      'Password and authenticator key kept, encrypted, so updates sign in on their own.',
    )
    expect(keptPasswordFact({ has_totp: false, second_factor: 'email' })).toMatch(
      /codes are read from the billing mailbox/,
    )
    expect(keptPasswordFact({ has_totp: false, second_factor: '' })).toBe(
      'Password kept, encrypted, so updates sign in on their own.',
    )
  })
})
