/** `by_kind` nests money inside a list; this proves `MoneyShape` reaches it. */

import { describe, expect, it } from 'vitest'

import { coerceMoney } from '@/lib/api'
import { moneyFromCents, sumMoney } from '@/lib/money'

import { NET_WORTH_SHAPE, amountOn, kindsOn, type NetWorth } from './networth'

/** One day, as `networth.go` serializes it. */
function point(on: string) {
  return {
    on,
    assets: '1500.00',
    debt: '400.00',
    net: '1100.00',
    by_kind: [
      { kind: 'cash', amount: '1000.00' },
      { kind: 'investment', amount: '500.00' },
      { kind: 'credit_card', amount: '400.00' },
    ],
    equity: '1200.00',
  }
}

function response(): unknown {
  return {
    window: { from: '2026-08-01', to: '2026-08-31', date_field: 'posted' },
    granularity: 'day',
    points: [point('2026-08-01'), point('2026-08-31')],
    start: point('2026-08-01'),
    end: point('2026-08-31'),
    change: '0.00',
    change_pct: null,
    debt_to_asset: '0.2667',
    groups: [],
    included_accounts: 3,
    total_accounts: 3,
  }
}

function parsed(): NetWorth {
  return coerceMoney<NetWorth>(response(), NET_WORTH_SHAPE)
}

describe('by_kind', () => {
  it('coerces each kind amount to Money', () => {
    expect(amountOn(parsed().points[0], 'cash')).toBe(moneyFromCents(100_000))
  })

  it('reaches the window ends, not only the sampled days', () => {
    const worth = parsed()

    expect(amountOn(worth.start, 'investment')).toBe(moneyFromCents(50_000))
    expect(amountOn(worth.end, 'credit_card')).toBe(moneyFromCents(40_000))
  })

  it('adds up to the assets and the debt beside it', () => {
    const worth = parsed()
    const day = worth.points[0]
    const assets = kindsOn(worth.points, 'asset').map((kind) => amountOn(day, kind))
    const debt = kindsOn(worth.points, 'debt').map((kind) => amountOn(day, kind))

    expect(sumMoney(assets)).toBe(day.assets)
    expect(sumMoney(debt)).toBe(day.debt)
  })

  it('names one line per kind, on the side that kind belongs to', () => {
    const worth = parsed()

    expect(kindsOn(worth.points, 'asset')).toEqual(['cash', 'investment'])
    expect(kindsOn(worth.points, 'debt')).toEqual(['credit_card'])
  })

  it('reads a kind the day has no figure for as zero', () => {
    expect(amountOn(parsed().points[0], 'loan')).toBe(moneyFromCents(0))
  })
})

describe('equity', () => {
  it('coerces the point-level equity figure, not just the by_kind amounts', () => {
    const worth = parsed()

    expect(worth.points[0].equity).toBe(moneyFromCents(120_000))
    expect(worth.start.equity).toBe(moneyFromCents(120_000))
    expect(worth.end.equity).toBe(moneyFromCents(120_000))
  })
})
