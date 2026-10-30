/**
 * The tree, the ordering, the search, and the two shapes the category list can
 * arrive in that a naive walk loses rows to. An orphan is the expected result
 * of a soft delete, not a corrupt database.
 */

import { describe, expect, it } from 'vitest'

import {
  buildCategoryTree,
  categoryRows,
  flattenCategoryTree,
  searchCategoryTree,
  sortCategories,
  type CategoryChoice,
} from './categoryTree'

function category(
  id: string,
  name: string,
  parentId: string | null = null,
  sortOrder = 0,
): CategoryChoice {
  return { id, name, parent_id: parentId, sort_order: sortOrder }
}

/** `Auto & Transport › Registration › Plate Renewal`, a branch three levels deep, the most the tree allows. */
function threeLevels(): CategoryChoice[] {
  return [
    category('auto', 'Auto & Transport'),
    category('reg', 'Registration', 'auto'),
    category('plate', 'Plate Renewal', 'reg'),
    category('gas', 'Gas & Fuel', 'auto'),
    category('cash', 'Cash & ATM'),
  ]
}

describe('buildCategoryTree', () => {
  it('nests three levels and stamps each row with its depth', () => {
    const tree = buildCategoryTree(threeLevels())
    const rows = flattenCategoryTree(tree)

    expect(rows.map((row) => [row.category.name, row.depth])).toEqual([
      ['Auto & Transport', 0],
      ['Gas & Fuel', 1],
      ['Registration', 1],
      ['Plate Renewal', 2],
      ['Cash & ATM', 0],
    ])
  })

  it('sorts by sort_order first and by name within it', () => {
    const tree = buildCategoryTree([
      category('b', 'Banking', null, 1),
      category('a', 'Auto', null, 1),
      category('z', 'Zoo', null, 0),
    ])
    expect(tree.map((node) => node.category.name)).toEqual(['Zoo', 'Auto', 'Banking'])
  })

  it('names sort without regard to case', () => {
    const sorted = sortCategories([category('b', 'banking'), category('a', 'Auto')])
    expect(sorted.map((entry) => entry.name)).toEqual(['Auto', 'banking'])
  })

  // A soft-deleted parent leaves children holding an unresolvable parent_id.
  it('promotes a child whose parent has been deleted', () => {
    const orphaned = threeLevels().filter((entry) => entry.id !== 'auto')
    const tree = buildCategoryTree(orphaned)

    expect(tree.map((node) => [node.category.name, node.depth])).toEqual([
      ['Cash & ATM', 0],
      ['Gas & Fuel', 0],
      ['Registration', 0],
    ])
    expect(flattenCategoryTree(tree)).toHaveLength(4)
  })

  it('keeps a re-parent cycle visible instead of looping or losing it', () => {
    const tree = buildCategoryTree([
      category('a', 'Alpha', 'b'),
      category('b', 'Beta', 'a'),
    ])
    expect(flattenCategoryTree(tree).map((row) => row.category.name)).toEqual(['Alpha', 'Beta'])
  })
})

describe('flattenCategoryTree', () => {
  it('leaves the children of a collapsed node out', () => {
    const tree = buildCategoryTree(threeLevels())
    const rows = flattenCategoryTree(tree, new Set(['auto']))
    expect(rows.map((row) => row.category.name)).toEqual(['Auto & Transport', 'Cash & ATM'])
  })
})

describe('searchCategoryTree', () => {
  it('keeps the path to a match so a subcategory is not read as a group', () => {
    const tree = buildCategoryTree(threeLevels())
    const found = flattenCategoryTree(searchCategoryTree(tree, 'plate'))
    expect(found.map((row) => [row.category.name, row.depth])).toEqual([
      ['Auto & Transport', 0],
      ['Registration', 1],
      ['Plate Renewal', 2],
    ])
  })

  it('keeps everything under a node that matches', () => {
    const tree = buildCategoryTree(threeLevels())
    const found = flattenCategoryTree(searchCategoryTree(tree, 'registration'))
    expect(found.map((row) => row.category.name)).toEqual([
      'Auto & Transport',
      'Registration',
      'Plate Renewal',
    ])
  })

  it('answers an empty query with the whole tree', () => {
    const tree = buildCategoryTree(threeLevels())
    expect(searchCategoryTree(tree, '  ')).toHaveLength(tree.length)
  })

  it('folds case, accents and surrounding space', () => {
    const tree = buildCategoryTree([category('cafe', 'Café'), category('home', 'Home')])
    expect(searchCategoryTree(tree, '  CAFE ').map((node) => node.category.id)).toEqual(['cafe'])
    expect(searchCategoryTree(tree, 'café').map((node) => node.category.id)).toEqual(['cafe'])
  })

  it('drops a branch nothing in it matched', () => {
    expect(searchCategoryTree(buildCategoryTree(threeLevels()), 'zzz')).toEqual([])
  })
})

describe('categoryRows', () => {
  // A picker offers a narrowed set, so a child whose parent was left out
  // would otherwise never render.
  it('draws a child whose parent is not offered as top level', () => {
    const rows = categoryRows(threeLevels().filter((one) => one.id !== 'auto'))
    expect(rows.map((row) => [row.category.id, row.depth])).toEqual([
      ['cash', 0],
      ['gas', 0],
      ['reg', 0],
      ['plate', 1],
    ])
  })

  it('keeps the path to a matching subcategory, in display order', () => {
    expect(categoryRows(threeLevels(), 'fuel').map((row) => [row.category.id, row.depth])).toEqual([
      ['auto', 0],
      ['gas', 1],
    ])
  })
})
