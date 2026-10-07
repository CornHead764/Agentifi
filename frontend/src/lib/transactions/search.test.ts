import { describe, expect, it } from 'vitest'

import { EMPTY_UNIVERSE, type FilterUniverse } from './filter'
import { parseSearch } from './search'
import type { Category, Tag } from './types'

const DINING: Category = {
  id: 'cat-dining',
  parent_id: null,
  name: 'Dining & Drinks',
  kind: 'expense',
  known_category_id: null,
  txf_id: null,
  txf_ids: [],
  is_user_assignable: true,
  is_editable: true,
  protected_reason: null,
  excluded_from_reports: false,
  excluded_from_spending_plan: false,
  excluded_from_category_list: false,
  sort_order: 0,
}

const REIMBURSABLE: Tag = { id: 'tag-reimb', name: 'reimbursable', color: null }

const UNIVERSE: FilterUniverse = { categories: [DINING], tags: [REIMBURSABLE] }

/** Fixed so a relative window is a value and not whatever day the suite runs. */
const TODAY = new Date(2026, 7, 21)

function parse(query: string) {
  return parseSearch(query, { universe: UNIVERSE, today: TODAY })
}

describe('search DSL', () => {
  it('treats a bare word as free text over both names', () => {
    const { draft, errors } = parse('groceries')
    expect(draft.texts).toEqual(['groceries'])
    expect(errors).toEqual([])
  })

  it('keeps a quoted phrase together', () => {
    expect(parse('"Walmart Store"').draft.texts).toEqual(['Walmart Store'])
  })

  it('ANDs space-separated terms', () => {
    const { draft } = parse('date=this-month amount>500')
    expect(draft.date).toEqual({ from: '2026-08-01', to: '2026-08-21', preset: 'this-month' })
    expect(draft.amount).toEqual({ min: '500.00', max: null })
  })

  // The cheat sheet's row is "Payee contains", so each value is a substring test.
  it('reads a payee list as one contains facet', () => {
    const { draft } = parse('payee:walmart,target')
    expect(draft.payees).toEqual({
      values: ['walmart', 'target'],
      negated: false,
      operator: 'contains',
    })
  })

  it('resolves a category name to its id and negates with !=', () => {
    expect(parse('category:"Dining & Drinks"').draft.categories).toEqual({
      ids: ['cat-dining'],
      negated: false,
    })
    expect(parse('category!="Dining & Drinks"').draft.categories).toEqual({
      ids: ['cat-dining'],
      negated: true,
    })
  })

  it('reports a category nobody has', () => {
    const { errors } = parse('category:"Yacht Fuel"')
    expect(errors).toEqual(['No category named "Yacht Fuel".'])
  })

  it('does not call an unresolved name an error before the categories load', () => {
    const { errors, draft } = parseSearch('category:"Dining & Drinks"', {
      universe: EMPTY_UNIVERSE,
      today: TODAY,
    })
    expect(errors).toEqual([])
    expect(draft.categories.ids).toEqual([])
  })

  it('resolves the relative and absolute date forms', () => {
    expect(parse('date=-30d').draft.date).toEqual({
      from: '2026-07-22',
      to: '2026-08-21',
      preset: '-30d',
    })
    expect(parse('date>=2024-01-01').draft.date).toEqual({
      from: '2024-01-01',
      to: null,
      preset: null,
    })
    expect(parse('date:2024-01-01..2024-03-31').draft.date).toEqual({
      from: '2024-01-01',
      to: '2024-03-31',
      preset: null,
    })
  })

  it('computes a ± window in integer cents', () => {
    expect(parse('amount=50±10%').draft.amount).toEqual({ min: '45.00', max: '55.00' })
    expect(parse('amount=50+-10%').draft.amount).toEqual({ min: '45.00', max: '55.00' })
  })

  it('reads an amount range and a bare comparison', () => {
    expect(parse('amount:50..200').draft.amount).toEqual({ min: '50.00', max: '200.00' })
    expect(parse('amount<100').draft.amount).toEqual({ min: null, max: '100.00' })
  })

  it('narrows `expense` to money going out, not to size alone', () => {
    const { draft, limitations } = parse('expense>500')
    expect(draft.amount).toEqual({ min: '500.00', max: null, direction: 'expense' })
    expect(limitations).toEqual([])
  })

  it('leaves no sign test behind when the size is not a number', () => {
    const { draft, errors } = parse('expense>abc')
    expect(draft.amount).toBeNull()
    expect(errors).not.toEqual([])
  })

  it('leaves a plain amount term signless', () => {
    expect(parse('amount>500').draft.amount).toEqual({ min: '500.00', max: null })
  })

  it('maps the review and category state terms', () => {
    expect(parse('is:reviewed').draft.isReviewed).toBe(true)
    expect(parse('not:reviewed').draft.isReviewed).toBe(false)
    expect(parse('is:uncategorized').draft.uncategorized).toBe(true)
    expect(parse('is:tags').draft.hasTags).toBe(true)
    expect(parse('not:tags').draft.hasTags).toBe(false)
    expect(parse('is:suggested').draft.hasCategorySuggestion).toBe(true)
    expect(parse('not:suggested').draft.hasCategorySuggestion).toBe(false)
    expect(parse('is:attached').draft.hasAttachment).toBe(true)
    expect(parse('not:attached').draft.hasAttachment).toBe(false)
    expect(parse('is:missing-receipt').draft.missingReceipt).toBe(true)
    expect(parse('not:missing-receipt').draft.missingReceipt).toBe(false)
  })

  it('maps pending in both directions', () => {
    const pending = parse('is:pending')
    expect(pending.errors).toEqual([])
    expect(pending.limitations).toEqual([])
    expect(pending.draft.isPending).toBe(true)
    expect(parse('not:pending').draft.isPending).toBe(false)
    expect(parse('is:reviewed').draft.isPending).toBeNull()
  })

  it('resolves a tag name under either spelling of the key', () => {
    expect(parse('tags:reimbursable').draft.tags.ids).toEqual(['tag-reimb'])
    expect(parse('tag:reimbursable').draft.tags.ids).toEqual(['tag-reimb'])
  })

  it('rejects a key that is not in the grammar', () => {
    expect(parse('colour:blue').errors).toEqual(['`colour:blue` is not a search term.'])
  })

  it('rejects a date that is neither a date nor a period', () => {
    expect(parse('date=someday').errors).toEqual(['`date=someday` is not a date or a period.'])
  })

  it('is empty for an empty query', () => {
    const { draft, errors, limitations } = parse('   ')
    expect(draft.texts).toEqual([])
    expect(errors).toEqual([])
    expect(limitations).toEqual([])
  })
})
