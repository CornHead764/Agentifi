import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { TooltipProvider } from '@/components/ui'
import { PlanEntryPanel } from './PlanEntryPanel'
import { planBucket, planEntry } from './__fixtures__/plan'

function render(node: React.ReactNode): string {
  return renderToStaticMarkup(<TooltipProvider>{node}</TooltipProvider>)
}

const noop = () => undefined

const bills = planBucket('bills', {
  contributing: [
    planEntry({
      id: 'b1',
      name: 'Mortgage',
      group: 'bill',
      amount: moneyFromCents(-234_567),
    }),
    planEntry({
      id: 's1',
      name: 'Apple Music',
      group: 'subscription',
      status: 'upcoming',
      amount: moneyFromCents(-1_200),
    }),
    planEntry({
      id: 't1',
      name: 'Card Autopay',
      group: 'transfer',
      is_transfer: true,
      amount: moneyFromCents(-300_000),
    }),
  ],
  excluded: [
    planEntry({
      id: 'x1',
      name: 'Skipped Bill',
      amount: moneyFromCents(-1_000),
    }),
  ],
})

describe('the bills panel', () => {
  it('keeps the three families apart, each with its own header and subtotal', () => {
    const rendered = render(
      <PlanEntryPanel
        bucket={bills}
        frozen={false}
        onExclude={noop}
        onInclude={noop}
        emptyTitle="No bills this month"
      />,
    )
    expect(rendered).toContain('Regular bills')
    expect(rendered).toContain('Subscriptions')
    expect(rendered).toContain('2,345.67')
    expect(rendered).toContain('12.00')
  })

  it('labels transfers as netting to zero rather than as spending', () => {
    const rendered = render(
      <PlanEntryPanel
        bucket={bills}
        frozen={false}
        onExclude={noop}
        onInclude={noop}
        emptyTitle="No bills this month"
      />,
    )
    expect(rendered).toContain('Nets to zero')
  })

  it('counts the excluded rows behind a disclosure instead of hiding them', () => {
    const rendered = render(
      <PlanEntryPanel
        bucket={bills}
        frozen={false}
        onExclude={noop}
        onInclude={noop}
        emptyTitle="No bills this month"
      />,
    )
    expect(rendered).toContain('Excluded this month (1)')
  })

  it('makes every row pressable when the panel can open one, by name and by row', () => {
    const rendered = render(
      <PlanEntryPanel
        bucket={bills}
        frozen={false}
        onExclude={noop}
        onInclude={noop}
        onOpen={noop}
        emptyTitle="No bills this month"
      />,
    )
    expect(rendered).toContain('data-clickable="true"')
    expect(rendered).toContain('class="plan-entries__open"')
    // Without a handler the names are plain text, not dead buttons.
    const plain = render(
      <PlanEntryPanel
        bucket={bills}
        frozen={false}
        onExclude={noop}
        onInclude={noop}
        emptyTitle="No bills this month"
      />,
    )
    expect(plain).not.toContain('plan-entries__open')
  })

  it('shows the empty state when nothing counted', () => {
    const rendered = render(
      <PlanEntryPanel
        bucket={planBucket('bills')}
        frozen={false}
        onExclude={noop}
        onInclude={noop}
        emptyTitle="No bills this month"
      />,
    )
    expect(rendered).toContain('No bills this month')
  })
})

describe('the income panel', () => {
  // Invented: three lunch pads and a paycheck.
  const pad = (id: string, cents: number) =>
    planEntry({
      id,
      txn_id: id,
      name: 'Lunch (paycheck deduction)',
      group: null,
      status: 'received',
      category_name: 'Paycheck',
      is_padding: true,
      amount: moneyFromCents(cents),
    })
  const income = planBucket('income', {
    contributing: [
      pad('p1', 600),
      pad('p2', 850),
      planEntry({
        id: 'pay',
        name: 'Employer',
        group: null,
        status: 'received',
        category_name: 'Paycheck',
        amount: moneyFromCents(180_000),
      }),
      pad('p3', 500),
    ],
  })

  it('lumps the padding rows into one closed line with their count and sum', () => {
    const rendered = render(
      <PlanEntryPanel
        bucket={income}
        frozen={false}
        onExclude={noop}
        onInclude={noop}
        onOpen={noop}
        emptyTitle="No income this month"
      />,
    )
    expect(rendered).toContain('Lunch (paycheck deduction)')
    expect(rendered).toContain('3 transactions')
    expect(rendered).toContain('19.50')
    expect(rendered).toContain('aria-expanded="false"')
    expect(rendered).not.toContain('8.50')
    expect(rendered).toContain('Employer')
  })
})
