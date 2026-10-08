/** `by_kind` nests money inside a list; this proves the schema-directed conversion reaches it. */

import { describe, expect, it } from 'vitest'

import { fromJson } from '@bufbuild/protobuf'

import { GetNetWorthResponseSchema } from '@/gen/agentifi/v1/net_worth_pb'
import { moneyFromCents, sumMoney } from '@/lib/money'
import { fromWire } from '@/lib/rpc/wire'

import { amountOn, kindsOn, type NetWorth } from './networth'

/** One day, as `networth.go` serializes it. */
function point(on: string) {
  return {
    on,
    assets: { amount: '1500.00' },
    debt: { amount: '400.00' },
    net: { amount: '1100.00' },
    by_kind: [
      { kind: 'cash', amount: { amount: '1000.00' } },
      { kind: 'investment', amount: { amount: '500.00' } },
      { kind: 'credit_card', amount: { amount: '400.00' } },
    ],
    equity: { amount: '1200.00' },
  }
}

function response() {
  return {
    window: { from: '2026-08-01', to: '2026-08-31', date_field: 'posted' },
    granularity: 'day',
    points: [point('2026-08-01'), point('2026-08-31')],
    start: point('2026-08-01'),
    end: point('2026-08-31'),
    change: { amount: '0.00' },
    change_pct: null,
    debt_to_asset: '0.2667',
    groups: [],
    included_accounts: 3,
    total_accounts: 3,
  }
}

function parsed(): NetWorth {
  return fromWire<NetWorth>(GetNetWorthResponseSchema, fromJson(GetNetWorthResponseSchema, response()))
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

describe('the app shape', () => {
  it('reads an unset rate as null and an absent list as empty', () => {
    const worth = parsed()

    expect(worth.change_pct).toBeNull()
    expect(worth.debt_to_asset).toBe('0.2667')
    expect(worth.unconverted_currencies).toEqual([])
    expect(worth.window).toEqual({ from: '2026-08-01', to: '2026-08-31', date_field: 'posted' })
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
