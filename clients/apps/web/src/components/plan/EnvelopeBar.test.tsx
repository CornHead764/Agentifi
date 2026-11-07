import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@agentifi/core/money'
import type { Envelope } from '@/lib/spendingPlan'

import { EnvelopeBar } from './EnvelopeBar'

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
    released_amount: moneyFromCents(0),
    spent: moneyFromCents(0),
    budget: moneyFromCents(80_000),
    available: moneyFromCents(80_000),
    unspent_this_month: moneyFromCents(80_000),
    unspent_carried_in: moneyFromCents(0),
    pct_used: '0',
    bar_pct: '0',
    state: 'normal',
    auto_release_rollover: false,
    recurring: true,
    rolls_over: true,
    txn_ids: [],
    entries: [],
    ...overrides,
  }
}

describe('<EnvelopeBar>', () => {
  it('fills to 100% while printing the true percentage past it', () => {
    const html = renderToStaticMarkup(
      <EnvelopeBar envelope={envelope({ spent: moneyFromCents(232_000) })} />,
    )

    // The bar cannot draw past its end…
    expect(html).toContain('width:100%')
    // …and the label must not pretend it did not try.
    expect(html).toContain('290%')
    expect(html).toContain('aria-valuenow="100"')
  })

  it('prints 100% for a zero target that has been spent against', () => {
    const html = renderToStaticMarkup(
      <EnvelopeBar
        envelope={envelope({ target_amount: moneyFromCents(0), spent: moneyFromCents(4_200) })}
      />,
    )

    expect(html).toContain('100%')
    expect(html).toContain('width:100%')
  })

  it('starts the rollover slice where the target ends', () => {
    const html = renderToStaticMarkup(
      <EnvelopeBar
        envelope={envelope({
          target_amount: moneyFromCents(20_000),
          rollover_amount: moneyFromCents(60_000),
          spent: moneyFromCents(16_000),
        })}
      />,
    )

    // $160 spent and $200 of target in a $800.00 budget: the carried slice
    // starts after 20% of spend and 5% of target left.
    expect(html).toContain('meter__segment--series" style="width:20%"')
    expect(html).toContain('meter__segment--none" style="width:5%"')
    expect(html).toContain('meter__segment--carried" style="width:75%"')
    expect(html).not.toContain('meter__segment--carried-spent')
  })

  it('draws spend past the target as paid from rollover, against the whole budget', () => {
    const html = renderToStaticMarkup(
      <EnvelopeBar
        envelope={envelope({
          target_amount: moneyFromCents(80_000),
          rollover_amount: moneyFromCents(60_000),
          spent: moneyFromCents(105_000),
        })}
      />,
    )

    // $1,050 spent of $800 target and $600 carried in: $1,400 budget, 75% used.
    // $250 came out of rollover and $350 of it is left.
    expect(html).toContain('of <span class="money">$1,400.00</span>')
    expect(html).toContain('75%')
    const width = (tone: string) =>
      Number(html.match(new RegExp(`meter__segment--${tone}" style="width:([\\d.]+)%"`))?.[1])
    expect(width('series')).toBeCloseTo(57.14, 2)
    expect(width('carried-spent')).toBeCloseTo(17.86, 2)
    expect(width('carried')).toBeCloseTo(25, 2)
    expect(html).not.toContain('meter__segment--none')
  })

  it('names the target, not a budget below zero, when rollover is deep in debt', () => {
    const html = renderToStaticMarkup(
      <EnvelopeBar
        envelope={envelope({
          target_amount: moneyFromCents(5_000),
          rollover_amount: moneyFromCents(-300_000),
        })}
      />,
    )

    expect(html).toContain('of <span class="money">$50.00</span>')
    expect(html).not.toContain('$2,950.00')
  })
})
