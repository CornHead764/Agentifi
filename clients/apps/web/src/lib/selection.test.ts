import { describe, expect, it } from 'vitest'

import { clickRange, rangeToggled } from './selection'

const drawn = ['a', 'b', 'c', 'd', 'e']

describe('clickRange', () => {
  it('reaches from the anchor to the clicked item, either way down the list', () => {
    expect(clickRange(drawn, 'b', 'd', true)).toEqual(['b', 'c', 'd'])
    expect(clickRange(drawn, 'd', 'b', true)).toEqual(['b', 'c', 'd'])
  })

  it('is the clicked item alone on a plain click', () => {
    expect(clickRange(drawn, 'b', 'd', false)).toEqual(['d'])
  })

  it('is the clicked item alone with no anchor, or one no longer drawn', () => {
    expect(clickRange(drawn, null, 'd', true)).toEqual(['d'])
    expect(clickRange(drawn, 'z', 'd', true)).toEqual(['d'])
  })

  it('is the clicked item alone when the anchor is the clicked item', () => {
    expect(clickRange(drawn, 'c', 'c', true)).toEqual(['c'])
  })
})

describe('rangeToggled', () => {
  it('ticks the range when the clicked box was clear', () => {
    expect([...rangeToggled(drawn, new Set(['a']), 'a', 'c', true)].sort()).toEqual([
      'a',
      'b',
      'c',
    ])
  })

  it('clears the range when the clicked box was ticked, leaving the rest', () => {
    const selected = new Set(['a', 'b', 'c', 'd', 'e'])
    expect([...rangeToggled(drawn, selected, 'b', 'd', true)].sort()).toEqual(['a', 'e'])
  })

  it('applies the clicked box’s new state to boxes already in it', () => {
    expect([...rangeToggled(drawn, new Set(['c']), 'b', 'e', true)].sort()).toEqual([
      'b',
      'c',
      'd',
      'e',
    ])
  })

  it('flips just the clicked box on a plain click', () => {
    expect([...rangeToggled(drawn, new Set(['a']), 'a', 'c', false)].sort()).toEqual(['a', 'c'])
  })

  it('keeps selected items that are not drawn', () => {
    expect([...rangeToggled(drawn, new Set(['hidden']), 'a', 'b', true)].sort()).toEqual([
      'a',
      'b',
      'hidden',
    ])
  })
})
