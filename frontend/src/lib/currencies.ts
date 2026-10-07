import { displayLocale } from '@/lib/locale'

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
