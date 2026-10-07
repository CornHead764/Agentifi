/**
 * An empty portfolio on the dashboard is filled from the dashboard: a holding
 * when an investment account exists, the account itself when none does.
 */

import { describe, expect, it } from 'vitest'

import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import { renderScreen } from '@/test/renderScreen'

import { AddInvestmentAction } from './AddInvestmentAction'

function render(accounts: { id: string; name: string; kind: string; is_closed: boolean }[]) {
  return renderScreen(<AddInvestmentAction />, { seed: [[ACCOUNTS_KEY, accounts]] })
}

describe('AddInvestmentAction', () => {
  it('adds a holding in place when there is an investment account', () => {
    const html = render([{ id: 'b1', name: 'Taxable Brokerage', kind: 'investment', is_closed: false }])
    expect(html).toContain('Add a holding')
  })

  it('adds the investment account first when there is none', () => {
    const html = render([{ id: 'c1', name: 'Everyday Checking', kind: 'cash', is_closed: false }])
    expect(html).toContain('Add an investment account')
    expect(html).not.toContain('Go to Investments')
  })
})
