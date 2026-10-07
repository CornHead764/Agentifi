/**
 * The category list as a tree, three levels deep, and the search every
 * category list narrows it with: the settings screen, the pickers and the
 * checklists.
 *
 * A child can outlive its parent: a delete is a soft delete on one row, so its
 * children's `parent_id` resolves to nothing and they become roots here. The
 * same holds for a child whose parent a screen left out of the offered set.
 *
 * A cycle is representable (the API refuses only self-parenting). A node
 * reached twice is dropped from the second path, and anything unreachable is
 * appended as a root.
 */

import { matchesSearch } from '@/lib/search'

/** The fields the tree needs. Any category shape that carries them fits. */
export interface CategoryChoice {
  id: string
  name: string
  parent_id: string | null
  sort_order?: number
}

export interface CategoryNode<T extends CategoryChoice> {
  category: T
  /** 0, 1 or 2. */
  depth: number
  children: CategoryNode<T>[]
}

export function buildCategoryTree<T extends CategoryChoice>(
  categories: readonly T[],
): CategoryNode<T>[] {
  const byId = new Map(categories.map((category) => [category.id, category]))
  const children = new Map<string, T[]>()
  const roots: T[] = []

  for (const category of categories) {
    const parentId = category.parent_id
    if (parentId !== null && byId.has(parentId)) {
      const siblings = children.get(parentId)
      if (siblings) siblings.push(category)
      else children.set(parentId, [category])
    } else {
      roots.push(category)
    }
  }

  const placed = new Set<string>()
  const grow = (category: T, depth: number): CategoryNode<T> => {
    placed.add(category.id)
    const kids = (children.get(category.id) ?? []).filter((kid) => !placed.has(kid.id))
    return {
      category,
      depth,
      children: sortCategories(kids).map((kid) => grow(kid, depth + 1)),
    }
  }

  const tree = sortCategories(roots).map((category) => grow(category, 0))
  for (const category of sortCategories(categories)) {
    if (!placed.has(category.id)) tree.push(grow(category, 0))
  }
  return tree
}

/** `sort_order` first, then name: imported categories all share one `sort_order`. */
export function sortCategories<T extends CategoryChoice>(categories: readonly T[]): T[] {
  return [...categories].sort(
    (left, right) =>
      (left.sort_order ?? 0) - (right.sort_order ?? 0) ||
      left.name.localeCompare(right.name, undefined, { sensitivity: 'base' }) ||
      left.id.localeCompare(right.id),
  )
}

/** Display order, with the children of a collapsed node left out. */
export function flattenCategoryTree<T extends CategoryChoice>(
  nodes: readonly CategoryNode<T>[],
  collapsed: ReadonlySet<string> = new Set(),
): CategoryNode<T>[] {
  const rows: CategoryNode<T>[] = []
  const walk = (node: CategoryNode<T>) => {
    rows.push(node)
    if (collapsed.has(node.category.id)) return
    for (const child of node.children) walk(child)
  }
  for (const node of nodes) walk(node)
  return rows
}

/**
 * The tree narrowed to a search. An ancestor of a match survives, since a
 * subcategory alone reads as top-level, and a match keeps its whole subtree:
 * typing a parent's name asks for the parent, not for the one child that
 * happens to share its spelling.
 */
export function searchCategoryTree<T extends CategoryChoice>(
  nodes: readonly CategoryNode<T>[],
  search: string,
): CategoryNode<T>[] {
  if (search.trim() === '') return [...nodes]

  const keep = (node: CategoryNode<T>): CategoryNode<T> | null => {
    if (matchesSearch(search, node.category.name)) return node
    const children = node.children.map(keep).filter((child) => child !== null)
    return children.length > 0 ? { ...node, children } : null
  }
  return nodes.map(keep).filter((node) => node !== null)
}

/** A flat list as the rows a picker or checklist draws: the tree, searched, in display order. */
export function categoryRows<T extends CategoryChoice>(
  categories: readonly T[],
  search = '',
): CategoryNode<T>[] {
  return flattenCategoryTree(searchCategoryTree(buildCategoryTree(categories), search))
}

/** A row class with its depth's modifier: `option-list__row--child` at depth 1. */
export function categoryIndentClass(base: string, depth: number): string {
  if (depth >= 2) return `${base} ${base}--grandchild`
  return depth === 1 ? `${base} ${base}--child` : base
}
