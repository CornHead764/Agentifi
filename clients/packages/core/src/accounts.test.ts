import { describe, expect, it } from 'vitest'

import { accountDisplayName, accountNamer, maskedNumber } from './accounts'

describe('maskedNumber', () => {
  it('shows the last four digits behind dots', () => {
    expect(maskedNumber('1234')).toBe('····1234')
    expect(maskedNumber('xxxx-5678')).toBe('····5678')
    expect(maskedNumber('0000111122223333')).toBe('····3333')
  })

  it('is null when there is no number', () => {
    expect(maskedNumber(null)).toBeNull()
    expect(maskedNumber(undefined)).toBeNull()
    expect(maskedNumber('  ')).toBeNull()
  })
})

describe('accountNamer', () => {
  const accounts = [
    { id: 'a1', name: 'Everyday checking' },
    { id: 'a2', name: 'Rewards card' },
  ]

  it('names an account by id', () => {
    expect(accountNamer(accounts)('a2')).toBe('Rewards card')
  })

  it('reads an id the loaded list lacks as an unknown account', () => {
    expect(accountNamer(accounts)('gone')).toBe('Unknown account')
  })

  it('leaves every name blank until the list loads', () => {
    expect(accountNamer(undefined)('a1')).toBe('')
  })
})

describe('accountDisplayName', () => {
  it('drops the feed id a bank appends, whole or cut short', () => {
    expect(accountDisplayName('Coin Wallet (0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9)')).toBe('Coin Wallet')
    expect(accountDisplayName('Coin Wallet (0a1b2c3d-4e5f-...)')).toBe('Coin Wallet')
  })

  it('keeps a short account number in brackets', () => {
    expect(accountDisplayName('Online Savings (4321)')).toBe('Online Savings (4321)')
  })

  it('says a name the bank doubled once', () => {
    expect(accountDisplayName('12 Study Loan Plan 12 Study Loan Plan')).toBe('12 Study Loan Plan')
    expect(accountDisplayName('Savings Savings')).toBe('Savings')
  })

  it('leaves a name that only repeats a word alone', () => {
    expect(accountDisplayName('Savings for Savings')).toBe('Savings for Savings')
  })
})
