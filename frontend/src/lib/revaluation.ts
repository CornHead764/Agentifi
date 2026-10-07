/**
 * What a re-price did, said plainly. A result with an adjustment moved the
 * balance; one with an estimate and no adjustment found the balance already
 * right; anything else was skipped, and says why.
 */

import type { PricedAs, ValuationResult } from '@/lib/clients/connections'
import { asSentence, formatCount, plural } from '@/lib/format'
import { formatMoney, type Money, type MoneyFormatter } from '@/lib/money'
import type { Account } from '@/lib/transactions/types'

export interface RevaluationReport {
  title: string
  description?: string
  tone: 'neutral' | 'success' | 'error'
}

type Priceable = Pick<
  Account,
  'kind' | 'type' | 'connection_id' | 'simplefin_account_id' | 'property_address' | 'vehicle_vin'
>

/**
 * Whether an account can be re-priced from outside: an asset of a type this
 * server prices, with what the source looks it up by, and no bank feed that
 * owns its balance.
 */
export function canReprice(account: Priceable, configured: readonly string[]): boolean {
  if (account.kind !== 'asset' || !configured.includes(account.type)) return false
  if (account.connection_id !== null || account.simplefin_account_id) return false
  if (account.type === 'real_estate') return Boolean(account.property_address?.trim())
  if (account.type === 'vehicle') return Boolean(account.vehicle_vin?.trim())
  return false
}

type Outcome = 'repriced' | 'unchanged' | 'skipped'

export function revaluationOutcome(result: ValuationResult): Outcome {
  if (result.adjustment !== null) return 'repriced'
  if (result.estimate !== null) return 'unchanged'
  return 'skipped'
}

/**
 * What the source took the asset to be, so a mistyped VIN or an address
 * matched to the wrong house shows as well as the number does. Null when it
 * said nothing.
 */
export function pricedAsText(
  priced: PricedAs | null,
  currency?: string,
  format: MoneyFormatter = formatMoney,
): string | null {
  if (priced === null) return null
  const parts: string[] = []
  const vehicle = [priced.year, priced.make, priced.model, priced.trim]
    .filter((part): part is string => part !== null && part.trim() !== '')
    .join(' ')
  if (vehicle !== '') {
    const article = /^[aeiou]/i.test(vehicle) ? 'an' : 'a'
    let text = `Priced as ${article} ${vehicle}`
    if (priced.mileage !== null) {
      text += ` at ${formatCount(priced.mileage)} miles`
      if (priced.typical_mileage) text += ', a typical mileage, as no odometer reading is stored'
    }
    parts.push(`${text}.`)
  }
  if (priced.address !== null && priced.address.trim() !== '') {
    parts.push(`Matched to ${priced.address.trim()}.`)
  }
  if (priced.low !== null && priced.high !== null) {
    const money = (value: Money) => format(value, { currency, showCents: false })
    parts.push(`Range ${money(priced.low)} to ${money(priced.high)}.`)
  }
  return parts.length === 0 ? null : parts.join(' ')
}

/** The whole space, from "Re-price all now". */
export function revaluationReport(
  results: ValuationResult[],
  format: MoneyFormatter = formatMoney,
): RevaluationReport {
  if (results.length === 0) {
    return {
      title: 'Nothing was re-priced',
      description: 'No asset account has a type this server can price.',
      tone: 'neutral',
    }
  }

  const count = { repriced: 0, unchanged: 0, skipped: 0 }
  for (const result of results) count[revaluationOutcome(result)] += 1

  const tally = [
    count.repriced > 0 ? `${count.repriced} re-priced` : null,
    count.unchanged > 0 ? `${count.unchanged} unchanged` : null,
    count.skipped > 0 ? `${count.skipped} skipped` : null,
  ]
    .filter((part) => part !== null)
    .join(', ')
  // One line per asset: what it was priced as, or why it was skipped.
  const lines = results.flatMap((result) => {
    if (revaluationOutcome(result) === 'skipped') {
      return [`${result.name}: ${asSentence(result.skipped ?? 'no estimate')}`]
    }
    const priced = pricedAsText(result.priced_as, undefined, format)
    return priced === null ? [] : [`${result.name}: ${priced}`]
  })

  return {
    title: count.repriced > 0 ? `Re-priced ${plural(count.repriced, 'asset')}` : 'Nothing was re-priced',
    description: [asSentence(tally), ...lines].join('\n'),
    tone: count.repriced > 0 ? 'success' : 'neutral',
  }
}

/** The loading toast's title while one asset, or every asset, is looked up. */
export function repricingTitle(name?: string): string {
  return name === undefined ? 'Re-pricing every asset…' : `Re-pricing ${name}…`
}

/** The title of a re-price whose request failed; the server's reason goes beneath it. */
export function revaluationFailureTitle(name?: string): string {
  return name === undefined ? 'The assets were not re-priced' : `${name} was not re-priced`
}

/** One account's re-price, from the results the server sent back for it. */
export function singleRevaluationResults(
  results: ValuationResult[],
  name: string,
  currency?: string,
  format: MoneyFormatter = formatMoney,
): RevaluationReport {
  const [result] = results
  if (result) return singleRevaluationReport(result, currency, format)
  return {
    title: `${name} was not re-priced`,
    description: 'This server does not price this kind of asset.',
    tone: 'neutral',
  }
}

/** One account, from its menu. */
export function singleRevaluationReport(
  result: ValuationResult,
  currency?: string,
  format: MoneyFormatter = formatMoney,
): RevaluationReport {
  const money = (value: ValuationResult['estimate'], showPlus = false) =>
    value === null ? '' : format(value, { currency, showPlus })
  const priced = pricedAsText(result.priced_as, currency, format)
  const withPriced = (text: string) => (priced === null ? text : `${text} ${priced}`)

  switch (revaluationOutcome(result)) {
    case 'repriced':
      return {
        title: `${result.name} re-priced`,
        description: withPriced(
          `Now ${money(result.estimate)}, ${money(result.adjustment, true)} on its balance.`,
        ),
        tone: 'success',
      }
    case 'unchanged':
      return {
        title: `${result.name} is unchanged`,
        description: withPriced(`The estimate of ${money(result.estimate)} matches its balance.`),
        tone: 'neutral',
      }
    case 'skipped':
      return {
        title: `${result.name} was not re-priced`,
        description: asSentence(result.skipped ?? 'The source had no estimate for this asset.'),
        tone: 'neutral',
      }
  }
}
