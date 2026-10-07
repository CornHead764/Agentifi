import { describe, expect, it } from 'vitest'

import {
  blankTransferDraft,
  planTransfer,
  transferProblem,
  type TransferAccount,
  type TransferDraft,
} from './transferMoney'

const CHECKING: TransferAccount = { id: 'checking', name: 'Everyday Checking', currency: 'USD' }
const SAVINGS: TransferAccount = { id: 'savings', name: 'Rainy Day Savings', currency: 'USD' }
const EURO: TransferAccount = { id: 'euro', name: 'Berlin Account', currency: 'EUR' }
const ACCOUNTS = [CHECKING, SAVINGS, EURO]

function draft(patch: Partial<TransferDraft> = {}): TransferDraft {
  return {
    ...blankTransferDraft('checking', '2026-08-29'),
    toAccountId: 'savings',
    amount: '250.00',
    ...patch,
  }
}

describe('what stops a transfer', () => {
  it('accepts a plain move between two accounts', () => {
    expect(transferProblem(draft(), ACCOUNTS)).toBeNull()
  })

  it('refuses both halves in one account', () => {
    expect(transferProblem(draft({ toAccountId: 'checking' }), ACCOUNTS)).toBe('same-account')
  })

  it('refuses two currencies, which are not the same money', () => {
    expect(transferProblem(draft({ toAccountId: 'euro' }), ACCOUNTS)).toBe('mixed-currency')
  })

  it('refuses an amount that is not one', () => {
    expect(transferProblem(draft({ amount: 'some' }), ACCOUNTS)).toBe('unparseable')
    expect(transferProblem(draft({ amount: '' }), ACCOUNTS)).toBe('unparseable')
  })

  it('refuses zero, which moves nothing', () => {
    expect(transferProblem(draft({ amount: '0' }), ACCOUNTS)).toBe('not-positive')
  })

  it('waits for the other half to be chosen', () => {
    expect(transferProblem(draft({ toAccountId: '' }), ACCOUNTS)).toBe('no-accounts')
  })
})

describe('the two legs a transfer writes', () => {
  it('signs them for the user, who typed the size once', () => {
    const plan = planTransfer(draft(), ACCOUNTS)
    expect(plan?.paying.amount).toBe('-250.00')
    expect(plan?.receiving.amount).toBe('250.00')
    expect(plan?.paying.account_id).toBe('checking')
    expect(plan?.receiving.account_id).toBe('savings')
  })

  it('reads a signed amount as the size it is', () => {
    const plan = planTransfer(draft({ amount: '-250.00' }), ACCOUNTS)
    expect(plan?.paying.amount).toBe('-250.00')
    expect(plan?.receiving.amount).toBe('250.00')
  })

  it('names each leg after the account on the other side', () => {
    const plan = planTransfer(draft(), ACCOUNTS)
    expect(plan?.paying.payee).toBe('Transfer to Rainy Day Savings')
    expect(plan?.receiving.payee).toBe('Transfer from Everyday Checking')
  })

  it('marks both halves reviewed and dates them together', () => {
    const plan = planTransfer(draft(), ACCOUNTS)
    expect(plan?.paying.is_reviewed).toBe(true)
    expect(plan?.receiving.is_reviewed).toBe(true)
    expect(plan?.paying.date).toBe(plan?.receiving.date)
  })

  it('writes the note on both halves, or neither', () => {
    expect(planTransfer(draft({ notes: ' rent ' }), ACCOUNTS)?.paying.notes).toBe('rent')
    expect(planTransfer(draft({ notes: '   ' }), ACCOUNTS)?.receiving.notes).toBeNull()
  })

  it('plans nothing at all for a draft that would be refused', () => {
    expect(planTransfer(draft({ toAccountId: 'euro' }), ACCOUNTS)).toBeNull()
  })
})
