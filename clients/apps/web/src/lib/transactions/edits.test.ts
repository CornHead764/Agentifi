import { describe, expect, it } from 'vitest'

import type { Account } from './types'

import {
  ImmutableFieldError,
  buildTransactionUpdate,
  displayPayee,
  openingCurrency,
} from './edits'

describe('the two names', () => {
  it('refuses an update that carries the statement name', () => {
    expect(() => buildTransactionUpdate({ statement_name: 'SQ *COFFEE XXXXXX1234 USA' })).toThrow(
      ImmutableFieldError,
    )
  })

  it('refuses it even alongside an edit that is allowed', () => {
    expect(() =>
      buildTransactionUpdate({ payee: 'Harbor Coffee Usa', statement_name: 'anything' }),
    ).toThrow(ImmutableFieldError)
  })

  it('lets the payee through, which is the editable name', () => {
    expect(buildTransactionUpdate({ payee: 'Harbor Coffee Usa' })).toEqual({
      payee: 'Harbor Coffee Usa',
    })
  })

  it('displays the clean name, falling back to the bank string', () => {
    expect(displayPayee({ payee: 'Example Mobile', statement_name: 'POS DEBIT EXAMPLEMOBILE*AUTO' })).toBe(
      'Example Mobile',
    )
    expect(displayPayee({ payee: '', statement_name: 'POS DEBIT EXAMPLEMOBILE*AUTO' })).toBe(
      'POS DEBIT EXAMPLEMOBILE*AUTO',
    )
  })

  it('carries only the fields it was given, so an absent one means "leave alone"', () => {
    expect(buildTransactionUpdate({ category_id: null, is_reviewed: true })).toEqual({
      category_id: null,
      is_reviewed: true,
    })
  })

  it('carries the splits with the rest of the edit', () => {
    const splits = [
      { amount: '-30.00', category_id: 'groceries', memo: null, tag_ids: [] },
      { amount: '-20.00', category_id: null, memo: 'batteries', tag_ids: [] },
    ]
    expect(buildTransactionUpdate({ notes: 'shared', splits })).toEqual({ notes: 'shared', splits })
  })

  it('carries a chosen currency, which a saved row can be re-denominated in', () => {
    expect(buildTransactionUpdate({ amount: '12.50', currency: 'EUR' })).toEqual({
      amount: '12.50',
      currency: 'EUR',
    })
  })

  it('keeps the two exclusion flags independent', () => {
    expect(buildTransactionUpdate({ excluded_from_reports: true })).toEqual({
      excluded_from_reports: true,
    })
    expect(buildTransactionUpdate({ excluded_from_spending_plan: true })).toEqual({
      excluded_from_spending_plan: true,
    })
  })
})

describe('openingCurrency', () => {
  const account = (id: string, currency: string) => ({ id, currency }) as Account
  const accounts = [account('a1', 'EUR'), account('a2', 'GBP')]

  it('follows the account the row will be filed against', () => {
    expect(openingCurrency('a1', accounts, 'USD')).toBe('EUR')
    expect(openingCurrency('a2', accounts, 'USD')).toBe('GBP')
  })

  it('falls back to the space until an account is settled on', () => {
    expect(openingCurrency(null, accounts, 'EUR')).toBe('EUR')
    expect(openingCurrency('gone', accounts, 'EUR')).toBe('EUR')
  })

  // USD is the server's default for a new space.
  it('falls back to USD only when nothing else has answered', () => {
    expect(openingCurrency(null, [], null)).toBe('USD')
    expect(openingCurrency(null, [], undefined)).toBe('USD')
    expect(openingCurrency(null, [], '')).toBe('USD')
  })
})
