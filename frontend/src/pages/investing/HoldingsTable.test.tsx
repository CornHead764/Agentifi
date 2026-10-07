import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'
import type { Holding, PortfolioTotals, SecurityDetail } from '@/lib/clients/investments'
import { moneyFromCents, ZERO_MONEY } from '@/lib/money'

import { HoldingsTable, PortfolioHeader } from './HoldingsTable'
import { positionsMissingBasisText } from './incomplete'

const KNOWN: Holding = {
  id: 'h1',
  account_id: 'a1',
  security_id: 's1',
  symbol: 'EXMP',
  name: 'Example Holdings Inc',
  shares: '4',
  price: '119.00',
  currency: 'USD',
  market_value: moneyFromCents(47_600),
  cost_basis: moneyFromCents(170_000),
  total_gain: moneyFromCents(-122_400),
  total_gain_pct: '-0.7200',
  is_cost_basis_complete: true,
  day_change: moneyFromCents(2_800),
  day_change_pct: '0.0625',
  is_unquoted: false,
  share: '0.25',
}

/**
 * A position imported without its lots: basis unknown, not zero.
 * `investments.go` sends the three basis figures as null together, and this
 * row proves the client never turns them back into a number.
 */
const UNKNOWN: Holding = {
  ...KNOWN,
  id: 'h2',
  symbol: 'SMPL',
  name: 'Sample Technologies',
  shares: '8',
  price: '440.00',
  market_value: moneyFromCents(352_000),
  cost_basis: null,
  total_gain: null,
  total_gain_pct: null,
  is_cost_basis_complete: false,
  day_change: moneyFromCents(5_600),
  day_change_pct: '0.0162',
}

/** The shell mounts one provider for the whole app; the header needs it too. */
function render(node: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(<TooltipProvider>{node}</TooltipProvider>)
}

function text(html: string): string {
  return html.replace(/<[^>]*>/g, ' ')
}

/**
 * A position in an employer plan's own fund, which has no public quote and
 * never will. The provider's market value is the only figure there is.
 */
const UNQUOTED: Holding = {
  ...KNOWN,
  id: 'h3',
  symbol: 'NW60',
  name: 'Northwind Target Date Fund',
  shares: '1500.0000000000',
  price: null,
  market_value: moneyFromCents(4_812_500),
  day_change: null,
  day_change_pct: null,
  is_unquoted: true,
}

describe('a holding with no quote', () => {
  it('prints the value it has and no price', () => {
    const html = render(<HoldingsTable holdings={[UNQUOTED]} />)

    expect(text(html)).toContain('$48,125.00')
    // A price of $0.00 would read as a worthless position, not an unpriced one.
    expect(html).not.toContain('$0.00')
    expect(html).toContain('—')
  })

  it('says why the price is missing rather than implying a failed fetch', () => {
    const html = render(<HoldingsTable holdings={[UNQUOTED]} />)
    expect(html).toContain('No public quote')
    expect(html).not.toContain('No quote on file yet')
  })
})

describe('a holding with no cost basis', () => {
  it('renders no figure for the basis and a dash for the gain', () => {
    const html = render(<HoldingsTable holdings={[UNKNOWN]} />)

    // Gain $ is unknown and prints the dash.
    expect(html).toContain('money--absent')
    expect(html).toContain('—')
    // Total cost shows the incomplete state rather than a figure, and says
    // which holding it is about.
    expect(html).toContain(
      'aria-label="No cost basis for SMPL: its lots have no purchase price, so total cost and gain are not shown."',
    )
    // market_value − 0 would be +$3,520.00 of invented profit, and a basis of
    // $0.00 would be the same lie with a different sign.
    expect(text(html)).not.toContain('+$3,520.00')
    expect(html).not.toContain('$0.00')
  })

  it('still renders the figures it does have', () => {
    const html = render(<HoldingsTable holdings={[UNKNOWN]} />)
    expect(html).toContain('Sample Technologies')
    // Market value and day change are known and print normally.
    expect(text(html)).toContain('$56.00')
  })

  it('does not affect a holding whose basis is known', () => {
    const html = render(<HoldingsTable holdings={[KNOWN]} />)
    expect(text(html)).toContain('$1,700.00')
    expect(text(html)).toContain('-$1,224.00')
    expect(html).not.toContain('money--absent')
    expect(html).not.toContain('No cost basis')
  })
})

describe('the portfolio header', () => {
  const totals: PortfolioTotals = {
    market_value: moneyFromCents(32_000_000),
    cost_basis: moneyFromCents(29_000_000),
    total_gain: moneyFromCents(3_000_000),
    is_cost_basis_incomplete: true,
    day_change: moneyFromCents(68_750),
    day_change_pct: '0.0021',
    is_day_change_incomplete: false,
    account_balance_not_held: ZERO_MONEY,
    total_value: moneyFromCents(32_000_000),
  }

  it('badges the total Incomplete when some holdings have no basis', () => {
    const html = render(<PortfolioHeader totals={totals} holdings={[KNOWN, UNKNOWN]} />)
    expect(html).toContain('Incomplete')
    // The gain it does have is still shown — it covers the holdings that have
    // a basis, and it is not market value minus a partial basis.
    expect(text(html)).toContain('+$30,000.00')
  })

  it('names the holdings the incomplete total gain leaves out', () => {
    const html = render(<PortfolioHeader totals={totals} holdings={[KNOWN, UNKNOWN]} />)
    expect(html).toContain(
      'aria-label="Incomplete: 1 of 2 holdings has no cost basis (SMPL). ' +
        'Total gain leaves them out rather than counting them as pure gain."',
    )
  })

  it('names the holdings with no prior close when today’s change is incomplete', () => {
    const html = render(
      <PortfolioHeader
        totals={{ ...totals, is_cost_basis_incomplete: false, is_day_change_incomplete: true }}
        holdings={[KNOWN, UNQUOTED]}
      />,
    )
    expect(html).toContain(
      'aria-label="Incomplete: No prior close on file for NW60. ' +
        'Today’s change leaves them out rather than counting them as unchanged."',
    )
  })

  it('says how old the newest quote is when the prices are stale', () => {
    const html = render(
      <PortfolioHeader
        totals={totals}
        holdings={[KNOWN]}
        asOf={{ label: 'Priced 6 days ago', since: '6 days ago', stale: true }}
      />,
    )
    expect(html).toContain(
      'aria-label="Priced 6 days ago: The newest quote is from 6 days ago. ' +
        'Value, gain and today’s change use those prices until a refresh finds newer ones."',
    )
  })

  it('drops the badge when every holding has a basis', () => {
    const html = render(<PortfolioHeader totals={{ ...totals, is_cost_basis_incomplete: false }} holdings={[KNOWN]} />)
    expect(html).not.toContain('Incomplete')
  })

  it('dashes a total gain that is unknown entirely', () => {
    const html = render(
      <PortfolioHeader
        totals={{ ...totals, cost_basis: null, total_gain: null, is_cost_basis_incomplete: true }}
        holdings={[UNKNOWN]}
      />,
    )
    expect(html).toContain('money--absent')
    // The whole market value reported as gain is the exact failure being guarded.
    expect(text(html)).not.toContain('+$320,000.00')
  })

  it('shows the balance of accounts holding no positions, rather than dropping it', () => {
    const html = render(
      <PortfolioHeader
        totals={{
          ...totals,
          account_balance_not_held: moneyFromCents(500_000),
          total_value: moneyFromCents(32_500_000),
        }}
        holdings={[KNOWN, UNKNOWN]}
      />,
    )
    expect(text(html)).toContain('$325,000.00')
    expect(text(html)).toContain('$5,000.00')
  })
})

describe('the actions column', () => {
  // The empty row spans the table, and a header column it does not cover
  // leaves the message short by one cell.
  it('widens the empty row with itself', () => {
    expect(render(<HoldingsTable holdings={[]} />)).toContain('colSpan="10"')
    expect(render(<HoldingsTable holdings={[]} onRemove={() => {}} />)).toContain('colSpan="11"')
  })
})

describe('the row menu', () => {
  it('is not drawn when there is nothing to do from it', () => {
    const html = render(<HoldingsTable holdings={[KNOWN]} />)
    expect(html).not.toContain('Actions for EXMP')
  })

  it('opens per holding when a holding can be removed', () => {
    const html = render(<HoldingsTable holdings={[KNOWN]} onRemove={() => {}} />)
    expect(html).toContain('Actions for EXMP')
  })

  // The actions column is one cell whether it holds Why, the menu or both. An
  // empty row measured off `onExplain` alone comes up short by one.
  it('widens the empty row on its own, without the Why column', () => {
    expect(render(<HoldingsTable holdings={[]} onRemove={() => {}} />)).toContain('colSpan="11"')
  })
})

describe('reading the table', () => {
  it('shows each row\u2019s share of the portfolio', () => {
    // The server sends a fraction; every percent on this screen is drawn from
    // percent units, and the conversion happens once.
    const html = render(<HoldingsTable holdings={[{ ...KNOWN, share: '0.125' }]} />)
    expect(text(html)).toContain('12.5%')
  })

  it('opens the security from the symbol when there is somewhere to open', () => {
    expect(render(<HoldingsTable holdings={[KNOWN]} onOpen={() => {}} />)).toContain('Open EXMP')
    expect(render(<HoldingsTable holdings={[KNOWN]} />)).not.toContain('Open EXMP')
  })

  it('sorts by value largest first before anybody touches a heading', () => {
    const small = { ...KNOWN, id: 'h9', symbol: 'AAA', market_value: moneyFromCents(100) }
    const html = render(<HoldingsTable holdings={[small, KNOWN]} />)
    expect(html.indexOf('EXMP')).toBeLessThan(html.indexOf('AAA'))
  })

  it('draws a subhead per account when asked to group', () => {
    const ira = { ...KNOWN, id: 'h8', account_id: 'a2', symbol: 'VOO' }
    const html = render(
      <HoldingsTable
        holdings={[KNOWN, ira]}
        byAccount
        accountName={(id) => (id === 'a1' ? 'Brokerage' : 'Rollover IRA')}
      />,
    )
    expect(html).toContain('table__group')
    expect(text(html)).toContain('Brokerage')
    expect(text(html)).toContain('Rollover IRA')
  })
})

describe('a position in a small-balance account', () => {
  const dust: Holding = {
    ...KNOWN,
    id: 'h7',
    account_id: 'coin',
    symbol: 'DUST',
    name: 'Dust Coin',
    market_value: moneyFromCents(12),
  }
  const isSmallBalance = (id: string) => id === 'coin'

  it('is left out behind a line that says so', () => {
    const html = render(<HoldingsTable holdings={[KNOWN, dust]} isSmallBalance={isSmallBalance} />)
    expect(html).toContain('EXMP')
    expect(html).not.toContain('Dust Coin')
    expect(text(html)).toContain('1 small balance hidden')
  })

  it('takes its account’s subhead with it when grouped', () => {
    const html = render(
      <HoldingsTable
        holdings={[KNOWN, dust]}
        byAccount
        accountName={(id) => (id === 'a1' ? 'Brokerage' : 'Coin wallet')}
        isSmallBalance={isSmallBalance}
      />,
    )
    expect(text(html)).toContain('Brokerage')
    expect(text(html)).not.toContain('Coin wallet')
  })

  it('leaves no empty-table message when every row is a small balance', () => {
    const html = render(<HoldingsTable holdings={[dust]} isSmallBalance={isSmallBalance} />)
    expect(html).not.toContain('No holdings yet')
    expect(text(html)).toContain('1 small balance hidden')
  })

  it('draws no line when nothing is hidden', () => {
    const html = render(<HoldingsTable holdings={[KNOWN, dust]} />)
    expect(html).toContain('Dust Coin')
    expect(html).not.toContain('small balance')
  })
})

describe('a security whose basis is incomplete', () => {
  it('counts the positions its total gain leaves out', () => {
    const detail = {
      security: { symbol: 'EXMP' },
      positions: [KNOWN, { ...UNKNOWN, symbol: 'EXMP' }, { ...UNKNOWN, id: 'h4', symbol: 'EXMP' }],
    } as unknown as SecurityDetail
    expect(positionsMissingBasisText(detail)).toBe(
      '2 of 3 positions in EXMP have no cost basis. Total gain leaves them out rather than counting them as pure gain.',
    )
  })
})
