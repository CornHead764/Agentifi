import { describe, expect, it } from 'vitest'

import { holdingDraftFrom, type HoldingFields } from './investments'

describe('holdingDraftFrom', () => {
  const fields: HoldingFields = {
    accountId: 'acct-1',
    symbol: ' abcx ',
    name: '',
    shares: '12.5',
    costBasis: '',
    marketValue: '',
    needsValue: false,
  }

  it('sends a cost basis and a value typed with a dollar sign or thousands commas as amounts', () => {
    expect(
      holdingDraftFrom({ ...fields, costBasis: '$1,250.50', marketValue: '1,000', needsValue: true }),
    ).toEqual({
      account_id: 'acct-1',
      symbol: 'ABCX',
      name: '',
      shares: '12.5',
      cost_basis: '1250.50',
      market_value: '1000.00',
    })
  })

  it('sends no value for a priced symbol, and a blank cost basis as null', () => {
    expect(holdingDraftFrom({ ...fields, marketValue: '1,000' })).toMatchObject({
      cost_basis: null,
      market_value: null,
    })
  })

  it('is null with an amount that is not an amount, or a missing value', () => {
    expect(holdingDraftFrom({ ...fields, costBasis: 'a lot' })).toBe(null)
    expect(holdingDraftFrom({ ...fields, needsValue: true, marketValue: 'a lot' })).toBe(null)
    expect(holdingDraftFrom({ ...fields, needsValue: true })).toBe(null)
  })
})
