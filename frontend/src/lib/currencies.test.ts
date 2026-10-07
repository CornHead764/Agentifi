import { describe, expect, it } from 'vitest'

import { CURRENCIES, currencyOptions, currencySymbol } from './currencies'

describe('currencies', () => {
  it('offers only codes the rate provider can price', () => {
    // A currency with no rate is totalled at face value.
    for (const one of CURRENCIES) {
      expect(one.code).toMatch(/^[A-Z]{3}$/)
      expect(one.name).not.toBe('')
    }
    expect(new Set(CURRENCIES.map((one) => one.code)).size).toBe(CURRENCIES.length)
  })

  it('takes its symbol from the same formatter the amount will print with', () => {
    expect(currencySymbol('USD', 'en-US')).toBe('$')
    expect(currencySymbol('EUR', 'en-US')).toBe('€')
    expect(currencySymbol('GBP', 'en-US')).toBe('£')
  })

  it('falls back to the code rather than throwing on a keystroke', () => {
    // Read on every render, including mid-way through typing a code.
    expect(currencySymbol('ZZZ')).toBe('ZZZ')
    expect(currencySymbol('')).toBe('')
  })
})

// The backend's SUPPORTED_CURRENCIES default is the source of truth: it is
// what rates are fetched for.
describe('the offered list matches what rates are stored for', () => {
  it('offers exactly the backend default set', () => {
    const backendDefault = [
      'USD', 'EUR', 'GBP', 'CAD', 'AUD', 'CHF', 'JPY', 'MXN',
      'BRL', 'INR', 'SEK', 'DKK', 'NOK', 'PLN', 'CZK', 'NZD',
    ]
    expect([...CURRENCIES.map((one) => one.code)].sort()).toEqual([...backendDefault].sort())
  })
})

describe('currencyOptions', () => {
  it('labels each code with its name', () => {
    expect(currencyOptions()[0]).toEqual({ value: 'USD', label: 'USD · US dollar' })
  })

  it('leads with a held code the list does not offer, so a save keeps it', () => {
    const options = currencyOptions('XAF')
    expect(options[0]).toEqual({ value: 'XAF', label: 'XAF' })
    expect(options.slice(1)).toEqual(currencyOptions())
  })

  it('adds nothing for a held code it offers', () => {
    expect(currencyOptions('EUR')).toEqual(currencyOptions())
  })
})
