import { describe, expect, it } from 'vitest'

import { registerParams } from '@/lib/transactions/api'

import { recentTransactionsQuery } from './dashboard'

describe('recentTransactionsQuery', () => {
  it('ends the window today, so a forecast cannot lead the list', () => {
    // Newest-first over an open end is the furthest future row: an import
    // brings forward-projected instances.
    const query = recentTransactionsQuery(5, '2026-08-29')
    expect(query.to).toBe('2026-08-29')
    expect(query.order).toBe('desc')
    expect(registerParams(query)).toContain('to=2026-08-29')
  })

  it('leaves the start open, so a quiet week still shows the last five', () => {
    // Capping both ends would empty the panel for a household with no recent spending.
    const query = recentTransactionsQuery(5, '2026-08-29')
    expect(query.from).toBeNull()
    expect(registerParams(query)).not.toContain('from=')
  })

  it('narrows to the accounts the widget was pointed at', () => {
    const params = registerParams(recentTransactionsQuery(5, '2026-08-29', ['a', 'b']))
    expect(params).toContain('account_id=a')
    expect(params).toContain('account_id=b')
  })

  it('asks for every account only when it was given none to prefer', () => {
    // An empty list is none; no list at all is the whole ledger.
    expect(registerParams(recentTransactionsQuery(5, '2026-08-29'))).not.toContain('account_id')
    expect(registerParams(recentTransactionsQuery(5, '2026-08-29', []))).toContain('account_id=')
  })

  it('does not filter on pending, so an unsettled charge still appears', () => {
    // A pending charge is dated today and belongs in the window.
    const params = registerParams(recentTransactionsQuery(5, '2026-08-29'))
    expect(params).not.toContain('pending')
    expect(params).not.toContain('reviewed=')
  })
})
