import { describe, expect, it } from 'vitest'

import { parseMoney } from '@/lib/money'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { account } from '@/test/builders'

import { draftOf, newAccountBody, patchOf } from './accountDraft'

function vehicle(overrides: Partial<AccountWithBalances>): AccountWithBalances {
  return account({
    id: 'car-a',
    name: 'Blue Roadster',
    kind: 'asset',
    type: 'vehicle',
    vehicle_vin: 'EXAMPLEVIN0000001',
    vehicle_mileage: 41000,
    vehicle_mileage_as_of: '2026-01-15',
    vehicle_miles_per_year: 9000,
    ...overrides,
  })
}

const A = vehicle({})
const B = vehicle({
  id: 'car-b',
  name: 'Green Wagon',
  vehicle_vin: 'EXAMPLEVIN0000002',
  vehicle_mileage: 73000,
  vehicle_mileage_as_of: '2026-02-20',
  vehicle_miles_per_year: 11000,
})
const LOAN = vehicle({
  id: 'loan-b',
  name: 'Wagon loan',
  kind: 'loan',
  type: 'auto_loan',
  vehicle_vin: null,
  vehicle_mileage: null,
  vehicle_mileage_as_of: null,
  vehicle_miles_per_year: null,
  secured_by_account_id: 'car-b',
  interest_rate: '0.0399',
})

describe('account draft', () => {
  it("is the account's own details", () => {
    const draft = draftOf(B)
    expect(draft.name).toBe('Green Wagon')
    expect(draft.vin).toBe('EXAMPLEVIN0000002')
    expect(draft.mileage).toBe('73000')
    expect(draft.perYear).toBe('11000')
  })

  it('saves nothing when nothing was edited', () => {
    expect(patchOf(draftOf(B), B, true)).toEqual({ patch: {} })
    expect(patchOf(draftOf(LOAN), LOAN, false)).toEqual({ patch: {} })
    expect(patchOf(draftOf(A), A, true)).toEqual({ patch: {} })
  })

  it('stores the tab the register opens on, and clears it for the rows', () => {
    expect(patchOf({ ...draftOf(B), openingTab: 'income' }, B, true)).toEqual({
      patch: { default_register_tab: 'income' },
    })
    const spending = vehicle({ default_register_tab: 'spending' })
    expect(draftOf(spending).openingTab).toBe('spending')
    expect(patchOf({ ...draftOf(spending), openingTab: 'all' }, spending, true)).toEqual({
      patch: { default_register_tab: null },
    })
  })

  it('saves only what was edited', () => {
    const draft = { ...draftOf(B), mileage: '74500', notes: 'New tyres' }
    expect(patchOf(draft, B, true)).toEqual({
      patch: { vehicle_mileage: 74500, notes: 'New tyres' },
    })
  })

  it("would write one account's details into another if diffed against it", () => {
    // Why the dialog's form is keyed by account: the patch is a difference.
    const saved = patchOf(draftOf(A), B, true)
    expect(saved).toEqual({
      patch: {
        name: 'Blue Roadster',
        vehicle_vin: 'EXAMPLEVIN0000001',
        vehicle_mileage: 41000,
        vehicle_mileage_as_of: '2026-01-15',
        vehicle_miles_per_year: 9000,
      },
    })
  })

  it('clears a secured-by link and a credit limit only when edited', () => {
    expect(patchOf({ ...draftOf(LOAN), securedBy: 'none' }, LOAN, false)).toEqual({
      patch: { secured_by_account_id: null },
    })
    const card = vehicle({
      kind: 'credit_card',
      type: 'credit_card',
      credit_limit: parseMoney('5000.00'),
    })
    expect(patchOf(draftOf(card), card, false)).toEqual({ patch: {} })
    expect(patchOf({ ...draftOf(card), limit: '' }, card, false)).toEqual({
      patch: { credit_limit: null },
    })
  })

  it('stops the save on a typo', () => {
    expect(patchOf({ ...draftOf(B), opening: 'twelve' }, B, true)).toEqual({
      problem: '"twelve" is not an amount',
    })
  })
})

describe('new account body', () => {
  const fields = {
    name: 'Rainy Day',
    type: 'savings',
    currency: 'USD',
    balance: '',
    address: '',
    vin: '',
  }

  it('sends a starting balance typed with a dollar sign or thousands commas as an amount', () => {
    expect(newAccountBody({ ...fields, balance: '$1,250.50' })).toHaveProperty(
      'opening_balance',
      '1250.50',
    )
    expect(newAccountBody({ ...fields, balance: '1,000' })).toHaveProperty(
      'opening_balance',
      '1000.00',
    )
  })

  it('leaves a blank starting balance out', () => {
    expect(newAccountBody(fields)).toMatchObject({ name: 'Rainy Day', opening_balance: undefined })
  })

  it('is null with a starting balance that is not an amount, or with no name', () => {
    expect(newAccountBody({ ...fields, balance: 'plenty' })).toBe(null)
    expect(newAccountBody({ ...fields, name: '  ' })).toBe(null)
  })
})

describe('the receipts setting', () => {
  const hsa = account({ id: 'hsa-1', name: 'Health Savings', type: 'hsa', requires_receipts: true })

  it('is sent only when it is changed', () => {
    expect(patchOf(draftOf(hsa), hsa, false)).toEqual({ patch: {} })
    expect(patchOf({ ...draftOf(hsa), requiresReceipts: false }, hsa, false)).toEqual({
      patch: { requires_receipts: false },
    })
  })
})
