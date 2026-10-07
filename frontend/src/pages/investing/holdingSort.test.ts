import { describe, expect, it } from 'vitest'

import type { Holding } from '@/lib/clients/investments'
import { parseMoney } from '@/lib/money'

import { DEFAULT_SORT, groupHoldingsByAccount, nextSort, sortHoldings } from './holdingSort'

function holding(over: Partial<Holding> & Pick<Holding, 'symbol'>): Holding {
  return {
    id: over.symbol.toLowerCase(),
    account_id: 'acct-brokerage',
    security_id: `sec-${over.symbol.toLowerCase()}`,
    name: `${over.symbol} Inc`,
    shares: '10',
    price: '100.00',
    currency: 'USD',
    market_value: parseMoney('1000.00'),
    is_unquoted: false,
    cost_basis: parseMoney('600.00'),
    total_gain: parseMoney('400.00'),
    total_gain_pct: '0.6667',
    is_cost_basis_complete: true,
    day_change: parseMoney('50.00'),
    day_change_pct: '0.05',
    share: '0.5',
    ...over,
  }
}

const symbols = (rows: readonly Holding[]) => rows.map((row) => row.symbol)

describe('ordering the portfolio table', () => {
  it('opens on value, largest first', () => {
    expect(DEFAULT_SORT).toEqual({ key: 'market_value', direction: 'desc' })
  })

  it('sorts a numeric column descending and a name ascending on the first click', () => {
    expect(nextSort(DEFAULT_SORT, 'total_gain').direction).toBe('desc')
    expect(nextSort(DEFAULT_SORT, 'symbol').direction).toBe('asc')
  })

  it('flips the column already sorted', () => {
    expect(nextSort({ key: 'symbol', direction: 'asc' }, 'symbol')).toEqual({
      key: 'symbol',
      direction: 'desc',
    })
  })

  it('orders by the figure asked for', () => {
    const rows = [
      holding({ symbol: 'AAA', market_value: parseMoney('500.00') }),
      holding({ symbol: 'BBB', market_value: parseMoney('2500.00') }),
      holding({ symbol: 'CCC', market_value: parseMoney('1000.00') }),
    ]
    expect(symbols(sortHoldings(rows, { key: 'market_value', direction: 'desc' }))).toEqual([
      'BBB',
      'CCC',
      'AAA',
    ])
  })

  it('puts an unknown gain last whichever way the column points', () => {
    // A position whose lots never arrived has no gain, and must not sort as a
    // zero in either direction.
    const rows = [
      holding({ symbol: 'AAA', total_gain: parseMoney('400.00') }),
      holding({
        symbol: 'BBB',
        total_gain: null,
        cost_basis: null,
        is_cost_basis_complete: false,
      }),
      holding({ symbol: 'CCC', total_gain: parseMoney('-900.00') }),
    ]
    expect(symbols(sortHoldings(rows, { key: 'total_gain', direction: 'desc' }))).toEqual([
      'AAA',
      'CCC',
      'BBB',
    ])
    expect(symbols(sortHoldings(rows, { key: 'total_gain', direction: 'asc' }))).toEqual([
      'CCC',
      'AAA',
      'BBB',
    ])
  })

  it('treats an unquoted security as having no price rather than a price of nothing', () => {
    const rows = [
      holding({ symbol: 'AAA', price: '20.00' }),
      holding({ symbol: 'FFF', price: null, is_unquoted: true }),
    ]
    expect(symbols(sortHoldings(rows, { key: 'price', direction: 'asc' }))).toEqual(['AAA', 'FFF'])
  })

  it('breaks a tie on the symbol so rows do not swap places between renders', () => {
    const rows = [holding({ symbol: 'ZZZ' }), holding({ symbol: 'AAA' })]
    expect(symbols(sortHoldings(rows, { key: 'market_value', direction: 'desc' }))).toEqual([
      'AAA',
      'ZZZ',
    ])
  })

  it('leaves the rows it was given alone', () => {
    const rows = [holding({ symbol: 'ZZZ' }), holding({ symbol: 'AAA' })]
    sortHoldings(rows, { key: 'symbol', direction: 'asc' })
    expect(symbols(rows)).toEqual(['ZZZ', 'AAA'])
  })
})

describe('grouping the portfolio by account', () => {
  const names: Record<string, string> = {
    'acct-brokerage': 'Brokerage',
    'acct-ira': 'Rollover IRA',
  }

  it('orders the groups by what each account holds', () => {
    const rows = [
      holding({ symbol: 'AAA', market_value: parseMoney('500.00') }),
      holding({ symbol: 'BBB', account_id: 'acct-ira', market_value: parseMoney('2000.00') }),
    ]
    const groups = groupHoldingsByAccount(rows, (id) => names[id])
    expect(groups.map((group) => group.label)).toEqual(['Rollover IRA', 'Brokerage'])
  })

  it('keeps the order inside a group, so the sorted column still sorts', () => {
    const rows = [
      holding({ symbol: 'CCC' }),
      holding({ symbol: 'AAA' }),
      holding({ symbol: 'BBB', account_id: 'acct-ira' }),
    ]
    const groups = groupHoldingsByAccount(rows, (id) => names[id])
    expect(symbols(groups.find((group) => group.label === 'Brokerage')?.rows ?? [])).toEqual([
      'CCC',
      'AAA',
    ])
  })
})
