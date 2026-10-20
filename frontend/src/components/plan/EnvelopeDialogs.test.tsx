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
import { planEnvelope } from './__fixtures__/plan'
import {
  AddPlannedExpenseDialog,
  AddToPlannedSpendDialog,
  EnvelopeEditDialog,
} from './EnvelopeDialogs'

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

/** Whether the switch with this accessible name is on. */
function switchState(markup: string, label: string): string | undefined {
  return new RegExp(`aria-checked="(true|false)"[^>]*aria-label="${label}"|aria-label="${label}"[^>]*aria-checked="(true|false)"`)
    .exec(markup)
    ?.slice(1)
    .find(Boolean)
}

describe('the rollover switches', () => {
  const ROLL_OVER = 'Roll unspent money over into next month'
  const ROLL_UNDER = 'Roll overspending under into next month'

  it('offers roll over and roll under as separate switches, each with its hint, both off for a new expense', () => {
    const rendered = renderToStaticMarkup(
      <AddPlannedExpenseDialog
        open
        categories={CATEGORIES}
        month="2026-08"
        onOpenChange={() => undefined}
        onCreate={() => undefined}
      />,
    )
    expect(rendered).toContain(ROLL_OVER)
    expect(rendered).toContain(ROLL_UNDER)
    expect(rendered).toContain('Off, it goes back to free-to-spend.')
    expect(rendered).toContain('Off, it starts clean.')
    expect(switchState(rendered, ROLL_OVER)).toBe('false')
    expect(switchState(rendered, ROLL_UNDER)).toBe('false')
  })

  it('shows an editing envelope its own two settings, independently', () => {
    const edit = (overrides: Parameters<typeof planEnvelope>[0]) =>
      renderToStaticMarkup(
        <EnvelopeEditDialog
          envelope={planEnvelope(overrides)}
          mode="rollover"
          onOpenChange={() => undefined}
          onRelease={() => undefined}
          onSetAmount={() => undefined}
          onRolloverSetting={() => undefined}
        />,
      )

    const both = edit({ auto_release_rollover: false, roll_under: true })
    expect(switchState(both, ROLL_OVER)).toBe('true')
    expect(switchState(both, ROLL_UNDER)).toBe('true')

    const underOnly = edit({ auto_release_rollover: true, roll_under: true })
    expect(switchState(underOnly, ROLL_OVER)).toBe('false')
    expect(switchState(underOnly, ROLL_UNDER)).toBe('true')

    const overOnly = edit({ auto_release_rollover: false, roll_under: false })
    expect(switchState(overOnly, ROLL_OVER)).toBe('true')
    expect(switchState(overOnly, ROLL_UNDER)).toBe('false')
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
