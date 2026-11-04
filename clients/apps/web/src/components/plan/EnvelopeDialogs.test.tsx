import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { moneyFromCents } from '@agentifi/core/money'
import type { Envelope, OtherSpendSlice } from '@/lib/spendingPlan'
import {
  AddPlannedExpenseDialog,
  AddToPlannedSpendDialog,
  EnvelopeEditDialog,
} from './EnvelopeDialogs'
import { planEnvelope } from './__fixtures__/plan'

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

describe('the Release unspent funds dialog', () => {
  const release = (envelope: Envelope, monthEnded: boolean) =>
    renderToStaticMarkup(
      <EnvelopeEditDialog
        envelope={envelope}
        mode="release"
        monthEnded={monthEnded}
        onOpenChange={() => undefined}
        onRelease={() => undefined}
        onSetAmount={() => undefined}
        onAutoRelease={() => undefined}
      />,
    )

  it('releases what is left back to free-to-spend', () => {
    // $200.00 target − $120.00 spent.
    const rendered = release(planEnvelope(), true)
    expect(rendered).toContain('Back to free-to-spend')
    expect(rendered).toContain('Release <span')
    expect(rendered).toContain('$80.00')
    expect(rendered).not.toContain('This month hasn')
  })

  it('says an overspent envelope is covered from free-to-spend, not returned to it', () => {
    // $200.00 target − $250.00 spent is $50.00 overspent.
    const rendered = release(planEnvelope({ spent: moneyFromCents(25_000) }), true)
    expect(rendered).toContain('Taken from free-to-spend')
    expect(rendered).toContain('Cover <span')
    expect(rendered).not.toContain('Back to free-to-spend')
    expect(rendered).toContain('-$50.00')
  })

  it('warns that later spending overspends while the month runs', () => {
    const rendered = release(planEnvelope(), false)
    expect(rendered).toContain('This month hasn')
    expect(rendered).toContain('overspent by that amount')
  })
})
