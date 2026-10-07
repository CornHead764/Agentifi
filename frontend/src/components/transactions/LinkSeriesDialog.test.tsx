/**
 * The link picker: the charge on top, the recurring list under a kind filter.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { parseMoney } from '@/lib/money'
import { recurrenceFor } from '@/lib/recurrence'
import type { Series } from '@/lib/clients/upcoming'
import type { Account, Transaction } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'
import { LinkSeriesDialog } from './LinkSeriesDialog'

const ACCOUNT = { id: 'a1', name: 'Everyday Checking' } as Account

const TXN = {
  id: 't1',
  account_id: 'a1',
  date: '2026-08-28',
  amount: parseMoney('-2345.67'),
  payee: 'Mortgage',
  statement_name: 'MTG PAYMENT 00812',
  series_id: null,
} as unknown as Transaction

function series(over: Partial<Series>): Series {
  return {
    id: 's1',
    account_id: 'a1',
    category_id: null,
    kind: 'bill',
    description: 'MTG PAYMENT',
    display_name: 'Mortgage',
    label: 'Mortgage',
    amount: parseMoney('-2345.67'),
    currency: 'USD',
    recurrence: recurrenceFor('EVERY_MONTH', new Date(2026, 0, 1)),
    start_on: '2026-01-01',
    end_on: null,
    next_due_on: '2026-09-01',
    due_on: '2026-09-01',
    override_next_due_on: null,
    override_next_amount: null,
    auto_adjust_due_on: false,
    reminder_days: 3,
    match_criteria: 'auto',
    match_amount_min: null,
    match_amount_max: null,
    tag_ids: [],
    splits: [],
    is_active: true,
    annualized_amount: parseMoney('-28148.04'),
    ...over,
  } as Series
}

function render(rows: Series[]): string {
  return renderScreen(
    <LinkSeriesDialog
      txn={TXN}
      accounts={[ACCOUNT]}
      onClose={() => {}}
      onLink={() => {}}
      onCreateInstead={() => {}}
    />,
    { seed: [[['series', 'all', ''], rows]] },
  )
}

describe('the link-to-series picker', () => {
  it('names the charge being filed and lists the recurring rows', () => {
    const rendered = render([series({}), series({ id: 's2', label: 'Brokerage Transfer', kind: 'transfer' })])
    expect(rendered).toContain('Link to a recurring item')
    expect(rendered).toContain('Aug 28, 2026')
    expect(rendered).toContain('Mortgage')
    expect(rendered).toContain('Brokerage Transfer')
    expect(rendered).toContain('Everyday Checking')
  })

  it("offers Simplifi's kind filters and the create escape hatch", () => {
    const rendered = render([series({})])
    for (const label of ['All', 'Bills', 'Income', 'Transfers']) {
      expect(rendered).toContain(label)
    }
    expect(rendered).toContain('Make it a new recurring item')
  })

  it('leaves a paused series out: there is no slot to claim', () => {
    const rendered = render([series({ id: 's3', label: 'Canceled Gym', is_active: false })])
    expect(rendered).not.toContain('Canceled Gym')
  })

    // Only the list flexes, and every state it can be in is marked, so an
    // empty result cannot collapse the dialog.
  it('lets only the list resize as the search narrows it', () => {
    expect(render([series({})])).toContain('dialog__scroller')
    expect(render([])).toContain('dialog__scroller')
  })

  it('draws the search as one field with its magnifier inside it', () => {
    expect(render([series({})])).toContain('class="search link-series__search"')
  })
})
