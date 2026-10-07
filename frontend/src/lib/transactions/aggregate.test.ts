import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import {
  bucketFacet,
  collapseTail,
  monthSeries,
  type AggregateMonth,
  type Bucket,
} from './aggregate'

function bucket(key: string, cents: number) {
  return { key, label: key, total: moneyFromCents(cents) }
}

describe('the donut tail', () => {
  it('keeps the leading buckets and folds the rest into Everything else', () => {
    const folded = collapseTail(
      [bucket('a', -500), bucket('b', -400), bucket('c', -50), bucket('d', -25)],
      2,
    )
    expect(folded.map((one) => one.label)).toEqual(['a', 'b', 'Everything else'])
    expect(folded[2].total).toBe(moneyFromCents(-75))
  })

  it('goes negative when refunds in the tail outweigh spend there', () => {
    const folded = collapseTail(
      [bucket('a', -500), bucket('b', -400), bucket('c', -10), bucket('refund', 90)],
      2,
    )
    expect(folded[2].total).toBe(moneyFromCents(80))
  })

  it('leaves a list alone that a fold would not shorten', () => {
    const short = [bucket('a', -1), bucket('b', -2), bucket('c', -3)]
    expect(collapseTail(short, 2).map((one) => one.label)).toEqual(['a', 'b', 'c'])
  })
})

describe('the over-time clusters', () => {
  const ranked: Bucket[] = [bucket('a', -900), bucket('everything-else', -300)]
  const months: AggregateMonth[] = [
    { month: '2026-07', buckets: [bucket('a', -400), bucket('c', -100), bucket('d', -200)] },
    { month: '2026-08', buckets: [bucket('a', -500)] },
  ]

  it('folds a key the legend dropped into Everything else', () => {
    const series = monthSeries(months, ranked, 'spending')
    expect(series[0].amounts['everything-else']).toBe(moneyFromCents(-300))
  })

  it('draws a ranked bucket with nothing in a month at zero', () => {
    // Left out, recharts reads it as a gap rather than a bar of no height.
    const series = monthSeries(months, ranked, 'spending')
    expect(series[1].values['everything-else']).toBeCloseTo(0)
    expect(series[1].amounts['everything-else']).toBe(moneyFromCents(0))
  })

  it('plots spending upwards and income as it is stored', () => {
    expect(monthSeries(months, ranked, 'spending')[1].values.a).toBe(5)
    expect(monthSeries(months, ranked, 'income')[1].values.a).toBe(-5)
  })

  it('labels a month by its own name', () => {
    expect(monthSeries(months, ranked, 'spending').map((one) => one.label)).toEqual([
      'July 2026',
      'August 2026',
    ])
  })
})

describe('the facet behind a clicked wedge', () => {
  it('narrows to the category the wedge names', () => {
    expect(bucketFacet('category', { key: 'cat-food', label: 'Food & Dining' })).toEqual({
      categories: { ids: ['cat-food'], negated: false },
      uncategorized: false,
    })
  })

  it('lifts uncategorized when a category is picked, and the reverse', () => {
    expect(bucketFacet('category', { key: 'uncategorized', label: 'Uncategorized' })).toEqual({
      uncategorized: true,
      categories: { ids: [], negated: false },
    })
  })

  it('narrows to a payee by its display name, not the grouping key', () => {
    // The key is casefolded; the evaluator compares against the shown name.
    expect(bucketFacet('payee', { key: 'trader joe’s', label: 'Trader Joe’s' })).toEqual({
      payees: { values: ['Trader Joe’s'], negated: false },
    })
  })

  it('narrows to a tag, and reads the empty tag key as "has no tag"', () => {
    expect(bucketFacet('tag', { key: 'tag-1', label: 'Vacation' })).toEqual({
      tags: { ids: ['tag-1'], negated: false },
      hasTags: null,
    })
    expect(bucketFacet('tag', { key: '', label: 'No tag' })).toEqual({
      hasTags: false,
      tags: { ids: [], negated: false },
    })
  })

  it('has no facet for the folded tail, for no payee, or for no grouping', () => {
    expect(bucketFacet('category', { key: 'everything-else', label: 'Everything else' })).toBeNull()
    expect(bucketFacet('payee', { key: '', label: 'No payee' })).toBeNull()
    expect(bucketFacet('none', { key: 'all', label: 'All categories' })).toBeNull()
  })
})
