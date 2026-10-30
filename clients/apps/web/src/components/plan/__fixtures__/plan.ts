/** Shared builders for the plan component tests. Test-only. */
import { moneyFromCents } from '@/lib/money'
import type {
  BucketKey,
  Envelope,
  PlanBucket,
  PlanEntry,
  SpendingPlanMonth,
} from '@/lib/spendingPlan'

export function planEntry(overrides: Partial<PlanEntry> = {}): PlanEntry {
  return {
    id: 'e1',
    txn_id: null,
    series_id: null,
    name: 'Metro Electric',
    due_on: '2026-08-03',
    status: 'paid',
    category_name: 'Gas & Electric',
    amount: moneyFromCents(-30_000),
    account_id: null,
    is_split: false,
    group: 'bill',
    is_transfer: false,
    is_padding: false,
    ...overrides,
  }
}

export function planBucket(key: BucketKey, overrides: Partial<PlanBucket> = {}): PlanBucket {
  return {
    key,
    calculated_amount: moneyFromCents(0),
    effective_amount: moneyFromCents(0),
    overwritten_amount: null,
    contributing: [],
    excluded: [],
    contributing_txn_ids: [],
    excluded_entry_ids: [],
    ...overrides,
  }
}

export function planEnvelope(overrides: Partial<Envelope> = {}): Envelope {
  return {
    id: 'env1',
    name: 'Gas & Fuel',
    filter_id: 'f1',
    categories: [{ id: 'c1', name: 'Gas & Fuel' }],
    target_amount: moneyFromCents(20_000),
    overwritten_target_amount: null,
    target: moneyFromCents(20_000),
    rollover_amount: moneyFromCents(0),
    spent: moneyFromCents(12_000),
    budget: moneyFromCents(20_000),
    available: moneyFromCents(8_000),
    pct_used: '60.00',
    bar_pct: '60.00',
    state: 'normal',
    auto_release_rollover: false,
    recurring: true,
    txn_ids: ['t1', 't2'],
    entries: [],
    ...overrides,
  }
}

export function planMonth(overrides: Partial<SpendingPlanMonth> = {}): SpendingPlanMonth {
  return {
    month: '2026-08',
    as_of: '2026-08-30',
    is_closed_out: false,
    closed_out_at: null,
    buckets: {
      income: planBucket('income', {
        calculated_amount: moneyFromCents(850_000),
        effective_amount: moneyFromCents(850_000),
        contributing: [
          planEntry({
            id: 'i1',
            name: 'Acme Corp Payroll',
            status: 'received',
            group: null,
            category_name: 'Paycheck',
            amount: moneyFromCents(620_000),
          }),
        ],
        excluded: [
          planEntry({
            id: 'ix1',
            name: 'Refund',
            status: 'paid',
            group: null,
            amount: moneyFromCents(1_000),
          }),
        ],
      }),
      bills: planBucket('bills', {
        calculated_amount: moneyFromCents(-320_000),
        effective_amount: moneyFromCents(-320_000),
      }),
      planned_spend: planBucket('planned_spend', {
        calculated_amount: moneyFromCents(-100_000),
        effective_amount: moneyFromCents(-100_000),
      }),
      other_spend: planBucket('other_spend', {
        calculated_amount: moneyFromCents(-475_000),
        effective_amount: moneyFromCents(-475_000),
      }),
      goals: planBucket('goals'),
      rollover: planBucket('rollover', {
        calculated_amount: moneyFromCents(-65_000),
        effective_amount: moneyFromCents(-65_000),
      }),
    },
    bills: [],
    bill_subtotals: [],
    envelopes: [planEnvelope()],
    contested_txn_ids: {},
    other_spend_by_category: [],
    projection: {
      type: 'run_rate',
      buffer: moneyFromCents(0),
      window_months: 3,
      start_on: null,
      end_on: null,
    },
    left_this_month: moneyFromCents(-110_000),
    per_day: moneyFromCents(-55_000),
    days_remaining: 2,
    other_spend_to_date: moneyFromCents(475_000),
    projected_other_spending: moneyFromCents(475_000),
    projected_left: moneyFromCents(-110_000),
    month_result: moneyFromCents(-45_000),
    month_result_per_day: moneyFromCents(-22_500),
    days_elapsed: 30,
    projected_month_result: moneyFromCents(-45_000),
    ...overrides,
  }
}
