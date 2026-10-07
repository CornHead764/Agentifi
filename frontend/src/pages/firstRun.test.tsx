/**
 * A space with no accounts is led somewhere, not shown a page of zeros.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'

import { AccountsDrawer } from '@/components/shell/AccountsDrawer'
import { CURRENT_SPACE_KEY } from '@/lib/clients/spaces'
import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import { renderScreen } from '@/test/renderScreen'

import { DashboardPage } from './DashboardPage'
import { TransactionsPage } from './TransactionsPage'

function render(node: ReactNode, accounts: unknown[] = []): string {
  return renderScreen(node, {
    seed: [
      [ACCOUNTS_KEY, accounts],
      [CURRENT_SPACE_KEY, { id: 's1', name: 'Personal', is_owner: true }],
    ],
  })
}

describe('an empty space', () => {
  it('opens the dashboard on the three ways in, not the widget grid', () => {
    const html = render(<DashboardPage />)
    expect(html).toContain('Welcome to Agentifi')
    expect(html).toContain('href="/settings/accounts#connections"')
    expect(html).toContain('href="/settings/accounts#import"')
    expect(html).toContain('Add an account')
    expect(html).toContain('Import from Simplifi')
    expect(html).not.toContain('Customize')
  })

  it('points the accounts drawer at the same ways in', () => {
    const html = render(<AccountsDrawer tree={[]} />)
    expect(html).toContain('No accounts yet')
    expect(html).toContain('href="/settings/accounts#connections"')
  })

  it('does not blame filters in the register', () => {
    const html = render(<TransactionsPage />)
    expect(html).not.toContain('Clear all filters')
  })
})
