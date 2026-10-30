import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { builtInQuickFilters } from '@/lib/transactions/quickFilters'
import type { TransactionPage } from '@/lib/transactions/types'

import { QuickFilters } from './QuickFilters'

function page(overrides: Partial<TransactionPage> = {}): TransactionPage {
  return {
    items: [],
    count: 2,
    total: moneyFromCents(-10_500),
    full_total: moneyFromCents(-10_500),
    partial_count: 0,
    padding_count: 0,
    padding_total: moneyFromCents(0),
    window: { from: null, to: null, date_field: 'posted' },
    limit: 200,
    offset: 0,
    ...overrides,
  }
}

const noop = () => undefined

const BUILT_IN = builtInQuickFilters(new Date(2026, 8, 3))

function strip(summary: TransactionPage | null, activeId: string | null = null): string {
  return renderToStaticMarkup(
    <QuickFilters
      summary={summary}
      carried={null}
      quickFilters={BUILT_IN}
      activeId={activeId}
      waiting={0}
      onChoose={noop}
      onCreate={noop}
      onManage={noop}
      onClearAll={noop}
    />,
  )
}

const chip = (summary: TransactionPage) => strip(summary)

describe('the quick filters menu', () => {
  it('is one closed trigger, not a strip of toggles', () => {
    const html = strip(null)
    expect(html).toContain('Quick filters')
    expect(html).toContain('aria-haspopup="menu"')
    expect(html).not.toContain('Uncategorized')
  })

  it('puts the date window in the same row, after the quick filters', () => {
    const html = renderToStaticMarkup(
      <QuickFilters
        filter={<button type="button">Filter</button>}
        dates={<button type="button">All time</button>}
        summary={null}
        carried={null}
        quickFilters={BUILT_IN}
        activeId={null}
        waiting={0}
        onChoose={noop}
        onCreate={noop}
      onManage={noop}
        onClearAll={noop}
      />,
    )
    expect(html.indexOf('Filter')).toBeLessThan(html.indexOf('Quick filters'))
    expect(html.indexOf('Quick filters')).toBeLessThan(html.indexOf('All time'))
  })

  it('marks the trigger while one of them is what the register shows', () => {
    expect(strip(null)).not.toContain('btn--primary')
    expect(strip(null, 'uncategorized')).toContain('btn--primary')
  })
})

describe('the result chip', () => {
  it('shows one total when no split row is counted in part', () => {
    const html = chip(page())
    expect(html).toContain('105.00')
    expect(html).not.toContain('in full')
    expect(html).not.toContain('chip__partial')
  })

  it('leads with the matching splits and carries the whole rows beside them', () => {
    // A $25 row split $10 groceries / $15 dining, plus a $50 groceries row.
    const html = chip(
      page({
        total: moneyFromCents(-6_000),
        full_total: moneyFromCents(-7_500),
        partial_count: 1,
      }),
    )
    expect(html).toContain('60.00')
    expect(html).toContain('75.00')
    expect(html).toContain('in full')
    expect(html).toContain('Matching splits')
    expect(html.indexOf('60.00')).toBeLessThan(html.indexOf('75.00'))
  })
})

describe('the padding chip', () => {
  function withPadding(summary: TransactionPage, padding: 'show' | 'hide'): string {
    return renderToStaticMarkup(
      <QuickFilters
        summary={summary}
        carried={null}
        quickFilters={BUILT_IN}
        activeId={null}
        waiting={0}
        padding={padding}
        onPadding={noop}
        onChoose={noop}
        onCreate={noop}
        onManage={noop}
        onClearAll={noop}
      />,
    )
  }

  it('says how many padding rows were hidden and what they add up to', () => {
    // Invented: two lunches of $6.00 and $8.50, each padded.
    const html = withPadding(
      page({ total: moneyFromCents(-1_450), padding_count: 2, padding_total: moneyFromCents(1_450) }),
      'hide',
    )
    expect(html).toContain('2 padding rows hidden')
    expect(html).toContain('14.50')
    expect(html).toContain('aria-pressed="false"')
  })

  it('offers nothing when the query hid no padding', () => {
    expect(withPadding(page(), 'hide')).not.toContain('padding')
    expect(chip(page({ padding_count: 2 }))).not.toContain('padding rows')
  })

  it('stays pressed while padding is listed, to hide it again', () => {
    const html = withPadding(page(), 'show')
    expect(html).toContain('Padding shown')
    expect(html).toContain('aria-pressed="true"')
  })
})
