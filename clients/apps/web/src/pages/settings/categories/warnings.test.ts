/**
 * The delete confirmations, checked against what the endpoints do:
 * `useDeleteCategory` soft deletes one row and touches nothing else;
 * `useDeleteTag` unlinks the tag everywhere first, then soft deletes it.
 */

import { describe, expect, it } from 'vitest'

import {
  categoryDeleteWarning,
  strandedSubcategoryWarning,
  tagDeleteWarning,
} from './warnings'

describe('the category delete warning', () => {
  it('says the transactions keep the category', () => {
    const sentence = categoryDeleteWarning({ isError: false, count: 24 })
    expect(sentence).toContain('24 reviewed transactions stay filed under it')
    expect(sentence).toContain('go on showing its name')
  })

  it('reads as one transaction rather than as one transactions', () => {
    expect(categoryDeleteWarning({ isError: false, count: 1 })).toContain(
      '1 reviewed transaction stays filed under it',
    )
  })

  it('says no reviewed transaction is filed under an unused category, and what the rest keep', () => {
    const sentence = categoryDeleteWarning({ isError: false, count: 0 })
    expect(sentence).toContain('No reviewed transactions are filed under it today.')
    expect(sentence).toContain('Any unreviewed transaction filed under it keeps the category.')
  })

  // An unread count must not read as a count of zero: that is the one wrong
  // answer, because it is the answer that makes the delete look free.
  it('admits when the count could not be read', () => {
    const failed = categoryDeleteWarning({ isError: true, count: undefined })
    expect(failed).toContain('could not be read')
    expect(failed).not.toBe(categoryDeleteWarning({ isError: false, count: 0 }))
  })

  it('says it is still counting rather than guessing', () => {
    expect(categoryDeleteWarning({ isError: false, count: undefined })).toContain('Counting')
  })
})

describe('the stranded subcategory warning', () => {
  it('is absent for a category with no children', () => {
    expect(strandedSubcategoryWarning([])).toBeNull()
  })

  it('names the one subcategory that will be left behind', () => {
    const sentence = strandedSubcategoryWarning(['Tolls'])
    expect(sentence).toContain('Its subcategory — Tolls — is not deleted with it')
    expect(sentence).toContain('moves to the top of the list')
  })

  it('counts and names several', () => {
    const sentence = strandedSubcategoryWarning(['Tolls', 'Parking'])
    expect(sentence).toContain('Its 2 subcategories — Tolls, Parking — are not deleted')
  })
})

describe('the tag delete warning', () => {
  it('says the chip comes off every row and does not come back', () => {
    const sentence = tagDeleteWarning({ isError: false, count: 312 })
    expect(sentence).toContain('312 reviewed transactions carry it')
    expect(sentence).toContain('All of them lose the chip')
    expect(sentence).toContain('nothing here puts it back')
  })

  it('reads as one transaction rather than as one transactions', () => {
    const sentence = tagDeleteWarning({ isError: false, count: 1 })
    expect(sentence).toContain('1 reviewed transaction carries it')
    expect(sentence).toContain('It loses the chip')
  })

  it('is the only one of the two that promises a change to transactions', () => {
    const tag = tagDeleteWarning({ isError: false, count: 5 })
    const category = categoryDeleteWarning({ isError: false, count: 5 })
    expect(tag).toContain('lose the chip')
    expect(category).not.toContain('lose')
  })

  it('admits when the count could not be read', () => {
    expect(tagDeleteWarning({ isError: true, count: undefined })).toContain('could not be read')
  })

  it('says the chip comes off unreviewed rows the count left out', () => {
    expect(tagDeleteWarning({ isError: false, count: 0 })).toBe(
      'No reviewed transactions carry it. It comes off any unreviewed transaction that does.',
    )
    expect(tagDeleteWarning({ isError: false, count: 3 })).toContain(
      'as does any unreviewed transaction carrying it',
    )
  })
})

