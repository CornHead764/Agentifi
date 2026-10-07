/**
 * Import History with nothing ticked in the account filter still imports: it
 * offers every account rather than sitting greyed out.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { AccountWithBalances } from '@/lib/transactions/types'

import { ImportHistoryControl } from '../InvestingPage'

const BROKERAGE = { id: 'b1', name: 'Taxable Brokerage' } as AccountWithBalances

function render(chosen: string[], options: AccountWithBalances[]): string {
  return renderToStaticMarkup(
    <ImportHistoryControl chosen={chosen} options={options} onOpen={() => {}} />,
  )
}

describe('ImportHistoryControl', () => {
  it('stays usable when the filter has nothing ticked', () => {
    expect(render([], [BROKERAGE])).not.toContain('disabled')
  })

  it('is disabled only when there is no account at all', () => {
    expect(render([], [])).toContain('disabled')
  })
})
