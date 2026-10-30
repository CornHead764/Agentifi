import type { ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { transaction } from '@/test/builders'

import { ForeignAmount } from './ForeignAmount'

function render(node: ReactNode): string {
  return renderToStaticMarkup(node)
}

describe('<ForeignAmount>', () => {
  it('prints one figure for a row in the household’s own currency', () => {
    const html = render(<ForeignAmount txn={transaction({ amount: moneyFromCents(-4000) })} />)
    expect(html).toContain('-$40.00')
    expect(html).not.toContain('foreign-amount')
  })

  it('leads with the converted amount, because that is what every total uses', () => {
    // A row is foreign exactly when it carries a converted amount: nothing is
    // written unless the currency differed from the space's.
    const html = render(
      <ForeignAmount
        txn={transaction({
          amount: moneyFromCents(-4000),
          currency: 'EUR',
          amount_primary: moneyFromCents(-4444),
        })}
      />,
    )
    const converted = html.indexOf('-$44.44')
    const original = html.indexOf('€40.00')
    expect(converted).toBeGreaterThanOrEqual(0)
    expect(original).toBeGreaterThan(converted)
  })

  it('prints the original in its own currency, not the household’s symbol', () => {
    // A statement showing €40 beside an app showing $40 is a discrepancy
    // somebody spends an evening on.
    const html = render(
      <ForeignAmount
        txn={transaction({
          amount: moneyFromCents(-4000),
          currency: 'GBP',
          amount_primary: moneyFromCents(-5100),
        })}
      />,
    )
    expect(html).toContain('£40.00')
    expect(html).toContain('-$51.00')
  })
})
