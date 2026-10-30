/** The chosen locale reaches every formatter, read back through the functions screens call. */

import { afterEach, describe, expect, it } from 'vitest'

import { currencySymbol } from './currencies'
import { formatCount, formatDate, formatMonthKey, formatPercent, formatShares } from './format'
import { displayLocale, setDisplayLocale } from './locale'
import { formatMoney, moneyFromCents, parseMoney } from './money'

const cents = moneyFromCents

afterEach(() => setDisplayLocale(null))

describe('the display locale', () => {
  it('writes money in the chosen locale, in the space currency', () => {
    setDisplayLocale('de-DE')
    expect(formatMoney(cents(123456), { currency: 'EUR' })).toBe('1.234,56 €')
    setDisplayLocale('en-US')
    expect(formatMoney(cents(123456), { currency: 'EUR' })).toBe('€1,234.56')
  })

  it('keeps every digit of the largest amount the client holds exactly', () => {
    // Ten-thousandths of the major unit, so a safe integer stops just past 900 billion.
    setDisplayLocale('en-US')
    expect(formatMoney(cents(89_999_999_999_999), { currency: 'USD' })).toBe('$899,999,999,999.99')
    expect(formatMoney(cents(-5), { currency: 'USD' })).toBe('-$0.05')
    expect(() => cents(999_999_999_999_999)).toThrow(TypeError)
  })

  it('writes a dinar with three places and a yen with none, in the chosen locale', () => {
    setDisplayLocale('de-DE')
    expect(formatMoney(parseMoney('1234.567'), { currency: 'KWD', showSymbol: false })).toBe('1.234,567')
    expect(formatMoney(parseMoney('1500'), { currency: 'JPY', showSymbol: false })).toBe('1.500')
  })

  it('writes dates and month names in the chosen locale', () => {
    setDisplayLocale('de-DE')
    expect(formatDate('2026-08-21', 'full')).toBe('21. Aug. 2026')
    expect(formatMonthKey('2026-08')).toBe('August 2026')
    setDisplayLocale('fr-FR')
    expect(formatMonthKey('2026-08')).toBe('août 2026')
  })

  it('writes counts, percentages and share counts in the chosen locale', () => {
    setDisplayLocale('de-DE')
    expect(formatCount(12345)).toBe('12.345')
    expect(formatPercent(0.125, { digits: 1 })).toBe('12,5%')
    expect(formatShares('1.0065')).toBe('1,0065')
  })

  it('gives the currency symbol the chosen locale uses', () => {
    setDisplayLocale('en-CA')
    expect(currencySymbol('USD')).toBe('US$')
    setDisplayLocale('en-US')
    expect(currencySymbol('USD')).toBe('$')
  })

  it('falls back to the browser for a blank or unusable tag', () => {
    setDisplayLocale('de-DE')
    setDisplayLocale('')
    expect(displayLocale()).toBeUndefined()
    setDisplayLocale('not a tag!')
    expect(displayLocale()).toBeUndefined()
    setDisplayLocale(' en-gb ')
    expect(displayLocale()).toBe('en-GB')
  })
})
