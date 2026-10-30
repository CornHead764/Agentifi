import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { TooltipProvider } from '@/components/ui'
import { EnvelopeControls, EnvelopePanel } from './EnvelopePanel'
import { planEnvelope } from './__fixtures__/plan'

function render(node: React.ReactNode): string {
  return renderToStaticMarkup(<TooltipProvider>{node}</TooltipProvider>)
}

const noop = () => undefined
const handlers = {
  month: '2026-09-01',
  view: 'list' as const,
  sort: 'name' as const,
  onReleaseRollover: noop,
  onChangeRollover: noop,
  onEditTarget: noop,
  onClearTarget: noop,
  onEditCategories: noop,
}

describe('the envelopes panel', () => {
  it('reads each of the three states in its own words', () => {
    const rendered = render(
      <EnvelopePanel
        frozen={false}
        envelopes={[
          planEnvelope({ id: 'a', name: 'Gas & Fuel' }),
          planEnvelope({
            id: 'b',
            name: 'Gifts',
            rollover_amount: moneyFromCents(60_000),
            spent: moneyFromCents(16_000),
            budget: moneyFromCents(80_000),
            available: moneyFromCents(64_000),
            state: 'with_rollover',
          }),
          planEnvelope({
            id: 'c',
            name: 'Food & Dining',
            spent: moneyFromCents(232_000),
            available: moneyFromCents(-152_000),
            pct_used: '290.00',
            bar_pct: '100',
            state: 'overspent',
          }),
        ]}
        {...handlers}
      />,
    )
    expect(rendered).toContain('Available to spend')
    expect(rendered).toContain('Available with rollover')
    expect(rendered).toContain('Overspent')
  })

  it('names the categories each envelope claims and counts what matched', () => {
    const rendered = render(
      <EnvelopePanel frozen={false} envelopes={[planEnvelope()]} {...handlers} />,
    )
    expect(rendered).toContain('Gas &amp; Fuel')
    expect(rendered).toContain('2') // txn_ids.length — the matched count
  })

  it('offers the list and card views and a sort', () => {
    const rendered = render(
      <EnvelopeControls view="list" sort="name" onView={noop} onSort={noop} />,
    )
    expect(rendered).toContain('Card view')
    expect(rendered).toContain('Sort envelopes')
  })

  it('draws the same envelope as a card in the card view', () => {
    const rendered = render(
      <EnvelopePanel frozen={false} envelopes={[planEnvelope()]} {...handlers} view="cards" />,
    )
    expect(rendered).toContain('card__title')
    expect(rendered).toContain('Available to spend')
  })
})
