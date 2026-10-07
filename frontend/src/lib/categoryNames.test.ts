import { describe, expect, it } from 'vitest'

import { category } from '@/test/builders'

import { categoryCreateOffer, categoryLabel, categoryName, categoryNamed, findCategory } from './categoryNames'
import { inlineCategoryCreate } from './clients/categories'

/**
 * A category created excluded silently takes its rows out of reports or the
 * spending plan, so both flags are sent explicitly rather than left to defaults.
 */

const categories = [
  { id: 'c1', name: 'Groceries' },
  { id: 'c2', name: 'Café' },
  // Duplicate names are legal — a Simplifi export arrives with two of these.
  { id: 'c3', name: 'Child Support' },
  { id: 'c4', name: 'Child Support' },
]

describe('finding a category by name', () => {
  it('finds the first category carrying a name, and identifies it by id', () => {
    expect(categoryNamed(categories, 'cafe')?.id).toBe('c2')
    expect(categoryNamed(categories, 'Child Support')?.id).toBe('c3')
    expect(categoryNamed(categories, 'Rent')).toBeUndefined()
    expect(categoryNamed(categories, '  ')).toBeUndefined()
  })
})

describe('the offer to create', () => {
  it('offers a name nothing matches', () => {
    const offer = categoryCreateOffer(categories, ' Rideshare ', 0)
    expect(offer).toEqual({ name: 'Rideshare', taken: undefined, canCreate: true })
  })

  it('offers nothing for a blank or whitespace name', () => {
    expect(categoryCreateOffer(categories, '', 0).canCreate).toBe(false)
    expect(categoryCreateOffer(categories, '   ', 0).canCreate).toBe(false)
  })

  it('offers nothing while the list still has something to pick', () => {
    expect(categoryCreateOffer(categories, 'Groc', 1).canCreate).toBe(false)
  })

  it('refuses a name an existing category carries, and names that one', () => {
    // The search compares letters as typed, so only the folded check stops a second Café.
    const offer = categoryCreateOffer(categories, 'cafe', 0)
    expect(offer.canCreate).toBe(false)
    expect(offer.taken?.name).toBe('Café')
  })
})

describe('reading a category id back, honestly', () => {
  const rows = [
    category('auto', 'Auto & Transport'),
    category('gas', 'Gas', 'auto'),
    category('reg', 'Registration', 'auto'),
    category('plate', 'Plate Renewal', 'reg'),
  ]

  it('finds the row a category_id names, and nothing once it has been deleted', () => {
    expect(findCategory(rows, 'gas')?.name).toBe('Gas')
    expect(findCategory(rows, 'gone')).toBeUndefined()
    expect(findCategory(rows, null)).toBeUndefined()
  })

  it('never categorized is "Uncategorized"', () => {
    expect(categoryLabel(rows, null)).toBe('Uncategorized')
    expect(categoryName(rows, null)).toBe('Uncategorized')
  })

  it('still loading is "…", since every space is seeded and an empty list means no answer yet', () => {
    expect(categoryLabel([], 'gas')).toBe('…')
    expect(categoryName([], 'gas')).toBe('…')
  })

  it('a deleted category reads as "Uncategorized", not as still loading', () => {
    expect(categoryLabel(rows, 'gone' as never)).toBe('Uncategorized')
    expect(categoryName(rows, 'gone' as never)).toBe('Uncategorized')
  })

  it('names a top-level category on its own', () => {
    expect(categoryLabel(rows, 'auto')).toBe('Auto & Transport')
    expect(categoryName(rows, 'auto')).toBe('Auto & Transport')
  })

  it('reads the breadcrumb to the root; categoryName keeps only the leaf', () => {
    expect(categoryLabel(rows, 'gas')).toBe('Auto & Transport › Gas')
    expect(categoryLabel(rows, 'plate')).toBe('Auto & Transport › Registration › Plate Renewal')
    expect(categoryName(rows, 'plate')).toBe('Plate Renewal')
  })
})

describe('what an inline create writes', () => {
  it('is a top-level expense with nothing excluded', () => {
    expect(inlineCategoryCreate('  Rideshare ')).toEqual({
      name: 'Rideshare',
      kind: 'expense',
      parent_id: null,
      excluded_from_reports: false,
      excluded_from_spending_plan: false,
      excluded_from_category_list: false,
    })
  })
})
