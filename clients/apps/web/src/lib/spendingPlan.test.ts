import { afterEach, describe, expect, it, vi } from 'vitest'

import { absMoney, addMoney, divideMoney, moneyFromCents, subMoney, sumMoney } from './money'
import {
  STACKED_BUCKETS,
  capChildren,
  completedMonthsBefore,
  convertibleSlice,
  effectiveAmount,
  envelopeAvailable,
  envelopeBarPct,
  envelopeBarSegments,
  envelopeBudget,
  envelopePctUsed,
  envelopeSeedCategories,
  envelopeState,
  foldPadding,
  groupEntries,
  groupTinySlices,
  monthPhase,
  otherSpendExcludedRows,
  otherSpendRows,
  projectionMethod,
  releaseAllRollover,
  releaseRollover,
  savingsFigures,
  sliceKey,
  spendingPlanApi,
  trailingSavingsRate,
  type BillSubtotal,
  type BucketKey,
  type Envelope,
  type OtherSpendSlice,
  type PlanBucket,
  type PlanEntry,
  type SpendingPlanMonth,
  uncategorizedShare,
  withDirectChildren,
} from './spendingPlan'
import { planMonth } from '@/components/plan/__fixtures__/plan'

function envelope(overrides: Partial<Envelope> = {}): Envelope {
  return {
    id: 'e1',
    name: 'Food & Dining',
    filter_id: 'f1',
    categories: [{ id: 'c1', name: 'Food & Dining' }],
    target_amount: moneyFromCents(80_000),
    overwritten_target_amount: null,
    target: moneyFromCents(80_000),
    rollover_amount: moneyFromCents(0),
    spent: moneyFromCents(0),
    budget: moneyFromCents(80_000),
    available: moneyFromCents(80_000),
    pct_used: '0',
    bar_pct: '0',
    state: 'normal',
    auto_release_rollover: false,
    recurring: true,
    txn_ids: [],
    entries: [],
    ...overrides,
  }
}

function bucket(key: BucketKey, cents: number, overrideCents?: number): PlanBucket {
  return {
    key,
    calculated_amount: moneyFromCents(cents),
    effective_amount: moneyFromCents(overrideCents ?? cents),
    overwritten_amount: overrideCents === undefined ? null : moneyFromCents(overrideCents),
    contributing: [],
    excluded: [],
    contributing_txn_ids: [],
    excluded_entry_ids: [],
  }
}

/**
 * A month with the engine's headline fields filled in from its buckets, the
 * way the server would send them; an override of any of them wins.
 */
function month(overrides: Partial<SpendingPlanMonth> = {}): SpendingPlanMonth {
  const given: SpendingPlanMonth = {
    month: '2026-08',
    as_of: '2026-08-21',
    is_closed_out: false,
    closed_out_at: null,
    buckets: {
      income: bucket('income', 900_000),
      bills: bucket('bills', -400_000),
      planned_spend: bucket('planned_spend', -100_000),
      other_spend: bucket('other_spend', -620_000),
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
    left_this_month: moneyFromCents(0),
    per_day: null,
    days_remaining: 0,
    other_spend_to_date: moneyFromCents(0),
    projected_other_spending: moneyFromCents(0),
    projected_left: moneyFromCents(0),
    month_result: moneyFromCents(0),
    month_result_per_day: null,
    days_elapsed: 0,
    projected_month_result: moneyFromCents(0),
    ...overrides,
  }
  const left = sumMoney(Object.values(given.buckets).map(effectiveAmount))
  const own = subMoney(left, effectiveAmount(given.buckets.rollover))
  const toDate = absMoney(effectiveAmount(given.buckets.other_spend))
  const projectedOther = overrides.projected_other_spending ?? toDate
  const daysLeft = engineDaysRemaining(given.month, given.as_of)
  return {
    ...given,
    left_this_month: left,
    other_spend_to_date: toDate,
    projected_other_spending: projectedOther,
    projected_left: subMoney(left, subMoney(projectedOther, toDate)),
    days_remaining: daysLeft,
    per_day: daysLeft > 0 ? divideMoney(left, daysLeft) : null,
    month_result: own,
    month_result_per_day: daysLeft > 0 ? divideMoney(own, daysLeft) : null,
    projected_month_result: subMoney(own, subMoney(projectedOther, toDate)),
    ...overrides,
  }
}

/** `domain.DaysRemainingInMonth`, with a month not yet started counted whole. */
function engineDaysRemaining(key: string, asOf: string): number {
  const [year, index] = key.split('-').map(Number)
  const first = Date.UTC(year, index - 1, 1)
  const last = Date.UTC(year, index, 0)
  const today = Date.parse(`${asOf}T00:00:00Z`)
  const day = 86_400_000
  if (today > last) return 0
  if (today < first) return (last - first) / day + 1
  return (last - today) / day + 1
}

describe('envelope percentages', () => {
  it('reads a zero target with spend against it as fully used, not an error', () => {
    // No budget to divide into reads as 100% used.
    const zeroTarget = envelope({
      target_amount: moneyFromCents(0),
      spent: moneyFromCents(4_200),
    })

    expect(envelopeBudget(zeroTarget)).toBe(0)
    expect(envelopePctUsed(zeroTarget)).toBe(100)
    expect(envelopeBarPct(zeroTarget)).toBe(100)
    expect(envelopeState(zeroTarget)).toBe('overspent')
  })

  it('reads a zero target with no spend as untouched rather than fully used', () => {
    const untouched = envelope({ target_amount: moneyFromCents(0) })

    expect(envelopePctUsed(untouched)).toBe(0)
    expect(envelopeState(untouched)).toBe('normal')
  })

  it('clamps the bar at 100% while the label keeps overflowing', () => {
    // $2,320.00 spent against an $800 target reads 290%, and the bar is full.
    const overspent = envelope({
      target_amount: moneyFromCents(80_000),
      spent: moneyFromCents(232_000),
    })

    expect(Math.round(envelopePctUsed(overspent))).toBe(290)
    expect(envelopeBarPct(overspent)).toBe(100)
    expect(envelopeBarSegments(overspent).spentPct).toBe(100)
    expect(envelopeAvailable(overspent)).toBe(moneyFromCents(-152_000))
    expect(envelopeState(overspent)).toBe('overspent')
  })

  it('divides by target plus rollover, so the bar and the label share a budget', () => {
    // Gifts: $160.00 spent, $200 target, $600.00 carried in — 20% of $800.00.
    const gifts = envelope({
      target_amount: moneyFromCents(20_000),
      rollover_amount: moneyFromCents(60_000),
      spent: moneyFromCents(16_000),
    })

    expect(envelopeBudget(gifts)).toBe(moneyFromCents(80_000))
    expect(Math.round(envelopePctUsed(gifts))).toBe(20)
    expect(envelopeAvailable(gifts)).toBe(moneyFromCents(64_000))
    expect(envelopeState(gifts)).toBe('with_rollover')

    const segments = envelopeBarSegments(gifts)
    expect(Math.round(segments.spentPct)).toBe(20)
    // The rollover slice starts where the target ends: $200 of $800.00.
    expect(Math.round(segments.targetPct)).toBe(25)
  })

  it('prefers a target override over the stored target', () => {
    const bumped = envelope({
      target_amount: moneyFromCents(20_000),
      overwritten_target_amount: moneyFromCents(40_000),
      spent: moneyFromCents(20_000),
    })

    expect(envelopePctUsed(bumped)).toBe(50)
  })
})

describe('rollover release', () => {
  it('hands back exactly what was carried and leaves the target alone', () => {
    const gaming = envelope({
      target_amount: moneyFromCents(7_500),
      rollover_amount: moneyFromCents(16_000),
      spent: moneyFromCents(0),
    })

    const { envelope: after, released } = releaseRollover(gaming)

    expect(released).toBe(moneyFromCents(16_000))
    expect(after.rollover_amount).toBe(0)
    expect(after.target_amount).toBe(gaming.target_amount)
    // The released money left the envelope; the caller owes it to the plan.
    expect(envelopeAvailable(gaming)).toBe(moneyFromCents(23_500))
    expect(envelopeAvailable(after)).toBe(moneyFromCents(7_500))
    expect(envelopeAvailable(gaming) - envelopeAvailable(after)).toBe(released)
  })

  it('releases an overspent envelope back to its target, debt included', () => {
    const overspent = envelope({
      target_amount: moneyFromCents(10_000),
      rollover_amount: moneyFromCents(5_000),
      spent: moneyFromCents(18_000),
    })

    const { envelope: after, released } = releaseRollover(overspent)

    expect(released).toBe(moneyFromCents(5_000))
    expect(envelopeAvailable(after)).toBe(moneyFromCents(-8_000))
  })

  it('sums what release-all hands back across every envelope', () => {
    const { envelopes, released } = releaseAllRollover([
      envelope({ id: 'a', rollover_amount: moneyFromCents(16_000) }),
      envelope({ id: 'b', rollover_amount: moneyFromCents(60_000) }),
      envelope({ id: 'c', rollover_amount: moneyFromCents(0) }),
    ])

    expect(released).toBe(moneyFromCents(76_000))
    expect(envelopes.every((item) => item.rollover_amount === 0)).toBe(true)
  })
})

describe('bucket overrides', () => {
  it('lets an override win over the calculated figure', () => {
    const overridden = month({
      buckets: {
        ...month().buckets,
        other_spend: bucket('other_spend', -620_000, -600_000),
      },
    })

    expect(effectiveAmount(overridden.buckets.other_spend)).toBe(moneyFromCents(-600_000))
  })

  it('treats an override of zero as a decision, not as absent', () => {
    const zeroed = month({
      buckets: { ...month().buckets, bills: bucket('bills', -400_000, 0) },
    })

    expect(effectiveAmount(zeroed.buckets.bills)).toBe(0)
  })
})

/**
 * One month exactly as `GET /spending-plan/2026-08` sends it, from the seeded
 * August in `backend/internal/api/spendingplan_test.go`, uuids shortened.
 */
const WIRE_MONTH = {
  month: '2026-08',
  as_of: '2026-08-20',
  is_closed_out: false,
  closed_out_at: null,
  buckets: [
    {
      key: 'income',
      calculated_amount: '2000.00',
      effective_amount: '2000.00',
      overwritten_amount: null,
      contributing_txn_ids: [],
      excluded_entry_ids: [],
      contributing: [
        {
          id: 'series-salary:2026-08-25',
          txn_id: null,
          series_id: 'series-salary',
          name: 'Salary',
          due_on: '2026-08-25',
          status: 'upcoming',
          category_name: null,
          amount: '2000.00',
          account_id: null,
          is_split: false,
          group: null,
          is_transfer: false,
          is_padding: false,
        },
      ],
      excluded: [],
    },
    {
      key: 'bills',
      calculated_amount: '-60.00',
      effective_amount: '-60.00',
      overwritten_amount: null,
      contributing_txn_ids: [],
      excluded_entry_ids: [],
      contributing: [
        {
          id: 'series-power:2026-08-12',
          txn_id: null,
          series_id: 'series-power',
          name: 'Power',
          due_on: '2026-08-12',
          status: 'past_due',
          category_name: null,
          amount: '-60.00',
          account_id: null,
          is_split: false,
          group: 'bill',
          is_transfer: false,
          is_padding: false,
        },
      ],
      excluded: [],
    },
    {
      key: 'planned_spend',
      calculated_amount: '-100.00',
      effective_amount: '-100.00',
      overwritten_amount: null,
      contributing_txn_ids: ['txn-grocer'],
      excluded_entry_ids: [],
      contributing: [
        {
          id: 'txn-grocer',
          txn_id: 'txn-grocer',
          series_id: null,
          name: 'Corner Grocer',
          due_on: '2026-08-05',
          status: 'paid',
          category_name: 'Groceries',
          amount: '-50.00',
          account_id: null,
          is_split: false,
          group: null,
          is_transfer: false,
          is_padding: false,
        },
      ],
      excluded: [],
    },
    {
      key: 'other_spend',
      calculated_amount: '-25.00',
      effective_amount: '-25.00',
      overwritten_amount: null,
      contributing_txn_ids: ['txn-corner'],
      excluded_entry_ids: [],
      contributing: [
        {
          id: 'txn-corner',
          txn_id: 'txn-corner',
          series_id: null,
          name: 'Corner Store',
          due_on: '2026-08-20',
          status: 'paid',
          category_name: null,
          amount: '-25.00',
          account_id: null,
          is_split: false,
          group: null,
          is_transfer: false,
          is_padding: false,
        },
      ],
      excluded: [],
    },
    {
      key: 'goals',
      calculated_amount: '0.00',
      effective_amount: '0.00',
      overwritten_amount: null,
      contributing_txn_ids: [],
      excluded_entry_ids: [],
      contributing: [],
      excluded: [],
    },
    {
      key: 'rollover',
      calculated_amount: '1840.00',
      effective_amount: '1840.00',
      overwritten_amount: null,
      contributing_txn_ids: [],
      excluded_entry_ids: [],
      contributing: [],
      excluded: [],
    },
  ],
  bills: [
    {
      id: 'series-power:2026-08-12',
      group: 'bill',
      series_id: 'series-power',
      name: 'Power',
      due_on: '2026-08-12',
      amount: '-60.00',
      is_fulfilled: false,
      txn_ids: [],
      is_excluded: false,
    },
  ],
  bill_subtotals: [
    { group: 'bill', amount: '-60.00' },
    { group: 'subscription', amount: '0.00' },
    { group: 'transfer', amount: '0.00' },
  ],
  envelopes: [
    {
      id: 'env-groceries',
      name: 'Groceries',
      filter_id: 'filter-groceries',
      categories: [{ id: 'cat-groceries', name: 'Groceries' }],
      target_amount: '100.00',
      overwritten_target_amount: null,
      target: '100.00',
      rollover_amount: '25.00',
      spent: '50.00',
      budget: '125.00',
      available: '75.00',
      pct_used: '40',
      bar_pct: '40',
      state: 'with_rollover',
      auto_release_rollover: false,
      recurring: true,
      txn_ids: ['txn-grocer'],
      entries: [],
    },
  ],
  contested_txn_ids: {},
  other_spend_by_category: [
    {
      category_id: 'cat-food',
      category_name: 'Food & Dining',
      spent: '25.00',
      txn_ids: ['txn-grocer'],
      children: [
        {
          category_id: 'cat-groceries',
          category_name: 'Groceries',
          spent: '25.00',
          txn_ids: ['txn-grocer'],
          children: [],
        },
      ],
    },
  ],
  projection: {
    type: 'run_rate',
    buffer: '0.00',
    window_months: 3,
    start_on: null,
    end_on: null,
  },
  left_this_month: '3655.00',
  per_day: '304.58',
  days_remaining: 12,
  other_spend_to_date: '25.00',
  projected_other_spending: '38.75',
  projected_left: '3641.25',
  month_result: '1815.00',
  month_result_per_day: '151.25',
  days_elapsed: 20,
  projected_month_result: '1801.25',
}

function respondWith(body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    text: () => Promise.resolve(JSON.stringify(body)),
  })
}

describe('a month off the wire', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  async function fetched(body: unknown = WIRE_MONTH): Promise<SpendingPlanMonth> {
    vi.stubGlobal('fetch', respondWith(body))
    return spendingPlanApi.month('2026-08')
  }

  it('keys the buckets the server sent as an array', async () => {
    const parsed = await fetched()

    expect(Object.keys(parsed.buckets)).toEqual([
      'income',
      'bills',
      'planned_spend',
      'other_spend',
      'goals',
      'rollover',
    ])
    expect(parsed.buckets.rollover.key).toBe('rollover')
  })

  it('coerces every bucket amount rather than leaving it a string', async () => {
    // `"2000.00" + "-60.00"` concatenates.
    const parsed = await fetched()

    for (const bucket of Object.values(parsed.buckets)) {
      expect(typeof bucket.calculated_amount).toBe('number')
      expect(typeof bucket.effective_amount).toBe('number')
    }
    expect(parsed.buckets.income.calculated_amount).toBe(moneyFromCents(200_000))
    expect(parsed.buckets.rollover.calculated_amount).toBe(moneyFromCents(184_000))
  })

  it("coerces the month's own result, and it is the five stacked buckets", async () => {
    // The fixture has to agree with its own buckets.
    const parsed = await fetched()

    expect(parsed.month_result).toBe(
      sumMoney(STACKED_BUCKETS.map((key) => effectiveAmount(parsed.buckets[key]))),
    )
    expect(parsed.month_result_per_day).toBe(moneyFromCents(15_125))
    expect(parsed.projected_month_result).toBe(moneyFromCents(180_125))
  })

  it('names the rows behind a bucket, amounts included', async () => {
    const parsed = await fetched()

    const [salary] = parsed.buckets.income.contributing
    expect(salary.name).toBe('Salary')
    expect(salary.status).toBe('upcoming')
    expect(salary.txn_id).toBeNull()
    expect(salary.amount).toBe(moneyFromCents(200_000))

    const [power] = parsed.buckets.bills.contributing
    expect(power.group).toBe('bill')
    expect(power.amount).toBe(moneyFromCents(-6_000))

    const [grocer] = parsed.buckets.planned_spend.contributing
    expect(grocer.category_name).toBe('Groceries')
    expect(grocer.amount).toBe(moneyFromCents(-5_000))
  })

  it('leaves no envelope figure NaN', async () => {
    const [groceries] = (await fetched()).envelopes

    expect(groceries.rollover_amount).toBe(moneyFromCents(2_500))
    expect(envelopeBudget(groceries)).toBe(groceries.budget)
    expect(envelopeAvailable(groceries)).toBe(groceries.available)
    expect(envelopePctUsed(groceries)).toBe(Number(groceries.pct_used))
    expect(envelopeState(groceries)).toBe(groceries.state)
    expect(groceries.categories).toEqual([{ id: 'cat-groceries', name: 'Groceries' }])
  })

  it('carries the other-spend slices the chart draws, and their children', async () => {
    const [slice] = (await fetched()).other_spend_by_category

    expect(slice.category_name).toBe('Food & Dining')
    expect(slice.spent).toBe(moneyFromCents(2_500))
    expect(slice.txn_ids).toEqual(['txn-grocer'])

    const [child] = slice.children
    expect(child.category_name).toBe('Groceries')
    expect(child.spent).toBe(moneyFromCents(2_500))
  })

  it('fails the query rather than the page when a bucket is missing', async () => {
    // Thrown here, the page shows its own error state instead of a blank app.
    const short = {
      ...WIRE_MONTH,
      buckets: WIRE_MONTH.buckets.filter((bucket) => bucket.key !== 'goals'),
    }

    await expect(fetched(short)).rejects.toThrow('goals')
  })
})

describe('the rows behind an Other Spend bubble', () => {
  function row(id: string, name: string): PlanEntry {
    return {
      id,
      txn_id: id,
      series_id: null,
      name,
      due_on: '2026-08-04',
      status: 'paid',
      category_name: name,
      amount: moneyFromCents(-2_500),
      account_id: null,
      is_split: false,
      group: null,
      is_transfer: false,
      is_padding: false,
    }
  }

  function slice(
    name: string,
    txnIds: string[],
    children: OtherSpendSlice[] = [],
  ): OtherSpendSlice {
    return {
      category_id: `cat-${name}`,
      category_name: name,
      spent: moneyFromCents(2_500 * txnIds.length),
      txn_ids: txnIds,
      children,
    }
  }

  const groceries = slice('Groceries', ['t1', 't2'])
  const restaurants = slice('Restaurants', ['t3'])
  const food = slice('Food', ['t1', 't2', 't3'], [groceries, restaurants])
  const spend = bucket('other_spend', -10_000)
  spend.contributing = [
    row('t1', 'Corner Grocer'),
    row('t2', 'Costco'),
    row('t3', 'Thai'),
    row('t4', 'Gas'),
  ]

  it('lists everything the bucket counted when nothing is open', () => {
    expect(otherSpendRows(spend, null, null).map((entry) => entry.id)).toEqual([
      't1',
      't2',
      't3',
      't4',
    ])
  })

  it('narrows to the open group', () => {
    expect(otherSpendRows(spend, food, null).map((entry) => entry.id)).toEqual(['t1', 't2', 't3'])
  })

  it('narrows again to the selected bubble inside it', () => {
    expect(otherSpendRows(spend, food, groceries).map((entry) => entry.id)).toEqual(['t1', 't2'])
  })

  it('drops an id the bucket counted but did not list', () => {
    const ghost = slice('Ghost', ['t1', 'nope'])
    expect(otherSpendRows(spend, null, ghost).map((entry) => entry.id)).toEqual(['t1'])
  })

  it('keys an uncategorized slice by its name, since it has no id', () => {
    expect(sliceKey(groceries)).toBe('cat-Groceries')
    expect(
      sliceKey({
        category_id: null,
        category_name: 'Uncategorized',
        spent: moneyFromCents(0),
        txn_ids: [],
        children: [],
      }),
    ).toBe('name:Uncategorized')
  })
})

describe('the savings rate', () => {
  it('reports both the rate to date and the projection', () => {
    const subject = month({
      buckets: {
        income: bucket('income', 500_000),
        bills: bucket('bills', -100_000),
        planned_spend: bucket('planned_spend', 0),
        other_spend: bucket('other_spend', -100_000),
        goals: bucket('goals', 0),
        rollover: bucket('rollover', 0),
      },
      projected_other_spending: moneyFromCents(200_000),
    })
    const { rate: toDate, projectedRate: projected } = savingsFigures(subject)
    // $3,000 of $5,000 is unspent today. Another $1,000 of other spending is
    // still to come, so the month is on course to end at $2,000.
    expect(toDate).toBeCloseTo(0.6)
    expect(projected).toBeCloseTo(0.4)
  })

  it('goes negative on an overspent month without counting last month twice', () => {
    const subject = month({
      buckets: {
        income: bucket('income', 500_000),
        bills: bucket('bills', -200_000),
        planned_spend: bucket('planned_spend', 0),
        other_spend: bucket('other_spend', -400_000),
        goals: bucket('goals', 0),
        rollover: bucket('rollover', -100_000),
      },
      projected_other_spending: moneyFromCents(400_000),
    })
    const { rate: toDate, projectedRate: projected } = savingsFigures(subject)
    // $6,000 went out against $5,000 in: −20%. The $1,000 rolled over from
    // last month sits in the headline but not the rate — it already lowered
    // last month's rate once.
    expect(toDate).toBeCloseTo(-0.2)
    expect(projected).toBeCloseTo(-0.2)
  })

  it('has no rate at all when nothing came in', () => {
    const subject = month({
      buckets: {
        income: bucket('income', 0),
        bills: bucket('bills', 0),
        planned_spend: bucket('planned_spend', 0),
        other_spend: bucket('other_spend', -5_000),
        goals: bucket('goals', 0),
        rollover: bucket('rollover', 0),
      },
    })
    // Not zero: a month with no income did not fail to save any of it.
    const figures = savingsFigures(subject)
    expect(figures.rate).toBeNull()
    expect(figures.projectedRate).toBeNull()
  })
})

/** A month with round figures, so every expectation below is arithmetic. */
function savingsMonth(
  key: string,
  cents: {
    income: number
    bills: number
    planned: number
    other: number
    goals: number
    rollover?: number
    projectedOther?: number
  },
): SpendingPlanMonth {
  return month({
    month: key,
    buckets: {
      income: bucket('income', cents.income),
      bills: bucket('bills', cents.bills),
      planned_spend: bucket('planned_spend', cents.planned),
      other_spend: bucket('other_spend', cents.other),
      goals: bucket('goals', cents.goals),
      rollover: bucket('rollover', cents.rollover ?? 0),
    },
    projected_other_spending: moneyFromCents(cents.projectedOther ?? -cents.other),
  })
}

describe('savingsFigures', () => {
  // $5,000 in. $1,000 of bills, $400 of envelope targets, $600 spent loose and
  // $200 into a goal, which is $2,200 claimed and $2,800 left — 56%. Another
  // $400 of loose spending is still expected, so the month is on course to end
  // at $2,400, which is 48%.
  const subject = savingsMonth('2026-08', {
    income: 500_000,
    bills: -100_000,
    planned: -40_000,
    other: -60_000,
    goals: -20_000,
    projectedOther: 100_000,
  })

  it('shows the amounts the rate is made of', () => {
    const figures = savingsFigures(subject)
    expect(figures.income).toBe(moneyFromCents(500_000))
    expect(figures.committed).toBe(moneyFromCents(220_000))
    expect(figures.saved).toBe(moneyFromCents(280_000))
    expect(figures.rate).toBeCloseTo(0.56)
  })

  it('shows the same three once the spending still expected has landed', () => {
    const figures = savingsFigures(subject)
    expect(figures.projectedCommitted).toBe(moneyFromCents(260_000))
    expect(figures.projectedSaved).toBe(moneyFromCents(240_000))
    expect(figures.projectedRate).toBeCloseTo(0.48)
  })

  it('leaves the rollover out of both the rate and the amounts', () => {
    // A rate pairs this month's income with this month's spending. Last
    // month's overspend already lowered last month's rate once.
    const carried = savingsMonth('2026-08', {
      income: 500_000,
      bills: -100_000,
      planned: -40_000,
      other: -60_000,
      goals: -20_000,
      rollover: -150_000,
      projectedOther: 100_000,
    })
    expect(savingsFigures(carried).saved).toBe(moneyFromCents(280_000))
    expect(savingsFigures(carried).rate).toBeCloseTo(0.56)
  })

  it('has no rate over no income, and still reports the amounts', () => {
    const barren = savingsMonth('2026-08', {
      income: 0,
      bills: 0,
      planned: 0,
      other: -30_000,
      goals: 0,
    })
    expect(savingsFigures(barren).rate).toBeNull()
    expect(savingsFigures(barren).projectedRate).toBeNull()
    // The spending is still real and still worth showing.
    expect(savingsFigures(barren).committed).toBe(moneyFromCents(30_000))
  })
})

describe('trailingSavingsRate', () => {
  it('divides everything saved by everything earned', () => {
    // $2,000 saved of $5,000, then $0 of $5,000, then $1,000 of $10,000:
    // $3,000 of $20,000 is 15%.
    const months = [
      savingsMonth('2026-07', { income: 500_000, bills: -300_000, planned: 0, other: 0, goals: 0 }),
      savingsMonth('2026-06', { income: 500_000, bills: -500_000, planned: 0, other: 0, goals: 0 }),
      savingsMonth('2026-05', {
        income: 1_000_000, bills: -900_000, planned: 0, other: 0, goals: 0,
      }),
    ]
    const trailing = trailingSavingsRate(months)
    expect(trailing.income).toBe(moneyFromCents(2_000_000))
    expect(trailing.saved).toBe(moneyFromCents(300_000))
    expect(trailing.rate).toBeCloseTo(0.15)
    expect(trailing.months).toBe(3)
  })

  it('does not let a tiny month vote as loudly as a large one', () => {
    // The mean of 90% and 0% is 45%; $90 kept of $10,100 earned is 0.89%.
    const months = [
      savingsMonth('2026-07', { income: 10_000, bills: -1_000, planned: 0, other: 0, goals: 0 }),
      savingsMonth('2026-06', {
        income: 1_000_000, bills: -1_000_000, planned: 0, other: 0, goals: 0,
      }),
    ]
    expect(trailingSavingsRate(months).rate).toBeCloseTo(0.0089, 4)
  })

  it('counts a month with no income as spending, not as a rate it cannot have', () => {
    // Averaging rates has no answer for it at all; total over total does.
    const months = [
      savingsMonth('2026-07', { income: 0, bills: -50_000, planned: 0, other: 0, goals: 0 }),
      savingsMonth('2026-06', {
        income: 500_000, bills: -300_000, planned: 0, other: 0, goals: 0,
      }),
    ]
    const trailing = trailingSavingsRate(months)
    expect(trailing.saved).toBe(moneyFromCents(150_000))
    expect(trailing.rate).toBeCloseTo(0.3)
  })

  it('has no rate at all where nothing came in across the window', () => {
    const months = [
      savingsMonth('2026-07', { income: 0, bills: -50_000, planned: 0, other: 0, goals: 0 }),
    ]
    expect(trailingSavingsRate(months).rate).toBeNull()
    expect(trailingSavingsRate([]).rate).toBeNull()
  })
})

describe('completedMonthsBefore', () => {
  it('names the finished months, newest first, and never the current one', () => {
    // A month under way is a partial numerator over a partial denominator.
    expect(completedMonthsBefore('2026-08', 3)).toEqual(['2026-07', '2026-06', '2026-05'])
  })

  it('walks back over a year boundary', () => {
    expect(completedMonthsBefore('2026-02', 3)).toEqual(['2026-01', '2025-12', '2025-11'])
  })
})

describe('converting an Other Spend bubble to Planned Spend', () => {
  function slice(name: string, categoryId: string | null): OtherSpendSlice {
    return {
      category_id: categoryId,
      category_name: name,
      spent: moneyFromCents(10_000),
      txn_ids: [],
      children: [],
    }
  }

  const shopping = slice('Shopping', 'cat-shopping')
  const electronics = slice('Electronics', 'cat-electronics')
  const uncategorized = slice('Uncategorized', null)

  it('converts the most specific thing on screen: the selection over the open group', () => {
    expect(convertibleSlice(shopping, null)).toBe(shopping)
    expect(convertibleSlice(shopping, electronics)).toBe(electronics)
    expect(convertibleSlice(null, null)).toBeNull()
  })

  it('never offers to convert Uncategorized — an envelope needs a category to point at', () => {
    expect(convertibleSlice(uncategorized, null)).toBeNull()
    expect(convertibleSlice(shopping, uncategorized)).toBeNull()
  })

  it('claims the group and every subcategory, spent this month or not', () => {
    // Envelope filters are a strict membership test, so unspent subcategories must be included.
    const categories = [
      { id: 'cat-electronics', name: 'Electronics', parent_id: 'cat-shopping' },
      { id: 'cat-clothing', name: 'Clothing', parent_id: 'cat-shopping' },
      { id: 'cat-travel', name: 'Travel', parent_id: null },
    ]
    expect(envelopeSeedCategories(shopping, categories)).toEqual([
      'cat-shopping',
      'cat-electronics',
      'cat-clothing',
    ])
  })

  it('claims exactly the category itself for a leaf', () => {
    const categories = [
      { id: 'cat-electronics', name: 'Electronics', parent_id: 'cat-shopping' },
      { id: 'cat-clothing', name: 'Clothing', parent_id: 'cat-shopping' },
    ]
    expect(envelopeSeedCategories(electronics, categories)).toEqual(['cat-electronics'])
    expect(envelopeSeedCategories(uncategorized, categories)).toEqual([])
  })
})

describe('the excluded rows behind an Other Spend view', () => {
  function excludedRow(id: string, categoryName: string | null): PlanEntry {
    return {
      id,
      txn_id: id,
      series_id: null,
      name: `payee-${id}`,
      due_on: '2026-08-10',
      status: 'paid',
      category_name: categoryName,
      amount: moneyFromCents(-1_000),
      account_id: null,
      is_split: false,
      group: null,
      is_transfer: false,
      is_padding: false,
    }
  }

  function slice(name: string, children: OtherSpendSlice[] = []): OtherSpendSlice {
    return {
      category_id: `cat-${name}`,
      category_name: name,
      spent: moneyFromCents(1_000),
      txn_ids: [],
      children,
    }
  }

  const electronics = slice('Electronics')
  const shopping = slice('Shopping', [electronics])
  const spend: PlanBucket = {
    key: 'other_spend',
    calculated_amount: moneyFromCents(-1_000),
    effective_amount: moneyFromCents(-1_000),
    overwritten_amount: null,
    contributing: [],
    excluded: [
      excludedRow('x1', 'Shopping'),
      excludedRow('x2', 'Electronics'),
      excludedRow('x3', 'Travel'),
      excludedRow('x4', null),
    ],
    contributing_txn_ids: [],
    excluded_entry_ids: ['x1', 'x2', 'x3', 'x4'],
  }

  it('lists everything the bucket dropped when nothing is open', () => {
    expect(otherSpendExcludedRows(spend, null, null).map((entry) => entry.id)).toEqual([
      'x1',
      'x2',
      'x3',
      'x4',
    ])
  })

  it('narrows to the open group and its children together', () => {
    expect(otherSpendExcludedRows(spend, shopping, null).map((entry) => entry.id)).toEqual([
      'x1',
      'x2',
    ])
  })

  it('narrows to the selection alone once one is made', () => {
    expect(otherSpendExcludedRows(spend, shopping, electronics).map((entry) => entry.id)).toEqual([
      'x2',
    ])
  })

  it('files an uncategorized excluded row under the Uncategorized slice', () => {
    const uncategorized: OtherSpendSlice = {
      category_id: null,
      category_name: 'Uncategorized',
      spent: moneyFromCents(1_000),
      txn_ids: [],
      children: [],
    }
    expect(otherSpendExcludedRows(spend, uncategorized, null).map((entry) => entry.id)).toEqual([
      'x4',
    ])
  })
})

describe('the bill family subtotals', () => {
  function entry(group: 'bill' | 'subscription' | 'transfer', cents: number): PlanEntry {
    return {
      id: `e-${group}-${cents}`,
      txn_id: null,
      series_id: null,
      name: group,
      due_on: '2026-08-10',
      status: 'paid',
      category_name: null,
      amount: moneyFromCents(cents),
      account_id: null,
      is_split: false,
      group,
      is_transfer: group === 'transfer',
      is_padding: false,
    }
  }

  it('derives the same figures the server materializes as bill_subtotals', () => {
    // Holds the panel's derivation to the server's `bill_subtotals`.
    const entries = [
      entry('bill', -10_000),
      entry('subscription', -2_000),
      entry('bill', -5_000),
      entry('transfer', -7_500),
    ]
    const materialized: BillSubtotal[] = [
      { group: 'bill', amount: moneyFromCents(-15_000) },
      { group: 'subscription', amount: moneyFromCents(-2_000) },
      { group: 'transfer', amount: moneyFromCents(-7_500) },
    ]

    const families = groupEntries(entries)
    for (const subtotal of materialized) {
      const family = families.find(([group]) => group === subtotal.group)
      expect(sumMoney((family?.[1] ?? []).map((one) => one.amount))).toBe(subtotal.amount)
    }
  })

  it('keeps the families in the fixed order whatever the month holds', () => {
    const families = groupEntries([entry('transfer', -1), entry('bill', -2)])
    expect(families.map(([group]) => group)).toEqual(['bill', 'transfer'])
  })
})

describe('capping an open group at five bubbles', () => {
  function child(name: string, cents: number): OtherSpendSlice {
    return {
      category_id: `cat-${name}`,
      category_name: name,
      spent: moneyFromCents(cents),
      txn_ids: [`t-${name}`],
      children: [],
    }
  }
  function group(children: OtherSpendSlice[]): OtherSpendSlice {
    return {
      category_id: 'cat-home',
      category_name: 'Home',
      spent: children.reduce((sum, one) => addMoney(sum, one.spent), moneyFromCents(0)),
      txn_ids: children.flatMap((one) => one.txn_ids),
      children,
    }
  }

  it('folds everything past the four largest into a fifth Other bubble', () => {
    const kids = [
      child('Furniture', 50_000),
      child('Garden', 900),
      child('Repairs', 30_000),
      child('Decor', 700),
      child('Appliances', 20_000),
      child('Cleaning', 500),
      child('Tools', 10_000),
    ]
    const [capped] = capChildren([group(kids)])
    expect(capped.children.map((one) => one.category_name)).toEqual([
      'Furniture',
      'Repairs',
      'Appliances',
      'Tools',
      'Other',
    ])
    const rest = capped.children[4]
    expect(rest.rest).toBe(true)
    expect(rest.spent).toBe(moneyFromCents(900 + 700 + 500))
    expect(rest.txn_ids).toEqual(['t-Garden', 't-Decor', 't-Cleaning'])
    // The folded slices ride along, so the fold still reaches each one.
    expect(rest.children.map((one) => one.category_name)).toEqual(['Garden', 'Decor', 'Cleaning'])
  })

  it('leaves a group of five or fewer exactly as it was', () => {
    const five = group([child('A', 5), child('B', 4), child('C', 3), child('D', 2), child('E', 1)])
    expect(capChildren([five])[0]).toBe(five)
  })

  it('keys the fold apart from a top-level Other bubble', () => {
    const other: OtherSpendSlice = {
      category_id: null,
      category_name: 'Other',
      spent: moneyFromCents(1_000),
      txn_ids: [],
      children: [],
    }
    const [capped] = capChildren([
      group([
        child('A', 6),
        child('B', 5),
        child('C', 4),
        child('D', 3),
        child('E', 2),
        child('F', 1),
      ]),
    ])
    expect(sliceKey(capped.children[4])).not.toBe(sliceKey(other))
  })
})

describe('the direct-spend child of a group', () => {
  const electronics: OtherSpendSlice = {
    category_id: 'cat-electronics',
    category_name: 'Electronics',
    spent: moneyFromCents(41_400),
    txn_ids: ['t2'],
    children: [],
  }
  const shopping: OtherSpendSlice = {
    category_id: 'cat-shopping',
    category_name: 'Shopping',
    spent: moneyFromCents(370_000),
    txn_ids: ['t1', 't2'],
    children: [electronics],
  }

  it('surfaces what was filed on the group itself as a child named after it', () => {
    const [group] = withDirectChildren([shopping])
    expect(group.children.map((child) => child.category_name)).toEqual(['Shopping', 'Electronics'])
    const direct = group.children[0]
    expect(direct.direct).toBe(true)
    expect(direct.spent).toBe(moneyFromCents(370_000 - 41_400))
    // Its rows are the group's minus the real children's — never a re-query.
    expect(direct.txn_ids).toEqual(['t1'])
  })

  it('keys the direct child apart from its group, or selecting one selects both', () => {
    const [group] = withDirectChildren([shopping])
    expect(sliceKey(group.children[0])).not.toBe(sliceKey(group))
  })

  it('leaves a leaf, and a group its children fully account for, alone', () => {
    const leaf: OtherSpendSlice = { ...electronics, txn_ids: [] }
    const covered: OtherSpendSlice = {
      ...shopping,
      spent: moneyFromCents(41_400),
    }
    expect(withDirectChildren([leaf])[0]).toBe(leaf)
    expect(withDirectChildren([covered])[0].children).toHaveLength(1)
  })

  it('converts to an envelope claiming only the group category itself', () => {
    const [group] = withDirectChildren([shopping])
    const categories = [{ id: 'cat-electronics', name: 'Electronics', parent_id: 'cat-shopping' }]
    expect(envelopeSeedCategories(group.children[0], categories)).toEqual(['cat-shopping'])
    expect(envelopeSeedCategories(group, categories)).toEqual(['cat-shopping', 'cat-electronics'])
  })
})

describe('folding the unlabellable bubbles into Other', () => {
  function tiny(name: string, cents: number, ids: string[]): OtherSpendSlice {
    return {
      category_id: `cat-${name}`,
      category_name: name,
      spent: moneyFromCents(cents),
      txn_ids: ids,
      children: [],
    }
  }
  const shopping = tiny('Shopping', 600_000, ['t1'])
  const travel = tiny('Travel', 350_000, ['t2'])

  it('merges every too-small slice into one Other bubble that still opens', () => {
    const grouped = groupTinySlices([
      shopping,
      travel,
      tiny('Health', 6_000, ['t3']),
      tiny('Personal Care', 8_100, ['t4']),
      tiny('Fees', 5_000, ['t5']),
    ])

    expect(grouped.map((one) => one.category_name)).toEqual(['Shopping', 'Travel', 'Other'])
    const other = grouped[2]
    expect(other.category_id).toBeNull()
    expect(other.spent).toBe(moneyFromCents(19_100))
    expect(other.txn_ids).toEqual(['t3', 't4', 't5'])
    // The tiny slices become its children, so the drill-down still reaches them.
    expect(other.children.map((one) => one.category_name)).toEqual([
      'Health',
      'Personal Care',
      'Fees',
    ])
  })

  it('folds them into a real Other group when the month already has one', () => {
    const realOther = {
      ...tiny('Other', 40_000, ['t6']),
    }
    const grouped = groupTinySlices([shopping, travel, realOther, tiny('Health', 6_000, ['t3'])])

    expect(grouped.map((one) => one.category_name)).toEqual(['Shopping', 'Travel', 'Other'])
    const other = grouped[2]
    expect(other.category_id).toBe('cat-Other')
    expect(other.spent).toBe(moneyFromCents(46_000))
    expect(other.txn_ids).toEqual(['t6', 't3'])
    expect(other.children.map((one) => one.category_name)).toEqual(['Health'])
  })

  it('leaves a lone tiny slice alone rather than renaming it', () => {
    const slices = [shopping, travel, tiny('Health', 6_000, ['t3'])]
    expect(groupTinySlices(slices).map((one) => one.category_name)).toEqual([
      'Shopping',
      'Travel',
      'Health',
    ])
  })

  it('touches nothing when every bubble can carry its label', () => {
    expect(groupTinySlices([shopping, travel])).toEqual([shopping, travel])
  })
})

describe('the month on its own', () => {
  it('knows whether the month is over, under way or still to come', () => {
    expect(monthPhase(planMonth({ as_of: '2026-08-30' }))).toBe('current')
    expect(monthPhase(planMonth({ as_of: '2026-08-01' }))).toBe('current')
    expect(monthPhase(planMonth({ as_of: '2026-08-31' }))).toBe('current')
    expect(monthPhase(planMonth({ as_of: '2026-09-01' }))).toBe('past')
    expect(monthPhase(planMonth({ as_of: '2026-07-31' }))).toBe('future')
  })

  it("explains a future month's run rate as the prior months' average", () => {
    const future = planMonth({ as_of: '2026-07-20' })
    expect(projectionMethod(future.projection, future)).toContain('month not started')
    expect(projectionMethod(planMonth().projection, planMonth())).toContain('days so far')
  })
})

describe('uncategorizedShare', () => {
  const slice = (id: string | null, spent: number, ids: string[] = []) => ({
    category_id: id,
    category_name: id === null ? 'Uncategorized' : id,
    spent: moneyFromCents(spent),
    txn_ids: ids,
    children: [],
  })

  it('names the share when uncategorized dominates, with the count behind it', () => {
    const found = uncategorizedShare([
      slice(null, 440_000, ['a', 'b', 'c']),
      slice('home', 360_000),
    ])
    expect(found).not.toBeNull()
    expect(found?.count).toBe(3)
    expect(found?.share).toBeCloseTo(0.55, 2)
  })

  it('says nothing when most of the month is filed', () => {
    expect(uncategorizedShare([slice(null, 100), slice('home', 9_900)])).toBeNull()
  })

  it('says nothing when every row is filed, and nothing about an empty month', () => {
    expect(uncategorizedShare([slice('home', 9_900)])).toBeNull()
    expect(uncategorizedShare([])).toBeNull()
  })

  it('takes the threshold from its caller', () => {
    const slices = [slice(null, 3_000), slice('home', 7_000)]
    expect(uncategorizedShare(slices, 0.5)).toBeNull()
    expect(uncategorizedShare(slices, 0.25)).not.toBeNull()
  })
})

describe('folding padding rows', () => {
  // Invented: three $6.00 and $8.50 lunch pads, a paycheck between them,
  // and one pad under another name.
  function income(id: string, name: string, cents: number, isPadding: boolean): PlanEntry {
    return {
      id,
      txn_id: id,
      series_id: null,
      name,
      due_on: '2026-08-12',
      status: 'received',
      category_name: 'Paycheck',
      amount: moneyFromCents(cents),
      account_id: 'acct-lunch',
      is_split: false,
      group: null,
      is_transfer: false,
      is_padding: isPadding,
    }
  }
  const pad = (id: string, cents: number) => income(id, 'Lunch (paycheck deduction)', cents, true)

  it('lumps the pads under one line per name, where the first stood, adding up to them', () => {
    const lines = foldPadding([
      pad('p1', 600),
      income('pay', 'Employer', 180_000, false),
      pad('p2', 850),
      pad('p3', 600),
      income('kiosk', 'Kiosk (paycheck deduction)', 300, true),
    ])
    expect(lines.map((line) => (line.kind === 'entry' ? line.entry.id : line.key))).toEqual([
      'padding:Paycheck\u0000Lunch (paycheck deduction)',
      'pay',
      'kiosk',
    ])
    const lump = lines[0]!
    if (lump.kind !== 'padding') throw new Error('expected the padding line first')
    expect(lump.entries.map((one) => one.id)).toEqual(['p1', 'p2', 'p3'])
    expect(lump.amount).toBe(moneyFromCents(2_050))
    expect(lump.name).toBe('Lunch (paycheck deduction)')
    expect(lump.category_name).toBe('Paycheck')
  })

  it('leaves a list without padding as it was', () => {
    const rows = [income('pay', 'Employer', 180_000, false)]
    expect(foldPadding(rows)).toEqual([{ kind: 'entry', entry: rows[0] }])
  })
})
