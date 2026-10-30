/**
 * Monthly Summary renders the endpoint's two ranked lists and nothing else:
 * the bills and subscriptions exclusion happens server side (`calculations.md`
 * §11), and the panel must not undo it. A row with nothing in the prior month
 * prints "—", not "0%" or "∞".
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { MonthlySummary } from '@/lib/clients/reports'
import { moneyFromCents } from '@/lib/money'

import { MonthlySummaryReport } from './MonthlySummaryReport'

/** A July whose rent is in `bills` and absent from both ranked lists, as the endpoint sends it. */
const SUMMARY: MonthlySummary = {
  month: '2026-07',
  prior_month: '2026-06',
  income: moneyFromCents(1_000_000),
  expenses: moneyFromCents(-1_500_000),
  net: moneyFromCents(-500_000),
  // Percent units, which is what `domain.Percent` serializes: 15.00 is 15%.
  income_change_pct: '15.00',
  expenses_change_pct: '9.60',
  net_change_pct: '-20.00',
  bills: moneyFromCents(-450_000),
  discretionary: moneyFromCents(-1_050_000),
  top_categories: [
    { key: 'travel', label: 'Travel', total: moneyFromCents(-600_000), count: 3, change_pct: '7.82' },
    { key: 'food', label: 'Food & Dining', total: moneyFromCents(-150_000), count: 12, change_pct: '3.30' },
    { key: 'home', label: 'Home', total: moneyFromCents(-50_000), count: 2, change_pct: null },
  ],
  top_payees: [
    { key: 'harbor', label: 'Harbor Travel', total: moneyFromCents(-500_000), count: 1, change_pct: '7.62' },
    { key: 'costco', label: 'Costco', total: moneyFromCents(-80_000), count: 4, change_pct: '1.70' },
    { key: 'air', label: 'Example Air', total: moneyFromCents(-60_000), count: 4, change_pct: null },
  ],
}

/** Tags out, entities back, runs of whitespace collapsed — what a reader sees. */
function text(summary: MonthlySummary): string {
  return renderToStaticMarkup(<MonthlySummaryReport summary={summary} />)
    .replace(/<[^>]*>/g, ' ')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
    .trim()
}

describe('the two ranked lists', () => {
  it('name the exclusion they were built under', () => {
    expect(text(SUMMARY)).toContain('Your spending excluding bills and subscriptions')
  })

  it('render exactly the entries the endpoint sent, in its order', () => {
    const rendered = text(SUMMARY)
    const positions = ['Travel', 'Food & Dining', 'Home'].map((label) => rendered.indexOf(label))
    expect(positions.every((index) => index >= 0)).toBe(true)
    expect(positions).toEqual([...positions].sort((a, b) => a - b))
  })

  /** The bills figure annotates the paired bar; the lists must not absorb it. */
  it('leave the bills total out of the lists it annotates the chart with', () => {
    const rendered = text(SUMMARY)
    expect(rendered).toContain('$4,500.00 on bills')
    expect(rendered).toContain('$10,500.00 on all other expenses')

    const listStart = rendered.indexOf('Your Spending Excluding')
    expect(rendered.slice(listStart)).not.toContain('$4,500.00')
  })

  it('carry an occurrence count on both lists, which is what makes them visits', () => {
    const rendered = text(SUMMARY)
    expect(rendered).toContain('4x')
    // Categories carry theirs too: the endpoint computes it for both lists.
    expect(rendered).toContain('12x')
  })
})

describe('a row the prior month had nothing of', () => {
  it('renders an em dash, never a percentage', () => {
    const rendered = text(SUMMARY)
    expect(rendered).toContain('—')
    // Both have a null change and neither may print as no change. A bare zero,
    // not the zero inside "10%".
    expect(rendered).not.toMatch(/(^|[^\d])0%/)
    expect(rendered).not.toContain('∞')
    expect(rendered).not.toContain('Infinity')
  })

  it('says there is no comparison rather than claiming a flat month', () => {
    const rendered = text({ ...SUMMARY, net_change_pct: null, income_change_pct: null })
    expect(rendered).toContain('No comparison against June')
  })
})
