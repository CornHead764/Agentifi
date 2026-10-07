/** The settings screen's moves over the category tree: re-parenting, re-ordering, paths. */

import { describe, expect, it } from 'vitest'

import type { Category } from '@/lib/transactions/types'

import { buildCategoryTree } from '@/lib/categoryTree'
import { category } from '@/test/builders'

import {
  categoryPath,
  descendantIds,
  findCategoryNode,
  moveWithinSiblings,
  parentChoices,
  subtreeHeight,
} from './tree'

/** `Auto & Transport › Registration › Plate Renewal`, a branch three levels deep, the most the tree allows. */
function threeLevels(): Category[] {
  return [
    category('auto', 'Auto & Transport'),
    category('reg', 'Registration', 'auto'),
    category('plate', 'Plate Renewal', 'reg'),
    category('gas', 'Gas & Fuel', 'auto'),
    category('cash', 'Cash & ATM'),
  ]
}

describe('parentChoices', () => {
  it('offers only the levels that keep the tree three deep', () => {
    const tree = buildCategoryTree(threeLevels())
    const names = parentChoices(tree, null).map((node) => node.category.name)
    expect(names).toEqual(['Auto & Transport', 'Gas & Fuel', 'Registration', 'Cash & ATM'])
  })

  // A branch takes its own subcategories with it, so the check is against the
  // height of what moves rather than against the one row being moved.
  it('offers nothing to a branch that already fills the three levels', () => {
    const tree = buildCategoryTree(threeLevels())
    const auto = findCategoryNode(tree, 'auto')
    if (auto === null) throw new Error('Auto & Transport is missing from the tree')

    expect(parentChoices(tree, auto)).toEqual([])
  })

  it('offers only groups to a two-level branch, and never its own descendant', () => {
    const tree = buildCategoryTree(threeLevels())
    const registration = findCategoryNode(tree, 'reg')
    if (registration === null) throw new Error('Registration is missing from the tree')

    expect(parentChoices(tree, registration).map((node) => node.category.id)).toEqual([
      'auto',
      'cash',
    ])
  })

  it('lets a leaf move anywhere that is not itself', () => {
    const tree = buildCategoryTree(threeLevels())
    const gas = findCategoryNode(tree, 'gas')
    expect(gas).not.toBeNull()
    if (gas === null) return

    expect(parentChoices(tree, gas).map((node) => node.category.id)).toEqual([
      'auto',
      'reg',
      'cash',
    ])
  })
})

describe('the tree helpers', () => {
  it('reports the height of a branch and the ids under it', () => {
    const tree = buildCategoryTree(threeLevels())
    const auto = findCategoryNode(tree, 'auto')
    if (auto === null) throw new Error('Auto & Transport is missing from the tree')

    expect(subtreeHeight(auto)).toBe(3)
    expect(descendantIds(auto).sort()).toEqual(['gas', 'plate', 'reg'])
  })

  it('spells a path out for a row read out of context', () => {
    const tree = buildCategoryTree(threeLevels())
    expect(categoryPath(tree, 'plate')).toBe('Auto & Transport › Registration › Plate Renewal')
    expect(categoryPath(tree, 'nothing')).toBe('')
  })
})

describe('moveWithinSiblings', () => {
  it('renumbers the level when every row shares one sort_order', () => {
    // The top row already holds 0, so only one write comes back.
    const tree = buildCategoryTree(threeLevels())
    expect(moveWithinSiblings(tree, 'cash', -1)).toEqual([{ id: 'auto', sort_order: 1 }])
  })

  it('moves a row among its own siblings, not among the rows on screen', () => {
    // Registration's neighbour is Gas & Fuel, two rows above it in the
    // flattened list, because Plate Renewal sits between them.
    const tree = buildCategoryTree(threeLevels())
    expect(moveWithinSiblings(tree, 'reg', -1)).toEqual([{ id: 'gas', sort_order: 1 }])
  })

  it('writes only the rows whose number changes', () => {
    const tree = buildCategoryTree([
      category('a', 'A', null, { sort_order: 0 }),
      category('b', 'B', null, { sort_order: 1 }),
      category('c', 'C', null, { sort_order: 2 }),
    ])
    expect(moveWithinSiblings(tree, 'c', -1)).toEqual([
      { id: 'c', sort_order: 1 },
      { id: 'b', sort_order: 2 },
    ])
  })

  it('has nowhere to move the first and last rows', () => {
    const tree = buildCategoryTree(threeLevels())
    expect(moveWithinSiblings(tree, 'auto', -1)).toEqual([])
    expect(moveWithinSiblings(tree, 'cash', 1)).toEqual([])
    expect(moveWithinSiblings(tree, 'plate', -1)).toEqual([])
  })
})
