import { displayLocale } from './locale'

/**
 * The currencies the picker offers (the server accepts any three-letter code).
 * Keep in step with the backend's SUPPORTED_CURRENCIES default in
 * `internal/config/config.go`: a currency with no rate never converts. The
 * test pins the set.
 */

export interface CurrencyOption {
  code: string
  name: string
}

export const CURRENCIES: readonly CurrencyOption[] = [
  { code: 'USD', name: 'US dollar' },
  { code: 'EUR', name: 'Euro' },
  { code: 'GBP', name: 'Pound sterling' },
  { code: 'CAD', name: 'Canadian dollar' },
  { code: 'AUD', name: 'Australian dollar' },
  { code: 'JPY', name: 'Japanese yen' },
  { code: 'CHF', name: 'Swiss franc' },
  { code: 'SEK', name: 'Swedish krona' },
  { code: 'NOK', name: 'Norwegian krone' },
  { code: 'DKK', name: 'Danish krone' },
  { code: 'NZD', name: 'New Zealand dollar' },
  { code: 'MXN', name: 'Mexican peso' },
  { code: 'BRL', name: 'Brazilian real' },
  { code: 'INR', name: 'Indian rupee' },
  { code: 'PLN', name: 'Polish złoty' },
  { code: 'CZK', name: 'Czech koruna' },
]

/**
 * The picker's choices. A `held` code the list does not offer leads it, or a
 * save would change the currency.
 */
export function currencyOptions(held?: string): { value: string; label: string }[] {
  const options = CURRENCIES.map((one) => ({ value: one.code, label: `${one.code} · ${one.name}` }))
  if (held === undefined || CURRENCIES.some((one) => one.code === held)) return options
  return [{ value: held, label: held }, ...options]
}

/**
 * Every ISO 4217 currency whose minor unit is not two places, copied from the
 * backend's committed table (`internal/minorunits/iso4217.go`); the test
 * compares the two. ISO, not Intl's CLDR digits: CLDR shows an Iraqi dinar
 * with none, but its amounts are stored and sent with three.
 */
const MINOR_DIGITS: Readonly<Record<string, number>> = {
  BHD: 3, BIF: 0, CLF: 4, CLP: 0, DJF: 0, GNF: 0, IQD: 3, ISK: 0, JOD: 3, JPY: 0,
  KMF: 0, KRW: 0, KWD: 3, LYD: 3, OMR: 3, PYG: 0, RWF: 0, TND: 3, UGX: 0, UYI: 0,
  UYW: 4, VND: 0, VUV: 0, XAF: 0, XOF: 0, XPF: 0,
}

/** The places a currency's amounts carry: 2 for USD, 3 for KWD, 0 for JPY. */
export function currencyDigits(code: string): number {
  return MINOR_DIGITS[code.toUpperCase()] ?? 2
}

/** From Intl, so it agrees with what `formatMoney` renders. */
export function currencySymbol(code: string, locale: string | undefined = displayLocale()): string {
  try {
    const parts = new Intl.NumberFormat(locale, {
      style: 'currency',
      currency: code,
      minimumFractionDigits: 0,
    }).formatToParts(0)
    return parts.find((part) => part.type === 'currency')?.value ?? code
  } catch {
    return code
  }
}
