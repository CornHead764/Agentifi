import { describe, expect, it } from 'vitest'

import {
  absMoney,
  addMoney,
  formatMoney,
  moneyFromCents,
  optionalAmountWire,
  parseAmountInput,
  parseMoney,
  parseWholeNumber,
  amountFieldError,
  amountToWire,
  digitsToCents,
  divideMoney,
  scaleMoney,
  sumByCurrency,
  sumMoney,
  ZERO_MONEY,
} from './money'

describe('parseMoney', () => {
  it('the "1900.00" concatenation trap: adding two amounts is arithmetic, not string join', () => {
    const a = parseMoney('1900.00')
    const b = parseMoney('1900.00')

    expect(addMoney(a, b)).toBe(380_000)
    expect(formatMoney(addMoney(a, b))).toBe('$3,800.00')
    // The bug in one line: what the same values do without the boundary.
    expect(('1900.00' as unknown as number) + ('1900.00' as unknown as number)).toBe(
      '1900.001900.00',
    )
  })

  it('reads cents exactly, without passing through a float', () => {
    expect(parseMoney('0.07')).toBe(7)
    expect(parseMoney('0.1')).toBe(10)
    expect(parseMoney('1900.00')).toBe(190_000)
    expect(parseMoney('-12.34')).toBe(-1234)
    expect(parseMoney('0.00')).toBe(0)
    expect(parseMoney('12')).toBe(1200)
  })

  it('holds the full range of Numeric(15,2)', () => {
    expect(parseMoney('9999999999999.99')).toBe(999_999_999_999_999)
    expect(Number.isSafeInteger(parseMoney('9999999999999.99'))).toBe(true)
  })

  it('rounds unquantized input half away from zero', () => {
    expect(parseMoney('1.005')).toBe(101)
    expect(parseMoney('-1.005')).toBe(-101)
    expect(parseMoney('1.004')).toBe(100)
  })

  it('rejects a JSON number, which means the field did not go through Money.MarshalJSON', () => {
    expect(() => parseMoney(1900.0 as unknown as string)).toThrow(/must serialize Decimal/)
  })

  it('rejects unparseable values rather than yielding NaN', () => {
    expect(() => parseMoney('')).toThrow()
    expect(() => parseMoney('1,900.00')).toThrow()
    expect(() => parseMoney('about a hundred')).toThrow()
  })
})

describe('arithmetic', () => {
  it('sums a long series without drift', () => {
    const tenCents = parseMoney('0.10')
    const twentyCents = parseMoney('0.20')

    expect(addMoney(tenCents, twentyCents)).toBe(parseMoney('0.30'))
    // The float version of the same sum, which is what integer cents avoids.
    expect(0.1 + 0.2).not.toBe(0.3)

    const rows = Array.from({ length: 40_000 }, () => parseMoney('0.07'))
    expect(sumMoney(rows)).toBe(parseMoney('2800.00'))
  })

  it('sums nothing to zero', () => {
    expect(sumMoney([])).toBe(ZERO_MONEY)
  })

  it('rounds a scaled amount half away from zero, once', () => {
    expect(scaleMoney(parseMoney('100.00'), 0.075)).toBe(parseMoney('7.50'))
    expect(scaleMoney(parseMoney('0.05'), 0.5)).toBe(parseMoney('0.03'))
    expect(scaleMoney(parseMoney('-0.05'), 0.5)).toBe(parseMoney('-0.03'))
  })

  it('refuses a non-integer cent count', () => {
    expect(() => moneyFromCents(1.5)).toThrow()
  })
})

describe('formatMoney', () => {
  const groceries = parseMoney('-412.00')

  it('renders the stored sign by default', () => {
    expect(formatMoney(groceries)).toBe('-$412.00')
  })

  it('flips the sign for spending contexts', () => {
    expect(formatMoney(groceries, { signs: 'spend' })).toBe('$412.00')
    expect(formatMoney(parseMoney('2500.00'), { signs: 'spend' })).toBe('-$2,500.00')
  })

  it('never renders negative zero', () => {
    expect(formatMoney(ZERO_MONEY, { signs: 'spend' })).toBe('$0.00')
  })

  it('marks a positive delta explicitly when asked', () => {
    expect(formatMoney(parseMoney('1250.50'), { showPlus: true })).toBe('+$1,250.50')
    expect(formatMoney(parseMoney('-1250.50'), { showPlus: true })).toBe('-$1,250.50')
  })

  it('drops cents for tiles and axes', () => {
    expect(formatMoney(parseMoney('1250.50'), { showCents: false })).toBe('$1,251')
  })

  it('honours a currency other than the default', () => {
    expect(formatMoney(parseMoney('-99.99'), { currency: 'EUR', locale: 'en-US' })).toBe(
      '-€99.99',
    )
  })

  it('takes the absolute value on request', () => {
    expect(absMoney(groceries)).toBe(parseMoney('412.00'))
  })
})

describe('showSymbol', () => {
  it('drops the symbol for a register column where every row shares a currency', () => {
    expect(formatMoney(parseMoney('-345.67'), { showSymbol: false })).toBe('-345.67')
  })

  it('leaves the sign alone, because dropping the symbol is typographic not semantic', () => {
    expect(formatMoney(parseMoney('1.00'), { showSymbol: false, showPlus: true })).toBe('+1.00')
  })

  it('still shows the symbol by default', () => {
    expect(formatMoney(parseMoney('-345.67'))).toBe('-$345.67')
  })
})

describe('sumByCurrency', () => {
  it('keeps one total per currency and never adds across them', () => {
    const totals = sumByCurrency([
      { currency: 'USD', amount: moneyFromCents(10_000) },
      { currency: 'eur', amount: moneyFromCents(5_000) },
      { currency: 'USD', amount: moneyFromCents(-2_500) },
    ])
    expect(totals).toEqual([
      { currency: 'USD', amount: moneyFromCents(7_500) },
      { currency: 'EUR', amount: moneyFromCents(5_000) },
    ])
  })
})

describe('divideMoney', () => {
  it('rounds half away from zero in both directions', () => {
    expect(divideMoney(moneyFromCents(5), 2)).toBe(moneyFromCents(3))
    expect(divideMoney(moneyFromCents(-5), 2)).toBe(moneyFromCents(-3))
    expect(divideMoney(moneyFromCents(30_000), 3)).toBe(moneyFromCents(10_000))
  })
})

describe('digitsToCents', () => {
  it('pads the fraction and rounds a third digit half up', () => {
    expect(digitsToCents('12', '5')).toBe(1_250)
    expect(digitsToCents('0', '075')).toBe(8)
    expect(digitsToCents('0', '074')).toBe(7)
    expect(digitsToCents('', '')).toBe(0)
  })
})

describe('amountToWire', () => {
  it('writes cents as a plain 2-dp string', () => {
    expect(amountToWire(moneyFromCents(7))).toBe('0.07')
    expect(amountToWire(moneyFromCents(-125_000))).toBe('-1250.00')
  })
})

describe('parseAmountInput', () => {
  it('reads the digits rather than passing through a float', () => {
    expect(parseAmountInput('0.07')).toEqual({ wire: '0.07', cents: moneyFromCents(7) })
    expect(parseAmountInput('345.67')).toEqual({ wire: '345.67', cents: moneyFromCents(34_567) })
  })

  it('accepts what a person actually types', () => {
    expect(parseAmountInput('$1,900')?.wire).toBe('1900.00')
    expect(parseAmountInput(' -20 ')?.wire).toBe('-20.00')
    expect(parseAmountInput('.5')?.wire).toBe('0.50')
    expect(parseAmountInput('$250')?.wire).toBe('250.00')
    expect(parseAmountInput('1,000')?.wire).toBe('1000.00')
    expect(parseAmountInput('-$1,000.50')?.wire).toBe('-1000.50')
  })

  it('rounds a third decimal half away from zero, as `quantize()` does', () => {
    expect(parseAmountInput('1.005')?.wire).toBe('1.01')
    expect(parseAmountInput('-1.005')?.wire).toBe('-1.01')
    expect(parseAmountInput('1.004')?.wire).toBe('1.00')
  })

  it('rejects anything that is not an amount', () => {
    expect(parseAmountInput('')).toBeNull()
    expect(parseAmountInput('one fifty')).toBeNull()
    expect(parseAmountInput('12.34.56')).toBeNull()
  })

  it('reads accounting parens as negative, with a redundant sign inside not cancelling it back out', () => {
    expect(parseAmountInput('(5)')?.wire).toBe('-5.00')
    expect(parseAmountInput('(-5)')?.wire).toBe('-5.00')
    expect(parseAmountInput('(1,200.00)')?.wire).toBe('-1200.00')
  })

  it('accepts £ and €, like it already accepts $', () => {
    expect(parseAmountInput('£1,900')?.wire).toBe('1900.00')
    expect(parseAmountInput('€250')?.wire).toBe('250.00')
  })

  it('round-trips through the wire format', () => {
    for (const cents of [0, 5, -5, 99, -100, 123_456_789]) {
      const wire = amountToWire(moneyFromCents(cents))
      expect(parseAmountInput(wire)?.cents).toBe(moneyFromCents(cents))
    }
  })
})

describe('optionalAmountWire', () => {
  it('reads a typed amount, blank as null and a typo as undefined', () => {
    expect(optionalAmountWire('$1,250.50')).toBe('1250.50')
    expect(optionalAmountWire('  ')).toBeNull()
    expect(optionalAmountWire('ten')).toBeUndefined()
  })

  it('names the typed text in the field error', () => {
    expect(amountFieldError('ten')).toBe('"ten" is not an amount.')
    expect(amountFieldError('1,000')).toBeNull()
    expect(amountFieldError('')).toBeNull()
  })
})

describe('parseWholeNumber', () => {
  it('reads digits, grouped by commas or not', () => {
    expect(parseWholeNumber('7')).toBe(7)
    expect(parseWholeNumber(' 12000 ')).toBe(12_000)
    expect(parseWholeNumber('12,000')).toBe(12_000)
    expect(parseWholeNumber('0')).toBe(0)
  })

  it('rejects anything that is not a whole number', () => {
    expect(parseWholeNumber('')).toBeNull()
    expect(parseWholeNumber('-3')).toBeNull()
    expect(parseWholeNumber('2.5')).toBeNull()
    expect(parseWholeNumber('12,00')).toBeNull()
    expect(parseWholeNumber('ten')).toBeNull()
    expect(parseWholeNumber('99999999999999999999')).toBeNull()
  })
})
