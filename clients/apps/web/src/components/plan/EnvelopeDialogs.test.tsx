import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { moneyFromCents } from '@/lib/money'
import type { OtherSpendSlice } from '@/lib/spendingPlan'
import { AddPlannedExpenseDialog, AddToPlannedSpendDialog } from './EnvelopeDialogs'

const CATEGORIES = [
  { id: 'c1', name: 'Gas & Fuel', parent_id: null },
  { id: 'c2', name: 'Groceries', parent_id: null },
]

describe('the Add Planned Expense dialog', () => {
  it("offers Simplifi's field set: cadence, name, target, categories, rollover", () => {
    const rendered = renderToStaticMarkup(
      <AddPlannedExpenseDialog
        open
        categories={CATEGORIES}
        month="2026-08"
        onOpenChange={() => undefined}
        onCreate={() => undefined}
      />,
    )
    expect(rendered).toContain('Recurring')
    expect(rendered).toContain('only')
    expect(rendered).toContain('Name')
    expect(rendered).toContain('Target')
    expect(rendered).toContain('Gas &amp; Fuel')
    expect(rendered.toLowerCase()).toContain('roll')
  })
})

describe('the Add to Planned Spend dialog', () => {
  const slice: OtherSpendSlice = {
    category_id: 'c-home',
    category_name: 'Home',
    spent: moneyFromCents(90_000),
    txn_ids: ['t1'],
    children: [],
  }

  it('starts the target at what the category has already spent', () => {
    const rendered = renderToStaticMarkup(
      <AddToPlannedSpendDialog
        slice={slice}
        onOpenChange={() => undefined}
        onConfirm={() => undefined}
      />,
    )
    expect(rendered).toContain('Move Home to Planned Spend?')
    expect(rendered).toContain('900.00')
  })
})
