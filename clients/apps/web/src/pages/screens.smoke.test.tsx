/**
 * Every screen renders with no data at all, catching what the type checker
 * cannot: a hook outside its provider, a `.map` over a pending query, a
 * component missing from a `switch`.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'

import { renderScreen } from '@/test/renderScreen'
import { BillsSettings } from './settings/BillsSettings'
import { DashboardPage } from './DashboardPage'
import { InvestingPage } from './InvestingPage'
import { NetWorthPage } from './NetWorthPage'
import { ReportsPage } from './ReportsPage'
import { TransactionsPage } from './TransactionsPage'
import { UpcomingPage } from './UpcomingPage'

function render(node: ReactNode): string {
  return renderScreen(node)
}

describe('the six screens', () => {
  it('render with no server behind them', () => {
    expect(render(<DashboardPage />)).toContain('Customize')
    expect(render(<NetWorthPage />)).toContain('Your net worth')
    expect(render(<UpcomingPage />)).toContain('Overview')
    expect(render(<ReportsPage />)).toContain('New report')
    expect(render(<InvestingPage />)).toContain('Portfolio')
    expect(render(<TransactionsPage />)).toContain('Transactions')
  })
})

describe('the settings sections that mount a resource of their own', () => {
  it('renders bills with nothing behind it, which is where it starts', () => {
    // The `/bills` resource answers 404 on a server without its migration
    // applied, and the section must still render.
    expect(render(<BillsSettings />)).toContain('Bill providers')
  })
})
