import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { DUPLICATES_KEY, type DuplicateList, type DuplicateRow } from '@/lib/clients/duplicates'
import { moneyFromCents } from '@/lib/money'
import { renderScreen, testQueryClient } from '@/test/renderScreen'
import { DuplicatesSettings } from './DuplicatesSettings'

/** The screen, rendered off a seeded cache. The wording is what is asserted. */
function render(seed?: (client: QueryClient) => void) {
  const client = testQueryClient()
  seed?.(client)
  return renderScreen(<DuplicatesSettings />, { client })
}

function row(overrides: Partial<DuplicateRow>): DuplicateRow {
  return {
    id: 'txn-a',
    date: '2026-03-15',
    amount: moneyFromCents(-2_500),
    currency: 'USD',
    payee: 'Corner Market',
    statement_name: 'Corner Market',
    category_name: 'Groceries',
    notes: null,
    source: 'simplifi_import',
    is_pending: false,
    is_transfer_leg: false,
    ...overrides,
  }
}

const list: DuplicateList = {
  count: 1,
  pairs: [
    {
      id: 'pair-1',
      account_id: 'acct-checking',
      account_name: 'Everyday Checking',
      days_apart: 1,
      suggested_keep_id: 'txn-a',
      first: row({}),
      second: row({
        id: 'txn-b',
        date: '2026-03-16',
        payee: 'POS DEBIT CORNER MKT 0042',
        statement_name: 'POS DEBIT CORNER MKT 0042',
        category_name: null,
        source: 'sync',
      }),
    },
  ],
}

describe('the possible duplicates screen', () => {
  it('renders with no server behind it', () => {
    expect(render()).toContain('Possible duplicates')
  })

  it('says there is nothing to check when no pair waits', () => {
    const markup = render((client) =>
      client.setQueryData(DUPLICATES_KEY, { count: 0, pairs: [] } satisfies DuplicateList),
    )
    expect(markup).toContain('Nothing to check')
    expect(markup).not.toContain('Keep this one')
  })

  it('shows both copies side by side with where each came from', () => {
    const markup = render((client) => client.setQueryData(DUPLICATES_KEY, list))
    expect(markup).toContain('Everyday Checking')
    expect(markup).toContain('Dated 1 day apart')
    expect(markup).toContain('Simplifi import')
    expect(markup).toContain('Bank sync')
    expect(markup).toContain('Groceries')
    expect(markup).toContain('Uncategorized')
    expect(markup).toContain('POS DEBIT CORNER MKT 0042')
  })

  it('offers a copy to keep for each row and the answer that keeps both', () => {
    const markup = render((client) => client.setQueryData(DUPLICATES_KEY, list))
    expect(markup.match(/Keep this one/g)).toHaveLength(2)
    expect(markup).toContain('Two transactions')
    expect(markup).toContain('Look again')
  })

  it('warns that retiring half of a transfer releases the pairing', () => {
    const withTransfer: DuplicateList = {
      ...list,
      pairs: [{ ...list.pairs[0], second: row({ id: 'txn-b', source: 'sync', is_transfer_leg: true }) }],
    }
    const markup = render((client) => client.setQueryData(DUPLICATES_KEY, withTransfer))
    expect(markup).toContain('Half of a transfer')
  })

  it('stacks a pair on a phone so the amount and the answer stay on screen', () => {
    const markup = render((client) => client.setQueryData(DUPLICATES_KEY, list))
    expect(markup).toContain('table--stack')
    expect(markup).toContain('data-label="Amount"')
  })
})
