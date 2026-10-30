/**
 * The register's account header: a card's statement figure is drawn only when
 * there is one, and the actions menu is out of the card's flex flow.
 */

import { describe, expect, it } from 'vitest'

import { parseMoney } from '@/lib/money'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { account } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { AccountHeader, ValuationTooltip } from './AccountHeader'

const EM_DASH = '—'

function card(overrides: Partial<AccountWithBalances> = {}): AccountWithBalances {
  return account({
    name: 'Sample Rewards Card',
    kind: 'credit_card',
    type: 'credit_card',
    masked_number: '0000',
    provider_balance: parseMoney('-1200.00'),
    balances: {
      balance: parseMoney('-1200.00'),
      balance_with_pending: parseMoney('-1230.00'),
      available_balance: parseMoney('-1200.00'),
      credit_used_pct: null,
    },
    ...overrides,
  })
}

function render(account: AccountWithBalances): string {
  return renderScreen(<AccountHeader account={account} accounts={[account]} />)
}

describe('a card whose issuer reports no statement', () => {
  it('draws no em dash anywhere in the header', () => {
    expect(render(card())).not.toContain(EM_DASH)
  })

  it('leaves the labels out rather than heading an empty figure', () => {
    const markup = render(card())
    expect(markup).not.toContain('Statement balance')
    expect(markup).not.toContain('Minimum due')
    expect(markup).not.toContain('Due date')
    expect(markup).not.toContain('Interest rate')
    expect(markup).not.toContain('Credit used')
  })

  it('still draws the balance and the account', () => {
    const markup = render(card())
    expect(markup).toContain('Sample Rewards Card')
    expect(markup).toContain('$1,200.00')
  })
})

describe('a card with figures on file', () => {
  const filled = card({
    statement_balance: parseMoney('1200.00'),
    minimum_due: parseMoney('35.00'),
    due_date: '2026-09-28',
    interest_rate: '0.2499',
    provider_balance: parseMoney('-1250.00'),
    balances: {
      balance: parseMoney('-1250.00'),
      balance_with_pending: parseMoney('-1280.00'),
      available_balance: parseMoney('-1250.00'),
      credit_used_pct: '0.25',
    },
  })

  it('prints each one it has', () => {
    const markup = render(filled)
    expect(markup).toContain('Statement balance')
    expect(markup).toContain('$1,200.00')
    expect(markup).toContain('Minimum due')
    expect(markup).toContain('$35.00')
    expect(markup).toContain('Due date')
    expect(markup).toContain('24.99%')
    expect(markup).toContain('25%')
  })

  it('draws the gauge only for a credit-used figure that exists', () => {
    expect(render(filled)).toContain('class="gauge"')
    expect(render(card())).not.toContain('class="gauge"')
  })

    // Whichever of the last two figures exists holds the right edge.
  it('gives the right edge to whichever of the last two figures exists', () => {
    expect(render(filled)).toContain('strip__item strip--right')
    const limitOnly = card({
      balances: { ...card().balances, credit_used_pct: '0.25' },
    })
    expect(render(limitOnly)).toContain('strip__item strip--right')
  })
})

describe('the market-value tooltip', () => {
  it('prints the hovered point', () => {
    const point = { label: 'Mar 2026', value: 182000, amount: parseMoney('182000.00') }
    const markup = renderScreen(<ValuationTooltip payload={[{ payload: point }]} />)
    expect(markup).toContain('Mar 2026')
    expect(markup).toContain('Market value')
    expect(markup).toContain('$182,000.00')
  })

  it('draws nothing with no point under the cursor', () => {
    expect(renderScreen(<ValuationTooltip payload={[]} />)).not.toContain('Market value')
  })
})

describe('the actions menu', () => {
  it('is out of the card flow, so it cannot move the balance', () => {
    // Absolutely positioned by this class; the balance block holds the right
    // edge on its own margin whether or not this button is there.
    expect(render(card())).toContain('context-card__menu')
  })

  it('keeps a labelled control for the keyboard rather than right-click alone', () => {
    expect(render(card())).toContain('aria-label="Actions for Sample Rewards Card"')
  })
})
