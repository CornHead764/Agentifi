/**
 * Which accounts a hand-entered row may go into. A row typed into a synced
 * account is never matched by the next sync, so the charge counts twice.
 */

import { describe, expect, it } from 'vitest'

import { account } from '@/test/builders'

import {
  acceptsManualTransactions,
  accountDisplayName,
  accountNamer,
  defaultTargetAccountId,
  describeStatementSource,
  isUnderBalance,
  manualTransactionTargets,
  maskedNumber,
  openInvestmentAccounts,
  pickerAccounts,
  smallBalanceAccounts,
} from './accounts'
import { formatDate } from './format'
import { parseMoney } from './money'
import type { Uuid } from './transactions/types'

describe('acceptsManualTransactions', () => {
  it('accepts an open account nothing else writes to', () => {
    expect(acceptsManualTransactions(account())).toBe(true)
  })

  it('refuses a closed account', () => {
    expect(acceptsManualTransactions(account({ is_closed: true }))).toBe(false)
  })

  it('refuses an Amazon gift-card balance', () => {
    // Fed by the Amazon pull; nothing would reconcile a typed row.
    expect(acceptsManualTransactions(account({ kind: 'cash', type: 'gift_card' }))).toBe(false)
  })

  it('refuses an account SimpleFIN keeps current', () => {
    expect(acceptsManualTransactions(account({ connection_id: 'c1' as Uuid }))).toBe(false)
  })
})

describe('the accounts a new row may be written into', () => {
  const manual = account({ id: 'manual' as Uuid })
  const synced = account({ id: 'synced' as Uuid, connection_id: 'c1' as Uuid })
  const closed = account({ id: 'closed' as Uuid, is_closed: true })
  const gift = account({ id: 'gift' as Uuid, type: 'gift_card' })
  const all = [synced, closed, gift, manual]

  it('keeps only the eligible ones, in the order given', () => {
    expect(manualTransactionTargets(all).map((one) => one.id)).toEqual(['manual'])
  })

  it('keeps the row being edited in its own account, however ineligible', () => {
    // Otherwise the account field on a synced row would read as some other
    // account, and saving would move the row to it.
    expect(manualTransactionTargets(all, 'synced').map((one) => one.id)).toEqual([
      'synced',
      'manual',
    ])
  })

  it('opens a new row on the account being looked at when it can take one', () => {
    expect(defaultTargetAccountId(all, 'manual')).toBe('manual')
  })

  it('falls back to the first eligible account rather than the first account', () => {
    expect(defaultTargetAccountId(all, 'synced')).toBe('manual')
    expect(defaultTargetAccountId(all, null)).toBe('manual')
  })

  it('answers with nothing when no account can take a row', () => {
    expect(defaultTargetAccountId([synced, closed, gift], null)).toBe('')
  })
})

describe('where a statement came from', () => {
  const source = {
    bill_id: 'bill-1',
    provider: 'Northwind Card',
    issued_on: '2026-09-24',
    due_on: '2026-10-21',
    fetched_at: '2026-09-26T14:30:00Z',
  }

  it('names the bill, its provider and the statement’s own day', () => {
    expect(describeStatementSource({ ...source, source: 'email' })).toBe(
      `From the Northwind Card statement e-mail of ${formatDate('2026-09-24')}`,
    )
    expect(describeStatementSource({ ...source, source: 'manual', issued_on: null })).toBe(
      `From the Northwind Card bill entered on Bill providers, ${formatDate('2026-09-26')}`,
    )
  })
})

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

describe('openInvestmentAccounts', () => {
  it('keeps the open investment accounts and nothing else', () => {
    const brokerage = account({ id: 'b1', kind: 'investment' })
    const closed = account({ id: 'b2', kind: 'investment', is_closed: true })
    const checking = account({ id: 'c1', kind: 'cash' })
    expect(openInvestmentAccounts([brokerage, closed, checking])).toEqual([brokerage])
  })
})

describe('smallBalanceAccounts', () => {
  const isSmall = smallBalanceAccounts([
    { id: 'dust', hidden_small_balance: true },
    { id: 'held', hidden_small_balance: false },
    { id: 'patched' },
  ])

  it('answers the flag the server set', () => {
    expect(isSmall('dust')).toBe(true)
    expect(isSmall('held')).toBe(false)
  })

  it('shows an account the flag is absent from, or the list lacks', () => {
    expect(isSmall('patched')).toBe(false)
    expect(isSmall('gone')).toBe(false)
    expect(smallBalanceAccounts(undefined)('dust')).toBe(false)
  })
})

describe('pickerAccounts', () => {
  const accounts = [
    { id: 'checking', name: 'Everyday Checking' },
    { id: 'dust-1', name: 'Coin A', hidden_small_balance: true },
    { id: 'dust-2', name: 'Coin B', hidden_small_balance: true },
  ]

  it('leaves the small balances out and counts them', () => {
    const { shown, hidden } = pickerAccounts(accounts, null, false)
    expect(shown.map((account) => account.id)).toEqual(['checking'])
    expect(hidden).toBe(2)
  })

  it('always lists the account already chosen', () => {
    const { shown, hidden } = pickerAccounts(accounts, 'dust-2', false)
    expect(shown.map((account) => account.id)).toEqual(['checking', 'dust-2'])
    expect(hidden).toBe(1)
  })

  it('lists every account once revealed, still counting what the line hides', () => {
    const { shown, hidden } = pickerAccounts(accounts, null, true)
    expect(shown).toHaveLength(3)
    expect(hidden).toBe(2)
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

describe('isUnderBalance', () => {
  const dollar = parseMoney('1.00')

  it('compares the magnitude, so a debt is small too', () => {
    expect(isUnderBalance(parseMoney('0.40'), dollar)).toBe(true)
    expect(isUnderBalance(parseMoney('-0.40'), dollar)).toBe(true)
  })

  it('is strict: a balance at the threshold is not under it', () => {
    expect(isUnderBalance(parseMoney('1.00'), dollar)).toBe(false)
    expect(isUnderBalance(parseMoney('-1.00'), dollar)).toBe(false)
  })
})
