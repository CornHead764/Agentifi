import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
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
    spent: moneyFromCents(0),
    budget: moneyFromCents(80_000),
    available: moneyFromCents(80_000),
    pct_used: '0',
    bar_pct: '0',
    state: 'normal',
    auto_release_rollover: false,
    roll_under: true,
    recurring: true,
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

  it('says a negative carry rolled under, in the expense colour', () => {
    const html = renderToStaticMarkup(
      <EnvelopeBar
        envelope={envelope({
          rollover_amount: moneyFromCents(-5_000),
          spent: moneyFromCents(40_000),
          budget: moneyFromCents(75_000),
          available: moneyFromCents(35_000),
        })}
      />,
    )

    expect(html).toContain('envelope-bar__rollover--under')
    expect(html).toContain('rolled under')
  })

  it('leaves a positive carry unlabelled beyond its sign', () => {
    const html = renderToStaticMarkup(
      <EnvelopeBar envelope={envelope({ rollover_amount: moneyFromCents(60_000) })} />,
    )

    expect(html).not.toContain('rolled under')
    expect(html).not.toContain('envelope-bar__rollover--under')
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
  })
})
