/**
 * Which accounts a hand-entered row may go into. A row typed into a synced
 * account is never matched by the next sync, so the charge counts twice.
 */

import { describe, expect, it } from 'vitest'

import { account } from '@/test/builders'

import {
  acceptsManualTransactions,
  defaultTargetAccountId,
  describeStatementSource,
  isUnderBalance,
  manualTransactionTargets,
  openInvestmentAccounts,
  pickerAccounts,
  smallBalanceAccounts,
} from './accounts'
import { formatDate } from '@agentifi/core/format'
import { parseMoney } from '@agentifi/core/money'
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
