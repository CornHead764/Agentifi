/**
 * What the investing screens' warning marks say: each names the holdings or
 * positions it is about, from the same rows the figures are drawn from.
 */
import type { Holding, SecurityDetail } from '@/lib/clients/investments'
import { plural } from '@/lib/format'

import type { PriceFreshness } from './priceFreshness'

export const NO_BASIS = 'Incomplete cost basis: some lots have no purchase price.'
export const NO_CLOSE = 'No prior close yet: today’s move is unknown, not zero.'

/** "VTI, BND and 2 more": each symbol once, however many accounts hold it. */
function symbolList(holdings: readonly Holding[]): string {
  const symbols = [...new Set(holdings.map((one) => one.symbol))]
  if (symbols.length <= 3) return symbols.join(', ')
  return `${symbols.slice(0, 3).join(', ')} and ${symbols.length - 3} more`
}

/** Why the portfolio's total gain is badged Incomplete, naming the holdings it leaves out. */
export function missingBasisText(holdings: readonly Holding[]): string {
  const missing = holdings.filter((one) => !one.is_cost_basis_complete)
  if (missing.length === 0) return `${NO_BASIS} Total gain leaves those lots out.`
  return (
    `${missing.length} of ${plural(holdings.length, 'holding')} ` +
    `${missing.length === 1 ? 'has' : 'have'} no cost basis (${symbolList(missing)}). ` +
    'Total gain leaves them out rather than counting them as pure gain.'
  )
}

/** Why one holding's total cost is a warning rather than a figure. */
export function holdingMissingBasisText(holding: Holding): string {
  return `No cost basis for ${holding.symbol}: its lots have no purchase price, so total cost and gain are not shown.`
}

/** Why one security's total gain is badged Incomplete, counting the positions it leaves out. */
export function positionsMissingBasisText(detail: SecurityDetail): string {
  const missing = detail.positions.filter((one) => !one.is_cost_basis_complete).length
  if (missing === 0) return `${NO_BASIS} Total gain leaves those lots out.`
  return (
    `${missing} of ${plural(detail.positions.length, 'position')} in ${detail.security.symbol} ` +
    `${missing === 1 ? 'has' : 'have'} no cost basis. ` +
    'Total gain leaves them out rather than counting them as pure gain.'
  )
}

/** Why today's change is badged Incomplete, naming the holdings with no prior close. */
export function missingCloseText(holdings: readonly Holding[]): string {
  const missing = holdings.filter((one) => one.day_change === null)
  if (missing.length === 0) return `${NO_CLOSE} Today’s change leaves those holdings out.`
  return (
    `No prior close on file for ${symbolList(missing)}. ` +
    'Today’s change leaves them out rather than counting them as unchanged.'
  )
}

/** Why the price date is flagged: the newest quote is over four days old. */
export function staleQuoteText(asOf: PriceFreshness): string {
  return (
    `The newest quote is from ${asOf.since}. ` +
    'Value, gain and today’s change use those prices until a refresh finds newer ones.'
  )
}
