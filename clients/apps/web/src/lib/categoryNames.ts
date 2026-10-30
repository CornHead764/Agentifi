/**
 * Everything a category id or a typed name resolves to: whether a typed name
 * already exists, folding case and accents ("cafe" is "Café"), since
 * duplicate names are legal (a Simplifi export can carry two) and this names
 * a row in a prompt rather than identifying one; and what an id reads back as.
 * A row never categorized and a row whose category was deleted both read
 * "Uncategorized"; a list still loading reads "…", since every space is seeded
 * with categories and an empty list means the query has not returned rather
 * than that the space truly has none.
 */

import { foldText } from './search'
import type { Category, Uuid } from './transactions/types'

/** The first category whose name is the same word, or undefined. */
export function categoryNamed<T extends { name: string }>(
  categories: readonly T[],
  name: string,
): T | undefined {
  const folded = foldText(name)
  if (folded === '') return undefined
  return categories.find((category) => foldText(category.name) === folded)
}

export interface CategoryCreateOffer<T> {
  name: string
  taken: T | undefined
  canCreate: boolean
}

/** `matches` is counted by the caller, since each picker narrows its rows differently. */
export function categoryCreateOffer<T extends { name: string }>(
  categories: readonly T[],
  search: string,
  matches: number,
): CategoryCreateOffer<T> {
  const name = search.trim()
  const taken = categoryNamed(categories, name)
  return { name, taken, canCreate: name !== '' && matches === 0 && taken === undefined }
}

/** The row a transaction's `category_id` names, or undefined once it has been deleted. */
export function findCategory(
  categories: readonly Category[],
  id: Uuid | null,
): Category | undefined {
  return id === null ? undefined : categories.find((category) => category.id === id)
}

/** `Groceries`, or `Auto & Transport › Registration › Plate Renewal` to the root, for a category read out of context. */
export function categoryLabel(categories: readonly Category[], id: Uuid | null): string {
  if (id === null) return 'Uncategorized'
  if (categories.length === 0) return '…'
  const found = findCategory(categories, id)
  if (!found) return 'Uncategorized'
  const trail = [found.name]
  let current = found
  while (current.parent_id !== null) {
    const parent = findCategory(categories, current.parent_id)
    if (!parent) break
    trail.unshift(parent.name)
    current = parent
  }
  return trail.join(' › ')
}

/** Just the category's own name, for a column too narrow for `categoryLabel`'s breadcrumb. */
export function categoryName(categories: readonly Category[], id: Uuid | null): string {
  if (id === null) return 'Uncategorized'
  if (categories.length === 0) return '…'
  return findCategory(categories, id)?.name ?? 'Uncategorized'
}
