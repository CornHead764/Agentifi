import { describe, expect, it } from 'vitest'

import { toggled, toggledSet } from './toggle'

describe('toggled', () => {
  it('adds an item at the end and removes it where it stands', () => {
    expect(toggled(['a', 'b'], 'c', true)).toEqual(['a', 'b', 'c'])
    expect(toggled(['a', 'b', 'c'], 'b', false)).toEqual(['a', 'c'])
  })

  it('never lists an item twice or removes one that is absent', () => {
    expect(toggled(['a', 'b'], 'a', true)).toEqual(['a', 'b'])
    expect(toggled(['a'], 'z', false)).toEqual(['a'])
  })

  it('flips the item when told nothing', () => {
    expect(toggled(['a'], 'a')).toEqual([])
    expect(toggled(['a'], 'b')).toEqual(['a', 'b'])
  })
})

describe('toggledSet', () => {
  it('returns a new set with the item added, removed or flipped', () => {
    const start = new Set(['a'])
    expect([...toggledSet(start, 'b', true)]).toEqual(['a', 'b'])
    expect([...toggledSet(start, 'a', false)]).toEqual([])
    expect([...toggledSet(start, 'a')]).toEqual([])
    expect([...start]).toEqual(['a'])
  })
})
