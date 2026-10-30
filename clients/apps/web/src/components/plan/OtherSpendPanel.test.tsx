import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import type {
  BucketKey,
  OtherSpendSlice,
  PlanBucket,
  SpendingPlanMonth,
} from '@/lib/spendingPlan'

import { OtherSpendPanel } from './OtherSpendPanel'

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

function slice(name: string, cents: number, children: OtherSpendSlice[] = []): OtherSpendSlice {
  return {
    category_id: `cat-${name}`,
    category_name: name,
    spent: moneyFromCents(cents),
    txn_ids: [],
    children,
  }
}

function month(): SpendingPlanMonth {
  return {
    month: '2026-08',
    as_of: '2026-08-21',
    is_closed_out: false,
    closed_out_at: null,
    buckets: {
      income: bucket('income', 900_000),
      bills: bucket('bills', -400_000),
      planned_spend: bucket('planned_spend', -100_000),
      other_spend: {
        ...bucket('other_spend', -620_000),
        excluded: [
          {
            id: 'x1',
            txn_id: 'x1',
            series_id: null,
            name: 'Refunded Return',
            due_on: '2026-08-10',
            status: 'paid' as const,
            category_name: 'Shopping',
            amount: moneyFromCents(-1_000),
            account_id: null,
            is_split: false,
            group: null,
            is_transfer: false,
            is_padding: false,
          },
        ],
      },
      goals: bucket('goals', 0),
      rollover: bucket('rollover', 0),
    },
    bills: [],
    bill_subtotals: [],
    envelopes: [],
    contested_txn_ids: {},
    other_spend_by_category: [
      slice('Shopping', 370_000, [slice('Electronics', 40_000)]),
      slice('Travel', 220_000),
    ],
    projection: {
      type: 'run_rate',
      buffer: moneyFromCents(0),
      window_months: 3,
      start_on: null,
      end_on: null,
    },
    left_this_month: moneyFromCents(-220_000),
    per_day: moneyFromCents(-20_000),
    days_remaining: 11,
    other_spend_to_date: moneyFromCents(620_000),
    projected_other_spending: moneyFromCents(620_000),
    projected_left: moneyFromCents(-220_000),
    month_result: moneyFromCents(-220_000),
    month_result_per_day: moneyFromCents(-20_000),
    days_elapsed: 21,
    projected_month_result: moneyFromCents(-220_000),
  }
}

const noop = () => {}

describe('<OtherSpendPanel>', () => {
  it('offers no conversion while the chart shows everything', () => {
    // Add to Planned Spend appears only once the chart is narrowed to one thing.
    const html = renderToStaticMarkup(
      <OtherSpendPanel
        month={month()}
        frozen={false}
        onProjectionChange={noop}
        onBufferChange={noop}
        onAddToPlanned={noop}
      />,
    )

    expect(html).toContain('Shopping')
    expect(html).not.toContain('Add to Planned Spend')
  })

  it('rides the excluded rows along in the table, greyed and counted', () => {
    const html = renderToStaticMarkup(
      <OtherSpendPanel
        month={month()}
        frozen={false}
        onProjectionChange={noop}
        onBufferChange={noop}
        onAddToPlanned={noop}
      />,
    )

    expect(html).toContain('1 excluded (greyed, not in the total)')
    expect(html).toContain('other-spend__row--excluded')
    expect(html).toContain('Refunded Return')
  })
})
