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
  ReleaseAllDialog,
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

const ROLL_OVER = 'Roll underspending into the next month'
const ROLL_UNDER = 'Roll overspending into the next month'

/** Whether the switch labelled `label`, which follows its label, is on. */
function switchState(markup: string, label: string): string | undefined {
  const after = markup.slice(markup.indexOf(label))
  return /aria-checked="(true|false)"/.exec(after)?.[1]
}

describe('the rollover switches', () => {
  it('offers roll over and roll under as separate switches, both off for a new expense', () => {
    const rendered = renderToStaticMarkup(
      <AddPlannedExpenseDialog
        open
        categories={CATEGORIES}
        month="2026-08"
        onOpenChange={() => undefined}
        onCreate={() => undefined}
      />,
    )
    expect(switchState(rendered, ROLL_OVER)).toBe('false')
    expect(switchState(rendered, ROLL_UNDER)).toBe('false')
  })

  it('shows an editing envelope its own two settings, independently', () => {
    const edit = (overrides: Partial<Envelope>) =>
      renderToStaticMarkup(
        <EnvelopeEditDialog
          envelope={planEnvelope(overrides)}
          mode="rollover"
          monthEnded
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
        onRolloverSetting={() => undefined}
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

describe('the Release unspent funds dialog with money carried in', () => {
  const release = (envelope: Envelope) =>
    renderToStaticMarkup(
      <EnvelopeEditDialog
        envelope={envelope}
        mode="release"
        monthEnded
        onOpenChange={() => undefined}
        onRelease={() => undefined}
        onSetAmount={() => undefined}
        onRolloverSetting={() => undefined}
      />,
    )
  // $200.00 target + $50.00 carried in − $120.00 spent: $80.00 of this month
  // and $50.00 carried in.
  const carried = (rollsOver: boolean) =>
    planEnvelope({
      rollover_amount: moneyFromCents(5_000),
      unspent_this_month: moneyFromCents(8_000),
      unspent_carried_in: moneyFromCents(5_000),
      rolls_over: rollsOver,
    })

  it('offers all of it, each part and an amount of their own', () => {
    const rendered = release(carried(true))
    expect(rendered).toContain('All of it')
    expect(rendered).toContain('Only what carried in')
    expect(rendered).toContain('Only this month&#x27;s unspent')
    expect(rendered).toContain('Another amount')
  })

  it("starts at this month's part when the envelope rolls over, keeping the rest", () => {
    const rendered = release(carried(true))
    expect(rendered).toMatch(/value="this_month"[^>]*aria-checked="true"|aria-checked="true"[^>]*value="this_month"/)
    expect(rendered).toMatch(/Release <span[^>]*>\$80\.00/)
    expect(rendered).toMatch(/Available after<\/dt><dd[^>]*>.*?\$50\.00/)
  })

  it('starts at all of it when the envelope does not roll over', () => {
    const rendered = release(carried(false))
    expect(rendered).toMatch(/value="all"[^>]*aria-checked="true"|aria-checked="true"[^>]*value="all"/)
    expect(rendered).toMatch(/Release <span[^>]*>\$130\.00/)
  })
})

describe('the Release unspent funds dialog on an envelope that rolls over', () => {
  const release = (envelope: Envelope) =>
    renderToStaticMarkup(
      <EnvelopeEditDialog
        envelope={envelope}
        mode="release"
        monthEnded
        onOpenChange={() => undefined}
        onRelease={() => undefined}
        onSetAmount={() => undefined}
        onRolloverSetting={() => undefined}
      />,
    )

  it('says releasing gives up what next month would carry', () => {
    expect(release(planEnvelope({ rolls_over: true }))).toContain(
      'rolls over, so next month would carry what it has left. Releasing gives that up.',
    )
    expect(release(planEnvelope({ rolls_over: false }))).not.toContain('rolls over')
  })
})

describe('the Release all unspent funds dialog', () => {
  const envelopes = [
    // $200.00 − $120.00 = $80.00, and $200.00 − $230.00 = −$30.00.
    planEnvelope({ id: 'a', name: 'Gas & Fuel', rolls_over: false }),
    planEnvelope({ id: 'b', name: 'Gifts', rolls_over: false, spent: moneyFromCents(23_000) }),
    planEnvelope({ id: 'c', name: 'Groceries', rolls_over: true }),
    planEnvelope({ id: 'd', name: 'Travel', rolls_over: true }),
  ]
  const releaseAll = (monthEnded: boolean) =>
    renderToStaticMarkup(
      <ReleaseAllDialog
        open
        envelopes={envelopes}
        monthEnded={monthEnded}
        pending={false}
        onConfirm={() => undefined}
        onCancel={() => undefined}
      />,
    )

  it('shows the total and each envelope it releases', () => {
    const rendered = releaseAll(true)
    expect(rendered).toContain('Back to free-to-spend')
    expect(rendered).toContain('Release <span')
    expect(rendered).toContain('$50.00')
    expect(rendered).toContain('Gas &amp; Fuel')
    expect(rendered).toContain('$80.00')
    expect(rendered).toContain('Gifts')
    expect(rendered).toContain('-$30.00')
    expect(rendered).not.toContain('Groceries')
    expect(rendered).not.toContain('This month hasn')
  })

  it('names how many envelopes it leaves alone because they roll over', () => {
    expect(releaseAll(true)).toContain('2 envelopes that roll over are left alone')
  })

  it('warns that later spending overspends while the month runs', () => {
    expect(releaseAll(false)).toContain('This month hasn')
  })
})
