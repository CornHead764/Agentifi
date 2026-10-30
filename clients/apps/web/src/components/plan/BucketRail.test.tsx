import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'
import { divideMoney, moneyFromCents, subMoney, sumMoney } from '@/lib/money'
import { effectiveAmount } from '@/lib/spendingPlan'
import { BucketRail } from './BucketRail'
import { planBucket, planMonth } from './__fixtures__/plan'

function render(node: React.ReactNode): string {
  return renderToStaticMarkup(<TooltipProvider>{node}</TooltipProvider>)
}

const noop = () => undefined

describe('the bucket rail', () => {
  it('stacks the five buckets over the month\'s one number', () => {
    const rendered = render(
      <BucketRail month={planMonth()} selected="income" onSelect={noop} />,
    )
    for (const label of ['Income', 'Bills', 'Planned Spend', 'Other Spend', 'Goals']) {
      expect(rendered).toContain(label)
    }
    // The month's own five buckets, without what rolled in; the headline with
    // rollover stands one line down, so the arithmetic still closes.
    expect(rendered).toContain('So far this month')
    expect(rendered).toContain('450.00')
    expect(rendered).toContain('With rollover')
    expect(rendered).toContain('1,100.00')
  })

  it('names what rolled over and the per-day rate while days remain', () => {
    const rendered = render(
      <BucketRail month={planMonth()} selected="income" onSelect={noop} />,
    )
    expect(rendered).toContain('Rolled over')
    expect(rendered).toContain('Per day')
    expect(rendered).toContain('Projected to end the month')
  })

  it('reports how a month that is over ended, with no rate and no projection', () => {
    const rendered = render(
      <BucketRail
        month={planMonth({ as_of: '2026-09-04' })}
        selected="income"
        onSelect={noop}
       
      />,
    )
    expect(rendered).toContain('Ended the month at')
    expect(rendered).toContain('450.00')
    expect(rendered).not.toContain('Per day')
    expect(rendered).not.toContain('Projected to end the month')
  })

  it('expects a month not yet started from its schedule and the prior months', () => {
    const buckets = { ...planMonth().buckets, other_spend: planBucket('other_spend') }
    // The engine's figures for these buckets: nothing spent yet, all 31 days left.
    const left = sumMoney(Object.values(buckets).map(effectiveAmount))
    const own = subMoney(left, effectiveAmount(buckets.rollover))
    const rendered = render(
      <BucketRail
        month={planMonth({
          as_of: '2026-07-20',
          buckets,
          projected_other_spending: moneyFromCents(650_000),
          other_spend_to_date: moneyFromCents(0),
          left_this_month: left,
          projected_left: subMoney(left, moneyFromCents(650_000)),
          days_remaining: 31,
          per_day: divideMoney(left, 31),
          month_result: own,
          projected_month_result: subMoney(own, moneyFromCents(650_000)),
          month_result_per_day: divideMoney(own, 31),
          days_elapsed: 0,
        })}
        selected="income"
        onSelect={noop}
       
      />,
    )
    expect(rendered).toContain('Expected this month')
    // Income less bills, envelopes and the projected 6,500 of other spending.
    expect(rendered).toContain('2,200.00')
    expect(rendered).toContain('month not started')
    expect(rendered).not.toContain('Per day')
  })
})
