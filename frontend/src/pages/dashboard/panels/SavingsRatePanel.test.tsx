/**
 * The Savings Rate card shows the amounts behind the rate, last month's, and a
 * trailing rate over the three completed months before this one.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import type { BucketKey, PlanBucket, SpendingPlanMonth } from '@/lib/spendingPlan'
import { renderScreen } from '@/test/renderScreen'

import { SavingsRatePanel } from './SavingsRatePanel'

function bucket(key: BucketKey, cents: number): PlanBucket {
  return {
    key,
    calculated_amount: moneyFromCents(cents),
    effective_amount: moneyFromCents(cents),
    overwritten_amount: null,
    contributing: [],
    excluded: [],
    contributing_txn_ids: [],
    excluded_entry_ids: [],
  }
}

/** A month with round figures: `income` in, `bills` out, nothing else. */
function planMonth(key: string, income: number, bills: number): SpendingPlanMonth {
  return {
    month: key,
    as_of: `${key}-21`,
    is_closed_out: false,
    closed_out_at: null,
    buckets: {
      income: bucket('income', income),
      bills: bucket('bills', bills),
      planned_spend: bucket('planned_spend', 0),
      other_spend: bucket('other_spend', 0),
      goals: bucket('goals', 0),
      rollover: bucket('rollover', 0),
    },
    bills: [],
    bill_subtotals: [],
    envelopes: [],
    contested_txn_ids: {},
    other_spend_by_category: [],
    projection: {
      type: 'run_rate',
      buffer: moneyFromCents(0),
      window_months: 3,
      start_on: null,
      end_on: null,
    },
    left_this_month: moneyFromCents(income + bills),
    per_day: null,
    days_remaining: 10,
    other_spend_to_date: moneyFromCents(0),
    projected_other_spending: moneyFromCents(0),
    projected_left: moneyFromCents(income + bills),
    month_result: moneyFromCents(income + bills),
    month_result_per_day: null,
    days_elapsed: 21,
    projected_month_result: moneyFromCents(income + bills),
  }
}

function render(months: Record<string, SpendingPlanMonth>): string {
  return renderScreen(<SavingsRatePanel />, {
    seed: Object.entries(months).map(([key, value]) => [['spending-plan', key], value] as const),
  })
}

afterEach(() => vi.useRealTimers())

function atAugust2026(): void {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(2026, 7, 21))
}

describe('the Savings Rate card', () => {
  it('shows the amounts behind this month`s rate', () => {
    atAugust2026()
    // $5,000 in, $2,000 of bills: $3,000 left, which is 60%.
    const markup = render({ '2026-08': planMonth('2026-08', 500_000, -200_000) })
    expect(markup).toContain('60%')
    expect(markup).toContain('$5,000.00')
    expect(markup).toContain('$2,000.00')
    expect(markup).toContain('$3,000.00')
  })

  it('shows last month and the trailing three once they land', () => {
    atAugust2026()
    const markup = render({
      '2026-08': planMonth('2026-08', 500_000, -200_000),
      // 20% last month, and $2,700 kept of $15,000 across the three: 18%.
      '2026-07': planMonth('2026-07', 500_000, -400_000),
      '2026-06': planMonth('2026-06', 500_000, -400_000),
      '2026-05': planMonth('2026-05', 500_000, -430_000),
    })
    expect(markup).toContain('Last month')
    expect(markup).toContain('20%')
    expect(markup).toContain('Last 3 months')
    expect(markup).toContain('18%')
  })

  it('waits for every month rather than naming a window it did not cover', () => {
    atAugust2026()
    // Two of the three months are missing, so the trailing figure is a dash
    // and not a rate over whichever months happened to be cached.
    const markup = render({
      '2026-08': planMonth('2026-08', 500_000, -200_000),
      '2026-07': planMonth('2026-07', 500_000, -400_000),
    })
    expect(markup).toContain('Last 3 months')
    expect(markup).not.toContain('Jun 2026')
  })

  it('draws a dash rather than 0% for a month with no income', () => {
    atAugust2026()
    // A month with no income did not fail to save any of it.
    const markup = render({ '2026-08': planMonth('2026-08', 0, -30_000) })
    expect(markup).not.toContain('0%')
    expect(markup).toContain('money--absent')
    // The spending is still real and still on screen.
    expect(markup).toContain('$300.00')
  })
})
