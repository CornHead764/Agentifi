import { describe, expect, it } from 'vitest'

import { foldText, matchesSearch } from './search'

describe('foldText', () => {
  it('ignores case, accents and the spaces around it', () => {
    expect(foldText('  Café ')).toBe('cafe')
    expect(foldText('CAFE')).toBe(foldText('café'))
    expect(foldText('   ')).toBe('')
  })
})

describe('matchesSearch', () => {
  it('matches everything when nothing is typed', () => {
    expect(matchesSearch('', 'Groceries')).toBe(true)
    expect(matchesSearch('   ')).toBe(true)
  })

  it('finds the text in any of the fields, ignoring case and accents', () => {
    expect(matchesSearch('cafe', 'Café Rouge')).toBe(true)
    expect(matchesSearch(' GROC ', 'Food', 'Groceries')).toBe(true)
    expect(matchesSearch('rent', 'Food', null, undefined)).toBe(false)
  })
})
