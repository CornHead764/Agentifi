/**
 * What the settings screen does with the category tree: the three-level
 * limit, re-parenting, re-ordering and the path read out of context.
 */

import {
  flattenCategoryTree,
  sortCategories,
  type CategoryNode,
} from '@/lib/categoryTree'
import type { Category, Uuid } from '@/lib/transactions/types'

/** Group › category › subcategory. Depths run 0, 1, 2. */
export const MAX_DEPTH = 3

export function descendantIds(node: CategoryNode<Category>): Uuid[] {
  const ids: Uuid[] = []
  const walk = (current: CategoryNode<Category>) => {
    for (const child of current.children) {
      ids.push(child.category.id)
      walk(child)
    }
  }
  walk(node)
  return ids
}

/** Levels this node occupies: 1 for a leaf, 2 for a category with children. */
export function subtreeHeight(node: CategoryNode<Category>): number {
  return 1 + node.children.reduce((tallest, child) => Math.max(tallest, subtreeHeight(child)), 0)
}

export function findCategoryNode(
  nodes: readonly CategoryNode<Category>[],
  id: Uuid,
): CategoryNode<Category> | null {
  for (const node of nodes) {
    if (node.category.id === id) return node
    const found = findCategoryNode(node.children, id)
    if (found) return found
  }
  return null
}

/**
 * Where a category may be filed. `moving` is the node being re-parented, or
 * null for a new one; the depth check counts the height of its whole subtree.
 */
export function parentChoices(
  nodes: readonly CategoryNode<Category>[],
  moving: CategoryNode<Category> | null,
): CategoryNode<Category>[] {
  const height = moving === null ? 1 : subtreeHeight(moving)
  const barred = new Set<Uuid>(
    moving === null ? [] : [moving.category.id, ...descendantIds(moving)],
  )
  return flattenCategoryTree(nodes).filter(
    (node) => !barred.has(node.category.id) && node.depth + height <= MAX_DEPTH - 1,
  )
}

/** The row's own level, in display order, including the row itself. */
function siblingsOf(nodes: readonly CategoryNode<Category>[], id: Uuid): Category[] {
  if (nodes.some((node) => node.category.id === id)) {
    return nodes.map((node) => node.category)
  }
  for (const node of nodes) {
    const deeper = siblingsOf(node.children, id)
    if (deeper.length > 0) return deeper
  }
  return []
}

/**
 * Moving one row among its siblings, as the writes that does. The whole level
 * is renumbered, because imported rows share one `sort_order` and swapping
 * equal numbers moves nothing. Only changed rows are returned; empty when
 * there is nowhere to go.
 */
export function moveWithinSiblings(
  nodes: readonly CategoryNode<Category>[],
  id: Uuid,
  direction: -1 | 1,
): { id: Uuid; sort_order: number }[] {
  const ordered = sortCategories(siblingsOf(nodes, id))
  const at = ordered.findIndex((one) => one.id === id)
  const to = at + direction
  if (at === -1 || to < 0 || to >= ordered.length) return []

  const moved = [...ordered]
  moved[at] = ordered[to]
  moved[to] = ordered[at]
  return moved
    .map((one, index) => ({ id: one.id, sort_order: index }))
    .filter((_, index) => moved[index].sort_order !== index)
}

/** `Auto & Transport › Registration`, for a row that is read out of context. */
export function categoryPath(nodes: readonly CategoryNode<Category>[], id: Uuid): string {
  const trail: string[] = []
  const walk = (current: readonly CategoryNode<Category>[]): boolean => {
    for (const node of current) {
      trail.push(node.category.name)
      if (node.category.id === id || walk(node.children)) return true
      trail.pop()
    }
    return false
  }
  return walk(nodes) ? trail.join(' › ') : ''
}
