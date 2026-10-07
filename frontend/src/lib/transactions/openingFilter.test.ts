import { describe, expect, it } from 'vitest'

import { openingFilter } from './openingFilter'

const TODAY = new Date(2026, 8, 3)
const FOOD = '11111111-2222-4333-8444-555555555555'
const AUTO = '66666666-7777-4888-8999-aaaaaaaaaaaa'

function from(query: string) {
  return openingFilter(new URLSearchParams(query), TODAY)
}

describe('the state the register opens in', () => {
  it('opens unfiltered with no parameters', () => {
    const opening = from('')
    expect(opening.panel.isReviewed).toBeNull()
    expect(opening.panel.uncategorized).toBe(false)
    expect(opening.panel.isBillOrSubscription).toBeNull()
  })

  it('opens the review queue for the dashboard tiles', () => {
    expect(from('isReviewed=0').panel.isReviewed).toBe(false)
  })

  it('opens narrowed to uncategorized rows', () => {
    expect(from('isReviewed=0&uncategorized=1').panel.uncategorized).toBe(true)
  })

  it('opens narrowed to bills and subscriptions', () => {
    expect(from('isReviewed=0&isBill=1').panel.isBillOrSubscription).toBe(true)
  })

  it('opens on every row missing a receipt, over all time', () => {
    const opening = from('displayNode=all&missingReceipt=1&datePreset=all-time')
    expect(opening.panel.missingReceipt).toBe(true)
    expect(opening.panel.isReviewed).toBeNull()
    expect(opening.dates).toEqual({ range: { from: null, to: null }, preset: null })
    expect(from('missingReceipt=yes').panel.missingReceipt).toBeNull()
  })

  it('carries every tile filter at once', () => {
    const opening = from('isReviewed=0&uncategorized=1&isBill=1')
    expect(opening.panel.isReviewed).toBe(false)
    expect(opening.panel.uncategorized).toBe(true)
    expect(opening.panel.isBillOrSubscription).toBe(true)
  })

  it('narrows only on the exact values it documents', () => {
    expect(from('isReviewed=1').panel.isReviewed).toBeNull()
    expect(from('isReviewed=yes').panel.isReviewed).toBeNull()
    expect(from('uncategorized=true').panel.uncategorized).toBe(false)
    expect(from('isBill=0').panel.isBillOrSubscription).toBeNull()
  })

  it('names no window unless the link does', () => {
    expect(from('').dates).toBeNull()
    expect(from('isReviewed=0').dates).toBeNull()
  })

  it('opens the window a preset link names, as a token', () => {
    expect(from('datePreset=this-month').dates).toEqual({
      range: { from: '2026-09-01', to: '2026-09-03' },
      preset: 'this-month',
    })
  })

  it('opens all time with no token, so the picker highlights it', () => {
    expect(from('datePreset=all-time').dates).toEqual({
      range: { from: null, to: null },
      preset: null,
    })
  })

  it('opens a hand-written window from both ends, or from one', () => {
    expect(from('from=2026-01-01&to=2026-03-31').dates).toEqual({
      range: { from: '2026-01-01', to: '2026-03-31' },
      preset: null,
    })
    expect(from('from=2026-01-01').dates?.range).toEqual({ from: '2026-01-01', to: null })
  })

  it('drops a token that resolves to nothing and a date that is not one', () => {
    expect(from('datePreset=since-forever').dates).toBeNull()
    expect(from('from=yesterday&to=today').dates).toBeNull()
    expect(from('from=2026-01-01&to=nonsense').dates?.range).toEqual({
      from: '2026-01-01',
      to: null,
    })
  })

  it('leaves every other facet alone', () => {
    const opening = from('isReviewed=0&uncategorized=1&isBill=1')
    expect(opening.panel.categories.ids).toEqual([])
    expect(opening.panel.payees.values).toEqual([])
    expect(opening.panel.texts).toEqual([])
    expect(opening.panel.amount).toBeNull()
  })

  it('ticks the category a link names, and every one it names', () => {
    expect(from(`category=${FOOD}`).panel.categories).toEqual({ ids: [FOOD], negated: false })
    expect(from(`category=${FOOD}&category=${AUTO}`).panel.categories.ids).toEqual([FOOD, AUTO])
  })

  it('drops a category id that is not one', () => {
    expect(from('category=food').panel.categories.ids).toEqual([])
    expect(from(`category=food&category=${AUTO}`).panel.categories.ids).toEqual([AUTO])
  })
})
