/**
 * A billed account's row offers to match its history once a reminder is
 * linked: an unlinked account has no payments to match. Every name is
 * invented.
 */

import { describe, expect, it } from 'vitest'

import type { BillSubaccount } from '@/lib/clients/bills'
import { renderScreen } from '@/test/renderScreen'

import { SubaccountRow } from './SubaccountRow'

function subaccount(over: Partial<BillSubaccount> = {}): BillSubaccount {
  return {
    id: 'sub1',
    connection_id: 'c1',
    biller: 'northwestern-mutual',
    external_id: 'acct-1',
    label: 'Billing account',
    masked_number: null,
    is_selected: true,
    series_id: null,
    account_id: null,
    ...over,
  }
}

describe('a billed account row', () => {
  it('offers Match history for a linked reminder', () => {
    const rendered = renderScreen(
      <SubaccountRow subaccount={subaccount({ series_id: 'ser1' })} provider="Example Insurance" />,
    )
    expect(rendered).toContain('Match history')
    expect(rendered).toContain('aria-label="Match the history of Billing account"')
  })

  it('offers nothing to match before a reminder is linked', () => {
    const rendered = renderScreen(
      <SubaccountRow subaccount={subaccount()} provider="Example Insurance" />,
    )
    expect(rendered).not.toContain('Match history')
    expect(rendered).toContain('Link a reminder')
  })
})
