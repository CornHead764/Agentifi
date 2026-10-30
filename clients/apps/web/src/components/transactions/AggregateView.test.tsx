/**
 * The Spending and Income tabs' chart shows the server's aggregate, and an
 * unloaded figure is a dash rather than $0.00.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import type { DrillCrumb } from '@/components/charts'
import { TooltipProvider } from '@/components/ui'
import { moneyFromCents } from '@/lib/money'
import type { TransactionAggregate } from '@/lib/transactions/aggregate'

import { AggregateView } from './AggregateView'

const AGGREGATE: TransactionAggregate = {
  direction: 'spending',
  group_by: 'category',
  total: moneyFromCents(-125_00),
  count: 4,
  buckets: [
    { key: 'cat-food', label: 'Food & Dining', total: moneyFromCents(-100_00) },
    { key: 'uncategorized', label: 'Uncategorized', total: moneyFromCents(-25_00) },
  ],
  months: [
    {
      month: '2026-08',
      buckets: [{ key: 'cat-food', label: 'Food & Dining', total: moneyFromCents(-100_00) }],
    },
  ],
}

function render(
  data: TransactionAggregate | undefined,
  drill: { under: string | null; trail: DrillCrumb[] } = { under: null, trail: [] },
): string {
  return renderToStaticMarkup(
    <TooltipProvider>
      <MemoryRouter>
        <AggregateView
          direction="spending"
          shape="total"
          data={data}
          from="2026-08-01"
          to="2026-08-31"
          groupBy="category"
          under={drill.under}
          trail={drill.trail}
          onGroupByChange={() => undefined}
          onShapeChange={() => undefined}
          onDrill={() => undefined}
          onStep={() => undefined}
        />
      </MemoryRouter>
    </TooltipProvider>,
  )
}

describe('the aggregate chart', () => {
  it('leads with the total the server computed over the whole match set', () => {
    expect(render(AGGREGATE)).toContain('$125.00')
  })

  it('lists every bucket the server ranked', () => {
    const markup = render(AGGREGATE)
    expect(markup).toContain('Food &amp; Dining')
    expect(markup).toContain('Uncategorized')
    expect(markup).toContain('$100.00')
  })

  it('shows a dash rather than zero while the figure is in flight', () => {
    // An empty window and a window still loading are different answers, and
    // "$0.00 spent" is the wrong one to give for either.
    const markup = render(undefined)
    expect(markup).not.toContain('$0.00')
    expect(markup).not.toContain('Nothing in this window.')
  })

  it('says so once the server has answered with nothing', () => {
    expect(render({ ...AGGREGATE, total: moneyFromCents(0), buckets: [], months: [] })).toContain(
      'Nothing in this window.',
    )
  })

  it('offers each line as a step further in, with no breadcrumb at the top', () => {
    const markup = render(AGGREGATE)
    expect(markup).toContain('aria-label="Narrow to Food &amp; Dining"')
    expect(markup).not.toContain('Chart breakdown')
  })

  it('draws the way back once drilled, and the drilled category’s own line is not a step', () => {
    const drilled: TransactionAggregate = {
      ...AGGREGATE,
      buckets: [
        { key: 'cat-employee', label: 'Lunch', total: moneyFromCents(-60_00) },
        { key: 'cat-food', label: 'Food & Dining', total: moneyFromCents(-40_00) },
      ],
    }
    const markup = render(drilled, {
      under: 'cat-food',
      trail: [{ key: 'category:cat-food', label: 'Food & Dining' }],
    })
    expect(markup).toContain('aria-label="Chart breakdown"')
    expect(markup).toContain('>All</button>')
    expect(markup).toContain('aria-label="Narrow to Lunch"')
    expect(markup).not.toContain('aria-label="Narrow to Food &amp; Dining"')
  })
})
