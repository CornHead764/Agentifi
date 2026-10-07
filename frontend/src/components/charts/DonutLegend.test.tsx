import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'

import { DonutLegend, type DonutSlice } from './index'

const SLICES: DonutSlice[] = [
  { key: 'home', label: 'Home', value: moneyFromCents(-450_000), color: '#111' },
  { key: 'shopping', label: 'Shopping', value: moneyFromCents(-350_000), color: '#222' },
]

function markup() {
  return renderToStaticMarkup(<DonutLegend slices={SLICES} />)
}

describe('the donut legend', () => {
  /**
   * The stacked layout places the figure by source order, in the label's
   * column on the next line; any other order puts a figure nearer another
   * category.
   */
  it('renders swatch, then label, then figure, in that order', () => {
    const row = markup().split('<li')[1]
    const order = [...row.matchAll(/class="(legend__swatch|legend__label|money[^"]*)"/g)].map(
      (match) => match[1].split(' ')[0],
    )

    expect(order).toEqual(['legend__swatch', 'legend__label', 'money'])
  })

  it('keeps every slice’s figure inside the same row as its label', () => {
    for (const row of markup().split('<li').slice(1)) {
      const label = /legend__label">([^<]*)</.exec(row)?.[1]
      const figure = />(\$[\d,.]+|-\$[\d,.]+)</.exec(row)?.[1]

      expect(label).toBeTruthy()
      expect(figure).toBeTruthy()
    }
  })

  it('pairs each label with its own amount', () => {
    const rows = markup().split('<li').slice(1)

    expect(rows[0]).toContain('Home')
    expect(rows[0]).toContain('4,500.00')
    expect(rows[1]).toContain('Shopping')
    expect(rows[1]).toContain('3,500.00')
  })
})
