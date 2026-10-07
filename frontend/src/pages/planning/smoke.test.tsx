/**
 * Planning Tools renders with no server behind it: the first render has the
 * projection pending, so every assumption placeholder reads `undefined`.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'

import { PlanningToolsPage } from '@/pages/PlanningToolsPage'
import { renderScreen } from '@/test/renderScreen'

function render(node: ReactNode): string {
  return renderScreen(node)
}

describe('Planning Tools', () => {
  it('renders with no server behind it', () => {
    const markup = render(<PlanningToolsPage />)
    expect(markup).toContain('Projected investments')
    expect(markup).toContain('Assumptions')
  })

  it('offers no credit score', () => {
    // There is no bureau feed behind one, and a tab whose only content is an
    // apology for its own emptiness is a click that returns nothing.
    const markup = render(<PlanningToolsPage />)
    expect(markup).not.toContain('Credit Score')
    expect(markup).not.toContain('Credit score')
  })

  it('puts every assumption on screen as its own control', () => {
    // The rule the page exists to keep: a projection that hides its return
    // rate is a number nobody can check.
    const markup = render(<PlanningToolsPage />)
    for (const label of [
      'Your age',
      'Retirement age',
      'Current investments',
      'Monthly contribution',
      'Annual return %',
      'Inflation %',
      'Withdrawal rate %',
      'Income you want',
    ]) {
      expect(markup).toContain(label)
    }
  })

  it('gives each assumption the input its kind needs', () => {
    const markup = render(<PlanningToolsPage />)
    // Ages are whole numbers, rates take a decimal point, and money reads "No target" when blank.
    expect(markup.match(/inputMode="numeric"/g)?.length).toBe(3)
    expect(markup).toContain('inputMode="decimal"')
    expect(markup).toContain('placeholder="No target"')
  })
})

describe('the planner modes', () => {
  it('offers Basic and Advanced, opening on Basic', () => {
    const markup = render(<PlanningToolsPage />)
    expect(markup).toContain('>Basic<')
    expect(markup).toContain('>Advanced<')
    // Basic's own fields are on screen; Advanced's tax-split ones are not.
    expect(markup).toContain('Current investments')
    expect(markup).not.toContain('Already-taxed balance')
  })
})
